package sources

import (
	"encoding/json"
	"errors"
	"github.com/modelcontextprotocol/go-sdk/mcp"
	"lykeion/server/internal/auth"
	"lykeion/server/internal/middleware"
	"lykeion/server/internal/service"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgconn"
	"lykeion/server/internal/store"
)

func TestRecordCallAuthorityFenceSharesEvidenceTransaction(t *testing.T) {
	for _, cardless := range []bool{false, true} {
		name := "card"
		if cardless {
			name = "cardless"
		}
		t.Run(name, func(t *testing.T) {
			f := newConnFix(t, ok("unused"))
			ctx := t.Context()
			card := f.card
			if cardless {
				card = uuid.Nil
			}
			role := f.role(card, "agent")
			body, rest := NewCall(role, "library", "echo", nil, strings.Repeat("x", MaxCallAnswer+1), false)
			rejected := errors.New("connector authority withdrawn")
			calls := 0
			check := func(tx *store.Tx) error {
				calls++
				competing, err := f.env.Owner.Begin(ctx)
				if err != nil {
					t.Fatal(err)
				}
				defer competing.Rollback(ctx)
				_, err = competing.Exec(ctx, `SELECT id FROM agent_run WHERE id=$1 FOR UPDATE NOWAIT`, role.AgentRunID)
				var pg *pgconn.PgError
				if !errors.As(err, &pg) || pg.Code != "55P03" {
					t.Fatalf("authority callback lacks run lock: %v", err)
				}
				if err := tx.Emit(ctx, "receipt.test", nil, store.System, map[string]any{}); err != nil {
					return err
				}
				return rejected
			}
			if _, err := f.h.RecordCall(ctx, role, runTok(role), body, rest, check); !errors.Is(err, rejected) {
				t.Fatalf("record ignored authority: %v", err)
			}
			if calls != 1 || f.count(`SELECT count(*) FROM record`) != 0 || f.count(`SELECT count(*) FROM event`) != 0 {
				t.Fatal("rejected fence persisted evidence or events")
			}
			if _, err := f.h.RecordCall(ctx, role, runTok(role), body, rest); err != nil {
				t.Fatal(err)
			}
			if cardless {
				if f.count(`SELECT count(*) FROM record`) != 0 || f.count(`SELECT count(*) FROM event WHERE type='sources.called'`) != 1 {
					t.Fatal("cardless call behavior changed")
				}
			} else if f.count(`SELECT count(*) FROM record WHERE kind='call'`) != 1 || f.count(`SELECT count(*) FROM event WHERE type='blob.created'`) != 1 {
				t.Fatal("ordinary call/blob behavior changed")
			}
		})
	}
}

func TestOnlyTheServerRecordsAnAttachment(t *testing.T) {
	f := newConnFix(t, ok("unused"))
	role := f.role(f.card, "agent")
	for _, server := range []string{"attachments", " Attachments ", "ATTACHMENTS\t"} {
		body, rest := NewCall(role, server, "attach", nil, "PGC1A,9.9", false)
		if _, err := f.h.RecordCall(t.Context(), role, runTok(role), body, rest); !errors.Is(err, ErrReserved) {
			t.Fatalf("%q: %v", server, err)
		}
	}
	if n := f.count(`SELECT count(*) FROM record`); n != 0 {
		t.Fatalf("records: %d", n)
	}
	body, rest := NewCall(role, "attachments-lab", "attach", nil, "PGC1A,9.9", false)
	if _, err := f.h.RecordCall(t.Context(), role, runTok(role), body, rest); err != nil {
		t.Fatalf("a name that only starts like it: %v", err)
	}
}

