package daemon

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/google/uuid"

	"lykeion/server/internal/apiclient"
	"lykeion/server/internal/auth"
	"lykeion/server/internal/handler"
	"lykeion/server/internal/realtime"
	"lykeion/server/internal/service"
	"lykeion/server/internal/storage"
	"lykeion/server/internal/store"
	"lykeion/server/internal/testutil"
	"lykeion/server/pkg/agent"
	db "lykeion/server/pkg/db/generated"
)

type daemonFixture struct {
	dropMessageACK     atomic.Bool
	dropReportACK      atomic.Bool
	env                *testutil.Env
	runs               *service.AgentRuns
	ws, user, card, id uuid.UUID
	cfg                Config
	rejectMessages     atomic.Bool
	messageCalls       atomic.Int32
}

func fixture(t *testing.T) *daemonFixture {
	t.Helper()
	t.Setenv("HOME", t.TempDir())
	t.Setenv("CODEX_HOME", "")
	t.Setenv("CLAUDE_CONFIG_DIR", "")
	t.Setenv("LYK_TOKEN", "lyk_owner_must_never_leak")
	t.Setenv("LYK_CONFIG", "/private/owner/config")
	f := &daemonFixture{env: testutil.Setup(t), ws: uuid.New()}
	ctx := context.Background()
	st := &store.Store{Pool: f.env.App}
	hub := &realtime.Hub{Store: st, Redis: f.env.Redis}
	st.Publish = hub.Publish
	f.runs = &service.AgentRuns{Store: st}
	issuer := auth.Issuer{Secret: []byte("test"), TTL: time.Hour}
	router := handler.NewRouter(&handler.H{Store: st, JWT: issuer, Limit: auth.Limiter{R: f.env.Redis}, Hub: hub, AgentRuns: f.runs, Blobs: storage.Local{Dir: t.TempDir()}})
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasSuffix(r.URL.Path, "/messages") {
			f.messageCalls.Add(1)
			if f.rejectMessages.Load() {
				http.Error(w, "temporary outage", 503)
				return
			}
		}
		drop := (strings.HasSuffix(r.URL.Path, "/messages") && f.dropMessageACK.Swap(false)) || (strings.HasSuffix(r.URL.Path, "/complete") && f.dropReportACK.Swap(false))
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
		c, err := tx.Q.CreateCard(ctx, db.CreateCardParams{WorkspaceID: f.ws, Number: 1, Title: "t", Question: "q", OwnerID: u.ID, FollowsFrom: []uuid.UUID{}})
		f.card = c.ID
		return err
	}))
	jwt, err := issuer.Issue(u.ID, u.Email)
	must(t, err)
	var token struct{ Token string }
	must(t, (&apiclient.Client{Server: srv.URL, Token: jwt}).Do(ctx, "POST", "/api/tokens", map[string]string{"name": "daemon"}, &token))
	f.cfg = Config{Server: srv.URL, Token: token.Token, StateDir: t.TempDir(), Home: t.TempDir(), Device: "test", Slots: 2, Poll: 40 * time.Millisecond, Heartbeat: 100 * time.Millisecond, Flush: 50 * time.Millisecond, Harnesses: map[string]string{"claude": fakeClaude(t)}}
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
	p := filepath.Join(t.TempDir(), "claude")
	must(t, os.WriteFile(p, []byte(`#!/bin/sh
case "$LYK_TOKEN" in ltk_*) ;; *) exit 41;; esac
case "$LYK_CONFIG" in "$PWD"/*) ;; *) exit 42;; esac
[ ! -f "$LYK_CONFIG" ] || exit 43
case "$HOME" in "$PWD"/*) ;; *) exit 44;; esac
cat >/dev/null
echo '{"type":"assistant","message":{"content":[{"type":"text","text":"working"}]}}'
echo '{"type":"result","is_error":false,"total_cost_usd":0.25,"session_id":"s"}'
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
	dir := filepath.Join(f.cfg.Home, f.id.String()+"-1")
	skill, err := os.ReadFile(filepath.Join(dir, "SKILL.md"))
	must(t, err)
	if string(skill) != "# Scoper" {
		t.Fatal(string(skill))
	}
	if _, err = os.Stat(filepath.Join(dir, ".finished")); err != nil {
		t.Fatal(err)
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
	home := t.TempDir()
	d := &daemon{cfg: Config{Home: home}, active: map[string]bool{"active": true}}
	for _, name := range []string{"active", "finished", "unfinished", "recent"} {
		p := filepath.Join(home, name)
		must(t, os.Mkdir(p, 0700))
		if name != "unfinished" {
			must(t, os.WriteFile(filepath.Join(p, ".finished"), []byte("done"), 0600))
			if name != "recent" {
				past := time.Now().Add(-25 * time.Hour)
				must(t, os.Chtimes(filepath.Join(p, ".finished"), past, past))
			}
		}
		past := time.Now().Add(-48 * time.Hour)
		must(t, os.Chtimes(p, past, past))
	}
	must(t, d.gc(context.Background()))
	for _, name := range []string{"active", "unfinished", "recent"} {
		if _, err := os.Stat(filepath.Join(home, name)); err != nil {
			t.Fatal("removed live/recent scratch", name, err)
		}
	}
	if _, err := os.Stat(filepath.Join(home, "finished")); !errors.Is(err, os.ErrNotExist) {
		t.Fatal("retained old finished scratch", err)
	}
}
func TestDaemonOwnerHintWakesWithoutPoll(t *testing.T) {
	f := fixture(t)
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
			Runtimes []runtimeRef `json:"runtimes"`
			Slots    int          `json:"slots"`
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Error(err)
		}
		if len(body.Runtimes) > 64 || len(body.Runtimes) == 0 {
			t.Error("bad reference batch", len(body.Runtimes))
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
	waitFor(t, func() bool { return f.messageCalls.Load() > 0 })
	time.Sleep(200 * time.Millisecond)
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
