package daemon

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"math"
	"mime/multipart"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"
	"lykeion/server/internal/apiclient"
	"lykeion/server/internal/coresearch"
	"lykeion/server/internal/daemon/envs"
	"lykeion/server/internal/daemon/kernels"
	"lykeion/server/internal/daemon/sandbox"
	"lykeion/server/pkg/agent"
)

var chatIdle = 30 * time.Minute
var coresearchPoll = time.Second

type chatHost struct {
	// filesMu orders final fill commits against the first writer launch. The
	// durable marker stays conservative across retirement and daemon restart.
	filesMu          sync.Mutex
	writerLaunched   bool
	kernelPython     string
	kernelGeneration uint64
	cellMu           sync.Mutex
	mu               sync.Mutex
	work             coresearch.WorkContext
	dir              string
	study            *envs.Study
	sessions         *kernels.Sessions
	lastUsed         time.Time
	users            int
	tmp, bridgeDir   string
	kernel           string
}

func (d *daemon) corePath(path string) string {
	return "/api/daemon/coresearch/" + path + "?daemon_id=" + url.QueryEscape(d.id)
}
func (d *daemon) coreAPI(ws uuid.UUID) *apiclient.Client {
	return &apiclient.Client{Server: d.api.Server, Token: d.api.Token, HTTP: d.api.HTTP, Workspace: ws.String()}
}
func (d *daemon) checkChat(ctx context.Context, w coresearch.WorkContext) error {
	if d.sandboxErr != nil {
		return d.sandboxErr
	}
	if w.ChatID == uuid.Nil || w.ProjectID == uuid.Nil || w.WorkspaceID == uuid.Nil || d.id == "" {
		return errors.New("invalid Coresearch identity")
	}
	d.mu.Lock()
	own := false
	for _, r := range d.refs {
		if r.WorkspaceID == w.WorkspaceID && d.ownerOf[r.RuntimeID] != uuid.Nil {
			own = true
		}
	}
	d.mu.Unlock()
	if !own {
		return errors.New("Coresearch requires an owned runtime")
	}
	var status struct {
		Status string `json:"status"`
	}
	if err := d.coreAPI(w.WorkspaceID).Do(ctx, "GET", d.corePath("chats/"+w.ChatID.String()+"/status"), nil, &status); err != nil {
		return err
	}
	if status.Status != "active" {
		return errors.New("Coresearch chat is no longer active")
	}
	return nil
}
func (d *daemon) chat(ctx context.Context, w coresearch.WorkContext) (*chatHost, error) {
	if err := d.checkChat(ctx, w); err != nil {
		return nil, err
	}
	d.chatsMu.Lock()
	defer d.chatsMu.Unlock()
	if h := d.chats[w.ChatID]; h != nil {
		if h.work.WorkspaceID != w.WorkspaceID || h.work.ProjectID != w.ProjectID {
			return nil, errors.New("chat binding changed")
		}
		h.mu.Lock()
		h.lastUsed = time.Now()
		err := h.acceptPython(w.PythonVersion)
		h.mu.Unlock()
		return h, err
	}
	_, prior := os.Lstat(d.chatFolder(w.ChatID))
	if prior != nil && !errors.Is(prior, os.ErrNotExist) {
		return nil, prior
	}
	for _, dir := range []string{filepath.Join(d.cfg.Home, "coresearch"), d.chatFolder(w.ChatID)} {
		if err := privateDir(dir); err != nil {
			return nil, err
		}
	}
	h := &chatHost{work: w, dir: d.chatFolder(w.ChatID), lastUsed: time.Now()}
	if _, err := os.Lstat(d.chatWriterRecord(h)); err == nil {
		h.writerLaunched = true
	} else if !errors.Is(err, os.ErrNotExist) {
		return nil, err
	}
	// An existing folder may predate the durable guard or belong to an
	// execution interrupted before this daemon acquired it. Do not assume
	// that its writer descendants disappeared when the daemon did.
	if prior == nil && !h.writerLaunched {
		if err := d.markChatWriter(h); err != nil {
			return nil, err
		}
	}
	if err := prepareChatMetadata(ctx, h.dir); err != nil {
		return nil, err
	}
	if d.bases != nil {
		dir := filepath.Join(d.bases.Dir, "coresearch", w.WorkspaceID.String(), w.ProjectID.String())
		// Each ancestor is daemon-owned; refuse replacement links before using it.
		for _, p := range []string{filepath.Join(d.bases.Dir, "coresearch"), filepath.Dir(dir), dir} {
			if err := privateDir(p); err != nil {
				return nil, err
			}
		}
		h.study = envs.ForStudy(d.bases, dir)
		if err := h.acceptPython(w.PythonVersion); err != nil {
			return nil, err
		}
	}
	if d.chats == nil {
		d.chats = map[uuid.UUID]*chatHost{}
	}
	d.chats[w.ChatID] = h
	return h, nil
}

