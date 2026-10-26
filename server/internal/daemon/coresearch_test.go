package daemon

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"syscall"
	"testing"
	"time"

	"github.com/google/uuid"
	"lykeion/server/internal/apiclient"
	"lykeion/server/internal/coresearch"
	"lykeion/server/internal/coresearch/localfiles"
	"lykeion/server/internal/service"
	"lykeion/server/pkg/agent"
)

type coreFixture struct {
	cancelled    atomic.Bool
	revoked      atomic.Bool
	finishes     atomic.Int32
	d            *daemon
	work         coresearch.WorkContext
	mu           sync.Mutex
	files        []coresearch.FileRef
	blobs        map[string]string
	finishedPath string
	finishedCell map[string]any
	states       []string
}

func newCoreFixture(t *testing.T) *coreFixture {
	t.Helper()
	f := &coreFixture{work: coresearch.WorkContext{ChatID: uuid.New(), ProjectID: uuid.New(), WorkspaceID: uuid.New()}, blobs: map[string]string{}}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		f.mu.Lock()
		defer f.mu.Unlock()
		if strings.HasPrefix(r.URL.Path, "/api/daemon/agent-runs/") {
			w.WriteHeader(204)
			return
		}
		if r.Header.Get("X-Workspace-ID") != f.work.WorkspaceID.String() || r.URL.Query().Get("daemon_id") != "machine" {
			http.Error(w, "identity missing", 403)
			return
		}
		switch {
		case strings.HasSuffix(r.URL.Path, "/status"):
			if f.revoked.Load() {
				http.Error(w, "revoked", 404)
				return
			}
			if f.cancelled.Load() && strings.Contains(r.URL.Path, "/cells/") {
				json.NewEncoder(w).Encode(map[string]string{"status": "cancelled"})
				return
			}
			json.NewEncoder(w).Encode(map[string]string{"status": "active"})
		case strings.HasSuffix(r.URL.Path, "/kernel"):
			var state struct{ State string }
			json.NewDecoder(r.Body).Decode(&state)
			f.states = append(f.states, state.State)
			w.WriteHeader(204)
		case strings.HasSuffix(r.URL.Path, "/files"):
			json.NewEncoder(w).Encode(f.files)
		case strings.Contains(r.URL.Path, "/blobs/"):
			_, _ = w.Write([]byte(f.blobs[filepath.Base(r.URL.Path)]))
		default:
			if strings.HasSuffix(r.URL.Path, "/finish") {
				f.finishes.Add(1)
				if strings.Contains(r.URL.Path, "/cells/") {
					f.finishedPath = r.URL.Path
					json.NewDecoder(r.Body).Decode(&f.finishedCell)
				}
			}
			w.WriteHeader(204)
		}
	}))
	t.Cleanup(srv.Close)
	rt := uuid.New()
	f.d = &daemon{cfg: Config{Home: t.TempDir(), StateDir: t.TempDir()}, api: &apiclient.Client{Server: srv.URL}, id: "machine", refs: []runtimeRef{{f.work.WorkspaceID, rt}}, ownerOf: map[uuid.UUID]uuid.UUID{rt: uuid.New()}}
	t.Cleanup(func() { f.d.stopChats() })
	_, err := f.d.chat(context.Background(), f.work)
	must(t, err)
	return f
}
func (f *coreFixture) saved(path, data string, n int32) {
	f.mu.Lock()
	defer f.mu.Unlock()
	sum := sha256.Sum256([]byte(data))
	sha := hex.EncodeToString(sum[:])
	f.blobs[sha] = data
	f.files = []coresearch.FileRef{{Path: path, N: n, SHA256: sha, Size: int64(len(data))}}
}
func (f *coreFixture) dir() string {
	return filepath.Join(f.d.cfg.Home, "coresearch", f.work.ChatID.String())
}
func TestFillWritesCurrentVersions(t *testing.T) {
	f := newCoreFixture(t)
	f.saved("data/a.txt", "first", 1)
	unsaved, e := f.d.fill(t.Context(), f.work.ChatID, f.work.ProjectID)
	must(t, e)
	if len(unsaved) != 0 {
		t.Fatal(unsaved)
	}
	f.saved("data/a.txt", "second", 2)
	_, e = f.d.fill(t.Context(), f.work.ChatID, f.work.ProjectID)
	must(t, e)
	b, e := os.ReadFile(filepath.Join(f.dir(), "data/a.txt"))
	must(t, e)
	if string(b) != "second" {
		t.Fatal(string(b))
	}
	folder, e := localfiles.Open(f.dir())
	must(t, e)
	defer folder.Close()
	m, e := folder.LockManifest(t.Context())
	must(t, e)
	defer m.Close()
	if m.Entries["data/a.txt"].N != 2 {
		t.Fatal(m.Entries)
	}
}
func TestFillKeepsLocalEdits(t *testing.T) {
	f := newCoreFixture(t)
	f.saved("a.txt", "first", 1)
	_, e := f.d.fill(t.Context(), f.work.ChatID, f.work.ProjectID)
	must(t, e)
	must(t, os.WriteFile(filepath.Join(f.dir(), "a.txt"), []byte("mine"), 0600))
	f.saved("a.txt", "second", 2)
	u, e := f.d.fill(t.Context(), f.work.ChatID, f.work.ProjectID)
	must(t, e)
	if !reflect.DeepEqual(u, []string{"a.txt"}) {
		t.Fatal(u)
	}
	folder, e := localfiles.Open(f.dir())
	must(t, e)
	defer folder.Close()
	m, e := folder.LockManifest(t.Context())
	must(t, e)
	defer m.Close()
	if m.Entries["a.txt"].N != 1 {
		t.Fatal("advanced unsaved base", m.Entries)
	}
	b, e := os.ReadFile(filepath.Join(f.dir(), "a.txt"))
	must(t, e)
	if string(b) != "mine" {
		t.Fatal(string(b))
	}
}
func TestFillNeverFollowsLinks(t *testing.T) {
	for _, parent := range []bool{false, true} {
		t.Run(map[bool]string{false: "leaf", true: "parent"}[parent], func(t *testing.T) {
			f := newCoreFixture(t)
			outside := t.TempDir()
			target := filepath.Join(outside, "a.txt")
			must(t, os.WriteFile(target, []byte("private"), 0600))
			path := "a.txt"
			if parent {
				path = "linked/a.txt"
				must(t, os.Symlink(outside, filepath.Join(f.dir(), "linked")))
			} else {
				must(t, os.Symlink(target, filepath.Join(f.dir(), path)))
			}
			f.saved(path, "remote", 1)
			if _, e := f.d.fill(t.Context(), f.work.ChatID, f.work.ProjectID); e == nil {
				t.Fatal("accepted link")
			}
			b, e := os.ReadFile(target)
			must(t, e)
			if string(b) != "private" {
				t.Fatal(string(b))
			}
		})
	}
}
func TestFillSkipsForbiddenPaths(t *testing.T) {
	for _, path := range []string{".CLAUDE/settings.json", "nested/.Git/config", ".lyk/fill.json"} {
		t.Run(path, func(t *testing.T) {
			f := newCoreFixture(t)
			f.saved(path, "malicious", 1)
			if _, e := f.d.fill(t.Context(), f.work.ChatID, f.work.ProjectID); e == nil {
				t.Fatal("accepted forbidden ref")
			}
		})
	}
}
func TestFillChecksDownloadSHA(t *testing.T) {
	f := newCoreFixture(t)
	f.saved("a.txt", "expected", 1)
	f.mu.Lock()
	for k := range f.blobs {
		f.blobs[k] = "tampered"
	}
	f.mu.Unlock()
	if _, e := f.d.fill(t.Context(), f.work.ChatID, f.work.ProjectID); e == nil {
		t.Fatal("accepted corrupt blob")
	}
	if _, e := os.Stat(filepath.Join(f.dir(), "a.txt")); !os.IsNotExist(e) {
		t.Fatal(e)
	}
}

