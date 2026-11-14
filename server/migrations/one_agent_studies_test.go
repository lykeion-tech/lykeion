package migrations_test

import (
	"io/fs"
	"testing"

	"github.com/google/uuid"

	"lykeion/server/internal/testutil"
	"lykeion/server/migrations"
)

func TestOneAgentStudiesMigration(t *testing.T) {
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
	id := func(sql string, args ...any) uuid.UUID {
		t.Helper()
		var id uuid.UUID
		if err := env.Owner.QueryRow(ctx, sql, args...).Scan(&id); err != nil {
			t.Fatalf("%s: %v", sql, err)
		}
		return id
	}
	count := func(sql string, args ...any) int {
		t.Helper()
		var n int
		if err := env.Owner.QueryRow(ctx, sql, args...).Scan(&n); err != nil {
			t.Fatalf("%s: %v", sql, err)
		}
		return n
	}
	exec(read("0030_one_agent_studies.down.sql"))

	u, ws, first := seedWorkspace(t, env)
	exec(`DELETE FROM card WHERE id = $1`, first)
	exec(`INSERT INTO member (workspace_id, user_id, role) VALUES ($1, $2, 'owner')`, ws, u)
	agent := id(`INSERT INTO agent (workspace_id, name, harness, owner_id) VALUES ($1, 'a', 'claude', $2) RETURNING id`, ws, u)
	number := 0
	study := func(col, scopeTo, state string) uuid.UUID {
		t.Helper()
		number++
		return id(`INSERT INTO card (workspace_id, number, title, question, owner_id, board_column, scope_to, state)
			VALUES ($1, $2, 't', 'q', $3, $4, $5, $6) RETURNING id`, ws, number, u, col, scopeTo, state)
	}
	pack := func(name, stage string) uuid.UUID {
		t.Helper()
		return id(`INSERT INTO field_pack (workspace_id, name, version, sha256, manifest, blob_key, source, stage)
			VALUES ($1, $2, '1', 'h', '{}', 'k', 'shipped', $3) RETURNING id`, ws, name, stage)
	}
	worked := func(card uuid.UUID, col string, p uuid.UUID) {
		t.Helper()
		exec(`INSERT INTO card_team (workspace_id, card_id, board_column, pack_id) VALUES ($1, $2, $3, $4)`, ws, card, col, p)
		exec(`INSERT INTO team_rating (workspace_id, card_id, board_column, pack_id, user_id, offered, drawn_at)
			VALUES ($1, $2, $3, $4, $5, false, now())`, ws, card, col, p, u)
		run := id(`INSERT INTO agent_run (workspace_id, card_id, responsible_user_id, board_column, round, role, harness, prompt)
			VALUES ($1, $2, $3, $4, 1, 'scoper', 'claude', 'p') RETURNING id`, ws, card, u, col)
		exec(`INSERT INTO record (workspace_id, card_id, board_column, round, kind, role, agent_run_id)
			VALUES ($1, $2, $3, 1, 'note', 'r', $4)`, ws, card, col, run)
	}
	other := pack("Other", "grounding")
	reportingPack := pack("Reporter", "reporting")

	inReporting := study("reporting", "reporting", "working")
	working := study("validation", "reporting", "working")
	done := study("done", "reporting", "done")
	worked(working, "grounding", other)
	worked(done, "grounding", other)
	onReporting := study("reporting", "reporting", "working")
	worked(onReporting, "reporting", reportingPack)
	offLabel := study("validation", "validation", "working")
	worked(offLabel, "validation", reportingPack)

	mixed := id(`INSERT INTO card (workspace_id, number, title, question, owner_id, board_column, scope_to, assignee_type, assignee_id)
		VALUES ($1, 90, 't', 'q', $2, 'validation', 'validation', 'team', $3) RETURNING id`, ws, u, agent)
	// card_follows_end_to_end allows Follows from only on a study that ends at Reporting,
	// so a follower always goes with the Reporting studies.
	follower := id(`INSERT INTO card (workspace_id, number, title, question, owner_id, board_column, scope_to, follows_from)
		VALUES ($1, 91, 't', 'q', $2, 'grounding', 'reporting', ARRAY[$3::uuid]) RETURNING id`, ws, u, mixed)
	withEnv := id(`INSERT INTO card (workspace_id, number, title, question, owner_id, board_column, scope_to, env)
		VALUES ($1, 92, 't', 'q', $2, 'grounding', 'validation', '{"A":"1"}') RETURNING id`, ws, u)
	exec(`INSERT INTO attachment (workspace_id, uploader_id, card_id, name, content_type, size_bytes, sha256, blob_key, kind, service_name, service_start, service_env)
		VALUES ($1, $2, $3, 's.zip', 'application/zip', 1, 'h', 'k', 'service', 'db', 'start', '{A}')`, ws, u, withEnv)

	reportingAR := id(`INSERT INTO autoresearch (workspace_id, owner_id, title, execution_mode, scope_to) VALUES ($1, $2, 'r', 'run_only', 'reporting') RETURNING id`, ws, u)
	exec(`INSERT INTO autoresearch_run (workspace_id, autoresearch_id, source, acting_user_id, planned_at, idempotency_key)
		VALUES ($1, $2, 'manual', $3, now(), 'k1')`, ws, reportingAR, u)
	validationAR := id(`INSERT INTO autoresearch (workspace_id, owner_id, title, execution_mode, scope_to) VALUES ($1, $2, 'v', 'run_only', 'validation') RETURNING id`, ws, u)
	packAR := id(`INSERT INTO autoresearch (workspace_id, owner_id, title, execution_mode, scope_to) VALUES ($1, $2, 'p', 'run_only', 'validation') RETURNING id`, ws, u)
	exec(`INSERT INTO autoresearch_team (workspace_id, autoresearch_id, board_column, pack_id) VALUES ($1, $2, 'validation', $3)`, ws, packAR, reportingPack)
	exec(`INSERT INTO team_agent (workspace_id, pack_id, user_id, member, agent_id) VALUES ($1, $2, $3, 'm', $4)`, ws, reportingPack, u, agent)
	exec(`INSERT INTO team_agent (workspace_id, pack_id, user_id, member, agent_id) VALUES ($1, $2, $3, 'm', $4)`, ws, other, u, agent)
	exec(`INSERT INTO card_team (workspace_id, card_id, board_column, pack_id) VALUES ($1, $2, 'grounding', $3)`, ws, mixed, other)

	exec(read("0030_one_agent_studies.up.sql"))

	gone := []uuid.UUID{inReporting, working, done, onReporting, offLabel, follower}
	if n := count(`SELECT count(*) FROM card WHERE id = ANY($1)`, gone); n != 0 {
		t.Fatalf("%d Reporting studies left", n)
	}
	for _, table := range []string{"card_team", "record", "agent_run", "team_rating"} {
		if n := count(`SELECT count(*) FROM `+table+` WHERE card_id = ANY($1) OR card_id IS NULL`, gone); n != 0 {
			t.Fatalf("%d %s rows of gone studies left", n, table)
		}
	}
	if n := count(`SELECT count(*) FROM field_pack WHERE id = $1`, reportingPack); n != 0 {
		t.Fatal("the Reporting Team is left")
	}
	for _, table := range []string{"autoresearch_team", "team_agent"} {
		if n := count(`SELECT count(*) FROM `+table+` WHERE pack_id = $1`, reportingPack); n != 0 {
			t.Fatalf("%d %s rows of the Reporting Team left", n, table)
		}
	}
	if n := count(`SELECT count(*) FROM autoresearch WHERE id IN ($1, $2)`, reportingAR, packAR); n != 0 {
		t.Fatalf("%d Reporting Autoresearch left", n)
	}
	if n := count(`SELECT count(*) FROM autoresearch_run WHERE autoresearch_id = $1`, reportingAR); n != 0 {
		t.Fatalf("%d runs of the Reporting Autoresearch left", n)
	}
	if n := count(`SELECT count(*) FROM card WHERE id IN ($1, $2)`, mixed, withEnv); n != 2 {
		t.Fatalf("%d of 2 kept studies left", n)
	}
	if n := count(`SELECT count(*) FROM field_pack WHERE id = $1`, other); n != 1 {
		t.Fatal("a Team of another stage is gone")
	}
	if n := count(`SELECT count(*) FROM team_agent WHERE pack_id = $1`, other); n != 1 {
		t.Fatalf("%d team_agent rows of a Team of another stage left", n)
	}
	if n := count(`SELECT count(*) FROM card_team WHERE card_id = $1`, mixed); n != 1 {
		t.Fatalf("%d card_team rows of a kept study left", n)
	}
	if n := count(`SELECT count(*) FROM attachment WHERE card_id = $1 AND kind = 'service' AND service_name = 'db'`, withEnv); n != 1 {
		t.Fatal("the hidden service went with its variables")
	}
	if n := count(`SELECT count(*) FROM autoresearch WHERE id = $1`, validationAR); n != 1 {
		t.Fatal("the Validation Autoresearch is gone")
	}
	if n := count(`SELECT count(*) FROM information_schema.columns WHERE (table_name, column_name) IN (('card', 'follows_from'), ('card', 'env'), ('attachment', 'service_env'))`); n != 0 {
		t.Fatalf("%d removed columns left", n)
	}

	for _, c := range []struct {
		sql  string
		args []any
	}{
		{`INSERT INTO card (workspace_id, number, title, question, owner_id, board_column) VALUES ($1, 99, 't', 'q', $2, 'reporting')`, []any{ws, u}},
		{`INSERT INTO card (workspace_id, number, title, question, owner_id, board_column, scope_to) VALUES ($1, 99, 't', 'q', $2, 'grounding', 'reporting')`, []any{ws, u}},
		{`INSERT INTO card_team (workspace_id, card_id, board_column, pack_id) VALUES ($1, $2, 'reporting', $3)`, []any{ws, mixed, other}},
		{`INSERT INTO team_rating (workspace_id, card_id, board_column, pack_id, user_id, offered, drawn_at) VALUES ($1, $2, 'reporting', $3, $4, false, now())`, []any{ws, mixed, other, u}},
		{`INSERT INTO stage_addon (workspace_id, board_column, updated_by) VALUES ($1, 'reporting', $2)`, []any{ws, u}},
		{`INSERT INTO field_pack (workspace_id, name, version, sha256, manifest, blob_key, source, stage) VALUES ($1, 'R', '1', 'h', '{}', 'k', 'shipped', 'reporting')`, []any{ws}},
		{`INSERT INTO autoresearch (workspace_id, owner_id, title, execution_mode, scope_to) VALUES ($1, $2, 'x', 'run_only', 'reporting')`, []any{ws, u}},
	} {
		_, err := env.Owner.Exec(ctx, c.sql, c.args...)
		wantCode(t, err, "23514", c.sql)
	}

	var def string
	if err := env.Owner.QueryRow(ctx, `SELECT column_default FROM information_schema.columns WHERE table_name = 'autoresearch' AND column_name = 'scope_to'`).Scan(&def); err != nil || def != "'validation'::text" {
		t.Fatalf("autoresearch.scope_to default %q, %v", def, err)
	}
	if n := count(`SELECT count(*) FROM board_candidates() WHERE card_id = $1`, mixed); n != 1 {
		t.Fatal("board_candidates() skips a working Validation study")
	}

	exec(read("0030_one_agent_studies.down.sql"))
	exec(read("0030_one_agent_studies.up.sql"))
}
