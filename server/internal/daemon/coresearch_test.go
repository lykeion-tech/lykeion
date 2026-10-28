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
	f := &coreFixture{work: coresearch.WorkContext{ChatID: uuid.New(), ProjectID: uuid.New(), WorkspaceID: uuid.New(), ClaimToken: uuid.New()}, blobs: map[string]string{}}
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
		case strings.HasSuffix(r.URL.Path, "/status") || strings.HasSuffix(r.URL.Path, "/heartbeat"):
			if f.revoked.Load() {
				http.Error(w, "revoked", 404)
				return
			}
			if f.cancelled.Load() && strings.Contains(r.URL.Path, "/cells/") {
				json.NewEncoder(w).Encode(map[string]string{"status": "cancelled"})
				return
			}
			status := "active"
			if strings.Contains(r.URL.Path, "/runs/") || strings.Contains(r.URL.Path, "/setups/") || strings.Contains(r.URL.Path, "/cells/") {
				status = "running"
			}
			json.NewEncoder(w).Encode(map[string]string{"status": status})
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
				if f.revoked.Load() {
					http.Error(w, "revoked", 404)
					return
				}
				if f.cancelled.Load() {
					http.Error(w, "cancelled", 409)
					return
				}
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

// Pause the HTTP response before any bytes are returned, so the destination
// edit is deterministically inside the old read/download/rename window.
type coreRoundTrip func(*http.Request) (*http.Response, error)

func (f coreRoundTrip) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

func TestFillKeepsEditDuringDownload(t *testing.T) {
	for _, existing := range []bool{true, false} {
		t.Run(strconv.FormatBool(existing), func(t *testing.T) {
			f := newCoreFixture(t)
			if existing {
				f.saved("a.txt", "first", 1)
				_, err := f.d.fill(t.Context(), f.work.ChatID, f.work.ProjectID)
				must(t, err)
			}
			f.saved("a.txt", "second", 2)
			started, release := make(chan struct{}), make(chan struct{})
			f.d.api.HTTP = &http.Client{Transport: coreRoundTrip(func(r *http.Request) (*http.Response, error) {
				if strings.Contains(r.URL.Path, "/blobs/") {
					close(started)
					<-release
				}
				return http.DefaultTransport.RoundTrip(r)
			})}
			type result struct {
				unsaved []string
				err     error
			}
			done := make(chan result, 1)
			go func() { u, err := f.d.fill(t.Context(), f.work.ChatID, f.work.ProjectID); done <- result{u, err} }()
			<-started
			must(t, os.WriteFile(filepath.Join(f.dir(), "a.txt"), []byte("local-edit-during-download"), 0600))
			close(release)
			r := <-done
			must(t, r.err)
			data, err := os.ReadFile(filepath.Join(f.dir(), "a.txt"))
			must(t, err)
			if string(data) != "local-edit-during-download" || !reflect.DeepEqual(r.unsaved, []string{"a.txt"}) {
				t.Fatalf("concurrent local edit lost: bytes=%q unsaved=%v", data, r.unsaved)
			}
			folder, err := localfiles.Open(f.dir())
			must(t, err)
			defer folder.Close()
			m, err := folder.LockManifest(t.Context())
			must(t, err)
			defer m.Close()
			want := 0
			if existing {
				want = 1
			}
			if m.Entries["a.txt"].N != want {
				t.Fatalf("advanced conflicting pin: %v", m.Entries)
			}
		})
	}
}

func TestCoresearchCachedHostAcceptsClaimedPython(t *testing.T) {
	f := newCoreFixture(t)
	f.d.bases = fakeStudy(t).Bases
	delete(f.d.chats, f.work.ChatID)
	h, err := f.d.chat(t.Context(), f.work)
	must(t, err)
	other := f.work
	other.ChatID = uuid.New()
	sibling, err := f.d.chat(t.Context(), other)
	must(t, err)
	if h.study.RecordedPython() != "" || sibling.study.RecordedPython() != "" {
		t.Fatal("fixture already pinned")
	}
	version := "3.12"
	updated := f.work
	updated.PythonVersion = &version
	got, err := f.d.chat(t.Context(), updated)
	must(t, err)
	if got != h || h.study.PythonVersion != version || h.study.RecordedPython() != version || sibling.study.RecordedPython() != version {
		t.Fatalf("claim was not reconciled with cached/shared Study: field=%q shared=%q sibling=%q", h.study.PythonVersion, h.study.RecordedPython(), sibling.study.RecordedPython())
	}
	_, err = f.d.chat(t.Context(), f.work)
	must(t, err)
	if h.study.PythonVersion != version {
		t.Fatal("older unpinned claim cleared current selection")
	}
}

