//go:build unix

package e2e

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"testing"
	"time"

	"github.com/google/uuid"

	"lykeion/server/internal/apiclient"
	"lykeion/server/internal/auth"
	"lykeion/server/internal/daemon"
	"lykeion/server/internal/handler"
	"lykeion/server/internal/realtime"
	"lykeion/server/internal/service"
	"lykeion/server/internal/storage"
	"lykeion/server/internal/store"
	"lykeion/server/internal/testutil"
	"lykeion/server/pkg/agent"
	db "lykeion/server/pkg/db/generated"
)

func must(t *testing.T, err error) {
	t.Helper()
	if err != nil {
		t.Fatal(err)
	}
}
func waitFor(t *testing.T, what string, fn func() bool) {
	t.Helper()
	waitWithin(t, 15*time.Second, what, fn)
}

// waitWithin is waitFor with its own deadline, for a wait that spans many
// runs one after another and so stretches with the machine's load.
func waitWithin(t *testing.T, limit time.Duration, what string, fn func() bool) {
	t.Helper()
	deadline := time.Now().Add(limit)
	for time.Now().Before(deadline) {
		if fn() {
			return
		}
		time.Sleep(25 * time.Millisecond)
	}
	t.Fatal("timed out waiting for " + what)
}

type lab struct {
	env         *testutil.Env
	st          *store.Store
	runs        *service.AgentRuns
	issuer      auth.Issuer
	ws          uuid.UUID
	url         string
	srv         *httptest.Server
	blobs       string
	mu          sync.Mutex
	connections map[net.Conn]bool
	calls       map[uuid.UUID]int
	loseACK     bool
}

func newLab(t *testing.T) *lab {
	t.Helper()
	t.Setenv("HOME", filepath.Dir(t.TempDir()))
	t.Setenv("CODEX_HOME", "")
	t.Setenv("CLAUDE_CONFIG_DIR", t.TempDir())
	if runtime.GOOS == "linux" {
		must(t, os.WriteFile(filepath.Join(os.Getenv("CLAUDE_CONFIG_DIR"), ".credentials.json"), []byte("test-signin-fixture"), 0600))
	}
	t.Setenv("LYK_TOKEN", "lyk_fake_parent_secret")
	t.Setenv("LYK_CONFIG", filepath.Join(t.TempDir(), "config"))
	l := &lab{env: testutil.Setup(t), ws: uuid.New(), issuer: auth.Issuer{Secret: []byte("test"), TTL: time.Hour}, blobs: t.TempDir(), calls: map[uuid.UUID]int{}, connections: map[net.Conn]bool{}}
	l.st = &store.Store{Pool: l.env.App}
	l.runs = l.service()
	must(t, l.st.InWorkspace(context.Background(), l.ws, uuid.Nil, func(tx *store.Tx) error {
		_, err := tx.Q.CreateWorkspace(context.Background(), db.CreateWorkspaceParams{ID: l.ws, Slug: "lab", Name: "Lab"})
		return err
	}))
	l.startHTTP(t)
	t.Cleanup(func() { l.stopHTTP(t) })
	return l
}
func (l *lab) service() *service.AgentRuns {
	return &service.AgentRuns{Store: l.st, Stale: 300 * time.Millisecond, OnSettlement: l.consume}
}

// The event is the durable consumer effect and deduplication key, committed in
// the same application store. A lost callback ACK must never duplicate it.
func (l *lab) consume(ctx context.Context, n service.Settlement) error {
	l.mu.Lock()
	l.calls[n.ID]++
	lose := l.loseACK
	l.loseACK = false
	l.mu.Unlock()
	err := l.st.InWorkspace(ctx, n.AgentRun.WorkspaceID, uuid.Nil, func(tx *store.Tx) error {
		if err := tx.Q.LockWorkspaceEvents(ctx, l.ws); err != nil {
			return err
		}
		run, err := tx.Q.GetAgentRun(ctx, db.GetAgentRunParams{WorkspaceID: l.ws, ID: n.ID})
		if err != nil {
			return err
		}
		if run.Status != n.AgentRun.Status {
			return fmt.Errorf("consumer saw uncommitted status %s, want %s", run.Status, n.AgentRun.Status)
		}
		if run.CardID == nil {
			return fmt.Errorf("fixture run has no card")
		}
		events, err := tx.Q.CardEventsAfter(ctx, db.CardEventsAfterParams{WorkspaceID: l.ws, CardID: *run.CardID})
		if err != nil {
			return err
		}
		for _, e := range events {
			if e.Type != "acceptance.settlement_applied" {
				continue
			}
			var p struct {
				ID uuid.UUID `json:"id"`
			}
			if err := json.Unmarshal(e.Payload, &p); err != nil {
				return err
			}
			if p.ID == n.ID {
				return nil
			}
		}
		return tx.Emit(ctx, "acceptance.settlement_applied", run.CardID, store.System, map[string]any{"id": n.ID, "status": run.Status})
	})
	if err != nil {
		return err
	}
	if lose {
		return errors.New("injected callback ACK loss after consumer commit")
	}
	return nil
}
func (l *lab) startHTTP(t *testing.T) {
	t.Helper()
	hub := &realtime.Hub{Store: l.st, Redis: l.env.Redis}
	l.st.Publish = hub.Publish
	srv := httptest.NewUnstartedServer(handler.NewRouter(&handler.H{Store: l.st, JWT: l.issuer, Limit: auth.Limiter{R: l.env.Redis}, Hub: hub, AgentRuns: l.runs, Blobs: storage.Local{Dir: l.blobs}}))
	if l.url != "" {
		must(t, srv.Listener.Close())
		listener, err := net.Listen("tcp", strings.TrimPrefix(l.url, "http://"))
		must(t, err)
		srv.Listener = listener
	}
	srv.Config.ConnState = func(c net.Conn, s http.ConnState) {
		l.mu.Lock()
		defer l.mu.Unlock()
		if s == http.StateNew {
			l.connections[c] = true
		}
		if s == http.StateClosed {
			delete(l.connections, c)
		}
	}
	srv.Start()
	l.srv = srv
	l.url = srv.URL
}
func (l *lab) stopHTTP(t *testing.T) {
	t.Helper()
	if l.srv == nil {
		return
	}
	// Close includes pooled HTTP connections; hijacked hint sockets need explicit closure.
	must(t, l.srv.Config.Close())
	l.mu.Lock()
	for c := range l.connections {
		if err := c.Close(); err != nil && !errors.Is(err, net.ErrClosed) {
			t.Error(err)
		}
		delete(l.connections, c)
	}
	l.mu.Unlock()
	l.srv.Close()
	l.srv = nil
}
func (l *lab) member(t *testing.T, email string) (uuid.UUID, string, string) {
	t.Helper()
	ctx := context.Background()
	u, err := l.st.Global().UpsertUserByEmail(ctx, email)
	must(t, err)
	must(t, l.st.InWorkspace(ctx, l.ws, u.ID, func(tx *store.Tx) error {
		return tx.Q.AddMember(ctx, db.AddMemberParams{WorkspaceID: l.ws, UserID: u.ID, Role: "member"})
	}))
	jwt, err := l.issuer.Issue(u.ID, email)
	must(t, err)
	var token struct{ Token string }
	must(t, (&apiclient.Client{Server: l.url, Token: jwt}).Do(ctx, "POST", "/api/tokens", map[string]string{"name": "acceptance"}, &token))
	if !strings.HasPrefix(token.Token, "lyk_") {
		t.Fatal("not a personal token")
	}
	return u.ID, token.Token, jwt
}
func (l *lab) queue(t *testing.T, owner uuid.UUID, n int) (uuid.UUID, []uuid.UUID) {
	t.Helper()
	return l.queueIn(t, owner, n, "grounding")
}