// acceptPython is called with h.mu held (or before the host is published).
// Views already handed to a run remain immutable; shared selection is kept by
// Bases, so sibling chats and later unpinned claims see the authoritative pin.
func (h *chatHost) acceptPython(version *string) error {
	if h.study == nil {
		return nil
	}
	if version != nil && *version != "" {
		if err := h.study.RememberPython(*version); err != nil {
			return err
		}
	}
	selected := h.study.RecordedPython()
	if h.study.PythonVersion != selected {
		view := h.study.ForRun()
		view.PythonVersion = selected
		h.study = view
	}
	return nil
}

func (d *daemon) chatWriterRecord(h *chatHost) string {
	return filepath.Join(d.cfg.StateDir, "coresearch-writers", h.work.ChatID.String())
}
func (d *daemon) markChatWriter(h *chatHost) error {
	h.filesMu.Lock()
	defer h.filesMu.Unlock()
	if h.writerLaunched {
		return nil
	}
	path := d.chatWriterRecord(h)
	if err := atomicJSON(filepath.Dir(path), filepath.Base(path), map[string]bool{"writer_launched": true}); err != nil {
		return err
	}
	h.writerLaunched = true
	return nil
}

func (h *chatHost) use() func() {
	h.mu.Lock()
	h.users++
	h.lastUsed = time.Now()
	h.mu.Unlock()
	return func() { h.mu.Lock(); h.users--; h.lastUsed = time.Now(); h.mu.Unlock() }
}
func (h *chatHost) closeLocked() error {
	var err error
	if h.sessions != nil {
		err = h.sessions.Close()
		h.sessions = nil
	}
	for _, dir := range []string{h.tmp, h.bridgeDir} {
		if dir != "" {
			err = errors.Join(err, os.RemoveAll(dir))
		}
	}
	h.tmp, h.bridgeDir = "", ""
	return err
}
func (d *daemon) stopChats() {
	d.chatsMu.Lock()
	var stopped []*chatHost
	for _, h := range d.chats {
		h.mu.Lock()
		hadKernel := h.sessions != nil
		_ = h.closeLocked()
		h.mu.Unlock()
		if hadKernel {
			stopped = append(stopped, h)
		}
	}
	d.chatsMu.Unlock()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	for _, h := range stopped {
		_ = d.kernelState(ctx, h, "stopped", 0)
	}
}

func (d *daemon) kernelState(ctx context.Context, h *chatHost, state string, seq int64) error {
	return d.coreAPI(h.work.WorkspaceID).Do(ctx, "POST", d.corePath("chats/"+h.work.ChatID.String()+"/kernel"), map[string]any{"state": state, "seq_done": seq}, nil)
}
func (d *daemon) reapChats(ctx context.Context) {
	d.chatsMu.Lock()
	hosts := make([]*chatHost, 0, len(d.chats))
	for _, h := range d.chats {
		hosts = append(hosts, h)
	}
	d.chatsMu.Unlock()
	for _, h := range hosts {
		h.mu.Lock()
		hasKernel := h.sessions != nil
		h.mu.Unlock()
		if !hasKernel {
			continue
		}
		revoked := d.checkChat(ctx, h.work) != nil
		h.mu.Lock()
		idle := h.users == 0 && h.sessions != nil && time.Since(h.lastUsed) >= chatIdle
		if idle || revoked {
			_ = h.closeLocked()
		}
		h.mu.Unlock()
		if idle && !revoked {
			_ = d.kernelState(ctx, h, "stopped", 0)
		}
	}
}
func (d *daemon) watchCoresearch(ctx context.Context, cancel context.CancelFunc, w coresearch.WorkContext, kind string, id uuid.UUID) {
	ticker := time.NewTicker(coresearchPoll)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			path := "chats/" + w.ChatID.String() + "/status"
			if kind != "" {
				path = kind + "/" + id.String() + "/status"
			}
			var status struct {
				Status string `json:"status"`
			}
			err := d.coreAPI(w.WorkspaceID).Do(ctx, "GET", d.corePath(path), nil, &status)
			// Fail closed when authority cannot be revalidated; never continue offline.
			if err != nil || status.Status == "cancelled" {
				cancel()
				return
			}
		}
	}
}

