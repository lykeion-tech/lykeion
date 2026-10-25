package daemon

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"math"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"

	"lykeion/server/internal/apiclient"
	"lykeion/server/internal/daemon/envs"
	"lykeion/server/internal/middleware"
	"lykeion/server/internal/service"
	"lykeion/server/internal/toolroute"
	"lykeion/server/pkg/agent"
)

func (d *daemon) runAgentRun(ctx context.Context, c claimed) (localErr error) {
	r := Report{AgentRunID: c.AgentRun.ID, WorkspaceID: c.AgentRun.WorkspaceID, RuntimeID: *c.AgentRun.RuntimeID, Attempt: c.AgentRun.Attempt, Kind: "complete"}
	dir := filepath.Join(d.cfg.Home, scratchName(r.AgentRunID, r.Attempt))
	created := false
	var cont *continuity
	var final agent.Result
	fail := func(err error) { r.Kind = "fail"; r.Error = err.Error(); r.FailureReason = agent.ReasonAgentError }
	// Registered before the deferred report, so it runs after the finished
	// marker: gc never finds the folder unheld with only an older marker.
	var reservation *agent.ScratchReservation
	defer func() { reservation.Release() }()

	// Journal before the server can enter running or any CLI can launch.
	journalRecord := r
	journalRecord.Kind = "fail"
	journalRecord.FailureReason = agent.ReasonRuntimeOffline
	if err := d.journal().Put(journalRecord); err != nil {
		return err
	}
	defer func() {
		if ctx.Err() != nil {
			r.Kind = "fail"
			r.Error = ctx.Err().Error()
			r.FailureReason = agent.ReasonRuntimeOffline
		}
		if err := d.out.Put(r); err != nil {
			localErr = errors.Join(localErr, err)
			return
		}
		// Only after the final report is durable may a baseline become resumable.
		localErr = errors.Join(localErr, d.finishContinuity(cont, final))
		if created {
			if err := d.markFinished(dir, map[string]any{"ended_at": time.Now().UTC(), "attempt": r.Attempt, "retention_days": journalRecord.RetentionDays}); err != nil {
				localErr = errors.Join(localErr, err)
				return
			}
			// The run saved what it keeps as outputs; what its code installed
			// here can be gigabytes. A folder still there is left to gc; one
			// retired but not wholly deleted is gc's too, under its new name.
			if reservation != nil {
				reservation.Remove()
				if _, err := os.Lstat(dir); errors.Is(err, os.ErrNotExist) {
					localErr = errors.Join(localErr, d.unmarkFinished(dir))
				}
			}
		}
		localErr = errors.Join(localErr, d.forgetAttempt(r))
	}()
	if c.AgentRun.Harness == "cursor" || c.AgentRun.Harness == "antigravity" {
		fail(agent.ErrHarnessRemoved)
		return nil
	}
	// The attempt keeps this snapshot even if the policy file changes later.
	pol, err := LoadPolicy(d.cfg.StateDir, c.AgentRun.WorkspaceID)
	if err != nil {
		fail(fmt.Errorf("workspace policy: %w", err))
		return nil
	}
	r.Mode, r.PolicyDigest = ModeSandboxed, pol.Digest
	err = os.Mkdir(dir, 0700)
	if err != nil {
		fail(fmt.Errorf("prepare scratch: %w", err))
		return nil
	}
	created = true
	reservation, err = agent.ReserveScratch(dir)
	if err != nil {
		fail(fmt.Errorf("reserve scratch: %w", err))
		return nil
	}
	prompt := c.AgentRun.Prompt
	if c.AgentRun.Skill != "" {
		if err := os.WriteFile(filepath.Join(dir, roleFile(c.AgentRun.Harness)), []byte(c.AgentRun.Skill), 0600); err != nil {
			fail(err)
			return nil
		}
	}
	backend, err := d.cfg.NewBackend(c.AgentRun.Harness, d.cfg.Harnesses[c.AgentRun.Harness])
	if err != nil {
		fail(err)
		return nil
	}
	base := "/api/daemon/agent-runs/" + r.AgentRunID.String()
	ref := reportBody(r)
	if err = d.api.Do(ctx, "POST", base+"/prepare-lease", ref, nil); err != nil {
		fail(err)
		if retryableControlLoss(err) {
			r.FailureReason = agent.ReasonRuntimeOffline
		}
		return nil
	}
	if err = d.api.Do(ctx, "POST", base+"/start", ref, nil); err != nil {
		fail(err)
		if retryableControlLoss(err) {
			r.FailureReason = agent.ReasonRuntimeOffline
		}
		return nil
	}
	// The run's token fetches the files, so this follows the start; the CLI
	// never starts with only some of them.
	if err = d.placeAttachments(ctx, c, dir); err != nil {
		fail(err)
		if retryableControlLoss(err) {
			r.FailureReason = agent.ReasonRuntimeOffline
		}
		return nil
	}
	if err = placeClaimedSkills(c.AgentRun.Harness, dir, c.Skills); err != nil {
		fail(err)
		return nil
	}
	prompt += skillsNote(c.Skills)
	runCtx, cancel := context.WithCancel(ctx)
	defer cancel()
	watched := make(chan struct{})
	go func(ref Report) { defer close(watched); d.watchCancel(runCtx, cancel, base, ref) }(r)
	defer func() { cancel(); <-watched }()
	self, err := os.Executable()
	if err != nil {
		fail(err)
		return nil
	}
	var sinkErr error
	var sequence int32
	var sequenceMu sync.Mutex
	messageRef := Report{AgentRunID: r.AgentRunID, WorkspaceID: r.WorkspaceID, RuntimeID: r.RuntimeID, Attempt: r.Attempt}
	var namer *stepNamer
	appendMessage := func(m agent.Message) (int32, error) {
		sequenceMu.Lock()
		defer sequenceMu.Unlock()
		if sequence == math.MaxInt32 {
			return 0, errors.New("message sequence exhausted")
		}
		sequence++
		return sequence, d.persistMessage(messageRef, sequence, m)
	}
	sink := func(m agent.Message) error {
		seq, err := appendMessage(m)
		if err != nil {
			sinkErr = errors.Join(sinkErr, err)
			return err
		}
		if namer != nil {
			namer.saw(seq, m)
		}
		return nil
	}
	path := filepath.Dir(self) + string(os.PathListSeparator)
	// The study's add-on folders are the door's alone: code runs only
	// through it, in the sandbox, so the CLI's environment holds none of the
	// study's interpreters or folders, which the study's code may have planted.
	var study *envs.Study
	if d.bases != nil {
		// A study's runs share its add-on folders. A run on no card keeps its
		// own beside them, never in its run folder: code may write the run
		// folder, and a link it left there would be the next run's folder.
		// An attempt's own goes when the run ends.
		studies := filepath.Join(d.bases.Dir, "studies", c.AgentRun.WorkspaceID.String())
		studyDir := filepath.Join(studies, "_run", filepath.Base(dir))
		switch {
		case c.AgentRun.CardID != nil:
			studyDir = filepath.Join(studies, c.AgentRun.CardID.String())
		default:
			defer os.RemoveAll(studyDir)
		}
		if err = os.MkdirAll(studyDir, 0700); err != nil {
			fail(fmt.Errorf("prepare study folder: %w", err))
			return nil
		}
		study = envs.ForStudy(d.bases, studyDir)
	}
	opts := agent.ExecOptions{ScratchReservation: reservation, OnMessage: sink, Dir: dir, MCPToken: c.Token, Resume: c.AgentRun.SessionID, Model: c.AgentRun.RequestedModel, Env: []string{
		"LYK_TOKEN=" + c.Token, "LYK_SERVER=" + d.cfg.Server, "LYK_CONFIG=" + filepath.Join(dir, ".lyk-config.json"), "PATH=" + path + os.Getenv("PATH"),
	}}
	opts.CacheDir = d.cacheDir()
	opts.Sandbox = d.cliSandbox(dir, study, opts.ReadOnly)
	if study != nil {
		opts.ReadDirs = append([]string{study.Dir}, d.bases.Folders()...)
	}
	// The CLI's one MCP server; add-on servers are reached through it too. The
	// call token stays with the door. Child reads opts when a local command
	// starts, after any change below.
	door := &Door{Server: d.cfg.Server, Sources: c.MCPURL, RunToken: c.Token, CallToken: c.CallToken, Connectors: c.Connectors, WorkspaceConnectors: c.WorkspaceConnectors, Addons: c.MCP, Harness: c.AgentRun.Harness, Study: study,
		SandboxErr: d.sandboxErr, Child: func() agent.ExecOptions { return opts }}
	if err = door.Start(); err != nil {
		fail(err)
		return nil
	}
	defer door.Close()
	opts.DoorURL, opts.DoorToken = door.URL, door.Token
	opts.Env = append(opts.Env, "LYK_DOOR="+door.URL, "LYK_DOOR_TOKEN="+door.Token)
	if cont, err = d.beginContinuity(c, pol); err != nil {
		fail(err)
		return nil
	}
	note := func(text string) error {
		return sink(agent.Message{Type: "status", Content: rawJSON(map[string]string{"type": "continuity", "message": text})})
	}
	if cont != nil {
		opts.Resume, opts.ClaudeBaseline = cont.resume, cont.baseline
		if cont.note != "" {
			if err = note(cont.note); err != nil {
				fail(err)
				return errors.Join(err, d.abandonContinuity(cont))
			}
		}
	}
	// Resume only what this run's CLI can find; otherwise continue fresh.
	if opts.Resume != "" && !agent.SessionReachable(c.AgentRun.Harness, opts.Resume) {
		if err = note("session " + opts.Resume + " is not in the CLI's session store; starting a fresh session"); err != nil {
			fail(err)
			return errors.Join(err, d.abandonContinuity(cont))
		}
		opts.Resume, opts.ClaudeBaseline = "", nil
	}

	namer = newStepNamer(runCtx, func(ctx context.Context) (toolNamingSession, error) { return d.openToolNamer(ctx, c) }, func(titles agent.StepTitles) error {
		_, err := appendMessage(agent.Message{Type: "status", Content: rawJSON(titles)})
		return err
	})
	defer func() { usage, err := namer.finish(); r.NamingUsage = usage; localErr = errors.Join(localErr, err) }()
	sandboxNoted := false
	sandboxNote := func(runErr error) error {
		if runErr == nil || sandboxNoted {
			return nil
		}
		if hint := sandboxHint(runErr.Error()); hint != "" {
			sandboxNoted = true
			return sink(agent.Message{Type: "status", Content: rawJSON(map[string]string{"type": "sandbox", "message": hint})})
		}
		return nil
	}
	// execute returns the launch error, or the result and the stream error.
	execute := func() (res agent.Result, streamErr, launchErr error) {
		sess, err := backend.Execute(runCtx, prompt, opts)
		if err != nil {
			return res, sandboxNote(err), err
		}
		for message := range sess.Messages {
			if err := sink(message); err != nil {
				streamErr = errors.Join(streamErr, err)
				cancel()
			}
		}
		res, ok := <-sess.Done
		if !ok {
			res.Err = errors.New("backend closed without a result")
		}
		streamErr = errors.Join(streamErr, sandboxNote(res.Err))
		return res, streamErr, nil
	}
	res, streamErr, launchErr := execute()
	if launchErr != nil {
		fail(launchErr)
		return errors.Join(d.abandonContinuity(cont), streamErr)
	}
	// A refused resume did no model work, so one fresh session is safe.
	if cont != nil && opts.Resume != "" && res.ResumeRefused && streamErr == nil && sinkErr == nil && runCtx.Err() == nil {
		if err = note("the CLI refused to resume session " + opts.Resume + "; starting a fresh session"); err == nil {
			opts.Resume, opts.ClaudeBaseline = "", nil
			if res, streamErr, launchErr = execute(); launchErr != nil {
				fail(launchErr)
				final.Err = launchErr
				return streamErr
			}
		}
	}
	final = res
	if final.Err == nil && streamErr != nil {
		final.Err = streamErr
	}
	if sinkErr != nil {
		var oversized *oversizedMessageError
		if !errors.As(sinkErr, &oversized) {
			localErr = errors.Join(localErr, sinkErr)
		}
	}
	r.Usage, r.SessionID, r.Model = res.Usage, res.SessionID, res.Model
	if res.Err != nil {
		fail(res.Err)
		r.FailureReason = res.Reason
		if r.FailureReason == "" {
			r.FailureReason = agent.ReasonAgentError
		}
	}
	if streamErr != nil {
		fail(streamErr)
		var oversized *oversizedMessageError
		if !errors.As(streamErr, &oversized) {
			localErr = streamErr
		}
	}
	if runCtx.Err() != nil && ctx.Err() == nil {
		// The ACK is informational; durable replay carries final usage.
		if err = d.api.Do(ctx, "POST", base+"/cancel-ack", ref, nil); err != nil {
			slog.Warn("cancellation acknowledgement pending", "run", r.AgentRunID, "attempt", r.Attempt, "err", err)
		}
	}
	return localErr
}