// queueIn opens a study in column and queues n runs of it.
func (l *lab) queueIn(t *testing.T, owner uuid.UUID, n int, column string) (uuid.UUID, []uuid.UUID) {
	t.Helper()
	ctx := context.Background()
	var card uuid.UUID
	ids := []uuid.UUID{}
	must(t, l.st.InWorkspace(ctx, l.ws, owner, func(tx *store.Tx) error {
		num, err := tx.Q.NextCardNumber(ctx, l.ws)
		if err != nil {
			return err
		}
		c, err := tx.Q.CreateCard(ctx, db.CreateCardParams{BoardColumn: column, WorkspaceID: l.ws, Number: num, Title: "Acceptance", Question: "q", OwnerID: owner})
		if err != nil {
			return err
		}
		card = c.ID
		for i := 0; i < n; i++ {
			r, err := l.runs.Enqueue(ctx, tx, owner, service.NewAgentRun{Card: card, Column: column, Round: 1, Role: "scoper", Harness: "claude", Prompt: "test", MaxAttempts: 2})
			if err != nil {
				return err
			}
			ids = append(ids, r.ID)
		}
		return nil
	}))
	return card, ids
}
func (l *lab) count(t *testing.T, q string, args ...any) int {
	t.Helper()
	var n int
	must(t, l.env.Owner.QueryRow(context.Background(), q, args...).Scan(&n))
	return n
}
func (l *lab) run(t *testing.T, id uuid.UUID) db.AgentRun {
	t.Helper()
	var r db.AgentRun
	must(t, l.st.InWorkspace(context.Background(), l.ws, uuid.Nil, func(tx *store.Tx) error {
		var err error
		r, err = tx.Q.GetAgentRun(context.Background(), db.GetAgentRunParams{WorkspaceID: l.ws, ID: id})
		return err
	}))
	return r
}
func (l *lab) settled(t *testing.T, ids []uuid.UUID) {
	t.Helper()
	for _, id := range ids {
		waitFor(t, "durable consumer effect for "+id.String(), func() bool {
			return l.count(t, "SELECT count(*) FROM event WHERE type='acceptance.settlement_applied' AND payload->>'id'=$1", id.String()) == 1 && l.count(t, "SELECT count(*) FROM agent_run_settlement WHERE agent_run_id=$1 AND delivered_at IS NOT NULL", id) == 1
		})
		if n := l.count(t, "SELECT count(*) FROM agent_run_settlement WHERE agent_run_id=$1 AND delivered_at IS NOT NULL", id); n != 1 {
			t.Fatalf("run %s acknowledged settlements=%d", id, n)
		}
	}
}
func (l *lab) config(t *testing.T, token, script string) daemon.Config {
	t.Helper()
	bin := filepath.Join(t.TempDir(), "claude")
	// Like a real Claude Code, the fake reports its version and schema flag,
	// answers the daemon's preflight hello, and, given a schema, ends with a
	// structured answer: a scoper's Grounding hand-in, or on Validation one
	// that could not be done.
	prelude := `#!/bin/sh
case "$1" in --version) echo "2.1.69 (Claude Code)"; exit 0;; --help) echo "  --json-schema <schema>  JSON Schema"; exit 0;; esac
IFS= read -r prompt
case "$prompt" in *'Reply with {'*'ok'*'true}.'*) echo '{"type":"result","is_error":false,"session_id":"hello","structured_output":{"ok":true}}'; exit 0;; esac
case "$prompt" in *models-init*) exit 1;; esac
case "$*" in *--json-schema*) if [ -n "$LYK_TOKEN$LYK_CONFIG$LYK_SERVER" ]; then exit 41; fi;; esac
case "$*" in *--json-schema*) handin=',"structured_output":{"status":"done","answer":"Scoped the question.","reason":null,"papers":[],"datasets":[],"no_qualifying":null,"replies":[]}';; esac
case "$*" in *--json-schema*assessments*) handin=',"structured_output":{"status":"could_not_do","answer":"Checked nothing.","reason":"No paper is attached.","assessments":[],"verdicts":[],"flagged_errors":[],"replies":[]}';; esac
`
	must(t, os.WriteFile(bin, []byte(prelude+script), 0700))
	state := t.TempDir()
	return daemon.Config{Server: l.url, Token: token, StateDir: state, Home: t.TempDir(), Device: "acceptance", Harnesses: map[string]string{"claude": bin}, Slots: 3, Poll: 50 * time.Millisecond, Heartbeat: 75 * time.Millisecond, Flush: 50 * time.Millisecond}
}
func startDaemon(t *testing.T, cfg daemon.Config) func() {
	t.Helper()
	t.Setenv("HOME", filepath.Dir(t.TempDir()))
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- daemon.Run(ctx, cfg) }()
	var once sync.Once
	stop := func() {
		once.Do(func() {
			cancel()
			select {
			case err := <-done:
				if err != nil {
					t.Error(err)
				}
			case <-time.After(8 * time.Second):
				t.Error("daemon shutdown did not join")
			}
		})
	}
	t.Cleanup(stop)
	return stop
}