func codePolicy(home, dir string, study *envs.Study, tmp string, private, roots []string) sandbox.Policy {
	p := sandbox.Policy{Home: home, Write: []string{dir, study.Dir, tmp}, Hidden: slices.Clone(private), RestrictedRoots: slices.Clone(roots)}
	for _, base := range study.Bases.Folders() {
		if isDir(base) {
			p.Read = append(p.Read, base)
		}
	}
	return p
}
func corePersonEnv() []string {
	return slices.DeleteFunc(envs.Isolate(os.Environ()), func(e string) bool {
		k, _, _ := strings.Cut(e, "=")
		k = strings.ToUpper(k)
		return privateCodeEnvName(k) || strings.HasPrefix(k, "AWS_") || strings.HasPrefix(k, "AZURE_") || slices.ContainsFunc(secretNames, func(s string) bool { return strings.Contains(k, s) })
	})
}
func coreTmpEnv(tmp string) []string {
	env := []string{"TMPDIR=" + tmp}
	for k, sub := range cacheDirs {
		env = append(env, k+"="+filepath.Join(tmp, sub))
	}
	return env
}
func coreJupyterEnv(study *envs.Study, env []string) []string {
	var dirs []string
	for _, name := range []string{"conda", "python"} {
		p := filepath.Join(study.Dir, name, "share", "jupyter")
		if isDir(p) {
			dirs = append(dirs, p)
		}
	}
	for _, v := range env {
		if p, ok := strings.CutPrefix(v, "JUPYTER_PATH="); ok {
			dirs = append(dirs, filepath.SplitList(p)...)
		}
	}
	out := slices.DeleteFunc(slices.Clone(env), func(v string) bool { return strings.HasPrefix(v, "JUPYTER_PATH=") })
	return append(out, "JUPYTER_PATH="+strings.Join(dirs, string(os.PathListSeparator)))
}