func TestCoresearchClaimedPythonRetiresCachedSession(t *testing.T) {
	f := coreKernelFixture(t)
	cell := coresearch.ClaimedCell{WorkContext: f.work, ID: uuid.New(), Kernel: "python3", Source: "host"}
	must(t, f.d.runCell(t.Context(), cell))
	h := f.d.chats[f.work.ChatID]
	old := h.sessions
	version := "3.13"
	cell.PythonVersion = &version
	cell.ID = uuid.New()
	must(t, f.d.runCell(t.Context(), cell))
	if h.sessions == old {
		t.Fatal("incompatible cached session was reused")
	}
	if _, err := old.CellWithID(t.Context(), uuid.NewString(), "python3", "host"); err == nil {
		t.Fatal("old session was not retired")
	}
}

func TestFillDefersAfterWriterLaunchAndReload(t *testing.T) {
	f := newCoreFixture(t)
	f.saved("a.txt", "first", 1)
	_, err := f.d.fill(t.Context(), f.work.ChatID, f.work.ProjectID)
	must(t, err)
	must(t, f.d.markChatWriter(f.d.chats[f.work.ChatID]))
	f.saved("a.txt", "second", 2)
	for _, reload := range []bool{false, true} {
		if reload {
			f.d.chats = nil
			_, err = f.d.chat(t.Context(), f.work)
			must(t, err)
		}
		changes, err := f.d.fillChanges(t.Context(), f.work.ChatID, f.work.ProjectID)
		must(t, err)
		if len(changes.Unsaved) != 0 || !reflect.DeepEqual(changes.Deferred, []string{"a.txt"}) {
			t.Fatalf("untruthful deferral: %+v", changes)
		}
		b, err := os.ReadFile(filepath.Join(f.dir(), "a.txt"))
		must(t, err)
		if string(b) != "first" {
			t.Fatalf("deferred bytes replaced: %q", b)
		}
		folder, err := localfiles.Open(f.dir())
		must(t, err)
		m, err := folder.LockManifest(t.Context())
		must(t, err)
		if m.Entries["a.txt"].N != 1 {
			t.Fatal("deferred pin advanced", m.Entries)
		}
		m.Close()
		folder.Close()
	}
	f.saved("new.txt", "new", 1)
	changes, err := f.d.fillChanges(t.Context(), f.work.ChatID, f.work.ProjectID)
	must(t, err)
	if len(changes.Unsaved)+len(changes.Deferred) != 0 {
		t.Fatal(changes)
	}
	data, err := os.ReadFile(filepath.Join(f.dir(), "new.txt"))
	must(t, err)
	if string(data) != "new" {
		t.Fatal(string(data))
	}
}

func TestFillDefersWriterLaunchedDuringDownload(t *testing.T) {
	f := newCoreFixture(t)
	f.saved("a.txt", "first", 1)
	_, err := f.d.fill(t.Context(), f.work.ChatID, f.work.ProjectID)
	must(t, err)
	f.saved("a.txt", "second", 2)
	started, release := make(chan struct{}), make(chan struct{})
	f.d.api.HTTP = &http.Client{Transport: coreRoundTrip(func(r *http.Request) (*http.Response, error) {
		if strings.Contains(r.URL.Path, "/blobs/") {
			close(started)
			<-release
		}
		return http.DefaultTransport.RoundTrip(r)
	})}
	type result struct {
		changes fillChanges
		err     error
	}
	done := make(chan result, 1)
	go func() { c, e := f.d.fillChanges(t.Context(), f.work.ChatID, f.work.ProjectID); done <- result{c, e} }()
	<-started
	must(t, f.d.markChatWriter(f.d.chats[f.work.ChatID]))
	close(release)
	r := <-done
	must(t, r.err)
	if !reflect.DeepEqual(r.changes.Deferred, []string{"a.txt"}) || len(r.changes.Unsaved) != 0 {
		t.Fatal(r.changes)
	}
	b, err := os.ReadFile(filepath.Join(f.dir(), "a.txt"))
	must(t, err)
	if string(b) != "first" {
		t.Fatal("launch gate failed", string(b))
	}
}