const quick = `echo '{"type":"assistant","message":{"content":[{"type":"text","text":"evidence"}]}}'
echo '{"type":"result","session_id":"synthetic","is_error":false'"$handin"',"modelUsage":{"model-a":{"inputTokens":10,"outputTokens":2,"costUSD":0.01},"model-b":{"inputTokens":20,"outputTokens":3,"costUSD":0.02}}}'
`

func (l *lab) accounting(t *testing.T, card uuid.UUID, ids []uuid.UUID) {
	t.Helper()
	for _, id := range ids {
		if n := l.count(t, "SELECT count(*) FROM agent_run_usage WHERE agent_run_id=$1", id); n != 2 {
			t.Fatalf("%s usage rows %d, want 2", id, n)
		}
		for _, m := range []struct {
			name          string
			input, output int
			cost          float64
		}{{"model-a", 10, 2, .01}, {"model-b", 20, 3, .02}} {
			if n := l.count(t, "SELECT count(*) FROM agent_run_usage WHERE agent_run_id=$1 AND attempt=1 AND model=$2 AND input_tokens=$3 AND output_tokens=$4 AND cost_usd=$5", id, m.name, m.input, m.output, m.cost); n != 1 {
				t.Fatalf("%s missing exact usage for %s", id, m.name)
			}
		}
		if r := l.run(t, id); math.Abs(r.CostUsd-.03) > 1e-9 {
			t.Fatalf("run cost %v", r.CostUsd)
		}
	}
	var spent float64
	must(t, l.env.Owner.QueryRow(context.Background(), "SELECT spent_usd FROM card WHERE id=$1", card).Scan(&spent))
	if math.Abs(spent-float64(len(ids))*.03) > 1e-9 {
		t.Fatalf("spend=%v", spent)
	}
}

func TestTwoDaemonsOfOneOwnerRunEachAgentRunOnce(t *testing.T) {
	l := newLab(t)
	owner, tok, _ := l.member(t, "owner@lab.org")
	card, ids := l.queue(t, owner, 12)
	flag := filepath.Join(t.TempDir(), "release-both")
	script := fmt.Sprintf("while [ ! -f '%s' ]; do sleep 0.05; done\n", flag) + quick
	stop1 := startDaemon(t, l.config(t, tok, script))
	stop2 := startDaemon(t, l.config(t, tok, script))
	waitFor(t, "both daemons executing before release", func() bool {
		return l.count(t, "SELECT count(DISTINCT runtime_id) FROM agent_run WHERE card_id=$1 AND status='running'", card) == 2
	})
	must(t, os.WriteFile(flag, nil, 0600))
	// Twelve runs on two runtimes run in about six waves; on a loaded machine
	// each takes seconds, so the wait scales with them rather than one run.
	waitWithin(t, time.Minute, "all twelve completed", func() bool {
		return l.count(t, "SELECT count(*) FROM agent_run WHERE card_id=$1 AND status='completed' AND attempt=1", card) == len(ids)
	})
	l.settled(t, ids)
	stop1()
	stop2()
	l.accounting(t, card, ids)
	for _, id := range ids {
		if n := l.count(t, `SELECT count(*) FROM agent_run_message WHERE agent_run_id=$1 AND attempt=1 AND type='text' AND content='"evidence"'::jsonb`, id); n != 1 {
			t.Fatalf("%s message executions=%d", id, n)
		}
	}
	if n := l.count(t, "SELECT count(DISTINCT runtime_id) FROM agent_run WHERE card_id=$1", card); n != 2 {
		t.Fatalf("both daemons must execute work, got %d runtimes", n)
	}
}
func TestAnotherMembersDaemonNeverRunsYourCard(t *testing.T) {
	l := newLab(t)
	owner, _, _ := l.member(t, "owner@lab.org")
	other, tok, jwt := l.member(t, "other@lab.org")
	card, ids := l.queue(t, owner, 2)
	own, ownIDs := l.queue(t, other, 1)
	api := &apiclient.Client{Server: l.url, Token: jwt}
	expectHTTP(t, api.Do(context.Background(), "GET", "/api/daemon/workspaces", nil, nil), 401)
	stop := startDaemon(t, l.config(t, tok, quick))
	waitFor(t, "other member's own run proves polling", func() bool { return l.run(t, ownIDs[0]).Status == "completed" })
	l.settled(t, ownIDs)
	stop()
	l.accounting(t, own, ownIDs)
	if n := l.count(t, "SELECT count(*) FROM agent_run WHERE card_id=$1 AND status='queued' AND attempt=0", card); n != len(ids) {
		t.Fatalf("other owner's queued runs=%d", n)
	}
	for _, id := range ids {
		if n := l.count(t, "SELECT count(*) FROM agent_run_settlement WHERE agent_run_id=$1", id); n != 0 {
			t.Fatal("foreign run settled")
		}
	}
}