func TestCoresearchSeesEnabledConnectors(t *testing.T) {
	const key = "server-only-task8-key"
	f := newConnFix(t, func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("X-API-KEY") != key {
			t.Errorf("upstream key missing")
		}
		w.Write([]byte(`{"echo":"` + r.Header.Get("X-API-KEY") + `"}`))
	})
	ctx := t.Context()
	chat, project, run := uuid.New(), uuid.New(), uuid.New()
	statements := []struct {
		sql  string
		args []any
	}{
		{`INSERT INTO member(workspace_id,user_id,role) VALUES($1,$2,'owner')`, []any{f.ws, f.user}},
		{`INSERT INTO coresearch_project(id,workspace_id,name,created_by) VALUES($1,$2,'Project',$3)`, []any{project, f.ws, f.user}},
		{`INSERT INTO coresearch_member(workspace_id,project_id,user_id,role) VALUES($1,$2,$3,'owner')`, []any{f.ws, project, f.user}},
		{`INSERT INTO coresearch_chat(id,workspace_id,project_id,owner_id,mode,cli) VALUES($1,$2,$3,$4,'experiment','claude')`, []any{chat, f.ws, project, f.user}},
		{`INSERT INTO agent_run(id,workspace_id,responsible_user_id,requester_id,kind,coresearch_chat_id,board_column,round,role,harness,prompt,status,attempt) VALUES($1,$2,$3,$3,'coresearch',$4,'',0,'coresearch','claude','research','running',1)`, []any{run, f.ws, f.user, chat}},
	}
	for _, s := range statements {
		if _, err := f.env.Owner.Exec(ctx, s.sql, s.args...); err != nil {
			t.Fatal(err)
		}
	}
	role := middleware.Role{AgentRunID: run, WorkspaceID: f.ws, Role: "coresearch", Scopes: []string{service.ScopeCoresearch}}
	token := runTok(role)
	if _, err := f.env.Owner.Exec(ctx, `INSERT INTO agent_run_token(token_hash,workspace_id,agent_run_id,role,attempt,scopes,expires_at) VALUES($1,$2,$3,'coresearch',1,$4,now()+interval '1 hour')`, auth.Hash(token), f.ws, run, role.Scopes); err != nil {
		t.Fatal(err)
	}
	sealed, err := f.h.Conn.Seal(f.ws, "materials-project", key)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = f.env.Owner.Exec(ctx, `INSERT INTO workspace_connector(workspace_id,name,key_ciphertext,updated_by) VALUES($1,'materials-project',$2,$3)`, f.ws, sealed, f.user); err != nil {
		t.Fatal(err)
	}
	if _, err = f.env.Owner.Exec(ctx, `INSERT INTO workspace_connector(workspace_id,name,enabled,updated_by) VALUES($1,'ensembl',false,$2)`, f.ws, f.user); err != nil {
		t.Fatal(err)
	}
	srv := httptest.NewServer(middleware.AgentRunAuth(f.st)(f.h.Handler()))
	defer srv.Close()
	cs, err := mcp.NewClient(&mcp.Implementation{Name: "test", Version: "1"}, nil).Connect(ctx, &mcp.StreamableClientTransport{Endpoint: srv.URL, HTTPClient: &http.Client{Transport: bearer(token)}}, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer cs.Close()
	tools, err := cs.ListTools(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	names := []string{}
	for _, tool := range tools.Tools {
		names = append(names, tool.Name)
	}
	if !slices.Contains(names, "fetch") || !slices.Contains(names, "list_connectors") || slices.Contains(names, "call_tool") {
		t.Fatalf("Coresearch tools: %v", names)
	}
	listed, err := cs.CallTool(ctx, &mcp.CallToolParams{Name: "list_connectors", Arguments: map[string]any{}})
	if err != nil || listed.IsError {
		t.Fatalf("list: %+v %v", listed, err)
	}
	data, err := json.Marshal(listed)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(data), "materials-project") || strings.Contains(string(data), `"ensembl"`) || strings.Contains(string(data), key) {
		t.Fatalf("wrong connectors: %s", data)
	}
	fetched, err := cs.CallTool(ctx, &mcp.CallToolParams{Name: "fetch", Arguments: map[string]any{"connector": "materials-project", "path": "/materials/summary/mp-149"}})
	if err != nil || fetched.IsError {
		t.Fatalf("fetch: %+v %v", fetched, err)
	}
	data, err = json.Marshal(fetched)
	if err != nil {
		t.Fatal(err)
	}
	if f.hits.Load() != 1 || strings.Contains(string(data), key) || !strings.Contains(string(data), "[key removed]") {
		t.Fatalf("key leak or missing upstream: %s hits=%d", data, f.hits.Load())
	}
	if f.count(`SELECT count(*) FROM event WHERE type='sources.called'`) != 1 || f.count(`SELECT count(*) FROM record`) != 0 {
		t.Fatal("Coresearch fetch must log a call without evidence")
	}
}
