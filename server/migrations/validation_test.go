package migrations_test

import (
	"io/fs"
	"testing"

	"github.com/google/uuid"

	"lykeion/server/internal/testutil"
	"lykeion/server/migrations"
)

func TestRerunRunKind(t *testing.T) {
	env := testutil.Setup(t)
	ctx := t.Context()
	u, ws, card := seedWorkspace(t, env)
	ins := `INSERT INTO agent_run (workspace_id, card_id, kind, responsible_user_id, board_column, round, role, harness, prompt)
		VALUES ($1, $2, 'rerun', $3, 'validation', 1, 'rerun', '', 'p')`
	if _, err := env.Owner.Exec(ctx, ins, ws, card, u); err != nil {
		t.Fatalf("a rerun on a card: %v", err)
	}
	_, err := env.Owner.Exec(ctx, ins, ws, nil, u)
	wantCode(t, err, "23514", "a rerun without a card")
	bare := `INSERT INTO agent_run (workspace_id, card_id, kind, responsible_user_id, board_column, round, role, harness, prompt)
		VALUES ($1, $2, $4, $3, 'validation', 1, 'rerun', $5, 'p')`
	if _, err := env.Owner.Exec(ctx, bare, ws, card, u, "rerun", ""); err != nil {
		t.Fatalf("a rerun with no harness: %v", err)
	}
	_, err = env.Owner.Exec(ctx, bare, ws, card, u, "role", "")
	wantCode(t, err, "23514", "a role run with no harness")
}

func TestValidationInProgressRestartsAtMigration(t *testing.T) {
	env := testutil.Setup(t)
	ctx := t.Context()
	u, ws, card := seedWorkspace(t, env)
	read := func(name string) string {
		t.Helper()
		b, err := fs.ReadFile(migrations.FS, name)
		if err != nil {
			t.Fatal(err)
		}
		return string(b)
	}
	exec := func(sql string, args ...any) {
		t.Helper()
		if _, err := env.Owner.Exec(ctx, sql, args...); err != nil {
			t.Fatal(err)
		}
	}
	exec(read("0045_validation.down.sql"))

	exec(`UPDATE card SET board_column='validation', state='working', round=2, redos=1, legacy_column='validation' WHERE id=$1`, card)
	var run uuid.UUID
	if err := env.Owner.QueryRow(ctx, `INSERT INTO agent_run (workspace_id, card_id, responsible_user_id, board_column, round, role, harness, prompt)
		VALUES ($1,$2,$3,'validation',2,'reviewer','claude','p') RETURNING id`, ws, card, u).Scan(&run); err != nil {
		t.Fatal(err)
	}
	var blocked uuid.UUID
	if err := env.Owner.QueryRow(ctx, `INSERT INTO card (workspace_id, number, title, question, owner_id, board_column, state, round, redos)
		VALUES ($1, 2, 't', 'q', $2, 'validation', 'blocked', 2, 1) RETURNING id`, ws, u).Scan(&blocked); err != nil {
		t.Fatal(err)
	}
	exec(`INSERT INTO record (workspace_id, card_id, board_column, round, kind, role) VALUES ($1,$2,'validation',2,'claim','reviewer')`, ws, card)

	exec(read("0045_validation.up.sql"))

	var round, redos int
	var legacy, status string
	if err := env.Owner.QueryRow(ctx, `SELECT round, redos, legacy_column FROM card WHERE id=$1`, card).Scan(&round, &redos, &legacy); err != nil {
		t.Fatal(err)
	}
	if round != 3 || redos != 0 || legacy != "" {
		t.Fatalf("round=%d redos=%d legacy=%q, want 3 0 \"\"", round, redos, legacy)
	}
	if err := env.Owner.QueryRow(ctx, `SELECT status FROM agent_run WHERE id=$1`, run).Scan(&status); err != nil || status != "cancelled" {
		t.Fatalf("run status=%q (%v), want cancelled", status, err)
	}
	var n int
	if err := env.Owner.QueryRow(ctx, `SELECT count(*) FROM record WHERE card_id=$1 AND round=2`, card).Scan(&n); err != nil || n != 1 {
		t.Fatalf("round-2 records=%d (%v), want 1", n, err)
	}
	if err := env.Owner.QueryRow(ctx, `SELECT round FROM card WHERE id=$1`, blocked).Scan(&round); err != nil || round != 2 {
		t.Fatalf("blocked card round=%d (%v), want 2", round, err)
	}

	exec(read("0045_validation.down.sql"))
	if err := env.Owner.QueryRow(ctx, `SELECT round FROM card WHERE id=$1`, card).Scan(&round); err != nil || round != 3 {
		t.Fatalf("down changed the round to %d (%v)", round, err)
	}
	_, err := env.Owner.Exec(ctx, `INSERT INTO agent_run (workspace_id, card_id, kind, responsible_user_id, board_column, round, role, harness, prompt)
		VALUES ($1,$2,'rerun',$3,'validation',1,'rerun','claude','p')`, ws, card, u)
	wantCode(t, err, "23514", "a rerun after the down migration")
	exec(read("0045_validation.up.sql"))
}