// The helper is the actual daemon, in a separately owned OS process so SIGKILL
// bypasses graceful shutdown, cancellation reports, and in-process defers.
func TestDaemonProcess(t *testing.T) {
	raw := os.Getenv("LYK_E2E_DAEMON")
	if raw == "" {
		return
	}
	var c struct{ Server, Token, StateDir, Home, Bin string }
	must(t, json.Unmarshal([]byte(raw), &c))
	must(t, daemon.Run(context.Background(), daemon.Config{Server: c.Server, Token: c.Token, StateDir: c.StateDir, Home: c.Home, Device: "kill-helper", Harnesses: map[string]string{"claude": c.Bin}, Slots: 1, Poll: 50 * time.Millisecond, Heartbeat: 75 * time.Millisecond, Flush: 50 * time.Millisecond}))
}
func startProcess(t *testing.T, c daemon.Config) *exec.Cmd {
	t.Helper()
	raw, err := json.Marshal(map[string]string{"Server": c.Server, "Token": c.Token, "StateDir": c.StateDir, "Home": c.Home, "Bin": c.Harnesses["claude"]})
	must(t, err)
	exe, err := os.Executable()
	must(t, err)
	cmd := exec.Command(exe, "-test.run=^TestDaemonProcess$")
	cmd.Env = append(os.Environ(), "LYK_E2E_DAEMON="+string(raw))
	log, err := os.Create(filepath.Join(c.StateDir, "helper.log"))
	must(t, err)
	cmd.Stdout = log
	cmd.Stderr = log
	must(t, cmd.Start())
	t.Cleanup(func() {
		if cmd.ProcessState == nil {
			if err := cmd.Process.Kill(); err != nil && !errors.Is(err, os.ErrProcessDone) {
				t.Error(err)
			}
			err := cmd.Wait()
			var exit *exec.ExitError
			if !errors.As(err, &exit) || exit.ProcessState.Sys().(syscall.WaitStatus).Signal() != syscall.SIGKILL {
				t.Errorf("helper cleanup: %v", err)
			}
		}
		if err := log.Close(); err != nil {
			t.Error(err)
		}
	})
	return cmd
}
func expectHTTP(t *testing.T, err error, status int) {
	t.Helper()
	var e *apiclient.HTTPError
	if !errors.As(err, &e) || e.Status != status {
		t.Fatalf("want HTTP %d, got %v", status, err)
	}
}
func runPath(id uuid.UUID, suffix string) string {
	return "/api/daemon/agent-runs/" + id.String() + "/" + suffix
}
func statusPath(id uuid.UUID, ref service.RuntimeRef) string {
	return runPath(id, "status") + fmt.Sprintf("?workspace_id=%s&runtime_id=%s&attempt=%d", ref.WorkspaceID, ref.RuntimeID, ref.Attempt)
}
func (l *lab) ref(t *testing.T, id uuid.UUID) service.RuntimeRef {
	r := l.run(t, id)
	if r.RuntimeID == nil {
		t.Fatal("run has no runtime")
	}
	return service.RuntimeRef{WorkspaceID: l.ws, RuntimeID: *r.RuntimeID, Attempt: r.Attempt}
}

// useAttemptScratch keeps crash and credential lifecycle scenarios on fresh
// attempt folders; scoped session reuse is exercised separately.
func (l *lab) useAttemptScratch(t *testing.T, id uuid.UUID) {
	t.Helper()
	result, err := l.env.Owner.Exec(t.Context(), "UPDATE agent_run SET continuity_scope='' WHERE id=$1", id)
	must(t, err)
	if result.RowsAffected() != 1 {
		t.Fatal("missing run for attempt scratch fixture")
	}
}