// placeAttachments puts the study's files in the run folder's attachments
// folder, each read-only; the sandbox keeps the folder read-only too. Files
// come through a cache in the daemon's state folder, keyed by sha256, which
// no run can see. A cached file is checked before each use, and one that no
// longer matches is fetched again. The run's copy is a hard link to the
// cached file where the two share a file system: neither path is writable in
// the sandbox, and Seatbelt refuses a link to a path it denies writing, while
// bubblewrap's read-only mount keeps a link from leaving it, so code can't
// reach the cached file through another name.
func (d *daemon) placeAttachments(ctx context.Context, c claimed, dir string) error {
	if len(c.Attachments) == 0 {
		return nil
	}
	cache := filepath.Join(d.cfg.StateDir, "cache", "attachments")
	if err := os.MkdirAll(cache, 0o700); err != nil {
		return fmt.Errorf("attached files: %w", err)
	}
	into := filepath.Join(dir, "attachments")
	if err := os.Mkdir(into, 0o700); err != nil {
		return fmt.Errorf("attached files: %w", err)
	}
	// The server bounds a file's download; the daemon's client timeout would
	// cut a large one short.
	api := &apiclient.Client{Server: d.cfg.Server, Token: c.Token, HTTP: &http.Client{}}
	for _, a := range c.Attachments {
		if err := placeAttachment(ctx, api, cache, into, a); err != nil {
			return fmt.Errorf("attached file %q: %w", a.DiskName, err)
		}
	}
	return nil
}