// startKernel is called with h.mu held. Its bridge outlives individual cells.
func (d *daemon) startKernel(ctx context.Context, h *chatHost) error {
	if err := d.retireChangedKernel(ctx, h); err != nil {
		return err
	}
	if h.sessions != nil {
		return nil
	}
	if h.study == nil {
		return errors.New("Coresearch has no project environment")
	}
	if err := d.checkChat(ctx, h.work); err != nil {
		return err
	}
	if err := d.markChatWriter(h); err != nil {
		return err
	}
	tmp, err := os.MkdirTemp("", "lyk-coresearch-")
	if err != nil {
		return err
	}
	h.tmp = tmp
	bridge, err := os.MkdirTemp("", "lyk-coresearch-bridge-")
	if err != nil {
		_ = h.closeLocked()
		return err
	}
	h.bridgeDir = bridge
	success := false
	defer func() {
		if !success {
			_ = h.closeLocked()
		}
	}()
	if err = os.Mkdir(filepath.Join(tmp, "home"), 0700); err != nil {
		return err
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return err
	}
	private, err := d.privatePaths()
	if err != nil {
		return err
	}
	roots := []string{d.cfg.Home, h.study.Bases.Dir}
	for _, root := range roots {
		if err = sandbox.PrepareRestrictedRoot(home, root); err != nil {
			return err
		}
	}
	if err = h.study.Bases.EnsureLeanRoot(); err != nil {
		return err
	}
	p := codePolicy(home, h.dir, h.study, tmp, private, roots)
	p.Read = append(p.Read, bridge)
	var placeholders []placeholder
	if err = protectRunFolder(&p, h.dir, true, &placeholders); err != nil {
		return err
	}
	p.ReadOnly = append(p.ReadOnly, filepath.Join(h.dir, ".lyk"))
	study := h.study.ForRun()
	study.Run = func(ctx context.Context, env []string, dir, name string, args ...string) ([]byte, error) {
		if err := d.checkChat(ctx, h.work); err != nil {
			return nil, err
		}
		cmd, err := p.Command(ctx, name, args...)
		if err != nil {
			return nil, err
		}
		cmd.Dir = dir
		cmd.Env = slices.Concat(corePersonEnv(), env, coreTmpEnv(tmp))
		cmd.Cancel = func() error { killGroup(cmd); return nil }
		cmd.WaitDelay = 5 * time.Second
		return cmd.CombinedOutput()
	}
	preparedPython := study.RecordedPython()
	env, err := study.Environment(ctx)
	if err != nil {
		return err
	}
	// A sibling can reserve the previously empty project pin while this
	// view prepares. Reprepare for that immutable choice before launching.
	if selected := study.RecordedPython(); selected != preparedPython {
		preparedPython = selected
		env, err = study.Environment(ctx)
		if err != nil {
			return err
		}
	}
	preparedGeneration := study.PythonGeneration()
	env = coreJupyterEnv(study, env)
	python, _, err := study.Interpreter(envs.Python)
	if err != nil {
		return err
	}
	env = slices.Concat(corePersonEnv(), env, coreTmpEnv(tmp), []string{"PATH=" + study.PathPrefix() + string(os.PathListSeparator) + os.Getenv("PATH")})
	command := func(name string, args ...string) (*exec.Cmd, error) {
		if err := d.checkChat(context.Background(), h.work); err != nil {
			return nil, err
		}
		return p.Command(context.Background(), name, args...)
	}
	h.sessions, err = kernels.Start(ctx, command, python, env, h.dir, bridge)
	if err != nil {
		return err
	}
	if err = d.kernelState(ctx, h, "idle", 0); err != nil {
		return err
	}
	// A later pin must not relabel this already-prepared environment. It
	// will cause retirement at the next cell boundary instead.
	h.kernelPython, h.kernelGeneration = preparedPython, preparedGeneration
	success = true
	return nil
}