func TestKilledDaemonsAgentRunIsRetriedAndKeepsWaiting(t *testing.T) {
	l := newLab(t)
	owner, tok, _ := l.member(t, "owner@lab.org")
	_, ids := l.queue(t, owner, 1)
	l.useAttemptScratch(t, ids[0])
	id := ids[0]
	api := &apiclient.Client{Server: l.url, Token: tok}
	var old service.RuntimeRef
	for attempt := int32(1); attempt <= 2; attempt++ {
		cfg := l.config(t, tok, `mkdir -p "$npm_config_cache"
echo $$ > "$npm_config_cache/agent.pid"
exec sleep 300
`)
		pidFile := filepath.Join(cfg.Home, ".caches", "npm", "agent.pid")
		cmd := startProcess(t, cfg)
		// Cleanup uses the adapter's birth-verified ownership record.
		t.Cleanup(func() {
			if err := agent.RecoverScratch(filepath.Join(cfg.Home, fmt.Sprintf("%s-%d", id, attempt))); err != nil {
				t.Error(err)
			}
		})
		waitFor(t, "owned helper running", func() bool {
			r := l.run(t, id)
			if r.Status == "failed" {
				t.Fatalf("helper run failed: %+v", r)
			}
			// The shell creates the pid file before it writes the pid into it.
			b, err := os.ReadFile(pidFile)
			return r.Status == "running" && r.Attempt == attempt && err == nil && strings.HasSuffix(string(b), "\n")
		})
		b, err := os.ReadFile(pidFile)
		must(t, err)
		pid, err := strconv.Atoi(strings.TrimSpace(string(b)))
		must(t, err)
		pid = ownedFixtureAgentPID(t, filepath.Join(cfg.Home, fmt.Sprintf("%s-%d", id, attempt)), pid)
		pgid, err := syscall.Getpgid(pid)
		must(t, err)
		if pid <= 1 || pgid <= 1 {
			t.Fatalf("agent has no owned group: %d/%d", pid, pgid)
		}
		ref := l.ref(t, id)
		// The attempt holds its run tokens until it is swept.
		tokens := func() int {
			return l.count(t, "SELECT count(*) FROM agent_run_token WHERE agent_run_id=$1 AND attempt=$2", id, attempt)
		}
		if n := tokens(); n == 0 {
			t.Fatalf("attempt %d holds no run token", attempt)
		}
		if attempt == 2 {
			expectHTTP(t, api.Do(context.Background(), "GET", statusPath(id, old), nil, nil), 409)
			var receipt db.AgentRun
			must(t, api.Do(context.Background(), "POST", runPath(id, "complete"), struct {
				service.RuntimeRef
				service.Report
			}{old, service.Report{Usage: []agent.Usage{{Model: "stale", CostUSD: floatPtr(9)}}}}, &receipt))
			if receipt.ID != id || receipt.Attempt != 1 || receipt.Status != "queued" || receipt.CostUsd != 0 || receipt.FailureReason == nil || *receipt.FailureReason != "runtime_offline" {
				t.Fatalf("wrong historical receipt: %+v", receipt)
			}
			if current := l.run(t, id); current.Status != "running" || current.Attempt != 2 || current.CostUsd != 0 {
				t.Fatalf("stale report changed current attempt: %+v", current)
			}
			expectHTTP(t, api.Do(context.Background(), "POST", runPath(id, "start"), old, nil), 409)
			// The earlier attempt's late messages, from the machine that ran
			// it, join that attempt; the running one never gets them.
			must(t, api.Do(context.Background(), "POST", runPath(id, "messages"), map[string]any{"workspace_id": old.WorkspaceID, "runtime_id": old.RuntimeID, "attempt": old.Attempt, "messages": []service.Message{{Seq: 1, Type: "text", Content: json.RawMessage(`"stale"`)}}}, nil))
		}
		must(t, cmd.Process.Signal(syscall.SIGKILL))
		err = cmd.Wait()
		var exit *exec.ExitError
		if !errors.As(err, &exit) || exit.ProcessState.Sys().(syscall.WaitStatus).Signal() != syscall.SIGKILL {
			t.Fatalf("helper was not killed: %v", err)
		}
		if r := l.run(t, id); r.Status != "running" || r.Attempt != attempt {
			t.Fatalf("hard death unexpectedly reported: %+v", r)
		}
		if n := l.count(t, "SELECT count(*) FROM agent_run_settlement WHERE agent_run_id=$1", id); n != 0 {
			t.Fatal("settlement before sweep")
		}
		time.Sleep(400 * time.Millisecond)
		must(t, l.runs.Sweep(context.Background()))
		if n := tokens(); n != 0 {
			t.Fatalf("the swept attempt %d still holds %d run tokens", attempt, n)
		}
		t.Logf("SIGKILL of owned daemon pid=%d left attempt=%d running until sweep; agent group=%d is retained until cleanup", cmd.Process.Pid, attempt, pgid)
		if attempt == 1 {
			if r := l.run(t, id); r.Status != "queued" || r.Attempt != 1 {
				t.Fatalf("first death did not queue retry: %+v", r)
			}
			// Skip the parked run's backoff.
			_, err = l.env.Owner.Exec(context.Background(), "UPDATE agent_run SET retry_after=NULL WHERE id=$1", id)
			must(t, err)
		}
		old = ref
	}
	// A lost machine never fails the run: it waits again, unsettled.
	r := l.run(t, id)
	if r.Status != "queued" || r.Attempt != 2 || r.FailureReason == nil || *r.FailureReason != "runtime_offline" {
		t.Fatalf("after the second death %+v", r)
	}
	if n := l.count(t, "SELECT count(*) FROM agent_run_settlement WHERE agent_run_id=$1", id); n != 0 {
		t.Fatalf("a parked run settled %d times", n)
	}
	if n := l.count(t, "SELECT count(*) FROM agent_run_usage WHERE agent_run_id=$1", id); n != 0 {
		t.Fatal("stale report charged usage")
	}
	if n := l.count(t, "SELECT count(*) FROM agent_run_message WHERE agent_run_id=$1 AND attempt=2", id); n != 0 {
		t.Fatal("stale message reached the running attempt")
	}
	if n := l.count(t, "SELECT count(*) FROM agent_run_message WHERE agent_run_id=$1 AND attempt=1 AND content='\"stale\"'::jsonb", id); n != 1 {
		t.Fatalf("the earlier attempt holds %d late messages, want 1", n)
	}
}
func floatPtr(v float64) *float64 { return &v }

// A Validation run's hand-in is kept with its report through an outage too.
func TestReportSurvivesUnreachableServerAndConsumerACKLoss(t *testing.T) {
	reportSurvivesOutage(t, "validation")
}