func placeAttachment(ctx context.Context, api *apiclient.Client, cache, into string, a service.ClaimedAttachment) error {
	// The server sanitised the name; it is checked again before it names a path.
	if a.DiskName == "." || !filepath.IsLocal(a.DiskName) || strings.ContainsAny(a.DiskName, "/\\\x00") {
		return errors.New("unsafe file name")
	}
	if sum, err := hex.DecodeString(a.Sha256); err != nil || len(sum) != sha256.Size || a.SizeBytes < 0 {
		return errors.New("invalid digest or size")
	}
	cached := filepath.Join(cache, a.Sha256)
	if !matches(cached, a) {
		if err := fetchAttachment(ctx, api, cache, a); err != nil {
			return err
		}
	}
	dst := filepath.Join(into, a.DiskName)
	if os.Link(cached, dst) == nil {
		return nil
	}
	return copyReadOnly(cached, dst)
}

// matches reports whether path is a regular file of a's size and digest.
func matches(path string, a service.ClaimedAttachment) bool {
	f, err := os.Open(path)
	if err != nil {
		return false
	}
	defer f.Close()
	fi, err := f.Stat()
	if err != nil || !fi.Mode().IsRegular() || fi.Size() != a.SizeBytes {
		return false
	}
	h := sha256.New()
	if _, err := io.Copy(h, f); err != nil {
		return false
	}
	return hex.EncodeToString(h.Sum(nil)) == a.Sha256
}

