package daemon

import (
	"archive/tar"
	"archive/zip"
	"bytes"
	"compress/gzip"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"hash"
	"maps"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/google/uuid"

	"lykeion/server/internal/apiclient"
	"lykeion/server/internal/auth"
	"lykeion/server/internal/connectors"
	"lykeion/server/internal/daemon/sandbox"
	"lykeion/server/internal/handler"
	"lykeion/server/internal/labmemory"
	"lykeion/server/internal/realtime"
	"lykeion/server/internal/service"
	"lykeion/server/internal/storage"
	"lykeion/server/internal/store"
	"lykeion/server/internal/testutil"
	"lykeion/server/pkg/agent"
	db "lykeion/server/pkg/db/generated"
)

type daemonFixture struct {
	handInOutput       string
	h                  *handler.H
	dropMessageACK     atomic.Bool
	dropReportACK      atomic.Bool
	dropStartACK       atomic.Bool
	refusePrepare      atomic.Bool
	env                *testutil.Env
	runs               *service.AgentRuns
	ws, user, card, id uuid.UUID
	cfg                Config
	rejectMessages     atomic.Bool
	messageCalls       atomic.Int32
	attachmentGets     atomic.Int32
	// intercept, when set, sees each request first and answers it by
	// returning true.
	intercept atomic.Pointer[func(http.ResponseWriter, *http.Request) bool]
}

func fixture(t *testing.T) *daemonFixture {
	t.Helper()
	t.Setenv("HOME", t.TempDir())
	t.Setenv("CODEX_HOME", "")
	t.Setenv("CLAUDE_CONFIG_DIR", t.TempDir())
	if runtime.GOOS == "linux" {
		must(t, os.WriteFile(filepath.Join(os.Getenv("CLAUDE_CONFIG_DIR"), ".credentials.json"), []byte("test-signin-fixture"), 0600))
	}
	t.Setenv("LYK_TOKEN", "lyk_owner_must_never_leak")
	t.Setenv("LYK_CONFIG", filepath.Join(t.TempDir(), "config"))
	f := &daemonFixture{env: testutil.Setup(t), ws: uuid.New()}
	ctx := context.Background()
	st := &store.Store{LessonsEnabled: true, Pool: f.env.App}
	hub := &realtime.Hub{Store: st, Redis: f.env.Redis}
	st.Publish = hub.Publish
	f.runs = &service.AgentRuns{Store: st}
	issuer := auth.Issuer{Secret: []byte("test"), TTL: time.Hour}
	h := &handler.H{Connectors: &connectors.Client{Secret: bytes.Repeat([]byte{7}, 32)}, Store: st, JWT: issuer, Limit: auth.Limiter{R: f.env.Redis}, Hub: hub, AgentRuns: f.runs, Blobs: storage.Local{Dir: t.TempDir()}}
	h.Lab = (labmemory.Tools{Store: st, Blobs: h.Blobs}).Handler()
	router := handler.NewRouter(h)
	f.h = h
	t.Cleanup(h.MCPBroker.Close)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if fn := f.intercept.Load(); fn != nil && (*fn)(w, r) {
			return
		}
		if strings.HasSuffix(r.URL.Path, "/prepare-lease") && f.refusePrepare.Swap(false) {
			conn, _, err := w.(http.Hijacker).Hijack()
			if err != nil {
				t.Error(err)
				return
			}
			conn.Close()
			return
		}
		if strings.HasPrefix(r.URL.Path, "/api/role/attachments/") {
			f.attachmentGets.Add(1)
		}
		if strings.HasSuffix(r.URL.Path, "/messages") {
			f.messageCalls.Add(1)
			if f.rejectMessages.Load() {
				http.Error(w, "temporary outage", 503)
				return
			}
		}
		drop := (strings.HasSuffix(r.URL.Path, "/start") && f.dropStartACK.Swap(false)) || (strings.HasSuffix(r.URL.Path, "/messages") && f.dropMessageACK.Swap(false)) || (strings.HasSuffix(r.URL.Path, "/complete") && f.dropReportACK.Swap(false))
		if drop {
			rec := httptest.NewRecorder()
			router.ServeHTTP(rec, r)
			if rec.Code >= 300 {
				t.Errorf("fault fixture request rejected: %d %s", rec.Code, rec.Body.String())
			}
			conn, _, err := w.(http.Hijacker).Hijack()
			if err != nil {
				t.Error(err)
				return
			}
			conn.Close()
			return
		}
		router.ServeHTTP(w, r)
	}))
	t.Cleanup(srv.Close)
	u, err := st.Global().UpsertUserByEmail(ctx, "daemon@lab.org")
	must(t, err)
	f.user = u.ID
	must(t, st.InWorkspace(ctx, f.ws, u.ID, func(tx *store.Tx) error {
		if _, err := tx.Q.CreateWorkspace(ctx, db.CreateWorkspaceParams{ID: f.ws, Slug: "lab", Name: "Lab"}); err != nil {
			return err
		}
		if err := tx.Q.AddMember(ctx, db.AddMemberParams{WorkspaceID: f.ws, UserID: u.ID, Role: "owner"}); err != nil {
			return err
		}
		c, err := tx.Q.CreateCard(ctx, db.CreateCardParams{BoardColumn: "grounding", WorkspaceID: f.ws, Number: 1, Title: "t", Question: "q", OwnerID: u.ID})
		f.card = c.ID
		return err
	}))
	jwt, err := issuer.Issue(u.ID, u.Email)
	must(t, err)
	var token struct{ Token string }
	must(t, (&apiclient.Client{Server: srv.URL, Token: jwt}).Do(ctx, "POST", "/api/tokens", map[string]string{"name": "daemon"}, &token))
	f.cfg = Config{Server: srv.URL, Token: token.Token, StateDir: t.TempDir(), Home: t.TempDir(), Device: "test", Slots: 2, Poll: 40 * time.Millisecond, Heartbeat: 100 * time.Millisecond, Flush: 50 * time.Millisecond, Harnesses: map[string]string{"claude": fakeClaude(t)},
		Preflight: func(context.Context, string) (string, string) { return "", "" }}
	t.Cleanup(func() { _ = agent.RemoveTree(f.cfg.StateDir) })
	// The fake CLIs give no real hand-in; tests of other things take it as accepted.
	accept := func(w http.ResponseWriter, r *http.Request) bool {
		if !strings.HasSuffix(r.URL.Path, "/hand-in") {
			return false
		}
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`{"accepted":true}`))
		return true
	}
	f.intercept.Store(&accept)
	f.enqueue(t)
	return f
}
func (f *daemonFixture) enqueue(t *testing.T) {
	t.Helper()
	ctx := context.Background()
	must(t, f.runs.Store.InWorkspace(ctx, f.ws, f.user, func(tx *store.Tx) error {
		r, err := f.runs.Enqueue(ctx, tx, f.user, service.NewAgentRun{Card: f.card, Column: "grounding", Round: 1, Role: "scoper", Harness: "claude", Prompt: "scope", Skill: "# Scoper", MaxAttempts: 1})
		f.id = r.ID
		return err
	}))
}
func fakeClaude(t *testing.T) string {
	t.Helper()
	p := filepath.Join(os.Getenv("HOME"), "fake-bin", "claude")
	must(t, os.MkdirAll(filepath.Dir(p), 0700))
	must(t, os.WriteFile(p, []byte(`#!/bin/sh
case "$*" in
*--json-schema*) [ -z "$LYK_TOKEN$LYK_SERVER$LYK_CONFIG$LYK_DOOR$LYK_DOOR_TOKEN" ] || exit 48;;
*)
case "$LYK_TOKEN" in ltk_*) ;; *) exit 41;; esac
case "$LYK_CONFIG" in "$PWD"/*) ;; *) exit 42;; esac
[ ! -f "$LYK_CONFIG" ] || [ "$(cat "$LYK_CONFIG")" = '{}' ] || exit 43;;
esac
mkdir -p "$npm_config_cache"
printf '%s\n' "$*" >> "$npm_config_cache/argv"
project="$CLAUDE_CONFIG_DIR/projects/$(printf '%s' "$PWD" | sed 's/[^a-zA-Z0-9]/-/g')"
case "$*" in
*"--input-format stream-json"*)
	# Continuity: the account's own HOME, one NDJSON frame, stdin left open.
	case "$HOME" in "$PWD"/*) exit 45;; esac
	IFS= read -r first || exit 46
	case "$first" in *'"type":"user"'*) ;; *) exit 47;; esac
	mkdir -p "$project" && : > "$project/s.jsonl" ;;
*)
	case "$HOME" in "$PWD"/*) ;; *) exit 44;; esac
	cat >/dev/null
	mkdir -p "$project" && : > "$project/s.jsonl" ;;
esac
echo '{"type":"assistant","message":{"content":[{"type":"text","text":"working"}]}}'
echo '{"type":"result","is_error":false,"total_cost_usd":0.25,"session_id":"s","modelUsage":{"m":{"inputTokens":1,"outputTokens":1,"cacheReadInputTokens":0,"cacheCreationInputTokens":0,"costUSD":0.25}}}'
`), 0700))
	return p
}

