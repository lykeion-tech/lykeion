package migrations_test

import (
	"io/fs"
	"lykeion/server/internal/testutil"
	"lykeion/server/migrations"
	"testing"
)

func TestProviderQuotaMigrationPreservesEvidenceOnRollback(t *testing.T) {
	env := testutil.Setup(t)
	u, ws, card := seedWorkspace(t, env)
	var id string
	err := env.Owner.QueryRow(t.Context(), `INSERT INTO agent_run (workspace_id,card_id,responsible_user_id,board_column,round,role,harness,prompt,failure_reason,error) VALUES ($1,$2,$3,'grounding',1,'scoper','claude','p','provider_quota','weekly limit') RETURNING id`, ws, card, u).Scan(&id)
	if err != nil {
		t.Fatal(err)
	}
	down, err := fs.ReadFile(migrations.FS, "0075_provider_quota.down.sql")
	if err != nil {
		t.Fatal(err)
	}
	if _, err = env.Owner.Exec(t.Context(), string(down)); err != nil {
		t.Fatal(err)
	}
	var reason, detail string
	if err = env.Owner.QueryRow(t.Context(), `SELECT failure_reason,error FROM agent_run WHERE id=$1`, id).Scan(&reason, &detail); err != nil || reason != "cli_broken" || detail != "weekly limit" {
		t.Fatalf("rollback: %s %s %v", reason, detail, err)
	}
	if _, err = env.Owner.Exec(t.Context(), `UPDATE agent_run SET failure_reason='provider_quota' WHERE id=$1`, id); err == nil {
		t.Fatal("old constraint accepted quota")
	}
	up, err := fs.ReadFile(migrations.FS, "0075_provider_quota.up.sql")
	if err != nil {
		t.Fatal(err)
	}
	if _, err = env.Owner.Exec(t.Context(), string(up)); err != nil {
		t.Fatal(err)
	}
	if _, err = env.Owner.Exec(t.Context(), `UPDATE agent_run SET failure_reason='provider_quota' WHERE id=$1`, id); err != nil {
		t.Fatal(err)
	}
	if _, err = env.Owner.Exec(t.Context(), `UPDATE agent_run SET failure_reason='unknown' WHERE id=$1`, id); err == nil {
		t.Fatal("constraint accepted unknown reason")
	}
}