// fetchAttachment downloads a into the cache: to a temporary file beside its
// place, checked for size and digest, then renamed into place read-only.
func fetchAttachment(ctx context.Context, api *apiclient.Client, cache string, a service.ClaimedAttachment) error {
	tmp, err := os.CreateTemp(cache, a.Sha256+"-*.part")
	if err != nil {
		return err
	}
	defer os.Remove(tmp.Name())
	defer tmp.Close()
	ctx, cancel := context.WithTimeout(ctx, middleware.AttachmentRequestTimeout)
	defer cancel()
	h := sha256.New()
	w := &capWriter{w: io.MultiWriter(tmp, h), left: a.SizeBytes}
	if err := api.Download(ctx, "/api/role/attachments/"+a.ID.String(), w); err != nil {
		return err
	}
	if w.left != 0 || hex.EncodeToString(h.Sum(nil)) != a.Sha256 {
		return errors.New("downloaded bytes don't match the file's size and sha256")
	}
	if err := tmp.Chmod(0o444); err != nil {
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	return os.Rename(tmp.Name(), filepath.Join(cache, a.Sha256))
}

// copyReadOnly copies src to a new file dst of mode 0444.
func copyReadOnly(src, dst string) error {
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()
	out, err := os.OpenFile(dst, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o444)
	if err != nil {
		return err
	}
	if _, err = io.Copy(out, in); err != nil {
		out.Close()
		return err
	}
	return out.Close()
}

// capWriter refuses bytes beyond left.
type capWriter struct {
	w    io.Writer
	left int64
}

func (c *capWriter) Write(p []byte) (int, error) {
	if int64(len(p)) > c.left {
		return 0, errors.New("more bytes than the file's size")
	}
	c.left -= int64(len(p))
	return c.w.Write(p)
}

func rawJSON(v any) json.RawMessage { b, _ := json.Marshal(v); return b }
func messageBody(r Report) map[string]any {
	return map[string]any{"workspace_id": r.WorkspaceID, "runtime_id": r.RuntimeID, "attempt": r.Attempt, "messages": r.Messages}
}

type oversizedMessageError struct {
	seq  int32
	size int
}

func (e *oversizedMessageError) Error() string {
	return fmt.Sprintf("message %d encoded size %d exceeds %d bytes; original evidence retained in daemon state", e.seq, e.size, middleware.MaxDaemonMessageBytes)
}

// stream persists byte-bounded batches before any send and drains through child
// exit even during cancellation. Network outages cannot discard its final batch.
func (d *daemon) stream(r Report, msgs <-chan agent.Message, cancel context.CancelFunc) error {
	r.Kind = "messages"
	var pending []wireMsg
	var seq int32
	var firstErr error
	flush := func() {
		if len(pending) == 0 {
			return
		}
		r.Messages = pending
		if err := d.messages.Put(r); err != nil {
			// Oversize is a run failure with preserved evidence. Persistence
			// failure must additionally stop the daemon and reach its caller.
			var oversized *oversizedMessageError
			if firstErr == nil || errors.As(firstErr, &oversized) {
				firstErr = err
			}
			cancel()
		}
		pending = nil
	}
	tick := time.NewTicker(time.Second)
	defer tick.Stop()
	for {
		select {
		case m, ok := <-msgs:
			if !ok {
				flush()
				return firstErr
			}
			if seq == math.MaxInt32 {
				if firstErr == nil {
					firstErr = errors.New("message sequence exhausted")
				}
				cancel()
				continue
			}
			seq++
			content := m.Content
			if len(content) == 0 {
				content = json.RawMessage(`null`)
			}
			wm := wireMsg{Seq: seq, Type: m.Type, Tool: m.Tool, Content: content}
			candidate := append(pending, wm)
			r.Messages = candidate
			b, err := json.Marshal(messageBody(r))
			if err != nil {
				if firstErr == nil {
					firstErr = err
				}
				cancel()
				continue
			}
			if len(b) > middleware.MaxDaemonMessageBytes {
				flush()
				r.Messages = []wireMsg{wm}
				b, err = json.Marshal(messageBody(r))
				if err != nil {
					if firstErr == nil {
						firstErr = err
					}
					cancel()
					continue
				}
				if len(b) > middleware.MaxDaemonMessageBytes {
					evidenceDir := filepath.Join(d.cfg.StateDir, "oversized-messages")
					if err := atomicJSON(evidenceDir, fmt.Sprintf("%s-%010d-%010d.json", r.AgentRunID, r.Attempt, seq), r); err != nil {
						firstErr = err
					} else if firstErr == nil {
						firstErr = &oversizedMessageError{seq: seq, size: len(b)}
					}
					cancel()
					continue
				}
				pending = []wireMsg{wm}
			} else {
				pending = candidate
			}
			if len(pending) >= 50 {
				flush()
			}
		case <-tick.C:
			flush()
		}
	}
}
func (d *daemon) watchCancel(ctx context.Context, cancel context.CancelFunc, base string, r Report) {
	q := url.Values{"workspace_id": {r.WorkspaceID.String()}, "runtime_id": {r.RuntimeID.String()}, "attempt": {strconv.Itoa(int(r.Attempt))}}
	t := time.NewTicker(5 * time.Second)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			var s struct{ Status string }
			err := d.api.Do(ctx, "GET", base+"/status?"+q.Encode(), nil, &s)
			var he *apiclient.HTTPError
			if (err == nil && s.Status != "running") || (errors.As(err, &he) && (he.Status == 404 || he.Status == 409)) {
				cancel()
				return
			}
		}
	}
}

