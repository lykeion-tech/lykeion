package migrations_test

import (
	"io/fs"
	"testing"

	"github.com/google/uuid"

	"lykeion/server/internal/testutil"
	"lykeion/server/migrations"
)

func TestJevStageReviewMigration(t *testing.T) {
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
	count := func(sql string, args ...any) int {
		t.Helper()
		var n int
		if err := env.Owner.QueryRow(ctx, sql, args...).Scan(&n); err != nil {
			t.Fatalf("%s: %v", sql, err)
		}
		return n
	}
	exec(read("0040_jev_stage_review.down.sql"))

	u, ws, working := seedWorkspace(t, env)
	exec(`UPDATE card SET block_kind = 'unconfirmed_results', state = 'blocked' WHERE id = $1`, working)
	var done uuid.UUID
	if err := env.Owner.QueryRow(ctx, `INSERT INTO card (workspace_id, number, title, question, owner_id, board_column, state)
		VALUES ($1, 2, 't', 'q', $2, 'experimentation', 'done') RETURNING id`, ws, u).Scan(&done); err != nil {
		t.Fatal(err)
	}
	exec(`INSERT INTO record (workspace_id, card_id, board_column, round, kind, role) VALUES ($1, $2, 'grounding', 1, 'preregistration', 'r')`, ws, working)
	exec(`INSERT INTO record (workspace_id, card_id, board_column, round, kind, role) VALUES ($1, $2, 'grounding', 1, 'note', 'r')`, ws, working)
	exec(`INSERT INTO inbox_item (workspace_id, user_id, kind, card_id, text) VALUES ($1, $2, 'preregistered', $3, 'p')`, ws, u, working)

	exec(read("0040_jev_stage_review.up.sql"))

	var legacy, block string
	if err := env.Owner.QueryRow(ctx, `SELECT legacy_column, block_kind FROM card WHERE id = $1`, working).Scan(&legacy, &block); err != nil {
		t.Fatal(err)
	}
	if legacy != "grounding" || block != "low_score" {
		t.Fatalf("the study blocked in Grounding: legacy_column %q, block_kind %q", legacy, block)
	}
	if n := count(`SELECT count(*) FROM card WHERE id = $1 AND legacy_column = ''`, done); n != 1 {
		t.Fatal("a done study got a legacy column")
	}
	if n := count(`SELECT count(*) FROM record WHERE kind = 'preregistration'`); n != 0 {
		t.Fatalf("%d preregistration records left", n)
	}
	if n := count(`SELECT count(*) FROM record WHERE kind = 'note'`); n != 1 {
		t.Fatal("another record went with the preregistration")
	}
	if n := count(`SELECT count(*) FROM inbox_item WHERE kind = 'preregistered'`); n != 0 {
		t.Fatalf("%d preregistered inbox items left", n)
	}

	var fresh uuid.UUID
	if err := env.Owner.QueryRow(ctx, `INSERT INTO card (workspace_id, number, title, question, owner_id, board_column)
		VALUES ($1, 3, 't', 'q', $2, 'grounding') RETURNING id`, ws, u).Scan(&fresh); err != nil {
		t.Fatal(err)
	}
	if n := count(`SELECT count(*) FROM card WHERE id = $1 AND legacy_column = '' AND grounding_criteria = '[]'`, fresh); n != 1 {
		t.Fatal("a new study does not default to no legacy column and no criteria")
	}
	exec(`UPDATE card SET grounding_criteria = '["the effect is measured"]' WHERE id = $1`, fresh)

	for _, c := range []struct {
		sql  string
		args []any
	}{
		{`INSERT INTO card (workspace_id, number, title, question, owner_id, board_column, grounding_criteria) VALUES ($1, 4, 't', 'q', $2, 'grounding', '{"a":1}')`, []any{ws, u}},
		{`UPDATE card SET grounding_criteria = '{"a":1}' WHERE id = $1`, []any{fresh}},
		{`UPDATE card SET grounding_criteria = jsonb_build_array(repeat('x', 5000)) WHERE id = $1`, []any{fresh}},
		{`UPDATE card SET block_kind = 'unconfirmed_results' WHERE id = $1`, []any{fresh}},
		{`INSERT INTO inbox_item (workspace_id, user_id, kind, card_id, text) VALUES ($1, $2, 'preregistered', $3, 'p')`, []any{ws, u, fresh}},
	} {
		_, err := env.Owner.Exec(ctx, c.sql, c.args...)
		wantCode(t, err, "23514", c.sql)
	}
	if _, err := env.Owner.Exec(ctx, `SELECT predicates FROM card`); err == nil {
		t.Fatal("card.predicates is still there")
	}

	exec(read("0040_jev_stage_review.down.sql"))
	if n := count(`SELECT count(*) FROM card WHERE predicates = '[]'`); n != 3 {
		t.Fatal("0040 down did not restore predicates")
	}
	exec(read("0040_jev_stage_review.up.sql"))
}
