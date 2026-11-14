package migrations_test

import (
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgconn"
	"io/fs"
	"lykeion/server/internal/testutil"
	"lykeion/server/migrations"
	"testing"
)

func TestMachineAttemptUpgradeBackfillAndRLS(t *testing.T) {
	env := testutil.Setup(t)
	ctx := t.Context()
	user, ws, card := seedWorkspace(t, env)
	exec := func(sql string, args ...any) {
		t.Helper()
		if _, err := env.Owner.Exec(ctx, sql, args...); err != nil {
			t.Fatal(err)
		}
	}
	migration := func(name string) {
		t.Helper()
		b, err := fs.ReadFile(migrations.FS, name)
		if err != nil {
			t.Fatal(err)
		}
		exec(string(b))
	}
	migration("0024_machine_attempts.down.sql")
	a, b, run := uuid.New(), uuid.New(), uuid.New()
	exec(`INSERT INTO runtime(id,workspace_id,owner_id,daemon_id,harness,device_name) VALUES($1,$3,$4,'A','codex','same name'),($2,$3,$4,'B','codex','same name')`, a, b, ws, user)
	exec(`INSERT INTO agent_run(id,workspace_id,card_id,responsible_user_id,board_column,round,role,harness,prompt,status,attempt,runtime_id) VALUES($1,$2,$3,$4,'grounding',1,'scoper','codex','p','running',6,$5)`, run, ws, card, user, b)
	exec(`INSERT INTO agent_run_receipt(workspace_id,agent_run_id,attempt,runtime_id,result) VALUES
 ($1,$2,1,$3,'{"started_at":null}'),
 ($1,$2,2,$4,'{"started_at":"2026-10-20T10:00:00Z"}'),
 ($1,$2,3,$5,'{"started_at":"2026-10-20T10:00:00Z"}'),
 ($1,$2,4,$3,'{"started_at":null}')`, ws, run, a, b, uuid.New())
	exec(`UPDATE agent_run_receipt SET result = result || jsonb_build_object('responsible_user_id', $1::uuid) WHERE agent_run_id=$2`, user, run)
	// A surviving runtime alone cannot supply a missing historical researcher.
	exec(`INSERT INTO agent_run_receipt(workspace_id,agent_run_id,attempt,runtime_id,result) VALUES($1,$2,5,$3,'{}')`, ws, run, a)
	exec(`INSERT INTO agent_run_usage(workspace_id,agent_run_id,attempt,model,input_tokens,output_tokens,cache_read_tokens,cache_write_tokens,cost_usd) VALUES($1,$2,1,'m',1,0,0,0,0),($1,$2,3,'m',3,0,0,0,0),($1,$2,5,'m',5,0,0,0,0)`, ws, run)
	exec(`INSERT INTO agent_run_token(token_hash,workspace_id,agent_run_id,card_id,role,attempt,expires_at) VALUES('current',$1,$2,$3,'scoper',6,now()+interval '1 hour')`, ws, run, card)
	for range 2 {
		migration("0024_machine_attempts.up.sql")
		rows, err := env.Owner.Query(ctx, `SELECT attempt,daemon_id FROM agent_run_machine_attempt ORDER BY attempt`)
		if err != nil {
			t.Fatal(err)
		}
		got := map[int]string{}
		for rows.Next() {
			var attempt int
			var daemon string
			if err := rows.Scan(&attempt, &daemon); err != nil {
				t.Fatal(err)
			}
			got[attempt] = daemon
		}
		rows.Close()
		if len(got) != 3 || got[1] != "A" || got[2] != "B" || got[6] != "B" {
			t.Fatalf("backfill guessed/lost provenance: %+v", got)
		}
		// Unknown runtime, receipt-only refusal, and usage without attempt provenance stay unknown.
		migration("0024_machine_attempts.down.sql")
	}
	migration("0024_machine_attempts.up.sql")
	exec(`DELETE FROM runtime WHERE id=$1`, a)
	var daemon string
	if err := env.Owner.QueryRow(ctx, `SELECT daemon_id FROM agent_run_machine_attempt WHERE agent_run_id=$1 AND attempt=1`, run).Scan(&daemon); err != nil || daemon != "A" {
		t.Fatalf("deleted attribution: %s %v", daemon, err)
	}
	var n int
	if err := env.App.QueryRow(ctx, `SELECT count(*) FROM agent_run_machine_attempt`).Scan(&n); err != nil || n != 0 {
		t.Fatalf("unscoped history: %d %v", n, err)
	}
	tx, err := env.App.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback(ctx)
	if _, err = tx.Exec(ctx, `SELECT set_config('app.workspace_id',$1,true)`, ws.String()); err != nil {
		t.Fatal(err)
	}
	if err = tx.QueryRow(ctx, `SELECT count(*) FROM agent_run_machine_attempt`).Scan(&n); err != nil || n != 3 {
		t.Fatalf("tenant history: %d %v", n, err)
	}
	if _, err = tx.Exec(ctx, `UPDATE agent_run_machine_attempt SET workspace_id=$1`, uuid.New()); err == nil {
		t.Fatal("cross-tenant update accepted")
	} else if pg, ok := err.(*pgconn.PgError); !ok || pg.Code != "42501" {
		t.Fatalf("want RLS error, got %v", err)
	}
	tx.Rollback(ctx)
	if _, err = env.Owner.Exec(ctx, `INSERT INTO agent_run_machine_attempt(workspace_id,agent_run_id,attempt,runtime_id,daemon_id,owner_id,responsible_user_id) VALUES($1,$2,1,$3,'wrong',$4,$4)`, ws, run, b, user); err == nil {
		t.Fatal("duplicate attempt overwritten")
	}
	other := uuid.New()
	exec(`INSERT INTO workspace(id,slug,name) VALUES($1,'attempt-other','other')`, other)
	if _, err = env.Owner.Exec(ctx, `INSERT INTO agent_run_machine_attempt(workspace_id,agent_run_id,attempt,runtime_id,daemon_id,owner_id,responsible_user_id) VALUES($1,$2,9,$3,'wrong',$4,$4)`, other, run, b, user); err == nil {
		t.Fatal("cross-workspace run accepted")
	}
}