func TestReadyCallbackRunsAfterRegistrationAndExitsUnderOwnership(t *testing.T) {
	f := fixture(t)
	want := errors.New("readiness record unavailable")
	var exited atomic.Bool
	f.cfg.Ready = func() error {
		return f.runs.Store.InWorkspace(t.Context(), f.ws, f.user, func(tx *store.Tx) error {
			runtimes, err := tx.Q.ListRuntimes(t.Context(), f.ws)
			if err != nil {
				return err
			}
			if len(runtimes) == 0 || runtimes[0].Status != "online" {
				return errors.New("readiness preceded runtime registration")
			}
			return want
		})
	}
	f.cfg.OnExit = func() { exited.Store(true) }
	if err := Run(t.Context(), f.cfg); !errors.Is(err, want) {
		t.Fatalf("readiness failure: %v", err)
	}
	if !exited.Load() {
		t.Fatal("exit callback was not run")
	}
}
func waitFor(t *testing.T, fn func() bool) {
	t.Helper()
	deadline := time.Now().Add(12 * time.Second)
	for time.Now().Before(deadline) {
		if fn() {
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatal("condition never reached")
}
func (f *daemonFixture) status(t *testing.T) string {
	t.Helper()
	var s string
	must(t, f.env.Owner.QueryRow(context.Background(), "SELECT status FROM agent_run WHERE id=$1", f.id).Scan(&s))
	return s
}

// parked reports whether the server put the run back to wait for the machine.
func (f *daemonFixture) parked(t *testing.T) bool {
	t.Helper()
	var ok bool
	must(t, f.env.Owner.QueryRow(context.Background(), "SELECT status = 'queued' AND infra_failures > 0 FROM agent_run WHERE id=$1", f.id).Scan(&ok))
	return ok
}

// skipBackoff lets the daemon reclaim a parked run at once.
func (f *daemonFixture) skipBackoff(t *testing.T) {
	t.Helper()
	_, err := f.env.Owner.Exec(context.Background(), "UPDATE agent_run SET retry_after = NULL WHERE id=$1", f.id)
	must(t, err)
}
func startDaemon(t *testing.T, cfg Config) (context.CancelFunc, <-chan error) {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- Run(ctx, cfg) }()
	t.Cleanup(func() { cancel() })
	return cancel, done
}
func stopDaemon(t *testing.T, cancel context.CancelFunc, done <-chan error) {
	t.Helper()
	cancel()
	select {
	case err := <-done:
		must(t, err)
	case <-time.After(8 * time.Second):
		t.Fatal("daemon did not join shutdown")
	}
}
func TestDaemonRunsQueuedAgentWithIsolatedRoleToken(t *testing.T) {
	f := fixture(t)
	var role []byte
	f.atRunEnd(func(dir string) { role, _ = os.ReadFile(filepath.Join(dir, ".cli-home", "role.md")) })
	cancel, done := startDaemon(t, f.cfg)
	waitFor(t, func() bool { return f.status(t) == "completed" })
	stopDaemon(t, cancel, done)
	var cost float64
	var messages, usage int
	must(t, f.env.Owner.QueryRow(context.Background(), "SELECT spent_usd FROM card WHERE id=$1", f.card).Scan(&cost))
	must(t, f.env.Owner.QueryRow(context.Background(), "SELECT count(*) FROM agent_run_message WHERE agent_run_id=$1", f.id).Scan(&messages))
	must(t, f.env.Owner.QueryRow(context.Background(), "SELECT count(*) FROM agent_run_usage WHERE agent_run_id=$1", f.id).Scan(&usage))
	if cost != .25 || usage != 1 || messages == 0 {
		t.Fatalf("cost %v usage %d messages %d", cost, usage, messages)
	}
	var scope, mode, digest, model string
	var reported bool
	must(t, f.env.Owner.QueryRow(context.Background(), "SELECT continuity_scope, profile_mode, policy_digest, model FROM agent_run WHERE id=$1", f.id).Scan(&scope, &mode, &digest, &model))
	must(t, f.env.Owner.QueryRow(context.Background(), "SELECT cost_reported FROM agent_run_usage WHERE agent_run_id=$1", f.id).Scan(&reported))
	if scope != "card:"+f.card.String()+"/role:scoper" || mode != ModeSandboxed || digest != defaultPolicyHash || model != "m" || !reported {
		t.Fatalf("scope %q mode %q digest %q model %q reported %v", scope, mode, digest, model, reported)
	}
	if string(role) != "# Scoper" {
		t.Fatalf("role file %q", role)
	}
	// a run's folder can hold gigabytes its code installed; what the run
	// keeps it saved as outputs, so the folder goes as the run ends
	dir := filepath.Join(f.cfg.Home, f.id.String()+"-1")
	if _, err := os.Stat(dir); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("the finished run's folder is still there: %v", err)
	}
	if _, err := os.Stat(finishedRecord(f.cfg.StateDir, dir)); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("the removed folder's finished record is still there: %v", err)
	}
}
func TestSecondRunOnTheCardAndRoleResumesWithoutDoubleCharge(t *testing.T) {
	f := fixture(t)
	cancel, done := startDaemon(t, f.cfg)
	waitFor(t, func() bool { return f.status(t) == "completed" })
	first := f.id
	f.enqueue(t)
	waitFor(t, func() bool { return f.status(t) == "completed" })
	stopDaemon(t, cancel, done)
	argv, err := os.ReadFile(filepath.Join(f.cfg.Home, ".caches", "npm", "argv"))
	must(t, err)
	lines := strings.Split(strings.TrimSpace(string(argv)), "\n")
	if len(lines) != 2 || strings.Contains(lines[0], "--resume") || !strings.Contains(lines[1]+" ", " --resume s ") {
		t.Fatalf("invocations:\n%s", argv)
	}
	var cost float64
	must(t, f.env.Owner.QueryRow(context.Background(), "SELECT spent_usd FROM card WHERE id=$1", f.card).Scan(&cost))
	// The resumed CLI reports cumulative usage; only the first run is charged.
	if cost != .25 || first == f.id {
		t.Fatalf("card spent %v", cost)
	}
}
func TestUnreachableSessionStartsFreshInTheSameRun(t *testing.T) {
	f := fixture(t)
	cancel, done := startDaemon(t, f.cfg)
	waitFor(t, func() bool { return f.status(t) == "completed" })
	must(t, os.RemoveAll(filepath.Join(os.Getenv("CLAUDE_CONFIG_DIR"), "projects")))
	f.enqueue(t)
	waitFor(t, func() bool { return f.status(t) == "completed" })
	stopDaemon(t, cancel, done)
	argv, err := os.ReadFile(filepath.Join(f.cfg.Home, ".caches", "npm", "argv"))
	must(t, err)
	if strings.Contains(string(argv), "--resume") {
		t.Fatalf("resumed a session its store no longer holds:\n%s", argv)
	}
	var notes int
	must(t, f.env.Owner.QueryRow(context.Background(), "SELECT count(*) FROM agent_run_message WHERE agent_run_id=$1 AND type='status' AND content->>'type'='continuity' AND content->>'message' LIKE '%not in the CLI''s session store%'", f.id).Scan(&notes))
	if notes != 1 {
		t.Fatalf("continuity notes %d", notes)
	}
}
func TestDaemonReplaysFinalMessagesBeforeReportAfterRestart(t *testing.T) {
	f := fixture(t)
	f.rejectMessages.Store(true)
	cancel, done := startDaemon(t, f.cfg)
	waitFor(t, func() bool {
		paths, _ := filepath.Glob(filepath.Join(f.cfg.StateDir, "outbox", "*.json"))
		return len(paths) > 0 && f.messageCalls.Load() > 0
	})
	if got := f.status(t); got != "running" {
		t.Fatal("settled before message ACK", got)
	}
	stopDaemon(t, cancel, done)
	f.rejectMessages.Store(false)
	cancel, done = startDaemon(t, f.cfg)
	waitFor(t, func() bool { return f.status(t) == "completed" })
	stopDaemon(t, cancel, done)
	var n int
	must(t, f.env.Owner.QueryRow(context.Background(), "SELECT count(*) FROM agent_run_message WHERE agent_run_id=$1", f.id).Scan(&n))
	if n != 1 {
		t.Fatalf("replayed messages %d", n)
	}
}

type backendFunc func(context.Context, string, agent.ExecOptions) (*agent.Session, error)

func (fn backendFunc) Execute(ctx context.Context, p string, o agent.ExecOptions) (*agent.Session, error) {
	return fn(ctx, p, o)
}
func TestDaemonPreparationFailureReportsWithoutLaunching(t *testing.T) {
	f := fixture(t)
	f.cfg.NewBackend = func(string, string) (agent.Backend, error) { return nil, errors.New("backend unavailable") }
	cancel, done := startDaemon(t, f.cfg)
	waitFor(t, func() bool { return f.status(t) == "failed" })
	stopDaemon(t, cancel, done)
	var reason string
	must(t, f.env.Owner.QueryRow(context.Background(), "SELECT failure_reason FROM agent_run WHERE id=$1", f.id).Scan(&reason))
	if reason != "agent_error" {
		t.Fatal(reason)
	}
}
func TestDaemonShutdownWaitsForBackendAndPreservesResult(t *testing.T) {
	f := fixture(t)
	started := make(chan struct{})
	exited := make(chan struct{})
	release := make(chan struct{})
	f.cfg.NewBackend = func(string, string) (agent.Backend, error) {
		return backendFunc(func(ctx context.Context, _ string, _ agent.ExecOptions) (*agent.Session, error) {
			msgs := make(chan agent.Message)
			done := make(chan agent.Result, 1)
			go func() {
				close(started)
				<-ctx.Done()
				<-release
				close(exited)
				close(msgs)
				cost := .1
				done <- agent.Result{Usage: []agent.Usage{{Model: "m", CostUSD: &cost}}, Err: ctx.Err(), Reason: agent.ReasonRuntimeOffline}
				close(done)
			}()
			return &agent.Session{Messages: msgs, Done: done}, nil
		}), nil
	}
	cancel, done := startDaemon(t, f.cfg)
	select {
	case <-started:
	case <-time.After(8 * time.Second):
		t.Fatal("backend not started")
	}
	cancel()
	select {
	case err := <-done:
		t.Fatalf("returned before child exit: %v", err)
	case <-time.After(100 * time.Millisecond):
	}
	close(release)
	select {
	case err := <-done:
		must(t, err)
	case <-time.After(8 * time.Second):
		t.Fatal("not joined")
	}
	<-exited
	paths, err := filepath.Glob(filepath.Join(f.cfg.StateDir, "outbox", "*.json"))
	must(t, err)
	if len(paths) != 1 {
		t.Fatalf("durable shutdown reports %v", paths)
	}
	b, err := os.ReadFile(paths[0])
	must(t, err)
	var r Report
	must(t, json.Unmarshal(b, &r))
	if r.FailureReason != "runtime_offline" || len(r.Usage) != 1 || r.Attempt != 1 {
		t.Fatalf("report %+v", r)
	}
}