// The caller holds h.mu. Changing the project pin or rebuilding its
// interpreter retires variables from the earlier environment before reuse.
func (d *daemon) retireChangedKernel(ctx context.Context, h *chatHost) error {
	if h.sessions == nil || h.study == nil || (h.kernelPython == h.study.RecordedPython() && h.kernelGeneration == h.study.PythonGeneration()) {
		return nil
	}
	if err := h.closeLocked(); err != nil {
		return err
	}
	return d.kernelState(ctx, h, "stopped", 0)
}
func (d *daemon) runCell(ctx context.Context, c coresearch.ClaimedCell) error {
	h, err := d.chat(ctx, c.WorkContext)
	if err != nil {
		return err
	}
	done := d.useChat(h)
	defer done()
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	watched := make(chan struct{})
	go func() { defer close(watched); d.watchCoresearch(ctx, cancel, c.WorkContext, "cells", c.ID) }()
	defer func() { cancel(); <-watched }()
	// Keep host cleanup inside the cell boundary too: a successor must never
	// start between bridge cancellation and retirement of its sandbox folders.
	h.cellMu.Lock()
	defer h.cellMu.Unlock()
	defer func() {
		if ctx.Err() == nil {
			return
		}
		h.mu.Lock()
		_ = h.closeLocked()
		h.mu.Unlock()
		stopped, stop := context.WithTimeout(context.Background(), 3*time.Second)
		defer stop()
		if d.checkChat(stopped, h.work) == nil {
			_ = d.kernelState(stopped, h, "stopped", 0)
		}
	}()
	api := d.coreAPI(c.WorkspaceID)
	base := "cells/" + c.ID.String()
	if err = api.Do(ctx, "POST", d.corePath(base+"/start"), nil, nil); err != nil {
		return err
	}
	h.mu.Lock()
	err = d.retireChangedKernel(ctx, h)
	h.mu.Unlock()
	if err == nil {
		_, err = d.fill(ctx, c.ChatID, c.ProjectID)
	}
	if err == nil {
		h.mu.Lock()
		err = d.startKernel(ctx, h)
		s := h.sessions
		h.kernel = c.Kernel
		h.mu.Unlock()
		if err == nil {
			if err = d.kernelState(ctx, h, "busy", 0); err == nil {
				result, e := s.CellWithID(ctx, c.ID.String(), c.Kernel, c.Source)
				err = e
				if ctx.Err() != nil {
					return ctx.Err()
				}
				if e == nil {
					status := "ok"
					if result.Interrupted {
						status = "interrupted"
					} else if !result.OK {
						status = "error"
					}
					err = api.Do(ctx, "POST", d.corePath(base+"/finish"), map[string]any{"status": status, "outputs": result.Outputs, "execution_count": result.ExecutionCount}, nil)
					stateErr := d.kernelState(ctx, h, "idle", 0)
					return errors.Join(err, stateErr)
				}
			}
		}
	}
	if ctx.Err() != nil {
		return ctx.Err()
	}
	finish := api.Do(ctx, "POST", d.corePath(base+"/finish"), map[string]any{"status": "error", "outputs": []kernels.Output{{Type: "error", Ename: "HostError", Evalue: err.Error()}}}, nil)
	_ = d.kernelState(ctx, h, "stopped", 0)
	return errors.Join(err, finish)
}
func (d *daemon) controlChat(ctx context.Context, c coresearch.ClaimedControl) error {
	h, err := d.chat(ctx, c.WorkContext)
	if err != nil {
		return err
	}
	done := d.useChat(h)
	defer done()
	h.mu.Lock()
	if c.Op == "restart" {
		if err := d.retireChangedKernel(ctx, h); err != nil {
			h.mu.Unlock()
			return err
		}
	}
	s, kernel := h.sessions, h.kernel
	h.mu.Unlock()
	state := "stopped"
	if s != nil {
		state = "idle"
		switch c.Op {
		case "interrupt":
			if err := s.Interrupt(); err != nil {
				return err
			}
		case "restart":
			if err := s.RestartKernel(ctx, kernel); err != nil {
				return err
			}
		default:
			return errors.New("unknown kernel control")
		}
	}
	return d.kernelState(ctx, h, state, c.Seq)
}

