package migrations_test

import (
	"io/fs"
	"testing"

	"github.com/google/uuid"

	"lykeion/server/internal/testutil"
	"lykeion/server/migrations"
)

func TestStageTeamsMigration(t *testing.T) {
	env := testutil.Setup(t)
	ctx := t.Context()
	read := func(name string) string {
		b, err := fs.ReadFile(migrations.FS, name)
		if err != nil {
			t.Fatal(err)
		}
		return string(b)
	}
	exec := func(sql string, args ...any) {
		t.Helper()
		if _, err := env.Owner.Exec(ctx, sql, args...); err != nil {
			t.Fatalf("%s: %v", sql, err)
		}
	}
	// Back to the state before 0020, then seed what it has to clean up.
	exec(read("0020_stage_teams.down.sql"))

	u, ws, first := seedWorkspace(t, env)
	exec(`DELETE FROM card WHERE id = $1`, first)
	exec(`INSERT INTO member (workspace_id, user_id, role) VALUES ($1, $2, 'owner')`, ws, u)
	var agent uuid.UUID
	if err := env.Owner.QueryRow(ctx, `INSERT INTO agent (workspace_id, name, harness, owner_id) VALUES ($1, 'a', 'claude', $2) RETURNING id`, ws, u).Scan(&agent); err != nil {
		t.Fatal(err)
	}
	study := func(n int, col, origin, atype string, aid *uuid.UUID) uuid.UUID {
		t.Helper()
		var id uuid.UUID
		if err := env.Owner.QueryRow(ctx, `INSERT INTO card (workspace_id, number, title, question, owner_id, board_column, origin, assignee_type, assignee_id)
			VALUES ($1, $2, 't', 'q', $3, $4, $5, NULLIF($6, ''), $7) RETURNING id`, ws, n, u, col, origin, atype, aid).Scan(&id); err != nil {
			t.Fatal(err)
		}
		return id
	}
	memberStudy := study(1, "backlog", "", "member", &u)
	agentStudy := study(2, "grounding", "", "agent", &agent)
	autoStudy := study(3, "grounding", "autoresearch:"+uuid.NewString(), "agent", &agent)
	teamStudy := study(4, "grounding", "", "", nil)

	pack := func(version, age string) uuid.UUID {
		t.Helper()
		var id uuid.UUID
		if err := env.Owner.QueryRow(ctx, `INSERT INTO field_pack (workspace_id, name, version, sha256, manifest, blob_key, source, created_at)
			VALUES ($1, 'T', $2, 'h', '{}', 'k', 'shipped', now() - $3::interval) RETURNING id`, ws, version, age).Scan(&id); err != nil {
			t.Fatal(err)
		}
		return id
	}
	v1, v2 := pack("1", "2 days"), pack("2", "1 day")
	var ar uuid.UUID
	if err := env.Owner.QueryRow(ctx, `INSERT INTO autoresearch (workspace_id, owner_id, title, execution_mode) VALUES ($1, $2, 'ar', 'run_only') RETURNING id`, ws, u).Scan(&ar); err != nil {
		t.Fatal(err)
	}
	exec(`INSERT INTO card_team (workspace_id, card_id, board_column, pack_id) VALUES ($1, $2, 'grounding', $3)`, ws, teamStudy, v1)
	exec(`INSERT INTO team_rating (workspace_id, card_id, board_column, pack_id, user_id, offered, drawn_at)
		VALUES ($1, $2, 'grounding', $3, $4, false, now())`, ws, teamStudy, v1, u)
	exec(`INSERT INTO autoresearch_team (workspace_id, autoresearch_id, board_column, pack_id) VALUES ($1, $2, 'grounding', $3)`, ws, ar, v1)
	exec(`INSERT INTO team_agent (workspace_id, pack_id, user_id, member, agent_id) VALUES ($1, $2, $3, 'm', $4)`, ws, v1, u, agent)
	exec(`INSERT INTO team_agent (workspace_id, pack_id, user_id, member, agent_id) VALUES ($1, $2, $3, 'm', $4)`, ws, v2, u, agent)
	exec(`INSERT INTO field_pack_approval (workspace_id, pack_id, approved_by) VALUES ($1, $2, $3)`, ws, v1, u)
	exec(`UPDATE card SET pack_id = $2 WHERE id = $1`, teamStudy, v1)

	exec(read("0020_stage_teams.up.sql"))

	var gone, kept bool
	if err := env.Owner.QueryRow(ctx, `SELECT
		NOT EXISTS (SELECT 1 FROM card WHERE id IN ($1, $2)),
		(SELECT count(*) FROM card WHERE id IN ($3, $4)) = 2`, memberStudy, agentStudy, autoStudy, teamStudy).Scan(&gone, &kept); err != nil {
		t.Fatal(err)
	}
	if !gone || !kept {
		t.Fatalf("person studies gone %v, Autoresearch and Team studies kept %v", gone, kept)
	}
	var packs int
	var only uuid.UUID
	if err := env.Owner.QueryRow(ctx, `SELECT count(*), min(id::text)::uuid FROM field_pack WHERE name = 'T'`).Scan(&packs, &only); err != nil {
		t.Fatal(err)
	}
	if packs != 1 || only != v2 {
		t.Fatalf("%d packs left, newest kept %v", packs, only == v2)
	}
	for _, table := range []string{"team_agent", "field_pack_approval"} {
		var n int
		if err := env.Owner.QueryRow(ctx, `SELECT count(*) FROM `+table).Scan(&n); err != nil || n != 1 {
			t.Fatalf("%s rows after the merge: %d, %v", table, n, err)
		}
	}
	for _, q := range []string{
		`SELECT pack_id FROM card_team`, `SELECT pack_id FROM team_rating`, `SELECT pack_id FROM autoresearch_team`,
		`SELECT pack_id FROM team_agent`, `SELECT pack_id FROM field_pack_approval`, `SELECT pack_id FROM card WHERE id = '` + teamStudy.String() + `'`,
	} {
		var got uuid.UUID
		if err := env.Owner.QueryRow(ctx, q).Scan(&got); err != nil || got != v2 {
			t.Fatalf("%s: %v, %v", q, got, err)
		}
	}

	if _, err := env.Owner.Exec(ctx, `INSERT INTO field_pack (workspace_id, name, version, sha256, manifest, blob_key, source)
		VALUES ($1, 'T', '3', 'h', '{}', 'k', 'shipped')`, ws); err == nil {
		t.Fatal("a second pack with the same name was accepted")
	}
	if _, err := env.Owner.Exec(ctx, `INSERT INTO card (workspace_id, number, title, question, owner_id, board_column) VALUES ($1, 9, 't', 'q', $2, 'backlog')`, ws, u); err == nil {
		t.Fatal("a card in backlog was accepted")
	}
	if _, err := env.Owner.Exec(ctx, `INSERT INTO card (workspace_id, number, title, question, owner_id) VALUES ($1, 9, 't', 'q', $2)`, ws, u); err == nil {
		t.Fatal("a card without a column was accepted")
	}

	exec(read("0020_stage_teams.down.sql"))
	exec(`INSERT INTO card (workspace_id, number, title, question, owner_id) VALUES ($1, 9, 't', 'q', $2)`, ws, u)
	exec(`DELETE FROM card WHERE number = 9`)
	exec(read("0020_stage_teams.up.sql"))
}