func TestStreamBatchesByEncodedBytesAndRetainsOversize(t *testing.T) {
	d := &daemon{cfg: Config{StateDir: t.TempDir()}}
	d.messages = &Outbox{Dir: filepath.Join(d.cfg.StateDir, "messages")}
	r := reportFixture()
	msgs := make(chan agent.Message, 3)
	content := json.RawMessage(`"` + strings.Repeat("x", 11<<20) + `"`)
	for i := 0; i < 3; i++ {
		msgs <- agent.Message{Type: "text", Content: content}
	}
	close(msgs)
	must(t, d.stream(r, msgs, func() { t.Error("cancelled valid batch") }))
	paths, err := filepath.Glob(filepath.Join(d.messages.Dir, "*.json"))
	must(t, err)
	if len(paths) != 2 {
		t.Fatalf("byte batches %v", paths)
	}
	total := 0
	for _, p := range paths {
		saved, err := readReport(p)
		must(t, err)
		body, err := json.Marshal(messageBody(saved))
		must(t, err)
		if len(body) > 32<<20 {
			t.Fatal("oversized request", len(body))
		}
		total += len(saved.Messages)
	}
	if total != 3 {
		t.Fatal(total)
	}
	r = reportFixture()
	msgs = make(chan agent.Message, 1)
	msgs <- agent.Message{Type: "text", Content: json.RawMessage(`"` + strings.Repeat("<", 6<<20) + `"`)}
	close(msgs)
	cancelled := false
	err = d.stream(r, msgs, func() { cancelled = true })
	var oversized *oversizedMessageError
	if !errors.As(err, &oversized) || !cancelled {
		t.Fatalf("oversize not explicit: %v %v", err, cancelled)
	}
	evidence, err := filepath.Glob(filepath.Join(d.cfg.StateDir, "oversized-messages", "*.json"))
	must(t, err)
	if len(evidence) != 1 {
		t.Fatal(evidence)
	}
	saved, err := readReport(evidence[0])
	must(t, err)
	var original string
	must(t, json.Unmarshal(saved.Messages[0].Content, &original))
	if len(original) != 6<<20 {
		t.Fatal("truncated evidence")
	}
}
func TestMessageQuarantineUnblocksTerminalWithoutDeletingEvidence(t *testing.T) {
	terminal := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasSuffix(r.URL.Path, "/messages") {
			http.Error(w, "stale attempt", 409)
			return
		}
		terminal++
		w.WriteHeader(204)
	}))
	defer srv.Close()
	d := &daemon{api: &apiclient.Client{Server: srv.URL}}
	dir := t.TempDir()
	d.messages = &Outbox{Dir: filepath.Join(dir, "messages"), Send: d.sendMessages}
	d.out = &Outbox{Dir: filepath.Join(dir, "outbox"), Send: d.send}
	r := reportFixture()
	must(t, d.out.Put(r))
	m := r
	m.Kind = "messages"
	m.Messages = []wireMsg{{Seq: 1, Type: "text", Content: json.RawMessage(`"tail"`)}}
	must(t, d.messages.Put(m))
	for i := 0; i < 3; i++ {
		must(t, d.flush(context.Background()))
	}
	if terminal != 0 {
		t.Fatal("reported before ACK/quarantine")
	}
	saved, err := readReport(d.messages.file(m))
	must(t, err)
	saved.Rejections[0] = time.Now().Add(-11 * time.Minute)
	must(t, atomicJSON(d.messages.Dir, filepath.Base(d.messages.file(m)), saved))
	must(t, d.flush(context.Background()))
	if terminal != 1 {
		t.Fatal("terminal blocked", terminal)
	}
	retained, err := readReport(filepath.Join(d.messages.Dir, "quarantine", filepath.Base(d.messages.file(m))))
	must(t, err)
	if string(retained.Messages[0].Content) != `"tail"` {
		t.Fatal("evidence lost")
	}
}
func TestStateOwnershipAndFinishedOnlyGC(t *testing.T) {
	dir := t.TempDir()
	owner, err := ownState(dir)
	must(t, err)
	if second, err := ownState(dir); err == nil {
		second.Close()
		t.Fatal("two state owners")
	}
	must(t, owner.Close())
	owner, err = ownState(dir)
	must(t, err)
	must(t, owner.Close())
	home, state := t.TempDir(), t.TempDir()
	d := &daemon{cfg: Config{Home: home, StateDir: state}, active: map[string]bool{"active": true}}
	for _, name := range []string{"active", "finished", "unfinished", "recent", "held"} {
		p := filepath.Join(home, name)
		must(t, os.Mkdir(p, 0700))
		if name != "unfinished" {
			finishedFor(t, state, p, "done", 0)
			if name != "recent" {
				finishedFor(t, state, p, "done", 25*time.Hour)
			}
		}
		past := time.Now().Add(-48 * time.Hour)
		must(t, os.Chtimes(p, past, past))
	}
	// A scope folder follows the retention its marker records; one whose
	// marker records none keeps F2's day.
	for name, m := range map[string]struct {
		body string
		age  time.Duration
	}{
		"scope-kept":   {`{"retention_days":30}`, 29 * 24 * time.Hour},
		"scope-old":    {`{"retention_days":30}`, 31 * 24 * time.Hour},
		"scope-short":  {`{"retention_days":2}`, 49 * time.Hour},
		"scope-legacy": {`done`, 25 * time.Hour},
	} {
		p := filepath.Join(home, name)
		must(t, os.Mkdir(p, 0700))
		finishedFor(t, state, p, m.body, m.age)
	}

	d.holdScratch("held")
	must(t, d.gc(context.Background()))
	for _, name := range []string{"active", "unfinished", "held"} {
		if _, err := os.Stat(filepath.Join(home, name)); err != nil {
			t.Fatal("removed live scratch", name, err)
		}
	}
	// an attempt's own folder goes as soon as its run is recorded finished
	for _, name := range []string{"finished", "recent"} {
		if _, err := os.Stat(filepath.Join(home, name)); !errors.Is(err, os.ErrNotExist) {
			t.Fatal("retained finished scratch", name, err)
		}
	}
	if _, err := os.Stat(filepath.Join(home, "scope-kept")); err != nil {
		t.Fatal("removed a scope folder within its retention", err)
	}
	for _, name := range []string{"scope-old", "scope-short", "scope-legacy"} {
		if _, err := os.Stat(filepath.Join(home, name)); !errors.Is(err, os.ErrNotExist) {
			t.Fatal("retained a scope folder past its retention", name, err)
		}
	}
	if !d.holdScratch("unfinished") {
		t.Fatal("gc kept its hold on a folder")
	}
}
func TestDaemonOwnerHintWakesWithoutPoll(t *testing.T) {
	f := fixture(t)
	defer func() {
		var status, detail string
		if err := f.env.Owner.QueryRow(t.Context(), "SELECT status,error FROM agent_run WHERE id=$1", f.id).Scan(&status, &detail); err == nil && status != "completed" {
			t.Logf("owner fixture state=%s error=%s", status, detail)
		}
	}()
	f.cfg.Poll = time.Hour
	cancel, done := startDaemon(t, f.cfg)
	waitFor(t, func() bool { return f.status(t) == "completed" })
	f.enqueue(t)
	waitFor(t, func() bool { return f.status(t) == "completed" })
	stopDaemon(t, cancel, done)
}
func TestDaemonRuntimeBatchingAndFreeSlots(t *testing.T) {
	var heartbeat, claims [][]runtimeRef
	slots := []int{}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body struct {
			Runtimes       []runtimeRef `json:"runtimes"`
			Slots          int          `json:"slots"`
			CoresearchOnly bool         `json:"coresearch_only"`
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Error(err)
		}
		if len(body.Runtimes) > 64 || len(body.Runtimes) == 0 {
			t.Error("bad reference batch", len(body.Runtimes))
		}
		if r.URL.Path == "/api/daemon/coresearch/claim" {
			w.Write([]byte(`{"cells":[],"runs":[],"setups":[],"controls":[]}`))
			return
		}
		if body.CoresearchOnly {
			w.Write([]byte(`[]`))
			return
		}
		if strings.HasSuffix(r.URL.Path, "heartbeat") {
			heartbeat = append(heartbeat, body.Runtimes)
			w.WriteHeader(204)
		} else {
			claims = append(claims, body.Runtimes)
			slots = append(slots, body.Slots)
			w.Write([]byte(`[]`))
		}
	}))
	defer srv.Close()
	d := &daemon{api: &apiclient.Client{Server: srv.URL}, cfg: Config{Slots: 2}, active: map[string]bool{"running": true}, failures: make(chan error, 1)}
	for i := 0; i < 130; i++ {
		d.refs = append(d.refs, runtimeRef{WorkspaceID: uuid.New(), RuntimeID: uuid.New()})
	}
	d.heartbeat(context.Background())
	d.claim(context.Background())
	d.claim(context.Background())
	if len(heartbeat) != 3 || len(claims) != 6 {
		t.Fatalf("batches heartbeat %d claim %d", len(heartbeat), len(claims))
	}
	if claims[0][0] == claims[3][0] {
		t.Fatal("no rotation")
	}
	for _, n := range slots {
		if n != 1 {
			t.Fatal("claimed occupied slot", n)
		}
	}
}

func TestDaemonLargeMessageCrossesRealHTTP(t *testing.T) {
	f := fixture(t)
	ordinaryErr := (&apiclient.Client{Server: f.cfg.Server, Token: f.cfg.Token}).Do(context.Background(), "POST", "/api/daemon/register", map[string]string{"device_name": strings.Repeat("x", 2<<20)}, nil)
	var boundError *apiclient.HTTPError
	if !errors.As(ordinaryErr, &boundError) || boundError.Status != 413 {
		t.Fatalf("ordinary JSON bound: %v", ordinaryErr)
	}
	content := json.RawMessage(`"` + strings.Repeat("x", 2<<20) + `"`)
	f.cfg.NewBackend = func(string, string) (agent.Backend, error) {
		return backendFunc(func(context.Context, string, agent.ExecOptions) (*agent.Session, error) {
			msgs := make(chan agent.Message, 1)
			msgs <- agent.Message{Type: "tool_use", Tool: "large", Content: content}
			close(msgs)
			done := make(chan agent.Result, 1)
			done <- agent.Result{SessionID: "large"}
			close(done)
			return &agent.Session{Messages: msgs, Done: done}, nil
		}), nil
	}
	cancel, done := startDaemon(t, f.cfg)
	// Handler entry does not mean the large payload's transaction committed.
	// Observe persistence before stopping; cancellation can abort an in-flight write.
	waitFor(t, func() bool {
		var n int
		must(t, f.env.Owner.QueryRow(context.Background(), "SELECT count(*) FROM agent_run_message WHERE agent_run_id=$1", f.id).Scan(&n))
		return n == 1
	})
	stopDaemon(t, cancel, done)
	var n int
	must(t, f.env.Owner.QueryRow(context.Background(), "SELECT count(*) FROM agent_run_message WHERE agent_run_id=$1", f.id).Scan(&n))
	if n != 1 {
		t.Fatalf("large HTTP message rejected, rows=%d", n)
	}
}