func (d *daemon) runCoresearchTurn(ctx context.Context, c claimed) (localErr error) {
	rt := uuid.Nil
	if c.AgentRun.RuntimeID != nil {
		rt = *c.AgentRun.RuntimeID
	}
	r := Report{AgentRunID: c.AgentRun.ID, WorkspaceID: c.AgentRun.WorkspaceID, RuntimeID: rt, Attempt: c.AgentRun.Attempt, Kind: "complete", Mode: ModeSandboxed}
	journal := r
	journal.Kind = "fail"
	journal.FailureReason = agent.ReasonRuntimeOffline
	if err := d.journal().Put(journal); err != nil {
		return err
	}
	var cont *continuity
	var final agent.Result
	fail := func(err error) {
		r.Kind = "fail"
		r.Error = err.Error()
		r.FailureReason = agent.ReasonAgentError
		final.Err = err
	}
	defer func() {
		if ctx.Err() != nil {
			fail(ctx.Err())
			r.FailureReason = agent.ReasonRuntimeOffline
		}
		if err := d.out.Put(r); err != nil {
			localErr = errors.Join(localErr, err)
			return
		}
		localErr = errors.Join(localErr, d.finishContinuity(cont, final), d.forgetAttempt(r))
	}()
	d.mu.Lock()
	owner := d.ownerOf[rt]
	d.mu.Unlock()
	if owner == uuid.Nil || owner != c.AgentRun.ResponsibleUserID {
		fail(errors.New("Coresearch requires the responsible user's own runtime"))
		return nil
	}
	if c.AgentRun.Harness != "claude" && c.AgentRun.Harness != "codex" {
		fail(agent.ErrHarnessRemoved)
		return nil
	}
	work := coresearch.WorkContext{WorkspaceID: c.AgentRun.WorkspaceID, ProjectID: c.Coresearch.ProjectID, ChatID: c.Coresearch.ChatID}
	if c.Coresearch.PythonVersion != "" {
		work.PythonVersion = &c.Coresearch.PythonVersion
	}
	h, err := d.chat(ctx, work)
	if err != nil {
		fail(err)
		return nil
	}
	release := d.useChat(h)
	defer release()
	pol, err := LoadPolicy(d.cfg.StateDir, c.AgentRun.WorkspaceID)
	if err != nil {
		fail(err)
		return nil
	}
	r.PolicyDigest = pol.Digest
	reservation, err := agent.ReserveScratch(h.dir)
	if err != nil {
		fail(err)
		return nil
	}
	defer reservation.Release()
	base := "/api/daemon/agent-runs/" + c.AgentRun.ID.String()
	ref := reportBody(r)
	for _, op := range []string{"prepare-lease", "start"} {
		if err = d.api.Do(ctx, "POST", base+"/"+op, ref, nil); err != nil {
			fail(err)
			return nil
		}
	}
	runCtx, cancel := context.WithCancel(ctx)
	defer cancel()
	var watch sync.WaitGroup
	watch.Add(2)
	go func() { defer watch.Done(); d.watchCoresearch(runCtx, cancel, work, "", uuid.Nil) }()
	go func(ref Report) { defer watch.Done(); d.watchCancel(runCtx, cancel, base, ref) }(r)
	defer func() { cancel(); watch.Wait() }()
	changes, err := d.fillChanges(runCtx, work.ChatID, work.ProjectID)
	if err != nil {
		fail(err)
		return nil
	}
	prompt := c.AgentRun.Prompt
	if len(changes.Unsaved) > 0 {
		prompt += "\nUnsaved local changes: " + strings.Join(changes.Unsaved, ", ")
	}
	if len(changes.Deferred) > 0 {
		prompt += "\nRemote file updates deferred because prior chat writers may still be active: " + strings.Join(changes.Deferred, ", ") + ". Use lyk file get for an intentional refresh."
	}
	backend, err := d.cfg.NewBackend(c.AgentRun.Harness, d.cfg.Harnesses[c.AgentRun.Harness])
	if err != nil {
		fail(err)
		return nil
	}
	var seq int32
	var sinkMu sync.Mutex
	sink := func(m agent.Message) error {
		sinkMu.Lock()
		defer sinkMu.Unlock()
		if err := runCtx.Err(); err != nil {
			return err
		}
		if err := d.checkChat(runCtx, work); err != nil {
			cancel()
			return err
		}
		if seq == math.MaxInt32 {
			return errors.New("message sequence exhausted")
		}
		seq++
		return d.persistMessage(r, seq, m)
	}
	h.mu.Lock()
	study := h.study
	h.mu.Unlock()
	opts := agent.ExecOptions{Coresearch: true, SourcesURL: c.MCPURL, Dir: h.dir, ScratchReservation: reservation, Sandbox: d.cliSandbox(h.dir, study, false), MCPToken: c.Token, CacheDir: d.cacheDir(), OnMessage: sink, Env: []string{"LYK_TOKEN=" + c.Token, "LYK_SERVER=" + d.cfg.Server, "LYK_CONFIG=" + filepath.Join(h.dir, ".lyk", "config.json")}}
	if exe, e := os.Executable(); e == nil {
		opts.Env = append(opts.Env, "PATH="+filepath.Dir(exe)+string(os.PathListSeparator)+os.Getenv("PATH"))
	}
	if study != nil {
		opts.ReadDirs = append([]string{study.Dir}, study.Bases.Folders()...)
	}
	if cont, err = d.beginContinuity(c, pol); err != nil {
		fail(err)
		return nil
	}
	if cont != nil {
		opts.Resume, opts.ClaudeBaseline = cont.resume, cont.baseline
	}
	if opts.Resume != "" && !agent.SessionReachable(c.AgentRun.Harness, opts.Resume, opts.ConfigHome) {
		opts.Resume = ""
		opts.ClaudeBaseline = nil
	}
	execute := func() (agent.Result, error) {
		if err := d.checkChat(runCtx, work); err != nil {
			return agent.Result{}, err
		}
		if err := d.markChatWriter(h); err != nil {
			return agent.Result{}, err
		}
		session, e := backend.Execute(runCtx, prompt, opts)
		if e != nil {
			return agent.Result{}, e
		}
		var stream error
		for m := range session.Messages {
			if e = sink(m); e != nil {
				stream = errors.Join(stream, e)
				cancel()
			}
		}
		result, ok := <-session.Done
		if !ok {
			return result, errors.New("backend closed without a result")
		}
		return result, stream
	}
	final, err = execute()
	if err == nil && final.ResumeRefused && opts.Resume != "" && runCtx.Err() == nil {
		opts.Resume = ""
		opts.ClaudeBaseline = nil
		final, err = execute()
	}
	r.Usage, r.SessionID, r.Model = final.Usage, final.SessionID, final.Model
	if err != nil {
		fail(err)
	} else if final.Err != nil {
		fail(final.Err)
	}
	if runCtx.Err() != nil {
		fail(runCtx.Err())
		_ = d.api.Do(ctx, "POST", base+"/cancel-ack", ref, nil)
	}
	return localErr
}