// A Grounding run's hand-in can't be posted while the server is down: it is
// kept with the report, posted when the server is back, and the run
// completes on its first attempt without the agent working again.
func TestHandInSurvivesUnreachableServerAndConsumerACKLoss(t *testing.T) {
	reportSurvivesOutage(t, "grounding")
}

func reportSurvivesOutage(t *testing.T, column string) {
	l := newLab(t)
	owner, tok, _ := l.member(t, "owner@lab.org")
	card, ids := l.queueIn(t, owner, 1, column)
	id := ids[0]
	flag := filepath.Join(t.TempDir(), "finish")
	cfg := l.config(t, tok, fmt.Sprintf("while [ ! -f '%s' ]; do sleep 0.05; done\n", flag)+quick)
	stop := startDaemon(t, cfg)
	waitFor(t, "running before outage", func() bool { return l.run(t, id).Status == "running" })
	transport := &http.Transport{}
	defer transport.CloseIdleConnections()
	client := &http.Client{Transport: transport, Timeout: time.Second}
	response, err := client.Get(l.url + "/health")
	must(t, err)
	_, err = io.Copy(io.Discard, response.Body)
	must(t, err)
	must(t, response.Body.Close())
	l.stopHTTP(t)
	response, err = client.Get(l.url + "/health")
	if err == nil {
		response.Body.Close()
		t.Fatal("pooled HTTP connection still delivered during outage")
	}
	t.Logf("HTTP delivery refused during outage: %v", err)
	must(t, os.WriteFile(flag, nil, 0600))
	waitFor(t, "durable message and terminal report while offline", func() bool {
		return len(queueFiles(t, cfg.StateDir, "messages")) == 1 && len(queueFiles(t, cfg.StateDir, "outbox")) == 1
	})
	stop()
	if r := l.run(t, id); r.Status != "running" {
		t.Fatal("report delivered while HTTP was stopped", r.Status)
	}
	if n := l.count(t, "SELECT count(*) FROM agent_run_message WHERE agent_run_id=$1", id); n != 0 {
		t.Fatal("messages reached stopped server")
	}
	for _, kind := range []string{"messages", "outbox"} {
		b, err := os.ReadFile(queueFiles(t, cfg.StateDir, kind)[0])
		must(t, err)
		var report daemon.Report
		must(t, json.Unmarshal(b, &report))
		if report.AgentRunID != id || report.Attempt != 1 {
			t.Fatalf("lost durable attempt: %+v", report)
		}
		if kind == "outbox" && (report.Kind != "complete" || len(report.HandIn) == 0) {
			t.Fatalf("durable report %s with hand-in %s", report.Kind, report.HandIn)
		}
	}
	l.mu.Lock()
	l.loseACK = true
	l.mu.Unlock()
	l.startHTTP(t)
	stop = startDaemon(t, cfg)
	waitFor(t, "replayed completion and empty queues", func() bool {
		return l.run(t, id).Status == "completed" && len(queueFiles(t, cfg.StateDir, "messages")) == 0 && len(queueFiles(t, cfg.StateDir, "outbox")) == 0
	})
	stop()
	if n := l.count(t, "SELECT count(*) FROM agent_run_settlement WHERE agent_run_id=$1 AND delivered_at IS NULL", id); n != 1 {
		t.Fatal("lost callback ACK was not pending")
	}
	if n := l.count(t, "SELECT count(*) FROM event WHERE type='acceptance.settlement_applied' AND payload->>'id'=$1", id.String()); n != 1 {
		t.Fatal("consumer effect did not commit before lost ACK")
	}
	l.stopHTTP(t)
	l.runs = l.service()
	l.startHTTP(t)
	must(t, l.runs.RecoverSettlements(context.Background()))
	must(t, l.runs.RecoverSettlements(context.Background()))
	l.settled(t, ids)
	l.accounting(t, card, ids)
	if r := l.run(t, id); r.Status != "completed" || r.Attempt != 1 {
		t.Fatalf("outage replay changed attempt or result: %+v", r)
	}
	l.mu.Lock()
	calls := l.calls[id]
	l.mu.Unlock()
	if calls != 2 {
		t.Fatalf("physical callback deliveries=%d, want 2", calls)
	}
	if n := l.count(t, "SELECT count(*) FROM agent_run_message WHERE agent_run_id=$1 AND attempt=1 AND content='\"evidence\"'::jsonb", id); n != 1 {
		t.Fatal("replay lost or duplicated stream evidence")
	}
	if n := l.count(t, "SELECT count(*) FROM record WHERE agent_run_id=$1 AND kind='hand_in'", id); n != 1 {
		t.Fatalf("hand-ins recorded: %d", n)
	}
}
func queueFiles(t *testing.T, state, kind string) []string {
	t.Helper()
	p, err := filepath.Glob(filepath.Join(state, kind, "*.json"))
	must(t, err)
	return p
}

func TestContextExhaustedFailsWithoutARetry(t *testing.T) {
	l := newLab(t)
	owner, tok, _ := l.member(t, "owner@lab.org")
	card, ids := l.queue(t, owner, 1)
	script := strings.Replace(quick, `"is_error":false`, `"is_error":true,"terminal_reason":"prompt_too_long"`, 1)
	stop := startDaemon(t, l.config(t, tok, script))
	waitFor(t, "context failure", func() bool { return l.run(t, ids[0]).Status == "failed" })
	l.settled(t, ids)
	stop()
	must(t, l.runs.Sweep(context.Background()))
	r := l.run(t, ids[0])
	if r.Attempt != 1 || r.FailureReason == nil || *r.FailureReason != "context_exhausted" {
		t.Fatalf("context exhaustion retried or misclassified %+v", r)
	}
	l.settled(t, ids)
	l.accounting(t, card, ids)
}