func TestDaemonCancellationDrainsTailBeforeLateUsage(t *testing.T) {
	f := fixture(t)
	started := make(chan struct{})
	exited := make(chan struct{})
	f.cfg.NewBackend = func(string, string) (agent.Backend, error) {
		return backendFunc(func(ctx context.Context, _ string, _ agent.ExecOptions) (*agent.Session, error) {
			msgs := make(chan agent.Message, 1)
			done := make(chan agent.Result, 1)
			go func() {
				close(started)
				<-ctx.Done()
				msgs <- agent.Message{Type: "text", Content: json.RawMessage(`"cancelled tail"`)}
				close(msgs)
				cost := .2
				done <- agent.Result{Usage: []agent.Usage{{Model: "m", CostUSD: &cost}}, Err: ctx.Err(), Reason: agent.ReasonRuntimeOffline}
				close(done)
				close(exited)
			}()
			return &agent.Session{Messages: msgs, Done: done}, nil
		}), nil
	}
	cancel, done := startDaemon(t, f.cfg)
	select {
	case <-started:
	case <-time.After(8 * time.Second):
		t.Fatal("not started")
	}
	must(t, f.runs.Cancel(context.Background(), f.ws, f.user, f.id))
	waitFor(t, func() bool {
		var n int
		must(t, f.env.Owner.QueryRow(context.Background(), "SELECT count(*) FROM agent_run_usage WHERE agent_run_id=$1", f.id).Scan(&n))
		return n == 1
	})
	stopDaemon(t, cancel, done)
	<-exited
	var n int
	must(t, f.env.Owner.QueryRow(context.Background(), "SELECT count(*) FROM agent_run_message WHERE agent_run_id=$1", f.id).Scan(&n))
	if n != 1 || f.status(t) != "cancelled" {
		t.Fatal("lost cancellation tail/status", n)
	}
}
func TestDaemonSurfacesStorageFailureAfterChildExit(t *testing.T) {
	f := fixture(t)
	started := make(chan struct{})
	release := make(chan struct{})
	f.cfg.NewBackend = func(string, string) (agent.Backend, error) {
		return backendFunc(func(ctx context.Context, _ string, _ agent.ExecOptions) (*agent.Session, error) {
			msgs := make(chan agent.Message)
			done := make(chan agent.Result, 1)
			go func() {
				close(started)
				select {
				case <-release:
				case <-ctx.Done():
				}
				close(msgs)
				done <- agent.Result{}
				close(done)
			}()
			return &agent.Session{Messages: msgs, Done: done}, nil
		}), nil
	}
	f.cfg.Flush = time.Hour
	cancel, done := startDaemon(t, f.cfg)
	defer cancel()
	select {
	case <-started:
	case <-time.After(8 * time.Second):
		t.Fatal("not started")
	}
	outbox := filepath.Join(f.cfg.StateDir, "outbox")
	must(t, os.Remove(outbox))
	must(t, os.WriteFile(outbox, []byte("storage fault"), 0600))
	close(release)
	select {
	case err := <-done:
		if err == nil {
			t.Fatal("swallowed storage error")
		}
	case <-time.After(8 * time.Second):
		t.Fatal("storage error did not stop daemon")
	}
}

func TestDaemonLostAcknowledgementsReplayIdempotently(t *testing.T) {
	f := fixture(t)
	f.dropMessageACK.Store(true)
	f.dropReportACK.Store(true)
	cancel, done := startDaemon(t, f.cfg)
	waitFor(t, func() bool {
		if f.status(t) != "completed" {
			return false
		}
		paths, err := filepath.Glob(filepath.Join(f.cfg.StateDir, "outbox", "*.json"))
		must(t, err)
		return len(paths) == 0
	})
	stopDaemon(t, cancel, done)
	if f.messageCalls.Load() < 2 {
		t.Fatal("message response loss did not retry")
	}
	var messages, usage int
	var cost float64
	must(t, f.env.Owner.QueryRow(context.Background(), "SELECT count(*) FROM agent_run_message WHERE agent_run_id=$1", f.id).Scan(&messages))
	must(t, f.env.Owner.QueryRow(context.Background(), "SELECT count(*) FROM agent_run_usage WHERE agent_run_id=$1", f.id).Scan(&usage))
	must(t, f.env.Owner.QueryRow(context.Background(), "SELECT spent_usd FROM card WHERE id=$1", f.card).Scan(&cost))
	if messages != 1 || usage != 1 || cost != .25 {
		t.Fatalf("replay duplicated evidence/spend: %d %d %v", messages, usage, cost)
	}
}

func TestOversizeDoesNotMaskLaterPersistenceFailure(t *testing.T) {
	state := t.TempDir()
	blocked := filepath.Join(state, "messages")
	must(t, os.WriteFile(blocked, []byte("disk fault"), 0600))
	d := &daemon{cfg: Config{StateDir: state}, messages: &Outbox{Dir: blocked}}
	msgs := make(chan agent.Message, 2)
	msgs <- agent.Message{Type: "text", Content: json.RawMessage(`"` + strings.Repeat("<", 6<<20) + `"`)}
	msgs <- agent.Message{Type: "text", Content: json.RawMessage(`"tail"`)}
	close(msgs)
	err := d.stream(reportFixture(), msgs, func() {})
	var oversized *oversizedMessageError
	if err == nil || errors.As(err, &oversized) {
		t.Fatalf("persistence failure hidden by oversize diagnostic: %v", err)
	}
}

// A control-plane loss must release the attempt for retry without executing it.
func TestControlPlaneTransportLossRetriesWithoutDuplicateCLI(t *testing.T) {
	for _, stage := range []string{"prepare", "start"} {
		t.Run(stage, func(t *testing.T) {
			f := fixture(t)
			_, err := f.env.Owner.Exec(t.Context(), "UPDATE agent_run SET max_attempts=2 WHERE id=$1", f.id)
			must(t, err)
			if stage == "prepare" {
				f.refusePrepare.Store(true)
			} else {
				f.dropStartACK.Store(true)
			}
			// The fake CLI appends one argv line per launch.
			invocations := filepath.Join(f.cfg.Home, ".caches", "npm", "argv")
			cancel, done := startDaemon(t, f.cfg)
			waitFor(t, func() bool { f.skipBackoff(t); s := f.status(t); return s == "completed" || s == "failed" })
			stopDaemon(t, cancel, done)
			if s := f.status(t); s != "completed" {
				t.Fatalf("transport loss terminally settled run: %s", s)
			}
			b, err := os.ReadFile(invocations)
			must(t, err)
			if strings.Count(string(b), "\n") != 1 {
				t.Fatalf("duplicate CLI execution: %q", b)
			}
			var attempt, usage int
			var cost float64
			must(t, f.env.Owner.QueryRow(t.Context(), "SELECT attempt,cost_usd FROM agent_run WHERE id=$1", f.id).Scan(&attempt, &cost))
			must(t, f.env.Owner.QueryRow(t.Context(), "SELECT count(*) FROM agent_run_usage WHERE agent_run_id=$1", f.id).Scan(&usage))
			if attempt != 2 || cost != .25 || usage != 1 {
				t.Fatalf("attempt=%d cost=%v usage=%d", attempt, cost, usage)
			}
		})
	}
}

func TestRealAdapterCancellationBacklogIsDurable(t *testing.T) {
	f := fixture(t)
	ready := filepath.Join(f.cfg.Home, ".caches", "written")
	bin := f.cfg.Harnesses["claude"]
	must(t, os.WriteFile(bin, []byte(`#!/bin/sh
[ "$1" = --version ] && exit 0
IFS= read -r first
case "$first" in *'"subtype":"initialize"'*) echo '{"type":"control_response","response":{"subtype":"success","request_id":"models-init","response":{"models":[]}}}'; exit 0;; esac
i=0; while [ "$i" -lt 200 ]; do echo '{"type":"log","message":"evidence"}';i=$((i+1));done
echo ready > '`+ready+`'
sleep 300
`), 0700))
	entered := make(chan struct{})
	release := make(chan struct{})
	f.cfg.Flush = time.Hour
	f.cfg.NewBackend = func(h, b string) (agent.Backend, error) {
		real, err := agent.New(h, b)
		if err != nil {
			return nil, err
		}
		return backendFunc(func(ctx context.Context, prompt string, o agent.ExecOptions) (*agent.Session, error) {
			persist := o.OnMessage
			first := true
			o.OnMessage = func(m agent.Message) error {
				if first {
					first = false
					close(entered)
					<-release
				}
				return persist(m)
			}
			return real.Execute(ctx, prompt, o)
		}), nil
	}
	cancel, done := startDaemon(t, f.cfg)
	<-entered
	waitFor(t, func() bool { _, err := os.Stat(ready); return err == nil })
	cancel()
	close(release)
	stopDaemon(t, cancel, done)
	paths, err := filepath.Glob(filepath.Join(f.cfg.StateDir, "messages", "*.json"))
	must(t, err)
	n := 0
	for _, p := range paths {
		r, err := readReport(p)
		must(t, err)
		n += len(r.Messages)
	}
	if n != 200 {
		t.Fatalf("real adapter cancellation lost durable messages: %d", n)
	}
	reports, err := filepath.Glob(filepath.Join(f.cfg.StateDir, "outbox", "*.json"))
	must(t, err)
	if len(reports) != 1 {
		t.Fatal("missing final report", reports)
	}
}