func (d *daemon) hostForFolder(dir string) *chatHost {
	d.chatsMu.Lock()
	defer d.chatsMu.Unlock()
	for _, h := range d.chats {
		if h.dir == dir {
			return h
		}
	}
	return nil
}
func protectChatCLI(p *sandbox.Policy, dir string) {
	metadata := filepath.Join(dir, ".lyk")
	p.Write = append(p.Write, metadata)
	p.ReadOnly = append(p.ReadOnly, filepath.Join(metadata, "config.json"))
	p.WriteData = append(p.WriteData, filepath.Join(metadata, "fill.lock"))
}

// claimCoresearchWork is independent of ordinary CLI slots. Recorded execution
// and environment installation enter through dispatchCoresearchRun/Setup.
func (d *daemon) claimCoresearchWork(ctx context.Context, refs []runtimeRef) {
	for i := 0; i < len(refs) && ctx.Err() == nil; i += 64 {
		var work coresearch.Work
		if err := d.api.Do(ctx, "POST", "/api/daemon/coresearch/claim", map[string]any{"daemon_id": d.id, "runtimes": refs[i:min(i+64, len(refs))]}, &work); err != nil {
			slog.Warn("Coresearch work claim failed", "err", err)
			return
		}
		start := func(id uuid.UUID, fn func(context.Context) error) {
			d.mu.Lock()
			if d.coreWork == nil {
				d.coreWork = map[uuid.UUID]bool{}
			}
			if d.coreWork[id] {
				d.mu.Unlock()
				return
			}
			d.coreWork[id] = true
			d.mu.Unlock()
			d.wg.Add(1)
			go func() {
				defer d.wg.Done()
				defer func() { d.mu.Lock(); delete(d.coreWork, id); d.mu.Unlock(); d.signal() }()
				if err := fn(ctx); err != nil && !errors.Is(err, context.Canceled) {
					slog.Warn("Coresearch work ended", "id", id, "err", err)
				}
			}()
		}
		for _, cell := range work.Cells {
			start(cell.ID, func(ctx context.Context) error { return d.runCell(ctx, cell) })
		}
		for _, run := range work.Runs {
			start(run.ID, func(ctx context.Context) error { return d.dispatchCoresearchRun(ctx, run) })
		}
		for _, setup := range work.Setups {
			start(setup.ID, func(ctx context.Context) error { return d.dispatchCoresearchSetup(ctx, setup) })
		}
		for _, control := range work.Controls {
			// A separate key leaves controls runnable while that chat has an active cell.
			key := uuid.NewSHA1(control.ChatID, []byte(fmt.Sprintf("%s:%d", control.Op, control.Seq)))
			start(key, func(ctx context.Context) error { return d.controlChat(ctx, control) })
		}
	}
}