func coreKernelFixture(t *testing.T) *coreFixture {
	f := newCoreFixture(t)
	study := fakeStudy(t, "python")
	f.d.bases = study.Bases
	h := f.d.chats[f.work.ChatID]
	h.study = study
	must(t, os.MkdirAll(filepath.Join(study.Dir, "python", "bin"), 0700))
	exe, e := os.Executable()
	must(t, e)
	must(t, os.Symlink(exe, filepath.Join(study.Dir, "python", "bin", "python")))
	return f
}
func TestCellClaimRunsInChatFolder(t *testing.T) {
	f := coreKernelFixture(t)
	t.Setenv("LYK_TOKEN", "lyk_private")
	c := coresearch.ClaimedCell{WorkContext: f.work, ID: uuid.New(), Kernel: "custom", Source: "host"}
	must(t, f.d.runCell(t.Context(), c))
	b, e := os.ReadFile(filepath.Join(f.dir(), "cell-host.json"))
	must(t, e)
	var got struct{ Dir, Token, CellID string }
	must(t, json.Unmarshal(b, &got))
	want, e := filepath.EvalSymlinks(f.dir())
	must(t, e)
	if got.Dir != want || got.Token != "" || got.CellID != c.ID.String() {
		t.Fatalf("host boundary: %+v", got)
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.finishedPath != "/api/daemon/coresearch/cells/"+c.ID.String()+"/finish" || f.finishedCell["execution_count"] != float64(1) {
		t.Fatalf("finish identity/count: %s %+v", f.finishedPath, f.finishedCell)
	}
	if _, ok := f.finishedCell["outputs"].([]any); !ok {
		t.Fatalf("noncanonical output transport: %+v", f.finishedCell)
	}
}
func TestKernelStopsWhenIdle(t *testing.T) {
	f := coreKernelFixture(t)
	must(t, f.d.runCell(t.Context(), coresearch.ClaimedCell{WorkContext: f.work, ID: uuid.New(), Kernel: "python3", Source: "host"}))
	h := f.d.chats[f.work.ChatID]
	h.mu.Lock()
	h.lastUsed = time.Now().Add(-chatIdle - time.Second)
	h.mu.Unlock()
	f.d.reapChats(t.Context())
	h.mu.Lock()
	defer h.mu.Unlock()
	if h.sessions != nil {
		t.Fatal("idle kernel still alive")
	}
}
func TestInterruptControl(t *testing.T) {
	f := coreKernelFixture(t)
	done := make(chan error, 1)
	go func() {
		done <- f.d.runCell(t.Context(), coresearch.ClaimedCell{WorkContext: f.work, ID: uuid.New(), Kernel: "python3", Source: "wait"})
	}()
	waitCoreFile(t, filepath.Join(f.dir(), "cell-wait"))
	must(t, f.d.controlChat(t.Context(), coresearch.ClaimedControl{WorkContext: f.work, Op: "interrupt", Seq: 7}))
	select {
	case e := <-done:
		must(t, e)
	case <-time.After(5 * time.Second):
		t.Fatal("cell was not interrupted")
	}
	h := f.d.chats[f.work.ChatID]
	h.mu.Lock()
	sessions := h.sessions
	h.mu.Unlock()
	if sessions == nil {
		t.Fatal("interrupt retired kernel")
	}
}
func waitCoreFile(t *testing.T, path string) {
	t.Helper()
	limit := time.After(10 * time.Second)
	for {
		if _, e := os.Stat(path); e == nil {
			return
		}
		select {
		case <-limit:
			t.Fatal("missing " + path)
		case <-time.After(10 * time.Millisecond):
		}
	}
}

func coreTurn(t *testing.T, f *coreFixture) claimed {
	t.Helper()
	d := f.d
	d.out = &Outbox{Dir: filepath.Join(d.cfg.StateDir, "outbox")}
	d.messages = &Outbox{Dir: filepath.Join(d.cfg.StateDir, "messages")}
	d.scopes = map[string]bool{}
	c := claimed{Coresearch: &service.ClaimedCoresearch{ChatID: f.work.ChatID, ProjectID: f.work.ProjectID}, Token: "ltk_scoped", MCPURL: "http://sources/mcp"}
	c.AgentRun.ID = uuid.New()
	c.AgentRun.WorkspaceID = f.work.WorkspaceID
	c.AgentRun.RuntimeID = &d.refs[0].RuntimeID
	c.AgentRun.ResponsibleUserID = d.ownerOf[*c.AgentRun.RuntimeID]
	c.AgentRun.Attempt = 1
	c.AgentRun.Harness = "claude"
	c.AgentRun.Prompt = "research"
	return c
}
func TestTurnUsesChatFolderAndNoDoor(t *testing.T) {
	f := newCoreFixture(t)
	c := coreTurn(t, f)
	called := false
	c.AgentRun.Skill = "do not inject"
	c.AgentRun.RequestedModel = "do not choose"
	f.d.cfg.NewBackend = func(string, string) (agent.Backend, error) {
		return backendFunc(func(ctx context.Context, p string, o agent.ExecOptions) (*agent.Session, error) {
			called = true
			if o.Dir != f.dir() || o.DoorURL != "" || o.DoorToken != "" || !o.Coresearch || o.SourcesURL != c.MCPURL || o.Model != "" || strings.Contains(p, "do not inject") {
				t.Fatalf("unexpected turn: %+v %s", o, p)
			}
			for _, v := range o.Env {
				if strings.HasPrefix(v, "LYK_DOOR=") {
					t.Fatal(v)
				}
			}
			if !slices.Contains(o.Env, "LYK_CONFIG="+filepath.Join(f.dir(), ".lyk/config.json")) {
				t.Fatal(o.Env)
			}
			return finishedSession(agent.Result{}), nil
		}), nil
	}
	must(t, f.d.runAgentRun(t.Context(), c))
	if !called {
		t.Fatal("turn never launched")
	}
}
func TestTurnFolderSurvives(t *testing.T) {
	f := newCoreFixture(t)
	c := coreTurn(t, f)
	f.d.cfg.NewBackend = func(string, string) (agent.Backend, error) {
		return backendFunc(func(_ context.Context, _ string, o agent.ExecOptions) (*agent.Session, error) {
			must(t, os.WriteFile(filepath.Join(o.Dir, "kept.txt"), []byte("keep"), 0600))
			return finishedSession(agent.Result{}), nil
		}), nil
	}
	must(t, f.d.runAgentRun(t.Context(), c))
	b, e := os.ReadFile(filepath.Join(f.dir(), "kept.txt"))
	must(t, e)
	if string(b) != "keep" {
		t.Fatal(string(b))
	}
}
func TestCoresearchNeverAsGuest(t *testing.T) {
	f := newCoreFixture(t)
	c := coreTurn(t, f)
	c.AgentRun.ResponsibleUserID = uuid.New()
	called := false
	f.d.cfg.NewBackend = func(string, string) (agent.Backend, error) { called = true; return nil, errors.New("must not launch") }
	must(t, f.d.runAgentRun(t.Context(), c))
	r, e := readReport(f.d.out.file(Report{AgentRunID: c.AgentRun.ID, Attempt: 1}))
	must(t, e)
	if called || r.Kind != "fail" || !strings.Contains(r.Error, "own") {
		t.Fatalf("guest boundary: %t %+v", called, r)
	}
}
func finishedSession(r agent.Result) *agent.Session {
	m := make(chan agent.Message)
	close(m)
	done := make(chan agent.Result, 1)
	done <- r
	close(done)
	return &agent.Session{Messages: m, Done: done}
}

func TestCoresearchChatsRunConcurrentlyWithOneSlot(t *testing.T) {
	f := coreKernelFixture(t)
	d := f.d
	d.cfg.Slots = 1
	d.active = map[string]bool{"ordinary": true}
	d.wake = make(chan struct{}, 1)
	d.failures = make(chan error, 1)
	first := coreTurn(t, f)
	second := first
	second.AgentRun.ID = uuid.New()
	second.Coresearch = &service.ClaimedCoresearch{ChatID: uuid.New(), ProjectID: f.work.ProjectID}
	started := make(chan string, 2)
	release := make(chan struct{})
	defer close(release)
	d.cfg.NewBackend = func(string, string) (agent.Backend, error) {
		return backendFunc(func(ctx context.Context, _ string, o agent.ExecOptions) (*agent.Session, error) {
			started <- o.Dir
			m := make(chan agent.Message)
			done := make(chan agent.Result, 1)
			go func() {
				select {
				case <-release:
				case <-ctx.Done():
				}
				close(m)
				done <- agent.Result{}
				close(done)
			}()
			return &agent.Session{Messages: m, Done: done}, nil
		}), nil
	}
	original := d.api.Server
	cellFinished := make(chan struct{}, 1)
	var modeCalls, workCalls int
	var mu sync.Mutex
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasSuffix(r.URL.Path, "/agent-runs/claim") {
			var body struct {
				Core bool `json:"coresearch_only"`
			}
			json.NewDecoder(r.Body).Decode(&body)
			if !body.Core {
				t.Error("ordinary claim ran while full")
			}
			mu.Lock()
			modeCalls++
			n := modeCalls
			mu.Unlock()
			if n == 1 {
				json.NewEncoder(w).Encode([]claimed{first, second})
			} else {
				json.NewEncoder(w).Encode([]claimed{})
			}
			return
		}
		if r.URL.Path == "/api/daemon/coresearch/claim" {
			mu.Lock()
			workCalls++
			n := workCalls
			mu.Unlock()
			work := coresearch.EmptyWork()
			if n == 2 {
				work.Cells = []coresearch.ClaimedCell{{WorkContext: f.work, ID: uuid.New(), Kernel: "python3", Source: "host"}}
			}
			json.NewEncoder(w).Encode(work)
			return
		}
		if strings.Contains(r.URL.Path, "/cells/") && strings.HasSuffix(r.URL.Path, "/finish") {
			cellFinished <- struct{}{}
		}
		req, _ := http.NewRequestWithContext(r.Context(), r.Method, original+r.URL.RequestURI(), r.Body)
		req.Header = r.Header
		resp, e := http.DefaultClient.Do(req)
		if e != nil {
			http.Error(w, e.Error(), 500)
			return
		}
		defer resp.Body.Close()
		w.WriteHeader(resp.StatusCode)
		io.Copy(w, resp.Body)
	}))
	defer srv.Close()
	d.api.Server = srv.URL
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	d.claim(ctx)
	for range 2 {
		select {
		case <-started:
		case <-time.After(5 * time.Second):
			t.Fatal("Coresearch chat blocked by Slots=1")
		}
	}
	d.claim(ctx)
	mu.Lock()
	n := workCalls
	mu.Unlock()
	if n < 2 {
		t.Fatal("work polling stopped while turns active")
	}
	select {
	case <-cellFinished:
	case <-time.After(5 * time.Second):
		t.Fatal("turns starved their queued cell")
	}
	cancel()
	d.wg.Wait()
}