func TestRecoveryPreservesExistingFinalAndMakesScratchGCEligible(t *testing.T) {
	r := reportFixture()
	r.Kind = "complete"
	cost := .4
	r.Usage = []agent.Usage{{Model: "m", CostUSD: &cost}}
	state, home := t.TempDir(), t.TempDir()
	d := &daemon{cfg: Config{StateDir: state, Home: home}, active: map[string]bool{}}
	d.messages = &Outbox{Dir: filepath.Join(state, "messages"), Send: func(context.Context, Report) error { return nil }}
	var delivered []Report
	d.out = &Outbox{Dir: filepath.Join(state, "outbox"), Send: func(_ context.Context, r Report) error {
		delivered = append(delivered, r)
		return errors.New("offline")
	}}
	j := r
	j.Kind = "fail"
	j.Usage = nil
	must(t, d.journal().Put(j))
	must(t, d.out.Put(r))
	dir := filepath.Join(home, scratchName(r.AgentRunID, r.Attempt))
	must(t, os.Mkdir(dir, 0700))
	must(t, os.Mkdir(filepath.Join(dir, ".cli-home"), 0700))
	must(t, os.WriteFile(filepath.Join(dir, ".cli-home", "auth.json"), []byte("synthetic"), 0600))
	must(t, d.recoverAttempts(t.Context()))
	saved, err := readReport(d.out.file(r))
	must(t, err)
	if saved.Kind != "complete" || len(saved.Usage) != 1 || *saved.Usage[0].CostUSD != .4 {
		t.Fatalf("final report replaced: %+v", saved)
	}
	for _, r := range delivered {
		if r.Kind != "complete" {
			t.Fatal("declared valid final lost")
		}
	}
	must(t, d.gc(t.Context()))
	if _, err := os.Stat(dir); !os.IsNotExist(err) {
		t.Fatal("recovered finished scratch never became collectible")
	}
}

func TestRecoveryNeverSynthesizesLossForAcknowledgedFinals(t *testing.T) {
	state := t.TempDir()
	d := &daemon{cfg: Config{StateDir: state, Home: t.TempDir()}}
	d.messages = &Outbox{Dir: filepath.Join(state, "messages"), Send: func(context.Context, Report) error { return nil }}
	count := 0
	d.out = &Outbox{Dir: filepath.Join(state, "outbox"), Send: func(_ context.Context, r Report) error {
		count++
		if r.Kind != "complete" {
			t.Errorf("valid final declared lost: %+v", r)
		}
		return nil
	}}
	for i := 0; i < 2; i++ {
		r := reportFixture()
		r.Kind = "complete"
		must(t, d.out.Put(r))
		j := r
		j.Kind = "fail"
		must(t, d.journal().Put(j))
	}
	must(t, d.recoverAttempts(t.Context()))
	if count != 2 {
		t.Fatalf("final deliveries=%d want 2", count)
	}
}

func TestPrepareConnectionRefusalAndTimeoutAreRetryable(t *testing.T) {
	for _, fault := range []string{"refused", "timeout"} {
		t.Run(fault, func(t *testing.T) {
			f := fixture(t)
			_, err := f.env.Owner.Exec(t.Context(), "UPDATE agent_run SET max_attempts=2 WHERE id=$1", f.id)
			must(t, err)
			refs, err := f.runs.Register(t.Context(), f.user, f.ws, "transport", "test", []string{"claude"}, nil, agent.Platform{})
			must(t, err)
			claims, err := f.runs.Claim(t.Context(), f.user, []service.RuntimeRef{{WorkspaceID: f.ws, RuntimeID: refs[0].ID}}, 1)
			must(t, err)
			if len(claims) != 1 {
				t.Fatal("no test claim")
			}
			b, err := json.Marshal(claims[0])
			must(t, err)
			var c claimed
			must(t, json.Unmarshal(b, &c))
			endpoint := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				select {
				case <-r.Context().Done():
				case <-time.After(150 * time.Millisecond):
				}
			}))
			defer endpoint.Close()
			if fault == "refused" {
				endpoint.Close()
			}
			f.cfg.defaults()
			d := &daemon{cfg: f.cfg, api: &apiclient.Client{Server: endpoint.URL, Token: f.cfg.Token, HTTP: &http.Client{Timeout: 40 * time.Millisecond}}}
			d.messages = &Outbox{Dir: filepath.Join(f.cfg.StateDir, "messages"), Send: d.sendMessages}
			d.out = &Outbox{Dir: filepath.Join(f.cfg.StateDir, "outbox"), Send: d.send}
			d.ownerOf = map[uuid.UUID]uuid.UUID{*c.AgentRun.RuntimeID: c.AgentRun.ResponsibleUserID}
			must(t, d.runAgentRun(t.Context(), c))
			r, err := readReport(d.out.file(Report{AgentRunID: f.id, Attempt: 1}))
			must(t, err)
			if r.FailureReason != agent.ReasonRuntimeOffline {
				t.Fatalf("%s became %s", fault, r.FailureReason)
			}
			if _, err := os.Stat(filepath.Join(f.cfg.Home, f.id.String()+"-1", ".agent-process")); !os.IsNotExist(err) {
				t.Fatal("CLI launched after unavailable prepare")
			}
			d.api = &apiclient.Client{Server: f.cfg.Server, Token: f.cfg.Token}
			must(t, d.flush(t.Context()))
			if status := f.status(t); status != "queued" {
				t.Fatalf("%s did not retry: %s", fault, status)
			}
			var spend float64
			must(t, f.env.Owner.QueryRow(t.Context(), "SELECT cost_usd FROM agent_run WHERE id=$1", f.id).Scan(&spend))
			if spend != 0 {
				t.Fatalf("transport loss spent %v", spend)
			}
		})
	}
}

func TestShutdownDuringInitialRegistrationIsGraceful(t *testing.T) {
	started := make(chan struct{})
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { close(started); <-r.Context().Done() }))
	defer srv.Close()
	ctx, cancel := context.WithCancel(t.Context())
	done := make(chan error, 1)
	go func() {
		done <- Run(ctx, Config{Server: srv.URL, Token: "lyk_synthetic", StateDir: t.TempDir(), Home: t.TempDir(), Harnesses: map[string]string{"claude": "unused"}, Ready: func() error { t.Error("cancelled registration announced ready"); return nil }})
	}()
	<-started
	cancel()
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("ordinary startup shutdown: %v", err)
		}
	case <-time.After(time.Second):
		t.Fatal("shutdown blocked")
	}
}

// Concurrent runs of a scope always receive separate attempt folders.
func twoScopeRuns(t *testing.T, live time.Duration) (dirs []string, gap time.Duration) {
	t.Helper()
	f := fixture(t)
	must(t, SavePolicy(f.cfg.StateDir, f.ws, Policy{}))
	var mu sync.Mutex
	var starts []time.Time
	firstStarted := make(chan struct{})
	both := make(chan struct{})
	f.cfg.NewBackend = func(string, string) (agent.Backend, error) {
		return backendFunc(func(_ context.Context, _ string, o agent.ExecOptions) (*agent.Session, error) {

			mu.Lock()
			dirs, starts = append(dirs, o.Dir), append(starts, time.Now())
			if len(dirs) == 1 {
				close(firstStarted)
			}
			if len(dirs) == 2 {
				close(both)
			}
			mu.Unlock()
			msgs := make(chan agent.Message)
			done := make(chan agent.Result, 1)
			go func() {
				select {
				case <-both:
				case <-time.After(live):
				}
				close(msgs)
				cost := .1
				done <- agent.Result{Usage: []agent.Usage{{Model: "m", CostUSD: &cost}}}
				close(done)
			}()
			return &agent.Session{Messages: msgs, Done: done}, nil
		}), nil
	}
	cancel, done := startDaemon(t, f.cfg)
	select {
	case <-firstStarted:
	case <-time.After(5 * time.Second):
		t.Fatal("first backend did not start")
	}
	f.enqueue(t)
	waitFor(t, func() bool {
		var n int
		must(t, f.env.Owner.QueryRow(context.Background(), "SELECT count(*) FROM agent_run WHERE card_id=$1 AND status IN ('completed','failed')", f.card).Scan(&n))
		return n == 2
	})
	stopDaemon(t, cancel, done)
	if len(dirs) != 2 {
		t.Fatalf("folders %v", dirs)
	}
	return dirs, starts[1].Sub(starts[0])
}

// Recovery still cleans up scope folders created before the upgrade.
func TestRecoveryReachesAScopeFolder(t *testing.T) {
	r := reportFixture()
	r.Kind = "fail"
	r.Scratch, r.RetentionDays = "scope-"+strings.Repeat("a", 64), 7
	state, home := t.TempDir(), t.TempDir()
	d := &daemon{cfg: Config{StateDir: state, Home: home}, active: map[string]bool{}}
	d.messages = &Outbox{Dir: filepath.Join(state, "messages"), Send: func(context.Context, Report) error { return nil }}
	d.out = &Outbox{Dir: filepath.Join(state, "outbox"), Send: func(context.Context, Report) error { return errors.New("offline") }}
	must(t, d.journal().Put(r))
	dir := filepath.Join(home, r.Scratch)
	must(t, os.MkdirAll(filepath.Join(dir, ".cli-home"), 0700))
	must(t, os.WriteFile(filepath.Join(dir, ".cli-home", "auth.json"), []byte("synthetic"), 0600))
	must(t, d.recoverAttempts(t.Context()))
	if _, err := os.Stat(filepath.Join(dir, ".cli-home", "auth.json")); !os.IsNotExist(err) {
		t.Fatal("sign-in copy left in a crashed scope folder")
	}
	b, err := os.ReadFile(finishedRecord(state, dir))
	var m struct {
		RetentionDays int `json:"retention_days"`
	}
	if err != nil || json.Unmarshal(b, &m) != nil || m.RetentionDays != 7 {
		t.Fatalf("crashed scope folder's marker %q %v", b, err)
	}
}

func TestScopeGCInvalidRetentionFallsBackToOneDay(t *testing.T) {
	for _, days := range []int{-1, 0, maxRetentionDays + 1, int(^uint(0) >> 1)} {
		t.Run(strconv.Itoa(days), func(t *testing.T) {
			dir, state := filepath.Join(t.TempDir(), "scope-invalid"), t.TempDir()
			must(t, os.Mkdir(dir, 0700))
			b, err := json.Marshal(map[string]int{"retention_days": days})
			must(t, err)
			finishedFor(t, state, dir, string(b), 25*time.Hour)
			must(t, collectScratch(dir, finishedRecord(state, dir)))
			if _, err := os.Stat(dir); !errors.Is(err, os.ErrNotExist) {
				t.Fatalf("invalid retention kept scope: %v", err)
			}
		})
	}
}