func TestCancellationRevokesRoleAndAcceptsLateUsageExactlyOnce(t *testing.T) {
	l := newLab(t)
	owner, tok, _ := l.member(t, "owner@lab.org")
	card, ids := l.queue(t, owner, 1)
	id := ids[0]
	api := &apiclient.Client{Server: l.url, Token: tok, Workspace: l.ws.String()}
	var runtimes []db.Runtime
	must(t, api.Do(context.Background(), "POST", "/api/daemon/register", map[string]any{"workspace_id": l.ws, "daemon_id": "wire", "harnesses": []string{"claude"}}, &runtimes))
	if len(runtimes) != 1 {
		t.Fatal("missing runtime")
	}
	ref := service.RuntimeRef{WorkspaceID: l.ws, RuntimeID: runtimes[0].ID}
	var claims []service.Claimed
	must(t, api.Do(context.Background(), "POST", "/api/daemon/agent-runs/claim", map[string]any{"runtimes": []service.RuntimeRef{ref}, "slots": 1}, &claims))
	if len(claims) != 1 || claims[0].AgentRun.ID != id || claims[0].AgentRun.Attempt != 1 {
		t.Fatalf("claim %+v", claims)
	}
	ref.Attempt = 1
	role := &apiclient.Client{Server: l.url, Token: claims[0].Token}
	if !strings.HasPrefix(role.Token, "ltk_") {
		t.Fatal("missing role token")
	}
	expectHTTP(t, api.Do(context.Background(), "GET", runPath(id, "status")+fmt.Sprintf("?workspace_id=%s&runtime_id=%s", l.ws, ref.RuntimeID), nil, nil), 400)
	must(t, api.Do(context.Background(), "POST", runPath(id, "start"), ref, nil))
	must(t, role.Do(context.Background(), "GET", "/api/role/records", nil, nil))
	must(t, api.Do(context.Background(), "POST", "/api/agent-runs/"+id.String()+"/cancel", map[string]any{"workspace_id": l.ws}, nil))
	var status struct{ Status string }
	must(t, api.Do(context.Background(), "GET", statusPath(id, ref), nil, &status))
	if status.Status != "cancelled" {
		t.Fatal(status.Status)
	}
	expectHTTP(t, role.Do(context.Background(), "GET", "/api/role/records", nil, nil), 401)
	l.settled(t, ids)
	report := struct {
		service.RuntimeRef
		service.Report
	}{ref, service.Report{Usage: []agent.Usage{{Model: "model-a", InputTokens: 10, OutputTokens: 2, CostUSD: floatPtr(.01)}, {Model: "model-b", InputTokens: 20, OutputTokens: 3, CostUSD: floatPtr(.02)}}}}
	for i := 0; i < 3; i++ {
		var result db.AgentRun
		must(t, api.Do(context.Background(), "POST", runPath(id, "complete"), report, &result))
		if result.Status != "cancelled" || result.Attempt != 1 {
			t.Fatalf("late report changed terminal result %+v", result)
		}
	}
	l.accounting(t, card, ids)
	l.settled(t, ids)
	l.mu.Lock()
	calls := l.calls[id]
	l.mu.Unlock()
	if calls != 1 {
		t.Fatalf("late usage redelivered cancellation %d times", calls)
	}
	expectHTTP(t, role.Do(context.Background(), "GET", "/api/role/records", nil, nil), 401)
}