// The CLI has not started: ambiguous control-plane acknowledgements are safe
// to settle as runtime loss, fenced by attempt, without rerunning the command.
func retryableControlLoss(err error) bool {
	var response *apiclient.HTTPError
	if errors.As(err, &response) {
		return response.Status == 408 || response.Status == 429 || response.Status >= 500
	}
	var transport *url.Error
	return errors.As(err, &transport) || errors.Is(err, context.DeadlineExceeded)
}

// The real adapter invokes this synchronously before accepting its next record.
func (d *daemon) persistMessage(r Report, seq int32, m agent.Message) error {
	r.Kind = "messages"
	content := m.Content
	if len(content) == 0 {
		content = json.RawMessage(`null`)
	}
	r.Messages = []wireMsg{{Seq: seq, Type: m.Type, Tool: m.Tool, Content: content}}
	b, err := json.Marshal(messageBody(r))
	if err != nil {
		return err
	}
	if len(b) > middleware.MaxDaemonMessageBytes {
		dir := filepath.Join(d.cfg.StateDir, "oversized-messages")
		if err := atomicJSON(dir, fmt.Sprintf("%s-%010d-%010d.json", r.AgentRunID, r.Attempt, seq), r); err != nil {
			return err
		}
		return &oversizedMessageError{seq: seq, size: len(b)}
	}
	return d.messages.Put(r)
}