func TestTheRequestedModelReachesTheCLI(t *testing.T) {
	f := fixture(t)
	_, err := f.env.Owner.Exec(context.Background(), "UPDATE agent_run SET requested_model = 'claude-opus-5-5' WHERE id = $1", f.id)
	must(t, err)
	models := make(chan string, 1)
	f.cfg.NewBackend = func(string, string) (agent.Backend, error) {
		return backendFunc(func(_ context.Context, _ string, o agent.ExecOptions) (*agent.Session, error) {
			models <- o.Model
			msgs := make(chan agent.Message)
			done := make(chan agent.Result, 1)
			close(msgs)
			done <- agent.Result{}
			close(done)
			return &agent.Session{Messages: msgs, Done: done}, nil
		}), nil
	}
	cancel, done := startDaemon(t, f.cfg)
	var got string
	select {
	case got = <-models:
	case <-time.After(8 * time.Second):
		t.Fatal("backend not started")
	}
	waitFor(t, func() bool { s := f.status(t); return s == "completed" || s == "failed" })
	stopDaemon(t, cancel, done)
	if got != "claude-opus-5-5" {
		t.Fatalf("ExecOptions.Model = %q", got)
	}
}

func TestTheDaemonSendsBaseStates(t *testing.T) {
	var mu sync.Mutex
	var bodies []map[string]json.RawMessage
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body map[string]json.RawMessage
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Error(err)
		}
		mu.Lock()
		bodies = append(bodies, body)
		mu.Unlock()
		w.WriteHeader(204)
	}))
	defer srv.Close()
	refs := []runtimeRef{{WorkspaceID: uuid.New(), RuntimeID: uuid.New()}}
	d := &daemon{api: &apiclient.Client{Server: srv.URL}, refs: refs, bases: readyPython(t)}
	d.heartbeat(context.Background())
	d.sandboxErr = errors.New("sandbox-exec\x00 missin" + strings.Repeat("é", 100))
	d.heartbeat(context.Background())
	d.bases = nil
	d.heartbeat(context.Background())
	mu.Lock()
	defer mu.Unlock()
	if len(bodies) != 3 {
		t.Fatalf("heartbeats: %d", len(bodies))
	}
	var bases map[string]string
	if err := json.Unmarshal(bodies[0]["bases"], &bases); err != nil || !maps.Equal(bases, map[string]string{"python": "ready", "sandbox": "ready"}) {
		t.Fatalf("bases %s: %v", bodies[0]["bases"], err)
	}
	bases = nil
	if err := json.Unmarshal(bodies[1]["bases"], &bases); err != nil || !maps.Equal(bases, map[string]string{"python": "ready", "sandbox": "failed: sandbox-exec missin" + strings.Repeat("é", 90)}) {
		t.Fatalf("bases with a failed sandbox %s: %v", bodies[1]["bases"], err)
	}
	bases = nil
	if err := json.Unmarshal(bodies[2]["bases"], &bases); err != nil || !maps.Equal(bases, map[string]string{"sandbox": "failed: sandbox-exec missin" + strings.Repeat("é", 90)}) {
		t.Fatalf("a daemon without bases lost sandbox failure %s: %v", bodies[2]["bases"], err)
	}

}

// finishedFor writes the daemon's record that a folder finished, age old.
func finishedFor(t *testing.T, state, dir, body string, age time.Duration) {
	t.Helper()
	at := time.Now().Add(-age)
	p := finishedRecord(state, dir)
	must(t, os.MkdirAll(filepath.Dir(p), 0700))
	must(t, os.WriteFile(p, []byte(body), 0600))
	must(t, os.Chtimes(p, at, at))
}

func TestRemovedHarnessRunFails(t *testing.T) {
	for _, harness := range []string{"cursor", "antigravity"} {
		t.Run(harness, func(t *testing.T) {
			state, home := t.TempDir(), t.TempDir()
			c := claimFor(uuid.New(), uuid.New(), uuid.New(), "analysis")
			rt := uuid.New()
			c.AgentRun.RuntimeID = &rt
			c.AgentRun.Harness = harness
			launched := false
			d := &daemon{cfg: Config{StateDir: state, Home: home, NewBackend: func(string, string) (agent.Backend, error) {
				launched = true
				return nil, errors.New("unexpected backend launch")
			}}}
			d.out = &Outbox{Dir: filepath.Join(state, "outbox")}
			d.ownerOf = map[uuid.UUID]uuid.UUID{*c.AgentRun.RuntimeID: c.AgentRun.ResponsibleUserID}
			must(t, d.runAgentRun(t.Context(), c))
			report, err := readReport(d.out.file(Report{AgentRunID: c.AgentRun.ID, Attempt: c.AgentRun.Attempt}))
			must(t, err)
			if launched || report.Kind != "fail" || report.Error != agent.ErrHarnessRemoved.Error() {
				t.Fatalf("launched=%v report=%+v", launched, report)
			}
		})
	}
}

func TestNoScopeFolders(t *testing.T) {
	dirs, _ := twoScopeRuns(t, 300*time.Millisecond)
	if len(dirs) != 2 || dirs[0] == dirs[1] {
		t.Fatalf("attempts shared folder: %v", dirs)
	}
	for _, dir := range dirs {
		if strings.HasPrefix(filepath.Base(dir), "scope-") {
			t.Fatalf("created scope folder: %s", dir)
		}
	}
}

func TestSandboxHint(t *testing.T) {
	for _, tc := range []struct{ name, text, want string }{
		{"sign-in", "Not logged in · Please run /login", "The CLI couldn't read your sign-in inside Lykeion's sandbox. Sign in to the CLI on this machine, then run again."},
		{"codex state", "failed to initialize in-process app-server client", "Codex couldn't write its state inside Lykeion's sandbox."},
		{"path before", "cat: /Users/person/.config/cache: Operation not permitted", "Lykeion's sandbox blocked /Users/person/.config/cache. A personal MCP server or hook that needs it won't work in Lykeion runs."},
		{"path after", "Operation not permitted: /Users/person/cache", "Lykeion's sandbox blocked /Users/person/cache. A personal MCP server or hook that needs it won't work in Lykeion runs."},
		{"quoted path", "open '/Users/person/Library/Application Support/cache': Operation not permitted", "Lykeion's sandbox blocked /Users/person/Library/Application Support/cache. A personal MCP server or hook that needs it won't work in Lykeion runs."},
		{"no path", "Operation not permitted", ""},
		{"unrelated path", "/Users/person/cache opened\nOperation not permitted", ""},
		{"unrelated", "provider temporarily unavailable", ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := sandboxHint(tc.text); got != tc.want {
				t.Fatalf("sandboxHint(%q) = %q, want %q", tc.text, got, tc.want)
			}
		})
	}
}

func TestSandboxHintRunStream(t *testing.T) {
	for _, diagnostic := range []bool{true, false} {
		t.Run(strconv.FormatBool(diagnostic), func(t *testing.T) {
			f := fixture(t)
			body := `#!/bin/sh
IFS= read -r first
echo '{"type":"assistant","message":{"content":[{"type":"text","text":"Not logged in · Please run /login"}]}}'
`
			if diagnostic {
				body += "echo 'Not logged in · Please run /login' >&2\nexit 1\n"
			} else {
				body += `echo '{"type":"result","is_error":false,"session_id":"s"}'
`
			}
			must(t, os.WriteFile(f.cfg.Harnesses["claude"], []byte(body), 0700))
			cancel, done := startDaemon(t, f.cfg)
			// A signed-out CLI parks the run for the machine instead of failing it.
			wantNotes := 0
			reached := func() bool { return f.status(t) == "completed" }
			if diagnostic {
				wantNotes, reached = 1, func() bool { return f.parked(t) }
			}
			waitFor(t, reached)
			stopDaemon(t, cancel, done)
			var count, matching int
			must(t, f.env.Owner.QueryRow(t.Context(), "SELECT count(*), count(*) FILTER (WHERE content->>'message'=$2) FROM agent_run_message WHERE agent_run_id=$1 AND type='status' AND content->>'type'='sandbox'", f.id, "The CLI couldn't read your sign-in inside Lykeion's sandbox. Sign in to the CLI on this machine, then run again.").Scan(&count, &matching))
			if count != wantNotes || matching != wantNotes {
				t.Fatalf("sandbox notes=%d, matching=%d, want %d", count, matching, wantNotes)
			}
		})
	}
}

// attach links a file to the fixture's study under diskName, as creating the
// study with it would.
func (f *daemonFixture) attach(t *testing.T, diskName, data string) {
	t.Helper()
	ctx := context.Background()
	sum := sha256.Sum256([]byte(data))
	id := uuid.New()
	key := f.ws.String() + "/attachments/" + id.String()
	must(t, f.h.Blobs.Put(ctx, key, strings.NewReader(data), int64(len(data))))
	must(t, f.runs.Store.InWorkspace(ctx, f.ws, f.user, func(tx *store.Tx) error {
		if _, err := tx.Q.InsertPendingAttachment(ctx, db.InsertPendingAttachmentParams{ID: id, WorkspaceID: f.ws, UploaderID: f.user, Name: diskName, Kind: "file",
			ContentType: "text/csv", SizeBytes: int64(len(data)), Sha256: hex.EncodeToString(sum[:]), BlobKey: key}); err != nil {
			return err
		}
		_, err := tx.Q.LinkAttachment(ctx, db.LinkAttachmentParams{ID: id, CardID: &f.card, DiskName: diskName, RecordKey: "c1"})
		return err
	}))
	// A run sees the files its study had when the run was made; the fixture's
	// first run stands for one made after this file was attached.
	_, err := f.env.Owner.Exec(ctx, "UPDATE agent_run SET attachment_snapshot=array_append(attachment_snapshot, $1) WHERE id=$2", id, f.id)
	must(t, err)
}

func zipAttachment(t *testing.T, name, body string) string {
	t.Helper()
	var b bytes.Buffer
	z := zip.NewWriter(&b)
	w, err := z.Create(name)
	must(t, err)
	_, err = w.Write([]byte(body))
	must(t, err)
	must(t, z.Close())
	return b.String()
}