func TestTurnReportsDeferredFilesSeparately(t *testing.T) {
	f := newCoreFixture(t)
	f.saved("a.txt", "first", 1)
	_, err := f.d.fill(t.Context(), f.work.ChatID, f.work.ProjectID)
	must(t, err)
	c := coreTurn(t, f)
	f.d.cfg.NewBackend = func(string, string) (agent.Backend, error) {
		return backendFunc(func(context.Context, string, agent.ExecOptions) (*agent.Session, error) {
			return finishedSession(agent.Result{}), nil
		}), nil
	}
	must(t, f.d.runAgentRun(t.Context(), c))
	f.saved("a.txt", "second", 2)
	c = coreTurn(t, f)
	called := false
	f.d.cfg.NewBackend = func(string, string) (agent.Backend, error) {
		return backendFunc(func(_ context.Context, prompt string, _ agent.ExecOptions) (*agent.Session, error) {
			called = true
			if !strings.Contains(prompt, "Remote file updates deferred") || !strings.Contains(prompt, "lyk file get") || strings.Contains(prompt, "Unsaved local changes") {
				t.Fatal(prompt)
			}
			return finishedSession(agent.Result{}), nil
		}), nil
	}
	must(t, f.d.runAgentRun(t.Context(), c))
	if !called {
		t.Fatal("turn never launched")
	}
}

func TestCoresearchRetiredKernelDoesNotProveWriterAbsence(t *testing.T) {
	f := coreKernelFixture(t)
	f.saved("a.txt", "first", 1)
	must(t, f.d.runCell(t.Context(), coresearch.ClaimedCell{WorkContext: f.work, ID: uuid.New(), Kernel: "python3", Source: "host"}))
	f.d.stopChats()
	f.saved("a.txt", "second", 2)
	changes, err := f.d.fillChanges(t.Context(), f.work.ChatID, f.work.ProjectID)
	must(t, err)
	if !reflect.DeepEqual(changes.Deferred, []string{"a.txt"}) {
		t.Fatal("retirement incorrectly reset writer history", changes)
	}
}

func TestCoresearchSharedPythonRetiresSiblingSession(t *testing.T) {
	f := coreKernelFixture(t)
	h := f.d.chats[f.work.ChatID]
	other := f.work
	other.ChatID = uuid.New()
	sibling, err := f.d.chat(t.Context(), other)
	must(t, err)
	sibling.study = h.study.ForRun()
	must(t, f.d.runCell(t.Context(), coresearch.ClaimedCell{WorkContext: other, ID: uuid.New(), Kernel: "python3", Source: "host"}))
	old := sibling.sessions
	oldView := sibling.study
	version := "3.12" // An explicit pin may replace unpinned default 3.13 scratch.
	// Exercise the supported setup path with the existing fake installer.
	// The test binary supplies the kernel bridge on both sides of the switch.
	installer := strings.ReplaceAll(recordedSetupStub, "3.13", version)
	installer = strings.Replace(installer, `mkdir -p "$dest/bin"`, `mkdir -p "$dest/bin"; rm -f "$dest/bin/python"`, 1)
	installRecordedSetupStub(t, h.study, installer)
	setup := h.study.ForRun()
	setup.Run = func(ctx context.Context, env []string, dir, name string, args ...string) ([]byte, error) {
		// The bridge helper is not a Python CLI; model its initially empty local package inventory.
		if name == filepath.Join(setup.Dir, "python/bin/python") && slices.Equal(args, []string{"-m", "pip", "freeze", "--local"}) {
			return nil, nil
		}
		return setup.Bases.Run(ctx, env, dir, name, args...)
	}
	_, err = setup.SetUpConda(t.Context(), version, nil, "")
	must(t, err)
	exe, err := os.Executable()
	must(t, err)
	python := filepath.Join(h.study.Dir, "python", "bin", "python")
	must(t, os.Remove(python))
	must(t, os.Symlink(exe, python))
	updated := f.work
	updated.PythonVersion = &version
	_, err = f.d.chat(t.Context(), updated)
	must(t, err)
	must(t, f.d.runCell(t.Context(), coresearch.ClaimedCell{WorkContext: other, ID: uuid.New(), Kernel: "python3", Source: "host"}))
	if sibling.sessions == old || sibling.study.PythonVersion != version || oldView.PythonVersion != "" {
		t.Fatal("shared selection did not retire session with immutable run views")
	}
	if _, err := old.CellWithID(t.Context(), uuid.NewString(), "python3", "host"); err == nil {
		t.Fatal("sibling old session still usable")
	}
}

