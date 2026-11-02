package workspacemcp

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"io"
	"lykeion/server/internal/availability"
	"lykeion/server/internal/toolroute"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/modelcontextprotocol/go-sdk/mcp"
	"lykeion/server/internal/connectors"
	"lykeion/server/internal/middleware"
	"lykeion/server/internal/service"
	"lykeion/server/internal/store"
	"lykeion/server/internal/testutil"
	"lykeion/server/pkg/agent"
	db "lykeion/server/pkg/db/generated"
)

func TestWorkspaceMCPRemoteSessionsArePerRunAndCloseOnCancellation(t *testing.T) {
	env := testutil.Setup(t)
	ctx := t.Context()
	st := &store.Store{Pool: env.App}
	ws, user, card := uuid.New(), uuid.New(), uuid.New()
	for _, seed := range []struct {
		sql  string
		args []any
	}{
		{`INSERT INTO "user"(id,email) VALUES($1,'broker@lab.org')`, []any{user}},
		{`INSERT INTO workspace(id,slug,name) VALUES($1,'broker','broker')`, []any{ws}},
		{`INSERT INTO member(workspace_id,user_id,role) VALUES($1,$2,'owner')`, []any{ws, user}},
		{`INSERT INTO card(workspace_id,id,number,title,question,owner_id,board_column) VALUES($1,$2,1,'q','q',$3,'grounding')`, []any{ws, card, user}},
	} {
		if _, err := env.Owner.Exec(ctx, seed.sql, seed.args...); err != nil {
			t.Fatal(err)
		}
	}
	keys := &connectors.Client{Secret: bytes.Repeat([]byte{7}, 32)}
	svc := Service{Store: st, Secrets: keys}
	var initialized atomic.Int32
	slowStarted := make(chan struct{}, 1)
	releaseUpstream := make(chan struct{})
	remote := mcp.NewServer(&mcp.Implementation{Name: "stateful", Version: "1"}, nil)
	mutate := make(chan func(), 1)
	mcp.AddTool(remote, &mcp.Tool{Name: "replace"}, func(ctx context.Context, _ *mcp.CallToolRequest, _ map[string]any) (*mcp.CallToolResult, any, error) {
		select {
		case fn := <-mutate:
			fn()
		case <-ctx.Done():
			return nil, nil, ctx.Err()
		}
		return &mcp.CallToolResult{Content: []mcp.Content{&mcp.TextContent{Text: "stale"}}}, nil, nil
	})
	mcp.AddTool(remote, &mcp.Tool{Name: "slow"}, func(ctx context.Context, _ *mcp.CallToolRequest, _ map[string]any) (*mcp.CallToolResult, any, error) {
		slowStarted <- struct{}{}
		select {
		case <-ctx.Done():
		case <-releaseUpstream:
		}
		return nil, nil, ctx.Err()
	})
	handler := mcp.NewStreamableHTTPHandler(func(*http.Request) *mcp.Server { return remote }, nil)
	upstream := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == "POST" {
			raw, _ := io.ReadAll(r.Body)
			r.Body = io.NopCloser(bytes.NewReader(raw))
			var rpc struct{ Method string }
			json.Unmarshal(raw, &rpc)
			if rpc.Method == "initialize" {
				initialized.Add(1)
			}
		}
		handler.ServeHTTP(w, r)
	}))
	defer upstream.Close()
	defer close(releaseUpstream)
	keys.HTTP = upstream.Client()
	saved, err := svc.Create(ctx, ws, user, "stateful", Config{URL: upstream.URL})
	if err != nil {
		t.Fatal(err)
	}
	runs := &service.AgentRuns{Store: st, Keys: keys}
	rts, err := runs.Register(ctx, user, ws, "broker", "laptop", []string{"claude"}, nil, agent.Platform{})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := env.Owner.Exec(ctx, `UPDATE runtime SET max_concurrent=8 WHERE id=$1`, rts[0].ID); err != nil {
		t.Fatal(err)
	}
	claim := func() (middleware.Role, context.Context) {
		t.Helper()
		if err := st.InWorkspace(ctx, ws, user, func(tx *store.Tx) error {
			_, err := runs.Enqueue(ctx, tx, user, service.NewAgentRun{Card: card, Column: "grounding", Round: 1, Role: "agent", Harness: "claude", Prompt: "q"})
			return err
		}); err != nil {
			t.Fatal(err)
		}
		ref := service.RuntimeRef{WorkspaceID: ws, RuntimeID: rts[0].ID}
		claims, err := runs.Claim(ctx, user, []service.RuntimeRef{ref}, 1)
		if err != nil || len(claims) != 1 {
			t.Fatalf("claim %v %+v", err, claims)
		}
		c := claims[0]
		ref.Attempt = c.AgentRun.Attempt
		if err := runs.Start(ctx, user, ref, c.AgentRun.ID); err != nil {
			t.Fatal(err)
		}
		return middleware.Role{WorkspaceID: ws, AgentRunID: c.AgentRun.ID, CardID: card, Column: "grounding", Round: 1, Role: "agent", Scopes: []string{service.ScopeCall}}, WithRunAuthority(ctx, c.CallToken)
	}
	b := &Broker{Store: st, Keys: keys}
	defer b.Close()
	role, callctx := claim()
	for range 2 {
		if _, err := b.Tools(callctx, role, saved.ID); err != nil {
			t.Fatal(err)
		}
	}
	if initialized.Load() != 1 {
		t.Fatalf("normal request replaced session: %d", initialized.Load())
	}
	other, otherctx := claim()
	if _, err := b.Tools(otherctx, other, saved.ID); err != nil {
		t.Fatal(err)
	}
	if initialized.Load() != 2 {
		t.Fatalf("runs shared a session: %d", initialized.Load())
	}
	cancelctx, cancel := context.WithCancel(callctx)
	done := make(chan error, 1)
	go func() { _, err := b.Call(cancelctx, role, saved.ID, "slow", nil); done <- err }()
	select {
	case <-slowStarted:
	case <-time.After(5 * time.Second):
		t.Fatal("upstream never started")
	}
	cancel()
	select {
	case err := <-done:
		if err == nil {
			t.Fatal("cancelled call answered")
		}
	case <-time.After(5 * time.Second):
		t.Fatal("cancellation hung")
	}
	for deadline := time.Now().Add(5 * time.Second); time.Now().Before(deadline); {
		b.mu.Lock()
		n := len(b.sessions)
		b.mu.Unlock()
		if n == 1 {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	b.mu.Lock()
	n := len(b.sessions)
	b.mu.Unlock()
	if n != 1 {
		t.Fatalf("cancelled session retained: %d", n)
	}
	if _, err := b.Tools(callctx, role, saved.ID); err != nil {
		t.Fatal(err)
	}
	if initialized.Load() != 3 {
		t.Fatal("cancelled session reused")
	}
	replacement, err := svc.Update(ctx, ws, user, saved.ID, 1, "stateful", Config{URL: upstream.URL})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := b.Tools(callctx, role, saved.ID); err != nil {
		t.Fatal(err)
	}
	if replacement.Revision != 2 || initialized.Load() != 4 {
		t.Fatal("replacement reused old session")
	}

	mutate <- func() {
		if _, err := svc.Update(ctx, ws, user, saved.ID, 2, "stateful", Config{URL: upstream.URL}); err != nil {
			t.Error(err)
		}
	}
	if _, err := b.Call(callctx, role, saved.ID, "replace", nil); err == nil {
		t.Fatal("changed connector answer accepted")
	}
	b.mu.Lock()
	remaining := len(b.sessions)
	b.mu.Unlock()
	if remaining != 1 {
		t.Fatalf("fence-rejected session retained: %d", remaining)
	}
	ref := service.RuntimeRef{WorkspaceID: ws, RuntimeID: rts[0].ID, Attempt: 1}
	if _, err := runs.Fail(ctx, user, ref, role.AgentRunID, service.Report{FailureReason: "provider_network", Error: "retry"}); err != nil {
		t.Fatal(err)
	}
	// Skip the parked run's backoff.
	if _, err := env.Owner.Exec(ctx, `UPDATE agent_run SET retry_after=NULL WHERE id=$1`, role.AgentRunID); err != nil {
		t.Fatal(err)
	}
	retry, err := runs.Claim(ctx, user, []service.RuntimeRef{ref}, 1)
	if err != nil || len(retry) != 1 || retry[0].AgentRun.ID != role.AgentRunID || retry[0].AgentRun.Attempt != 2 {
		t.Fatalf("retry claim: %+v %v", retry, err)
	}
	ref.Attempt = 2
	if err := runs.Start(ctx, user, ref, role.AgentRunID); err != nil {
		t.Fatal(err)
	}
	if _, err := b.Tools(WithRunAuthority(ctx, retry[0].CallToken), role, saved.ID); err != nil {
		t.Fatal(err)
	}
	if initialized.Load() != 5 {
		t.Fatalf("retry reused previous attempt session: %d initializations", initialized.Load())
	}
	b.CloseAttempt(ws, role.AgentRunID, 1)
	b.mu.Lock()
	n = len(b.sessions)
	b.mu.Unlock()
	if n != 2 {
		t.Fatal("late attempt cleanup closed the retry or another run")
	}
	b.CloseRun(ws, role.AgentRunID)
	b.mu.Lock()
	n = len(b.sessions)
	b.mu.Unlock()
	if n != 1 {
		t.Fatalf("CloseRun crossed run boundary: %d", n)
	}
	b.CloseConnector(ws, saved.ID)
	b.mu.Lock()
	n = len(b.sessions)
	b.mu.Unlock()
	if n != 0 {
		t.Fatal("CloseConnector leaked session")
	}
}

func TestWorkspaceMCPRedactsUpstreamEchoes(t *testing.T) {
	c := Config{URL: "https://example.org/mcp?token=topsecret", Headers: map[string]string{"Authorization": "Bearer headersecret"}}
	result := &mcp.CallToolResult{Content: []mcp.Content{&mcp.TextContent{Text: c.URL + " topsecret headersecret"}}}
	cleaned, err := scrub(result, c)
	if err != nil {
		t.Fatal(err)
	}
	raw, _ := json.Marshal(cleaned)
	for _, secret := range []string{c.URL, "topsecret", "headersecret"} {
		if strings.Contains(string(raw), secret) {
			t.Fatal("upstream echoed a credential")
		}
	}
}

func TestWorkspaceMCPStaleSnapshotCannotEvictNewSession(t *testing.T) {
	for _, newerAttempt := range []bool{false, true} {
		name := "revision"
		if newerAttempt {
			name = "attempt"
		}
		t.Run(name, func(t *testing.T) {
			b := &Broker{}
			defer b.Close()
			role := middleware.Role{WorkspaceID: uuid.New(), AgentRunID: uuid.New()}
			old := brokerRow{WorkspaceConnectorRow: service.WorkspaceConnectorRow{WorkspaceMcpServer: db.WorkspaceMcpServer{ID: uuid.New(), Revision: 1}, Attempt: 1}}
			current := old
			if newerAttempt {
				current.Attempt = 2
			} else {
				current.Revision = 2
			}
			session, err := b.entry(role, current)
			if err != nil {
				t.Fatal(err)
			}
			if _, err := b.entry(role, old); err == nil {
				t.Fatal("accepted stale snapshot after newer session")
			}
			select {
			case <-session.ctx.Done():
				t.Fatal("stale snapshot cancelled newer session")
			default:
			}
			again, err := b.entry(role, current)
			if err != nil || again != session {
				t.Fatalf("current session lost: %v", err)
			}
		})
	}
}

func TestWorkspaceMCPCleanupPreventsStaleRecreation(t *testing.T) {
	for _, kind := range []string{"connector", "attempt", "run"} {
		for _, existing := range []bool{false, true} {
			t.Run(fmt.Sprintf("%s/existing=%v", kind, existing), func(t *testing.T) {
				b := &Broker{}
				defer b.Close()
				role := middleware.Role{WorkspaceID: uuid.New(), AgentRunID: uuid.New()}
				snapshot := brokerRow{WorkspaceConnectorRow: service.WorkspaceConnectorRow{WorkspaceMcpServer: db.WorkspaceMcpServer{ID: uuid.New(), Revision: 1}, Attempt: 1}}
				if existing {
					if _, err := b.entry(role, snapshot); err != nil {
						t.Fatal(err)
					}
				}
				switch kind {
				case "connector":
					b.CloseConnector(role.WorkspaceID, snapshot.ID)
				case "attempt":
					b.CloseAttempt(role.WorkspaceID, role.AgentRunID, 1)
				case "run":
					b.CloseRun(role.WorkspaceID, role.AgentRunID)
				}
				if _, err := b.entry(role, snapshot); err == nil {
					t.Fatal("cleanup allowed paused snapshot to recreate a session")
				}
				b.mu.Lock()
				n := len(b.sessions)
				b.mu.Unlock()
				if n != 0 {
					t.Fatal("cleanup retained stale session")
				}
			})
		}
	}
}

func TestCommandInventoryIgnoresOldAvailabilityPolicy(t *testing.T) {
	for _, operation := range []string{"list", "publish"} {
		t.Run(operation, func(t *testing.T) {
			env := testutil.Setup(t)
			ctx := t.Context()
			st := &store.Store{Pool: env.App}
			ws, user, card := uuid.New(), uuid.New(), uuid.New()
			for _, seed := range []struct {
				sql  string
				args []any
			}{
				{`INSERT INTO "user"(id,email) VALUES($1,'fence@lab.org')`, []any{user}},
				{`INSERT INTO workspace(id,slug,name) VALUES($1,'fence','fence')`, []any{ws}},
				{`INSERT INTO member(workspace_id,user_id,role) VALUES($1,$2,'owner')`, []any{ws, user}},
				{`INSERT INTO card(workspace_id,id,number,title,question,owner_id,board_column) VALUES($1,$2,1,'q','q',$3,'grounding')`, []any{ws, card, user}},
			} {
				if _, err := env.Owner.Exec(ctx, seed.sql, seed.args...); err != nil {
					t.Fatal(err)
				}
			}
			keys := &connectors.Client{Secret: bytes.Repeat([]byte{7}, 32)}
			svc := Service{Store: st, Secrets: keys}
			saved, err := svc.Create(ctx, ws, user, "command", Config{Command: "fixture"})
			if err != nil {
				t.Fatal(err)
			}
			runs := &service.AgentRuns{Store: st, Keys: keys}
			rts, err := runs.Register(ctx, user, ws, "fence", "laptop", []string{"claude"}, nil, agent.Platform{})
			if err != nil {
				t.Fatal(err)
			}
			err = st.InWorkspace(ctx, ws, user, func(tx *store.Tx) error {
				_, e := runs.Enqueue(ctx, tx, user, service.NewAgentRun{Card: card, Column: "grounding", Round: 1, Role: "agent", Harness: "claude", Prompt: "q"})
				return e
			})
			if err != nil {
				t.Fatal(err)
			}
			ref := service.RuntimeRef{WorkspaceID: ws, RuntimeID: rts[0].ID}
			claims, err := runs.Claim(ctx, user, []service.RuntimeRef{ref}, 1)
			if err != nil || len(claims) != 1 {
				t.Fatalf("claim: %v", err)
			}
			claim := claims[0]
			ref.Attempt = claim.AgentRun.Attempt
			if err := runs.Start(ctx, user, ref, claim.AgentRun.ID); err != nil {
				t.Fatal(err)
			}
			role := middleware.Role{WorkspaceID: ws, AgentRunID: claim.AgentRun.ID, CardID: card, Column: "grounding", Round: 1, Role: "agent", Scopes: []string{service.ScopeCall}}
			type fenceKey struct{}
			entered, release := make(chan struct{}), make(chan struct{})
			var visits atomic.Int32
			cfg := env.App.Config()
			cfg.BeforeAcquire = func(ctx context.Context, _ *pgx.Conn) bool {
				if ctx.Value(fenceKey{}) == true && visits.Add(1) == 2 {
					close(entered)
					select {
					case <-release:
					case <-ctx.Done():
					}
				}
				return true
			}
			pool, err := pgxpool.NewWithConfig(ctx, cfg)
			if err != nil {
				t.Fatal(err)
			}
			defer pool.Close()
			b := &Broker{Store: &store.Store{Pool: pool}, Keys: keys}
			defer b.Close()
			callctx := WithRunAuthority(ctx, claim.CallToken)
			fingerprint := service.CommandFingerprint([]string{"fixture"}, nil)
			tools := []toolroute.Tool{{Name: "actual"}}
			if err := b.PublishTools(callctx, role, saved.ID, tools, fingerprint); err != nil {
				t.Fatal(err)
			}
			record := CommandRecord{Tool: "actual", Args: map[string]any{}, AnswerSHA256: AnswerHash("completed")}
			start, err := b.BeginCommand(callctx, role, saved.ID, fingerprint, record)
			if err != nil {
				t.Fatal(err)
			}
			done := make(chan error, 1)
			go func() {
				fenced := context.WithValue(callctx, fenceKey{}, true)
				if operation == "publish" {
					done <- b.PublishTools(fenced, role, saved.ID, tools, fingerprint)
				} else {
					_, e := b.Tools(fenced, role, saved.ID)
					done <- e
				}
			}()
			select {
			case <-entered:
			case <-time.After(5 * time.Second):
				t.Fatal("final inventory fence not reached")
			}
			_, err = availability.Set(ctx, st, ws, user, "mcp", saved.ID.String(), 0, false)
			close(release)
			if err != nil {
				t.Fatal(err)
			}
			select {
			case err := <-done:
				if err != nil {
					t.Fatalf("inventory unexpectedly refused: %v", err)
				}
			case <-time.After(5 * time.Second):
				t.Fatal("inventory request hung")
			}
			if _, err := b.CommandReceipt(callctx, role, saved.ID, fingerprint, record, start); err != nil {
				t.Fatalf("started call lost inventory: %v", err)
			}
		})
	}
}