func attachmentClaim(name, data string, unpacked int64, files int32) service.ClaimedAttachment {
	sum := sha256.Sum256([]byte(data))
	return service.ClaimedAttachment{ID: uuid.New(), DiskName: name, Sha256: hex.EncodeToString(sum[:]), SizeBytes: int64(len(data)), UnpackedBytes: unpacked, InnerFiles: files}
}

// attachmentPlacement uses real downloads and placement without a database or CLI.
func attachmentPlacement(t *testing.T, files []service.ClaimedAttachment, data map[uuid.UUID]string) (*daemon, func() (string, error), *atomic.Int32) {
	t.Helper()
	var downloads atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		downloads.Add(1)
		id, _ := uuid.Parse(strings.TrimPrefix(r.URL.Path, "/api/role/attachments/"))
		b, ok := data[id]
		if !ok {
			http.NotFound(w, r)
			return
		}
		_, _ = w.Write([]byte(b))
	}))
	t.Cleanup(srv.Close)
	d := &daemon{cfg: Config{StateDir: t.TempDir(), Server: srv.URL}}
	t.Cleanup(func() { _ = agent.RemoveTree(d.cfg.StateDir) })
	place := func() (string, error) {
		dir := t.TempDir()
		t.Cleanup(func() { _ = agent.RemoveTree(dir) })
		c := claimed{Attachments: files}
		c.AgentRun.Harness = "claude"
		_, err := d.placeAttachments(t.Context(), c, dir)
		return dir, err
	}
	return d, place, &downloads
}

func TestAnArchiveArrivesUnpackedAndReadOnly(t *testing.T) {
	f := fixture(t)
	body := zipAttachment(t, "a/b.txt", "research")
	f.attach(t, "data.zip", body)
	_, err := f.env.Owner.Exec(t.Context(), "UPDATE attachment SET unpacked_bytes=8, inner_files=2 WHERE card_id=$1", f.card)
	must(t, err)
	var got []byte
	var mode os.FileMode
	f.atRunEnd(func(dir string) {
		path := filepath.Join(dir, "attachments", "data", "a", "b.txt")
		got, _ = os.ReadFile(path)
		if fi, err := os.Stat(path); err == nil {
			mode = fi.Mode().Perm()
		}
	})
	cancel, done := startDaemon(t, f.cfg)
	waitFor(t, func() bool { return f.status(t) == "completed" || f.status(t) == "failed" })
	stopDaemon(t, cancel, done)
	if string(got) != "research" || mode&0222 != 0 {
		t.Fatalf("unpacked file=%q mode=%v status=%s", got, mode, f.status(t))
	}
}

func TestAnArchiveIsUnpackedOnce(t *testing.T) {
	body := zipAttachment(t, "a/b.txt", "payload")
	a := attachmentClaim("data.zip", body, 7, 2)
	d, place, _ := attachmentPlacement(t, []service.ClaimedAttachment{a}, map[uuid.UUID]string{a.ID: body})
	_, err := place()
	must(t, err)
	path := filepath.Join(d.cfg.StateDir, "cache", "attachments", a.Sha256+".d")
	first, err := os.Stat(path)
	must(t, err)
	_, err = place()
	must(t, err)
	second, err := os.Stat(path)
	must(t, err)
	if !os.SameFile(first, second) {
		t.Fatal("unpacked cache was rebuilt")
	}
}

func TestArchiveFoldersDoNotReplaceAttachments(t *testing.T) {
	var tarBody bytes.Buffer
	gz := gzip.NewWriter(&tarBody)
	tw := tar.NewWriter(gz)
	must(t, tw.WriteHeader(&tar.Header{Name: "value", Mode: 0600, Size: 3}))
	_, err := tw.Write([]byte("tar"))
	must(t, err)
	must(t, tw.Close())
	must(t, gz.Close())
	names := []string{"data", "data.zip", "data.tar.gz", "bundle", "DATA (2).ZIP", "é.zip", "e\u0301.tar.gz"}
	bodies := []string{"raw", zipAttachment(t, "value", "zip"), tarBody.String(), zipAttachment(t, "value", "extensionless"), zipAttachment(t, "value", "upper"), zipAttachment(t, "value", "accent"), zipAttachment(t, "value", "decomposed")}
	folders := []string{"", "data (2)", "data (3)", "bundle (2)", "DATA (2) (2)", "é", "e\u0301 (2)"}
	payloads := []string{"", "zip", "tar", "extensionless", "upper", "accent", "decomposed"}
	var claims []service.ClaimedAttachment
	data := map[uuid.UUID]string{}
	for i, name := range names {
		n := int32(1)
		if i == 0 {
			n = 0
		}
		a := attachmentClaim(name, bodies[i], int64(len(payloads[i])), n)
		claims = append(claims, a)
		data[a.ID] = bodies[i]
	}
	d, _, _ := attachmentPlacement(t, claims, data)
	for run := 0; run < 2; run++ {
		dir := t.TempDir()
		t.Cleanup(func() { _ = agent.RemoveTree(dir) })
		instructions, err := d.placeAttachments(t.Context(), claimed{Attachments: claims}, dir)
		must(t, err)
		for i, name := range names {
			raw, err := os.ReadFile(filepath.Join(dir, "attachments", name))
			must(t, err)
			if string(raw) != bodies[i] {
				t.Fatalf("raw %s replaced", name)
			}
			if folders[i] == "" {
				continue
			}
			path := filepath.Join(dir, "attachments", folders[i], "value")
			got, err := os.ReadFile(path)
			must(t, err)
			fi, err := os.Stat(path)
			must(t, err)
			if string(got) != payloads[i] || fi.Mode().Perm()&0222 != 0 {
				t.Fatalf("folder %s payload=%q mode=%v", folders[i], got, fi.Mode())
			}
			if !strings.Contains(string(instructions), "attachments/"+name) || !strings.Contains(string(instructions), "attachments/"+folders[i]+"/") {
				t.Fatalf("missing mapping %s -> %s: %s", name, folders[i], instructions)
			}
		}
	}
}

func TestALyingArchiveFailsTheRunAndLeavesNoTree(t *testing.T) {
	f := fixture(t)
	body := zipAttachment(t, "a/b.txt", "research")
	f.attach(t, "x.zip", body)
	_, err := f.env.Owner.Exec(t.Context(), "UPDATE attachment SET unpacked_bytes=1, inner_files=2 WHERE card_id=$1", f.card)
	must(t, err)
	cancel, done := startDaemon(t, f.cfg)
	waitFor(t, func() bool { return f.status(t) == "failed" || f.status(t) == "completed" })
	stopDaemon(t, cancel, done)
	var failure string
	must(t, f.env.Owner.QueryRow(t.Context(), "SELECT error FROM agent_run WHERE id=$1", f.id).Scan(&failure))
	if f.status(t) != "failed" || !strings.Contains(failure, `attached file "x.zip"`) {
		t.Fatalf("status=%s error=%s", f.status(t), failure)
	}
	sum := sha256.Sum256([]byte(body))
	root := filepath.Join(f.cfg.StateDir, "cache", "attachments", hex.EncodeToString(sum[:]))
	for _, suffix := range []string{".d", ".d.part"} {
		if _, err := os.Stat(root + suffix); !errors.Is(err, os.ErrNotExist) {
			t.Fatalf("left tree %s: %v", suffix, err)
		}
	}
}

func TestACompressedArchiveUsesItsUnpackedLimit(t *testing.T) {
	for _, tc := range []struct {
		name, body string
		bytes      int64
		files      int32
		ok         bool
	}{
		{"compressed", strings.Repeat("x", 10000), 10000, 1, true},
		{"zero bytes", "x", 0, 1, false}, {"zero files", "", 0, 0, false}, {"empty file", "", 0, 1, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			body := zipAttachment(t, "value", tc.body)
			a := attachmentClaim("data.zip", body, tc.bytes, tc.files)
			_, place, _ := attachmentPlacement(t, []service.ClaimedAttachment{a}, map[uuid.UUID]string{a.ID: body})
			dir, err := place()
			if tc.ok {
				must(t, err)
				got, err := os.ReadFile(filepath.Join(dir, "attachments", "data", "value"))
				must(t, err)
				if string(got) != tc.body {
					t.Fatal("wrong payload")
				}
			} else if err == nil {
				t.Fatal("claim limit ignored")
			}
		})
	}
}

func TestAVerifiedCacheIsNotRehashed(t *testing.T) {
	old := newAttachmentHash
	var hashes atomic.Int32
	newAttachmentHash = func() hash.Hash { hashes.Add(1); return old() }
	t.Cleanup(func() { newAttachmentHash = old })
	a := attachmentClaim("data.txt", "payload", 0, 0)
	_, place, _ := attachmentPlacement(t, []service.ClaimedAttachment{a}, map[uuid.UUID]string{a.ID: "payload"})
	_, err := place()
	must(t, err)
	_, err = place()
	must(t, err)
	if hashes.Load() != 1 {
		t.Fatalf("hashed %d times, want download verification once", hashes.Load())
	}
}

func TestATamperedCacheIsFetchedAgain(t *testing.T) {
	a := attachmentClaim("data.txt", "payload", 0, 0)
	d, place, gets := attachmentPlacement(t, []service.ClaimedAttachment{a}, map[uuid.UUID]string{a.ID: "payload"})
	_, err := place()
	must(t, err)
	path := filepath.Join(d.cfg.StateDir, "cache", "attachments", a.Sha256)
	must(t, os.Chmod(path, 0600))
	must(t, os.WriteFile(path, []byte("changed"), 0444))
	must(t, os.Chtimes(path, time.Now().Add(time.Hour), time.Now().Add(time.Hour)))
	dir, err := place()
	must(t, err)
	got, err := os.ReadFile(filepath.Join(dir, "attachments", a.DiskName))
	must(t, err)
	if string(got) != "payload" || gets.Load() != 2 {
		t.Fatalf("tamper got=%q downloads=%d", got, gets.Load())
	}
}