func TestCoresearchRevocationStopsSubmittedCell(t *testing.T) {
	f := coreKernelFixture(t)
	done := make(chan error, 1)
	go func() {
		done <- f.d.runCell(t.Context(), coresearch.ClaimedCell{WorkContext: f.work, ID: uuid.New(), Kernel: "python3", Source: "wait"})
	}()
	waitCoreFile(t, filepath.Join(f.dir(), "cell-wait"))
	raw, e := os.ReadFile(filepath.Join(f.dir(), "cell-host.json"))
	must(t, e)
	var host map[string]string
	must(t, json.Unmarshal(raw, &host))
	pid, e := strconv.Atoi(host["PID"])
	must(t, e)
	f.revoked.Store(true)
	select {
	case e := <-done:
		if e == nil {
			t.Fatal("revoked cell succeeded")
		}
	case <-time.After(5 * time.Second):
		t.Fatal("revoked cell still executing")
	}
	if syscall.Kill(pid, 0) == nil {
		t.Fatal("revoked bridge process is still alive")
	}
	if f.finishes.Load() != 0 {
		t.Fatal("content sent after revocation")
	}
	// A new invocation must pass authority again before opening any kernel.
	if e := f.d.runCell(t.Context(), coresearch.ClaimedCell{WorkContext: f.work, ID: uuid.New(), Kernel: "python3", Source: "host"}); e == nil {
		t.Fatal("revoked chat reopened")
	}
}
func TestCoresearchRevocationStopsIdleKernel(t *testing.T) {
	f := coreKernelFixture(t)
	must(t, f.d.runCell(t.Context(), coresearch.ClaimedCell{WorkContext: f.work, ID: uuid.New(), Kernel: "python3", Source: "host"}))
	f.revoked.Store(true)
	f.d.reapChats(t.Context())
	h := f.d.chats[f.work.ChatID]
	h.mu.Lock()
	defer h.mu.Unlock()
	if h.sessions != nil {
		t.Fatal("revoked idle kernel survived")
	}
}