func TestCoresearchVersionReconciliationConcurrentWithCell(t *testing.T) {
	f := coreKernelFixture(t)
	done := make(chan error, 1)
	go func() {
		done <- f.d.runCell(t.Context(), coresearch.ClaimedCell{WorkContext: f.work, ID: uuid.New(), Kernel: "python3", Source: "wait"})
	}()
	waitCoreFile(t, filepath.Join(f.dir(), "cell-wait"))
	version := "3.13"
	updated := f.work
	updated.PythonVersion = &version
	_, err := f.d.chat(t.Context(), updated)
	must(t, err)
	must(t, f.d.controlChat(t.Context(), coresearch.ClaimedControl{WorkContext: f.work, Op: "interrupt", Seq: 1}))
	must(t, <-done)
	must(t, f.d.runCell(t.Context(), coresearch.ClaimedCell{WorkContext: updated, ID: uuid.New(), Kernel: "python3", Source: "host"}))
	bad := "3.12"
	updated.PythonVersion = &bad
	if _, err := f.d.chat(t.Context(), updated); err == nil {
		t.Fatal("silently changed an existing authoritative pin")
	}
}

func TestCoresearchPinArrivingDuringBridgeLaunchRetiresEarlierEnvironment(t *testing.T) {
	f := coreKernelFixture(t)
	h := f.d.chats[f.work.ChatID]
	study := h.study
	var pinned atomic.Bool
	// The persistent bridge's authorization recheck uses its independent
	// lifetime, after Environment has completed. Cell/preparation requests
	// retain a cancellable context. Pin at exactly the bridge boundary.
	f.d.api.HTTP = &http.Client{Transport: coreRoundTrip(func(r *http.Request) (*http.Response, error) {
		if strings.HasSuffix(r.URL.Path, "/status") && r.Context().Done() == nil {
			if err := study.RememberPython("3.13"); err != nil {
				return nil, err
			}
			pinned.Store(true)
		}
		return http.DefaultTransport.RoundTrip(r)
	})}
	must(t, f.d.runCell(t.Context(), coresearch.ClaimedCell{WorkContext: f.work, ID: uuid.New(), Kernel: "python3", Source: "host"}))
	if !pinned.Load() {
		t.Fatal("did not exercise the launch boundary")
	}
	old := h.sessions
	must(t, f.d.runCell(t.Context(), coresearch.ClaimedCell{WorkContext: f.work, ID: uuid.New(), Kernel: "python3", Source: "host"}))
	if h.sessions == old {
		t.Fatal("late pin relabeled and reused an earlier prepared environment")
	}
}

func TestCoresearchRestartHonorsClaimedPython(t *testing.T) {
	f := coreKernelFixture(t)
	must(t, f.d.runCell(t.Context(), coresearch.ClaimedCell{WorkContext: f.work, ID: uuid.New(), Kernel: "python3", Source: "host"}))
	h := f.d.chats[f.work.ChatID]
	old := h.sessions
	version := "3.13"
	updated := f.work
	updated.PythonVersion = &version
	must(t, f.d.controlChat(t.Context(), coresearch.ClaimedControl{WorkContext: updated, Op: "restart", Seq: 7}))
	if h.sessions != nil || h.study.RecordedPython() != version {
		t.Fatal("restart reused the old environment despite its claimed pin")
	}
	if _, err := old.CellWithID(t.Context(), uuid.NewString(), "python3", "host"); err == nil {
		t.Fatal("restarted old session remains usable")
	}
	must(t, f.d.runCell(t.Context(), coresearch.ClaimedCell{WorkContext: f.work, ID: uuid.New(), Kernel: "python3", Source: "host"}))
	if h.kernelPython != version {
		t.Fatal("next cell lost restart's authoritative pin")
	}
}

func TestCoresearchCellRetainsFailedFinishReceipt(t *testing.T) {
	f := coreKernelFixture(t)
	original := http.DefaultTransport
	f.d.api.HTTP = &http.Client{Transport: coreRoundTrip(func(r *http.Request) (*http.Response, error) {
		if strings.HasSuffix(r.URL.Path, "/finish") {
			return &http.Response{StatusCode: 503, Header: http.Header{}, Body: io.NopCloser(strings.NewReader("temporary"))}, nil
		}
		return original.RoundTrip(r)
	})}
	c := coresearch.ClaimedCell{WorkContext: f.work, ID: uuid.New(), Kernel: "custom", Source: "host"}
	if err := f.d.runCell(t.Context(), c); err == nil {
		t.Fatal("finish failure disappeared")
	}
	entries, err := os.ReadDir(filepath.Join(f.d.cfg.StateDir, "coresearch-receipts"))
	if err != nil || len(entries) != 1 {
		t.Fatalf("terminal result was discarded: %v %v", entries, err)
	}
}