// probingClaude is the fake CLI that first tries, from inside the run's
// sandbox, to change the study's files or give one a second, writable name,
// and writes what it managed to probe.txt in its run folder.
func probingClaude(t *testing.T) string {
	t.Helper()
	b, err := os.ReadFile(fakeClaude(t))
	must(t, err)
	// The version check runs the CLI outside any run folder; it probes nothing.
	probe := `case "$*" in *--version*) ;; *) {
	cat attachments/data.csv >/dev/null && echo READ
	(printf x >> attachments/data.csv) 2>/dev/null && echo WROTE
	(: > attachments/new.csv) 2>/dev/null && echo CREATED
	(chmod u+w attachments/data.csv) 2>/dev/null && echo CHMOD
	ln attachments/data.csv linked.csv 2>/dev/null && echo LINKED
	rm -f attachments/data.csv 2>/dev/null; [ -e attachments/data.csv ] || echo REMOVED
	(mv attachments gone) 2>/dev/null && echo MOVED
} > probe.txt 2>&1 ;; esac
`
	p := filepath.Join(filepath.Dir(fakeClaude(t)), "probing-claude")
	must(t, os.WriteFile(p, []byte(strings.Replace(string(b), "#!/bin/sh\n", "#!/bin/sh\n"+probe, 1)), 0700))
	return p
}

func (f *daemonFixture) runFolder() string { return filepath.Join(f.cfg.Home, f.id.String()+"-1") }

// atRunEnd calls check with each run's folder once its CLI has finished and
// before the daemon removes the folder.
func (f *daemonFixture) atRunEnd(check func(dir string)) {
	f.cfg.NewBackend = func(h, b string) (agent.Backend, error) {
		real, err := agent.New(h, b)
		if err != nil {
			return nil, err
		}
		return backendFunc(func(ctx context.Context, p string, o agent.ExecOptions) (*agent.Session, error) {
			s, err := real.Execute(ctx, p, o)
			if err != nil {
				return nil, err
			}
			done := make(chan agent.Result, 1)
			go func() {
				defer close(done)
				r, ok := <-s.Done
				check(o.Dir)
				if ok {
					done <- r
				}
			}()
			return &agent.Session{Messages: s.Messages, Done: done}, nil
		}), nil
	}
}

func TestARunGetsItsStudysFilesReadOnly(t *testing.T) {
	if err := sandbox.Check(context.Background()); err != nil {
		t.Skipf("sandbox unavailable: %v", err)
	}
	f := fixture(t)
	f.cfg.Harnesses["claude"] = probingClaude(t)
	f.attach(t, "data.csv", "gene,fold\nPGC1A,2.4\n")
	var probe, got []byte
	var mode os.FileMode
	f.atRunEnd(func(dir string) {
		probe, _ = os.ReadFile(filepath.Join(dir, "probe.txt"))
		path := filepath.Join(dir, "attachments", "data.csv")
		got, _ = os.ReadFile(path)
		if fi, err := os.Stat(path); err == nil {
			mode = fi.Mode().Perm()
		}
	})
	cancel, done := startDaemon(t, f.cfg)
	waitFor(t, func() bool { return f.status(t) == "completed" })
	stopDaemon(t, cancel, done)
	if string(probe) != "READ\n" {
		t.Fatalf("inside the sandbox the run managed more than reading its file:\n%s", probe)
	}
	if string(got) != "gene,fold\nPGC1A,2.4\n" || mode != 0o444 {
		t.Fatalf("the run's file: %q mode %v", got, mode)
	}
}

func TestTheCacheIsCheckedAndReused(t *testing.T) {
	f := fixture(t)
	const data = "gene,fold\nTFAM,1.9\n"
	f.attach(t, "data.csv", data)
	sum := sha256.Sum256([]byte(data))
	cached := filepath.Join(f.cfg.StateDir, "cache", "attachments", hex.EncodeToString(sum[:]))
	var mu sync.Mutex
	var got string
	var linked bool
	f.atRunEnd(func(dir string) {
		mu.Lock()
		defer mu.Unlock()
		path := filepath.Join(dir, "attachments", "data.csv")
		b, _ := os.ReadFile(path)
		got = string(b)
		ri, err1 := os.Stat(path)
		ci, err2 := os.Stat(cached)
		linked = err1 == nil && err2 == nil && os.SameFile(ci, ri)
	})
	cancel, done := startDaemon(t, f.cfg)
	defer stopDaemon(t, cancel, done)
	runOnce := func() string {
		t.Helper()
		waitFor(t, func() bool { return f.status(t) == "completed" })
		mu.Lock()
		defer mu.Unlock()
		return got
	}
	if got := runOnce(); got != data || f.attachmentGets.Load() != 1 {
		t.Fatalf("first run: %q after %d downloads", got, f.attachmentGets.Load())
	}
	if b, err := os.ReadFile(cached); err != nil || string(b) != data {
		t.Fatalf("cache: %q %v", b, err)
	}
	// The run's file is the cached one, linked rather than copied.
	mu.Lock()
	if !linked {
		t.Fatal("the run's file is not linked to the cache")
	}
	mu.Unlock()
	mu.Lock()
	got = ""
	mu.Unlock()
	f.enqueue(t)
	if got := runOnce(); got != data || f.attachmentGets.Load() != 1 {
		t.Fatalf("second run: %q after %d downloads", got, f.attachmentGets.Load())
	}
	// A damaged cache entry is replaced, not handed to a run.
	must(t, os.Remove(cached))
	must(t, os.WriteFile(cached, []byte("gene,fold\nTFAM,9.9\n"), 0o444))
	mu.Lock()
	got = ""
	mu.Unlock()
	f.enqueue(t)
	if got := runOnce(); got != data || f.attachmentGets.Load() != 2 {
		t.Fatalf("after damage: %q after %d downloads", got, f.attachmentGets.Load())
	}
}

func TestAnUnsafeOrMissingFileFailsTheRun(t *testing.T) {
	for name, setup := range map[string]func(*daemonFixture){
		"a name that leaves the folder": func(f *daemonFixture) { f.attach(t, "../escape.csv", "x") },
		"a file that can't be fetched": func(f *daemonFixture) {
			f.attach(t, "data.csv", "x")
			must(t, os.RemoveAll(f.h.Blobs.(storage.Local).Dir))
		},
	} {
		t.Run(name, func(t *testing.T) {
			f := fixture(t)
			setup(f)
			cancel, done := startDaemon(t, f.cfg)
			waitFor(t, func() bool { return f.status(t) == "failed" })
			stopDaemon(t, cancel, done)
			var msg string
			must(t, f.env.Owner.QueryRow(context.Background(), "SELECT error FROM agent_run WHERE id=$1", f.id).Scan(&msg))
			if !strings.Contains(msg, "attached file") {
				t.Fatalf("error %q", msg)
			}
			if _, err := os.Stat(filepath.Join(f.runFolder(), "..", "escape.csv")); err == nil {
				t.Fatal("a file was written outside the run's attachments folder")
			}
			if b, err := os.ReadFile(filepath.Join(f.cfg.Home, ".caches", "npm", "argv")); err == nil {
				t.Fatalf("the CLI started: %s", b)
			}
		})
	}
}

func TestACachedArchiveStillEnforcesEachClaimsLimits(t *testing.T) {
	body := zipAttachment(t, "a/b.txt", "payload")
	a := attachmentClaim("data.zip", body, 7, 2)
	claims := []service.ClaimedAttachment{a}
	_, place, _ := attachmentPlacement(t, claims, map[uuid.UUID]string{a.ID: body})
	_, err := place()
	must(t, err)
	for _, limits := range []struct {
		bytes int64
		files int32
	}{{6, 2}, {7, 1}, {0, 2}, {7, 0}} {
		claims[0].UnpackedBytes = limits.bytes
		claims[0].InnerFiles = limits.files
		dir, err := place()
		if err == nil {
			t.Fatalf("cached archive bypassed limits %+v", limits)
		}
		if _, err := os.Stat(filepath.Join(dir, "attachments", "data")); !errors.Is(err, os.ErrNotExist) {
			t.Fatalf("published rejected tree: %v", err)
		}
	}
}

func TestConcurrentArchiveRunsBuildOneCacheTree(t *testing.T) {
	body := zipAttachment(t, "a/b.txt", strings.Repeat("x", 10000))
	a := attachmentClaim("data.zip", body, 10000, 2)
	_, place, gets := attachmentPlacement(t, []service.ClaimedAttachment{a}, map[uuid.UUID]string{a.ID: body})
	var wg sync.WaitGroup
	for range 4 {
		wg.Go(func() {
			dir, err := place()
			if err != nil {
				t.Error(err)
				return
			}
			got, err := os.ReadFile(filepath.Join(dir, "attachments", "data", "a", "b.txt"))
			if err != nil || len(got) != 10000 {
				t.Errorf("concurrent tree bytes=%d err=%v", len(got), err)
			}
		})
	}
	wg.Wait()
	if gets.Load() != 1 {
		t.Fatalf("concurrent cache downloaded %d times", gets.Load())
	}
}

func TestAnArchiveRecoversAReadOnlyPartialTree(t *testing.T) {
	body := zipAttachment(t, "a/b.txt", "payload")
	a := attachmentClaim("data.zip", body, 7, 2)
	d, place, _ := attachmentPlacement(t, []service.ClaimedAttachment{a}, map[uuid.UUID]string{a.ID: body})
	partial := filepath.Join(d.cfg.StateDir, "cache", "attachments", a.Sha256+".d.part")
	must(t, os.MkdirAll(filepath.Join(partial, "stale"), 0700))
	must(t, os.WriteFile(filepath.Join(partial, "stale", "old"), []byte("stale"), 0444))
	must(t, os.Chmod(filepath.Join(partial, "stale"), 0555))
	must(t, os.Chmod(partial, 0555))
	dir, err := place()
	must(t, err)
	got, err := os.ReadFile(filepath.Join(dir, "attachments", "data", "a", "b.txt"))
	must(t, err)
	if string(got) != "payload" {
		t.Fatal("wrong recovered payload")
	}
	if _, err := os.Stat(partial); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("left partial: %v", err)
	}
}

func TestArchiveTreePlacementPreservesAnExistingDestination(t *testing.T) {
	src := t.TempDir()
	must(t, os.WriteFile(filepath.Join(src, "value"), []byte("archive"), 0444))
	dst := filepath.Join(t.TempDir(), "raw")
	must(t, os.WriteFile(dst, []byte("raw file"), 0444))
	if err := linkReadOnlyTree(src, dst); err == nil {
		t.Fatal("existing destination replaced")
	}
	got, err := os.ReadFile(dst)
	must(t, err)
	if string(got) != "raw file" {
		t.Fatalf("raw destination changed: %q", got)
	}
}