// These dispatch seams fail visibly until the corresponding executor is wired.
// They never acknowledge claimed work as successful without running it.
func (d *daemon) dispatchCoresearchRun(ctx context.Context, c coresearch.ClaimedRun) error {
	if err := d.checkChat(ctx, c.WorkContext); err != nil {
		return err
	}
	reason := "Coresearch recorded-run executor is not installed"
	var body bytes.Buffer
	writer := multipart.NewWriter(&body)
	if err := writer.WriteField("facts", string(mustMarshal(map[string]any{"status": "failed", "error": reason}))); err != nil {
		return err
	}
	if err := writer.Close(); err != nil {
		return err
	}
	req, err := http.NewRequestWithContext(ctx, "POST", strings.TrimRight(d.api.Server, "/")+d.corePath("runs/"+c.ID.String()+"/finish"), &body)
	if err != nil {
		return err
	}
	req.Header.Set("Authorization", "Bearer "+d.api.Token)
	req.Header.Set("X-Workspace-ID", c.WorkspaceID.String())
	req.Header.Set("Content-Type", writer.FormDataContentType())
	client := http.Client{Timeout: 30 * time.Second}
	if d.api.HTTP != nil {
		client = *d.api.HTTP
	}
	client.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	resp, err := client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != 204 {
		return fmt.Errorf("recorded-run failure settlement: HTTP %d", resp.StatusCode)
	}
	return errors.New(reason)
}
func (d *daemon) dispatchCoresearchSetup(ctx context.Context, c coresearch.ClaimedSetup) error {
	if err := d.checkChat(ctx, c.WorkContext); err != nil {
		return err
	}
	reason := "Coresearch environment executor is not installed"
	if err := d.coreAPI(c.WorkspaceID).Do(ctx, "POST", d.corePath("setups/"+c.ID.String()+"/finish"), map[string]string{"status": "failed", "log": reason}, nil); err != nil {
		return err
	}
	return errors.New(reason)
}

func mustMarshal(v any) []byte { b, _ := json.Marshal(v); return b }

func (d *daemon) useChat(h *chatHost) func() {
	release := h.use()
	return func() {
		// Keep the activity count nonzero until the trusted retention record is durable.
		policy, err := LoadPolicy(d.cfg.StateDir, h.work.WorkspaceID)
		if err == nil {
			err = d.markFinished(h.dir, map[string]any{"retention_days": policy.RetentionDays})
		}
		if err != nil {
			d.fatal(err)
		}
		release()
	}
}
func (d *daemon) gcChats() error {
	root := filepath.Join(d.cfg.Home, "coresearch")
	entries, err := os.ReadDir(root)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	d.chatsMu.Lock()
	defer d.chatsMu.Unlock()
	for _, entry := range entries {
		id, e := uuid.Parse(entry.Name())
		if e != nil || !entry.IsDir() {
			continue
		}
		dir := filepath.Join(root, entry.Name())
		record := finishedRecord(d.cfg.StateDir, dir)
		info, e := os.Lstat(record)
		if errors.Is(e, os.ErrNotExist) {
			continue
		}
		if e != nil {
			return e
		}
		if !info.Mode().IsRegular() {
			continue
		}
		var stored struct {
			RetentionDays int `json:"retention_days"`
		}
		data, e := readBounded(record, recordLimit)
		if e != nil {
			return e
		}
		if e = json.Unmarshal(data, &stored); e != nil {
			return e
		}
		keep := time.Duration(stored.RetentionDays) * 24 * time.Hour
		if stored.RetentionDays <= 0 {
			keep = defaultRetention * 24 * time.Hour
		}
		if time.Since(info.ModTime()) < keep {
			continue
		}
		h := d.chats[id]
		if h != nil {
			h.mu.Lock()
			busy := h.users != 0 || time.Since(h.lastUsed) < keep
			if !busy {
				e = h.closeLocked()
			}
			h.mu.Unlock()
			if e != nil {
				return e
			}
			if busy {
				continue
			}
		}
		reservation, e := agent.ReserveScratch(dir)
		if errors.Is(e, agent.ErrScratchBusy) {
			continue
		}
		if e != nil {
			return e
		}
		e = reservation.Remove()
		reservation.Release()
		if e != nil {
			return e
		}
		if e = d.unmarkFinished(dir); e != nil {
			return e
		}
		delete(d.chats, id)
	}
	return nil
}