func TestCoresearchRecoveryRetriesReceiptWithoutExecutingCell(t *testing.T) {
	f := coreKernelFixture(t)
	original := http.DefaultTransport
	fail := true
	starts := 0
	f.d.api.HTTP = &http.Client{Transport: coreRoundTrip(func(r *http.Request) (*http.Response, error) {
		if strings.HasSuffix(r.URL.Path, "/start") {
			starts++
		}
		if fail && strings.HasSuffix(r.URL.Path, "/finish") {
			return &http.Response{StatusCode: 503, Header: http.Header{}, Body: io.NopCloser(strings.NewReader("temporary"))}, nil
		}
		return original.RoundTrip(r)
	})}
	c := coresearch.ClaimedCell{WorkContext: f.work, ID: uuid.New(), Kernel: "custom", Source: "host"}
	if err := f.d.runCell(t.Context(), c); err == nil {
		t.Fatal("missing failed finish")
	}
	f.d.stopChats()
	fail = false
	must(t, f.d.recoverCoresearch())
	must(t, f.d.flushCoresearch(t.Context()))
	if starts != 1 || f.finishes.Load() != 1 {
		t.Fatalf("recovery starts=%d finishes=%d", starts, f.finishes.Load())
	}
	entries, e := os.ReadDir(filepath.Join(f.d.cfg.StateDir, "coresearch-receipts"))
	must(t, e)
	if len(entries) != 0 {
		t.Fatal(entries)
	}
}
func TestCoresearchRecoveryFailsUnfinishedClaimsWithoutExecution(t *testing.T) {
	f := newCoreFixture(t)
	id := uuid.New()
	must(t, f.d.journalCoresearch(f.work, "cells", id))
	must(t, f.d.recoverCoresearch())
	must(t, f.d.flushCoresearch(t.Context()))
	if f.finishedCell["status"] != "interrupted" || f.finishes.Load() != 1 {
		t.Fatal(f.finishedCell)
	}
}
func TestCoresearchMonitorFailureCancelsAndReportsCell(t *testing.T) {
	f := coreKernelFixture(t)
	original := http.DefaultTransport
	f.d.api.HTTP = &http.Client{Transport: coreRoundTrip(func(r *http.Request) (*http.Response, error) {
		if strings.HasSuffix(r.URL.Path, "/heartbeat") {
			return &http.Response{StatusCode: 503, Header: http.Header{}, Body: io.NopCloser(strings.NewReader("temporary"))}, nil
		}
		return original.RoundTrip(r)
	})}
	done := make(chan error, 1)
	go func() {
		done <- f.d.runCell(t.Context(), coresearch.ClaimedCell{WorkContext: f.work, ID: uuid.New(), Kernel: "custom", Source: "wait"})
	}()
	waitCoreFile(t, filepath.Join(f.dir(), "cell-wait"))
	select {
	case err := <-done:
		if !errors.Is(err, context.Canceled) {
			t.Fatal(err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("offline cell continued")
	}
	if f.finishes.Load() != 1 || f.finishedCell["status"] != "interrupted" {
		t.Fatalf("lost terminal receipt: %+v", f.finishedCell)
	}
}

func TestCoresearchTransientReceiptFlushRetainsAndRetries(t *testing.T) {
	f := newCoreFixture(t)
	id := uuid.New()
	must(t, f.d.journalCoresearch(f.work, "cells", id))
	must(t, f.d.recoverCoresearch())
	f.d.api.HTTP = &http.Client{Transport: coreRoundTrip(func(r *http.Request) (*http.Response, error) {
		return &http.Response{StatusCode: 503, Header: http.Header{}, Body: io.NopCloser(strings.NewReader("temporary"))}, nil
	})}
	must(t, f.d.flushCoresearch(t.Context()))
	entries, e := os.ReadDir(filepath.Join(f.d.cfg.StateDir, "coresearch-receipts"))
	must(t, e)
	if len(entries) != 1 {
		t.Fatal("transient flush discarded evidence")
	}
	f.d.api.HTTP = &http.Client{}
	must(t, f.d.flushCoresearch(t.Context()))
	if f.finishes.Load() != 1 {
		t.Fatal("receipt did not retry")
	}
}