func TestCoresearchFolderRetention(t *testing.T) {
	f := newCoreFixture(t)
	h := f.d.chats[f.work.ChatID]
	release := f.d.useChat(h)
	release()
	record := finishedRecord(f.d.cfg.StateDir, h.dir)
	old := time.Now().Add(-31 * 24 * time.Hour)
	must(t, os.Chtimes(record, old, old))
	h.mu.Lock()
	h.lastUsed = old
	h.mu.Unlock()
	must(t, f.d.gc(t.Context()))
	if _, e := os.Stat(h.dir); !os.IsNotExist(e) {
		t.Fatalf("expired chat folder retained: %v", e)
	}
}

func TestCoresearchRealCLITurnFolderAndConfig(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	t.Setenv("CLAUDE_CONFIG_DIR", filepath.Join(os.Getenv("HOME"), ".claude"))
	t.Setenv("CODEX_HOME", "")
	t.Setenv("LYK_DOOR", "ambient-door")
	t.Setenv("LYK_TOKEN", "lyk_person")
	f := newCoreFixture(t)
	c := coreTurn(t, f)
	bin := fakeClaude(t)
	original, e := os.ReadFile(bin)
	must(t, e)
	probe := `printf '%s\n' "$PWD" "${LYK_DOOR-unset}" "$LYK_CONFIG" > cli-boundary
`
	must(t, os.WriteFile(bin, []byte(strings.Replace(string(original), "#!/bin/sh\n", "#!/bin/sh\n"+probe, 1)), 0700))
	f.d.cfg.NewBackend = agent.New
	f.d.cfg.Harnesses = map[string]string{"claude": bin}
	must(t, f.d.runAgentRun(t.Context(), c))
	r, e := readReport(f.d.out.file(Report{AgentRunID: c.AgentRun.ID, Attempt: 1}))
	must(t, e)
	if r.Kind != "complete" {
		t.Fatalf("CLI failed: %+v", r)
	}
	raw, e := os.ReadFile(filepath.Join(f.dir(), "cli-boundary"))
	must(t, e)
	dir, e := filepath.EvalSymlinks(f.dir())
	must(t, e)
	if string(raw) != dir+"\nunset\n"+filepath.Join(dir, ".lyk/config.json")+"\n" {
		t.Fatalf("CLI boundary: %s", raw)
	}
	raw, e = os.ReadFile(filepath.Join(f.dir(), ".lyk/config.json"))
	must(t, e)
	if string(raw) != "{}\n" {
		t.Fatal("nonempty CLI config")
	}
}
func TestCoresearchRevocationStopsActualCLI(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	t.Setenv("CLAUDE_CONFIG_DIR", filepath.Join(os.Getenv("HOME"), ".claude"))
	t.Setenv("CODEX_HOME", "")
	f := newCoreFixture(t)
	c := coreTurn(t, f)
	bin := filepath.Join(os.Getenv("HOME"), "waiting-cli")
	must(t, os.WriteFile(bin, []byte("#!/bin/sh\nprintf '%s' $$ > cli-pid\nsleep 60\n"), 0700))
	f.d.cfg.NewBackend = agent.New
	f.d.cfg.Harnesses = map[string]string{"claude": bin}
	done := make(chan error, 1)
	go func() { done <- f.d.runAgentRun(t.Context(), c) }()
	waitCoreFile(t, filepath.Join(f.dir(), "cli-pid"))
	raw, e := os.ReadFile(filepath.Join(f.dir(), "cli-pid"))
	must(t, e)
	pid, e := strconv.Atoi(string(raw))
	must(t, e)
	f.revoked.Store(true)
	select {
	case e := <-done:
		must(t, e)
	case <-time.After(7 * time.Second):
		t.Fatal("revoked CLI did not exit")
	}
	if syscall.Kill(pid, 0) == nil {
		t.Fatal("revoked CLI process is still alive")
	}
}

