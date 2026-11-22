package migrations_test

import (
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgconn"

	"lykeion/server/internal/testutil"
)

func TestSlackMigration(t *testing.T) {
	env := testutil.Setup(t)
	ctx := t.Context()
	u, wsA, _ := seedWorkspace(t, env)
	wsB := uuid.New()
	exec := func(sql string, args ...any) {
		t.Helper()
		if _, err := env.Owner.Exec(ctx, sql, args...); err != nil {
			t.Fatal(err)
		}
	}
	refuse := func(sql, code string, args ...any) {
		t.Helper()
		_, err := env.Owner.Exec(ctx, sql, args...)
		pgerr, ok := err.(*pgconn.PgError)
		if !ok || pgerr.Code != code {
			t.Fatalf("want SQLSTATE %s, got %v", code, err)
		}
	}
	exec(`INSERT INTO workspace (id,slug,name) VALUES ($1,'b','b')`, wsB)
	for _, ws := range []uuid.UUID{wsA, wsB} {
		exec(`INSERT INTO member (workspace_id,user_id,role) VALUES ($1,$2,'owner')`, ws, u)
	}
	install := func(ws uuid.UUID, app, status string) uuid.UUID {
		t.Helper()
		var id uuid.UUID
		if err := env.Owner.QueryRow(ctx, `INSERT INTO slack_installation (workspace_id,app_id,team_id,team_name,bot_user_id,bot_token_ciphertext,app_token_ciphertext,installed_by,status)
			VALUES ($1,$2,'T','team','B','\x01','\x02',$3,$4) RETURNING id`, ws, app, u, status).Scan(&id); err != nil {
			t.Fatal(err)
		}
		return id
	}
	instA := install(wsA, "A1", "active")
	instB := install(wsB, "B1", "revoked")
	refuse(`INSERT INTO slack_installation (workspace_id,app_id,team_id,team_name,bot_user_id,bot_token_ciphertext,app_token_ciphertext,installed_by)
		VALUES ($1,'A1','T','team','B','\x01','\x02',$2)`, "23505", wsB, u)

	exec(`INSERT INTO slack_binding (workspace_id,installation_id,slack_user_id,user_id) VALUES ($1,$2,'U1',$3)`, wsA, instA, u)
	exec(`INSERT INTO slack_link_token (workspace_id,installation_id,slack_user_id,token_hash,expires_at) VALUES ($1,$2,'U2','h1',now()+interval '15 minutes')`, wsA, instA)
	refuse(`INSERT INTO slack_link_token (workspace_id,installation_id,slack_user_id,token_hash,created_at,expires_at) VALUES ($1,$2,'U2','h2',now(),now()+interval '16 minutes')`, "23514", wsA, instA)
	var chat uuid.UUID
	if err := env.Owner.QueryRow(ctx, `INSERT INTO lab_chat (workspace_id,owner_id) VALUES ($1,$2) RETURNING id`, wsA, u).Scan(&chat); err != nil {
		t.Fatal(err)
	}
	exec(`INSERT INTO slack_chat (workspace_id,installation_id,user_id,channel,thread_ts,lab_chat_id) VALUES ($1,$2,$3,'D1','',$4)`, wsA, instA, u, chat)
	exec(`INSERT INTO slack_post (workspace_id,installation_id,user_id,channel,thread_ts,kind) VALUES ($1,$2,$3,'D1','','answer')`, wsA, instA, u)

	// Row-level security: under workspace B nothing of A is visible.
	tx, err := env.App.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback(ctx)
	if _, err := tx.Exec(ctx, `SELECT set_config('app.workspace_id',$1,true)`, wsB.String()); err != nil {
		t.Fatal(err)
	}
	for _, tbl := range []string{"slack_installation", "slack_binding", "slack_link_token", "slack_chat", "slack_post"} {
		var n int
		if err := tx.QueryRow(ctx, `SELECT count(*) FROM `+tbl+` WHERE workspace_id=$1`, wsA).Scan(&n); err != nil || n != 0 {
			t.Fatalf("%s: workspace B sees %d rows of A (%v)", tbl, n, err)
		}
	}
	tx.Rollback(ctx)

	// The cross-tenant lookup returns active rows of every workspace, none revoked.
	lookup := func() map[uuid.UUID]uuid.UUID {
		t.Helper()
		rows, err := env.App.Query(ctx, `SELECT id, workspace_id FROM slack_active_installations()`)
		if err != nil {
			t.Fatal(err)
		}
		defer rows.Close()
		got := map[uuid.UUID]uuid.UUID{}
		for rows.Next() {
			var id, ws uuid.UUID
			if err := rows.Scan(&id, &ws); err != nil {
				t.Fatal(err)
			}
			got[id] = ws
		}
		return got
	}
	if got := lookup(); len(got) != 1 || got[instA] != wsA {
		t.Fatalf("lookup=%v, want only %s", got, instA)
	}
	exec(`UPDATE slack_installation SET status='active' WHERE id=$1`, instB)
	if got := lookup(); len(got) != 2 || got[instB] != wsB {
		t.Fatalf("lookup=%v, want both installations", got)
	}

	// Cascades: the member row takes the binding, the lab chat takes its Slack chat.
	exec(`DELETE FROM lab_chat WHERE id=$1`, chat)
	exec(`DELETE FROM member WHERE workspace_id=$1 AND user_id=$2`, wsA, u)
	for _, tbl := range []string{"slack_chat", "slack_binding"} {
		var n int
		if err := env.Owner.QueryRow(ctx, `SELECT count(*) FROM `+tbl).Scan(&n); err != nil || n != 0 {
			t.Fatalf("%s rows=%d (%v), want 0", tbl, n, err)
		}
	}
}
