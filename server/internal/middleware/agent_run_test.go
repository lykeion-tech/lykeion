package middleware

import (
	"errors"
	"testing"

	"github.com/google/uuid"
	"lykeion/server/internal/auth"
	"lykeion/server/internal/store"
	"lykeion/server/internal/testutil"
)

func TestLockRoleCardlessAuthority(t *testing.T) {
	env := testutil.Setup(t)
	ctx := t.Context()
	ws, user, run := uuid.New(), uuid.New(), uuid.New()
	for _, q := range []struct {
		sql  string
		args []any
	}{
		{`INSERT INTO "user" (id,email) VALUES ($1,'cardless@lab.org')`, []any{user}},
		{`INSERT INTO workspace (id,slug,name) VALUES ($1,'cardless','Cardless')`, []any{ws}},
		{`INSERT INTO agent_run (id,workspace_id,kind,requester_id,responsible_user_id,role,board_column,round,harness,prompt,status,attempt) VALUES ($1,$2,'autoresearch',$3,$3,'autoresearch','',0,'claude','p','running',1)`, []any{run, ws, user}},
		{`INSERT INTO agent_run_token (token_hash,workspace_id,agent_run_id,role,attempt,scopes,expires_at) VALUES ($1,$2,$3,'autoresearch',1,'{report:write,card:create}',now()+interval '1 hour')`, []any{auth.Hash("ltk_current"), ws, run}},
		{`INSERT INTO agent_run_token (token_hash,workspace_id,agent_run_id,role,attempt,expires_at) VALUES ($1,$2,$3,'autoresearch',2,now()+interval '1 hour')`, []any{auth.Hash("ltk_stale"), ws, run}},
	} {
		if _, err := env.Owner.Exec(ctx, q.sql, q.args...); err != nil {
			t.Fatal(err)
		}
	}
	st := &store.Store{Pool: env.App}
	lookup, err := st.Global().LookupAgentRunToken(ctx, auth.Hash("ltk_current"))
	if err != nil || lookup.CardID != uuid.Nil {
		t.Fatalf("cardless lookup: %+v, %v", lookup, err)
	}
	base := Role{AgentRunID: run, WorkspaceID: ws, Role: "autoresearch"}
	for _, tc := range []struct {
		name   string
		change func(*Role)
		token  string
		ok     bool
	}{
		{"cardless current", nil, "ltk_current", true},
		{"stale attempt", nil, "ltk_stale", false},
		{"unknown token", nil, "ltk_unknown", false},
		{"wrong role", func(r *Role) { r.Role = "scoper" }, "ltk_current", false},
		{"wrong column", func(r *Role) { r.Column = "grounding" }, "ltk_current", false},
		{"wrong round", func(r *Role) { r.Round = 1 }, "ltk_current", false},
		{"wrong card", func(r *Role) { r.CardID = uuid.New() }, "ltk_current", false},
		{"wrong run", func(r *Role) { r.AgentRunID = uuid.New() }, "ltk_current", false},
		{"wrong workspace", func(r *Role) { r.WorkspaceID = uuid.New() }, "ltk_current", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			role := base
			if tc.change != nil {
				tc.change(&role)
			}
			err := st.InWorkspace(ctx, role.WorkspaceID, user, func(tx *store.Tx) error { return LockRole(ctx, tx, role, tc.token) })
			if tc.ok && err != nil {
				t.Fatal(err)
			}
			if !tc.ok && !errors.Is(err, ErrRoleUnauthorized) {
				t.Fatalf("want unauthorized, got %v", err)
			}
		})
	}
	if _, err := env.Owner.Exec(ctx, `UPDATE agent_run SET status='completed' WHERE id=$1`, run); err != nil {
		t.Fatal(err)
	}
	if err := st.InWorkspace(ctx, ws, user, func(tx *store.Tx) error { return LockRole(ctx, tx, base, "ltk_current") }); !errors.Is(err, ErrRoleUnauthorized) {
		t.Fatalf("settled run: %v", err)
	}
}