var sandboxDiagnosticPath = regexp.MustCompile(`"(/[^"\r\n]+)"|'(/[^'\r\n]+)'|(/[^\s:;'"<>]+)`)

func sandboxHint(text string) string {
	if strings.Contains(text, "Not logged in") {
		return "The CLI couldn't read your sign-in inside Lykeion's sandbox. Sign in to the CLI on this machine, then run again."
	}
	if strings.Contains(text, "failed to initialize in-process app-server client") {
		return "Codex couldn't write its state inside Lykeion's sandbox."
	}
	for _, line := range strings.Split(text, "\n") {
		if !strings.Contains(line, "Operation not permitted") {
			continue
		}
		if match := sandboxDiagnosticPath.FindStringSubmatch(line); match != nil {
			for _, path := range match[1:] {
				if path != "" {
					return "Lykeion's sandbox blocked " + path + ". A personal MCP server or hook that needs it won't work in Lykeion runs."
				}
			}
		}
	}
	return ""
}

// placeClaimedSkills writes a Team member's own skills where its CLI reads
// skills, so the CLI finds them when it starts. Routing may later place the
// same skill again; it replaces the copy. The folder is made even with no
// skills: Claude Code only notices skills added to a folder it saw at start.
func placeClaimedSkills(harness, dir string, skills []toolroute.RoutedSkill) error {
	root, err := agent.SkillsDir(harness, dir)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(root, 0o700); err != nil {
		return err
	}
	for _, s := range skills {
		if err := placeRouted(harness, dir, s); err != nil {
			return fmt.Errorf("skill %s: %w", s.Name, err)
		}
	}
	return nil
}

// skillsNote tells the run its staged skills by the names its CLI lists them
// under, each with the name its Team's instructions use.
func skillsNote(skills []toolroute.RoutedSkill) string {
	if len(skills) == 0 {
		return ""
	}
	names := make([]string, len(skills))
	for i, s := range skills {
		names[i] = "`" + agent.PinnedName(s.Name) + "` (the Team calls it " + s.Name + ")"
	}
	return "\n\nYour skills are installed under these names: " + strings.Join(names, ", ") + ". Use one when its task comes up."
}

// roleFile is the instructions file the harness's CLI loads from its working
// folder on its own, so a run's role reaches it before the first turn.
func roleFile(harness string) string {
	if harness == "claude" {
		return "CLAUDE.md"
	}
	return "AGENTS.md"
}