// Reusing the same state must reconcile the previous incarnation before its
// heartbeat can revive the runtime and strand its occupied slot indefinitely.
func TestSameStateImmediateRestartRecoversKilledAttempts(t *testing.T) {
	l := newLab(t)
	owner, tok, _ := l.member(t, "restart@lab.org")
	_, ids := l.queueIn(t, owner, 1, "validation")
	l.useAttemptScratch(t, ids[0])
	id := ids[0]
	signin := t.TempDir()
	must(t, os.WriteFile(filepath.Join(signin, ".credentials.json"), []byte("synthetic-signin"), 0600))
	t.Setenv("CLAUDE_CONFIG_DIR", signin)
	cfg := l.config(t, tok, `echo $$ > "$PWD/child.pid"
echo '{"type":"assistant","message":{"content":[{"type":"text","text":"durable-before-death"}]}}'
exec sleep 300
`)
	cmd := startProcess(t, cfg)
	for attempt := int32(1); attempt <= 2; attempt++ {
		dir := filepath.Join(cfg.Home, fmt.Sprintf("%s-%d", id, attempt))
		// The shell creates child.pid before it writes the pid into it.
		waitFor(t, "same-store attempt running", func() bool {
			r := l.run(t, id)
			b, e := os.ReadFile(filepath.Join(dir, "child.pid"))
			return r.Status == "running" && r.Attempt == attempt && e == nil && strings.HasSuffix(string(b), "\n")
		})
		b, err := os.ReadFile(filepath.Join(dir, "child.pid"))
		must(t, err)
		pid, err := strconv.Atoi(strings.TrimSpace(string(b)))
		must(t, err)
		pid = ownedFixtureAgentPID(t, dir, pid)
		group, err := syscall.Getpgid(pid)
		must(t, err)
		t.Cleanup(func() {
			if err := agent.RecoverScratch(dir); err != nil {
				t.Error(err)
			}
		})
		_ = group

		waitFor(t, "message persisted before death", func() bool {
			return l.count(t, "SELECT count(*) FROM agent_run_message WHERE agent_run_id=$1 AND attempt=$2", id, attempt) == 1
		})
		must(t, cmd.Process.Kill())
		err = cmd.Wait()
		var killed *exec.ExitError
		if !errors.As(err, &killed) {
			t.Fatalf("hard death: %v", err)
		}
		if l.run(t, id).Status != "running" {
			t.Fatal("hard death unexpectedly settled")
		}
		cmd = startProcess(t, cfg) // Deliberately no sleep, sweep, or state-directory change.
		waitFor(t, "prior child and copied credential recovered", func() bool {
			_, e := os.Stat(filepath.Join(dir, ".cli-home", ".credentials.json"))
			_, finished := os.Stat(filepath.Join(cfg.StateDir, "finished", filepath.Base(dir)))
			return errors.Is(e, os.ErrNotExist) && finished == nil
		})
		if attempt == 1 {
			waitFor(t, "lost attempt parked", func() bool { r := l.run(t, id); return r.Status == "queued" && r.RetryAfter != nil })
			// Skip the parked run's backoff.
			_, err = l.env.Owner.Exec(context.Background(), "UPDATE agent_run SET retry_after=NULL WHERE id=$1", id)
			must(t, err)
			waitFor(t, "retry after immediate restart", func() bool { r := l.run(t, id); return r.Status == "running" && r.Attempt == 2 })
		}
	}
	// A lost machine never fails the run: it waits again, unsettled.
	waitFor(t, "second lost attempt parked", func() bool { r := l.run(t, id); return r.Status == "queued" && r.Attempt == 2 })
	r := l.run(t, id)
	if r.FailureReason == nil || *r.FailureReason != "runtime_offline" || r.InfraFailures != 2 {
		t.Fatalf("recovery result: %+v", r)
	}
	if n := l.count(t, "SELECT count(*) FROM agent_run_settlement WHERE agent_run_id=$1", id); n != 0 {
		t.Fatalf("a parked run settled %d times", n)
	}
	if n := l.count(t, "SELECT count(*) FROM agent_run_message WHERE agent_run_id=$1", id); n != 2 {
		t.Fatalf("lost prior evidence: %d", n)
	}
	if n := l.count(t, "SELECT count(*) FROM agent_run_usage WHERE agent_run_id=$1", id); n != 0 {
		t.Fatalf("invented usage: %d", n)
	}
}

func TestAColleaguesStudyRunsOnASharedMachine(t *testing.T) {
	l := newLab(t)
	researcher, _, _ := l.member(t, "researcher@lab.org")
	machineOwner, tok, _ := l.member(t, "machine@lab.org")
	card, ids := l.queue(t, researcher, 1)
	cfg := l.config(t, tok, quick)
	machine := uuid.NewString()
	raw, err := json.Marshal(map[string]string{"server": l.url, "daemon_id": machine})
	must(t, err)
	must(t, os.WriteFile(filepath.Join(cfg.StateDir, "daemon.json"), raw, 0600))
	runtimes, err := l.runs.Register(t.Context(), machineOwner, l.ws, machine, "shared", []string{"claude"}, nil, agent.Platform{OS: runtime.GOOS})
	must(t, err)
	_, err = l.env.Owner.Exec(t.Context(), "UPDATE card SET machine=$1 WHERE id=$2", machine, card)
	must(t, err)
	_, err = l.env.Owner.Exec(t.Context(), "INSERT INTO runtime_share(workspace_id,runtime_id,user_id) VALUES($1,$2,$3)", l.ws, runtimes[0].ID, researcher)
	must(t, err)
	if runtime.GOOS == "linux" {
		must(t, os.WriteFile(filepath.Join(os.Getenv("CLAUDE_CONFIG_DIR"), ".credentials.json"), []byte("guest-signin-fixture"), 0600))
	}
	stop := startDaemon(t, cfg)
	if runtime.GOOS != "linux" {
		waitFor(t, "native runtime registration", func() bool {
			return l.count(t, "SELECT count(*) FROM runtime WHERE id=$1 AND os=$2", runtimes[0].ID, runtime.GOOS) == 1
		})
		claims, err := l.runs.Claim(t.Context(), machineOwner, []service.RuntimeRef{{WorkspaceID: l.ws, RuntimeID: runtimes[0].ID}}, 1)
		must(t, err)
		if len(claims) != 0 || l.run(t, ids[0]).Status != "queued" || l.run(t, ids[0]).Attempt != 0 || l.count(t, "SELECT count(*) FROM agent_run_token WHERE agent_run_id=$1", ids[0]) != 0 {
			t.Fatal("unsupported native daemon claimed colleague study")
		}
		stop()
		t.Log("native unsupported machine remains queued without attempt or tokens")
		return
	}
	waitFor(t, "colleague study completion", func() bool { return l.run(t, ids[0]).Status == "completed" })
	l.settled(t, ids)
	stop()
	l.accounting(t, card, ids)
	if run := l.run(t, ids[0]); run.ResponsibleUserID != researcher || run.MachineOwnerID == nil || *run.MachineOwnerID != machineOwner {
		t.Fatalf("ownership changed: %+v", run)
	}
}

func ownedFixtureAgentPID(t *testing.T, dir string, reported int) int {
	t.Helper()
	data, err := os.ReadFile(filepath.Join(dir, ".agent-process"))
	must(t, err)
	var owner struct {
		PID int `json:"pid"`
	}
	must(t, json.Unmarshal(data, &owner))
	host, err := testutil.OwnedFixtureHostPID(owner.PID, reported)
	must(t, err)
	return host
}
