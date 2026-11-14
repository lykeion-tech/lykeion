package migrations_test

import (
	"io/fs"
	"testing"

	"lykeion/server/internal/testutil"
	"lykeion/server/migrations"
)

func TestUnifiedRunsMigration(t *testing.T) {
	env := testutil.Setup(t)
	ctx := t.Context()
	var exists bool
	column := `SELECT EXISTS (SELECT 1 FROM information_schema.columns WHERE table_schema='public' AND table_name='agent_run' AND column_name='run_private')`
	if err := env.Owner.QueryRow(ctx, column).Scan(&exists); err != nil {
		t.Fatal(err)
	}
	if exists {
		t.Fatal("0018 up retained run_private")
	}
	down, err := fs.ReadFile(migrations.FS, "0018_unified_runs.down.sql")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := env.Owner.Exec(ctx, string(down)); err != nil {
		t.Fatal(err)
	}
	if err := env.Owner.QueryRow(ctx, column).Scan(&exists); err != nil {
		t.Fatal(err)
	}
	if !exists {
		t.Fatal("0018 down did not restore run_private")
	}
	user, ws, card := seedWorkspace(t, env)
	var private bool
	if err := env.Owner.QueryRow(ctx, `INSERT INTO agent_run (workspace_id,card_id,responsible_user_id,board_column,round,role,harness,prompt) VALUES ($1,$2,$3,'grounding',1,'writer','claude','p') RETURNING run_private`, ws, card, user).Scan(&private); err != nil {
		t.Fatal(err)
	}
	if private {
		t.Fatal("restored run_private did not default to false")
	}
	up, err := fs.ReadFile(migrations.FS, "0018_unified_runs.up.sql")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := env.Owner.Exec(ctx, string(up)); err != nil {
		t.Fatal(err)
	}
	if err := env.Owner.QueryRow(ctx, column).Scan(&exists); err != nil {
		t.Fatal(err)
	}
	if exists {
		t.Fatal("reapplying 0018 retained run_private")
	}
}
