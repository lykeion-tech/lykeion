package migrations_test

import (
	"io/fs"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgconn"

	"lykeion/server/internal/testutil"
	"lykeion/server/migrations"
)

func TestReliableRunsMigration(t *testing.T) {
	env := testutil.Setup(t)
	ctx := t.Context()
	u, ws, card := seedWorkspace(t, env)
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
	var runtime uuid.UUID
	if err := env.Owner.QueryRow(ctx, `INSERT INTO runtime (workspace_id, owner_id, daemon_id, harness) VALUES ($1,$2,'machine','claude') RETURNING id`, ws, u).Scan(&runtime); err != nil {
		t.Fatal(err)
	}
	insertRun := `INSERT INTO agent_run (workspace_id,card_id,responsible_user_id,board_column,round,role,harness,prompt,failure_reason) VALUES ($1,$2,$3,'grounding',1,'scoper','claude','p',$4)`
	for _, r := range []string{"sign_in", "cli_missing", "cli_too_old", "door", "cli_broken", "sandbox", "fence_breach", "hand_in_invalid"} {
		exec(insertRun, ws, card, u, r)
	}
	insertInbox := `INSERT INTO inbox_item (workspace_id,card_id,user_id,kind,text,runtime_id,cause) VALUES ($1,NULL,$2,$3,'t',$4,$5)`
	exec(insertInbox, ws, u, "needs_decision", nil, nil)
	exec(insertInbox, ws, u, "open_doubts", nil, nil)
	exec(insertInbox, ws, u, "waiting_for_machine", runtime, "sign_in")
	refuse(insertInbox, "23505", ws, u, "waiting_for_machine", runtime, "sign_in")
	exec(insertInbox, ws, u, "waiting_for_machine", runtime, "door")
	exec(`UPDATE inbox_item SET resolved_at=now() WHERE kind='waiting_for_machine' AND cause='sign_in'`)
	exec(insertInbox, ws, u, "waiting_for_machine", runtime, "sign_in")

	exec(`UPDATE agent_run SET retry_after=now(), infra_failures=1`)
	exec(`UPDATE runtime SET unhealthy_reason='sign_in'`)
	exec(`UPDATE agent_run_usage SET estimated=true`)

	b, err := fs.ReadFile(migrations.FS, "0041_reliable_agent_runs.down.sql")
	if err != nil {
		t.Fatal(err)
	}
	exec(string(b))
	refuse(insertRun, "23514", ws, card, u, "fence_breach")
	var n int
	if err := env.Owner.QueryRow(ctx, `SELECT count(*) FROM information_schema.columns WHERE (table_name='inbox_item' AND column_name IN ('runtime_id','cause'))
		OR (table_name='agent_run' AND column_name IN ('retry_after','infra_failures')) OR (table_name='runtime' AND column_name='unhealthy_reason')
		OR (table_name='agent_run_usage' AND column_name='estimated')`).Scan(&n); err != nil || n != 0 {
		t.Fatalf("columns left=%d (%v), want 0", n, err)
	}
	if err := env.Owner.QueryRow(ctx, `SELECT count(*) FROM agent_run`).Scan(&n); err != nil || n != 0 {
		t.Fatalf("runs with new reasons left=%d (%v), want 0", n, err)
	}
}