func TestCoresearchDaemonStopReportsStopped(t *testing.T) {
	f := coreKernelFixture(t)
	must(t, f.d.runCell(t.Context(), coresearch.ClaimedCell{WorkContext: f.work, ID: uuid.New(), Kernel: "python3", Source: "host"}))
	f.d.stopChats()
	f.mu.Lock()
	defer f.mu.Unlock()
	if len(f.states) == 0 || f.states[len(f.states)-1] != "stopped" {
		t.Fatal("shutdown state", f.states)
	}
}

func TestCoresearchCancelledWorkRetiresCell(t *testing.T) {
	f := coreKernelFixture(t)
	done := make(chan error, 1)
	go func() {
		done <- f.d.runCell(t.Context(), coresearch.ClaimedCell{WorkContext: f.work, ID: uuid.New(), Kernel: "python3", Source: "wait"})
	}()
	waitCoreFile(t, filepath.Join(f.dir(), "cell-wait"))
	raw, e := os.ReadFile(filepath.Join(f.dir(), "cell-host.json"))
	must(t, e)
	var host map[string]string
	must(t, json.Unmarshal(raw, &host))
	pid, e := strconv.Atoi(host["PID"])
	must(t, e)
	f.cancelled.Store(true)
	select {
	case e := <-done:
		if !errors.Is(e, context.Canceled) {
			t.Fatalf("cancel outcome: %v", e)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("cancelled cell still running")
	}
	if syscall.Kill(pid, 0) == nil || f.finishes.Load() != 0 {
		t.Fatal("cancelled process or output survived")
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.states[len(f.states)-1] != "stopped" {
		t.Fatal("cancelled kernel state", f.states)
	}
}

func TestCoresearchUnsupportedDispatchSettlesBothKinds(t *testing.T) {
	f := newCoreFixture(t)
	runID, setupID := uuid.New(), uuid.New()
	original := f.d.api.Server
	var mu sync.Mutex
	settled := map[string]string{}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/api/daemon/coresearch/claim" {
			work := coresearch.EmptyWork()
			work.Runs = []coresearch.ClaimedRun{{WorkContext: f.work, ID: runID}}
			work.Setups = []coresearch.ClaimedSetup{{WorkContext: f.work, ID: setupID}}
			json.NewEncoder(w).Encode(work)
			return
		}
		if strings.HasSuffix(r.URL.Path, "/finish") {
			var result struct{ Status string }
			if strings.Contains(r.URL.Path, "/runs/") {
				reader, e := r.MultipartReader()
				if e != nil {
					t.Error(e)
					w.WriteHeader(400)
					return
				}
				part, e := reader.NextPart()
				if e != nil {
					t.Error(e)
					w.WriteHeader(400)
					return
				}
				if part.FormName() != "facts" {
					t.Error("missing facts")
				}
				json.NewDecoder(part).Decode(&result)
			} else {
				json.NewDecoder(r.Body).Decode(&result)
			}
			mu.Lock()
			settled[r.URL.Path] = result.Status
			mu.Unlock()
			w.WriteHeader(204)
			return
		}
		req, _ := http.NewRequestWithContext(r.Context(), r.Method, original+r.URL.RequestURI(), r.Body)
		req.Header = r.Header
		resp, e := http.DefaultClient.Do(req)
		if e != nil {
			http.Error(w, e.Error(), 500)
			return
		}
		defer resp.Body.Close()
		w.WriteHeader(resp.StatusCode)
		io.Copy(w, resp.Body)
	}))
	defer srv.Close()
	f.d.api.Server = srv.URL
	f.d.claimCoresearchWork(t.Context(), f.d.refs)
	f.d.wg.Wait()
	mu.Lock()
	defer mu.Unlock()
	for _, path := range []string{"/api/daemon/coresearch/runs/" + runID.String() + "/finish", "/api/daemon/coresearch/setups/" + setupID.String() + "/finish"} {
		if settled[path] != "failed" {
			t.Fatalf("unsettled claimed work: %+v", settled)
		}
	}
}

func TestCoresearchProjectJupyterPathsPrecedeBases(t *testing.T) {
	study := fakeStudy(t, "python")
	for _, name := range []string{"conda", "python"} {
		must(t, os.MkdirAll(filepath.Join(study.Dir, name, "share/jupyter"), 0700))
	}
	base := []string{"JUPYTER_PATH=/base/one:/base/two", "OTHER=kept"}
	out := coreJupyterEnv(study, base)
	want := "JUPYTER_PATH=" + filepath.Join(study.Dir, "conda/share/jupyter") + ":" + filepath.Join(study.Dir, "python/share/jupyter") + ":/base/one:/base/two"
	if !slices.Contains(out, want) || !slices.Contains(out, "OTHER=kept") || base[0] != "JUPYTER_PATH=/base/one:/base/two" {
		t.Fatalf("project environment order: %v original %v", out, base)
	}
	count := 0
	for _, v := range out {
		if strings.HasPrefix(v, "JUPYTER_PATH=") {
			count++
		}
	}
	if count != 1 {
		t.Fatal("duplicate path values", out)
	}
}
