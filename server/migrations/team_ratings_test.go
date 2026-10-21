package migrations_test

import (
	"io/fs"
	"testing"

	"github.com/google/uuid"

	"lykeion/server/internal/testutil"
	"lykeion/server/migrations"
)

func TestTeamRatingsMigration(t *testing.T) {
	env := testutil.Setup(t)
	ctx := t.Context()
	u, ws, card := seedWorkspace(t, env)
	var pack uuid.UUID
	if err := env.Owner.QueryRow(ctx, `INSERT INTO field_pack (workspace_id, name, version, sha256, manifest, blob_key, source)
		VALUES ($1, 'Team', '1', 'h', '{}', 'k', 'shipped') RETURNING id`, ws).Scan(&pack); err != nil {
		t.Fatal(err)
	}
	ins := `INSERT INTO team_rating (workspace_id, card_id, board_column, pack_id, user_id, offered, drawn_at, rating)
		VALUES ($1, $2, $3, $4, $5, $6, now(), $7)`
	for name, args := range map[string][]any{
		"rating 5":              {ws, card, "grounding", pack, u, true, 5},
		"rating 0":              {ws, card, "grounding", pack, u, true, 0},
		"rated but not offered": {ws, card, "grounding", pack, u, false, 2},
	} {
		if _, err := env.Owner.Exec(ctx, ins, args...); err == nil {
			t.Fatalf("%s was accepted", name)
		}
	}
	if _, err := env.Owner.Exec(ctx, ins, ws, card, "grounding", pack, u, true, 3); err != nil {
		t.Fatal(err)
	}
	if _, err := env.Owner.Exec(ctx, ins, ws, card, "grounding", pack, u, false, nil); err == nil {
		t.Fatal("a second draw for the same column was accepted")
	}
	if _, err := env.Owner.Exec(ctx, `DELETE FROM card WHERE id = $1`, card); err != nil {
		t.Fatal(err)
	}
	var kept, unlinked bool
	if err := env.Owner.QueryRow(ctx, `SELECT count(*) = 1, bool_and(card_id IS NULL AND workspace_id = $1) FROM team_rating`, ws).Scan(&kept, &unlinked); err != nil {
		t.Fatal(err)
	}
	if !kept || !unlinked {
		t.Fatalf("after deleting the study: kept %v, unlinked %v", kept, unlinked)
	}

	table := `SELECT to_regclass('public.team_rating') IS NOT NULL`
	var exists bool
	down, err := fs.ReadFile(migrations.FS, "0019_team_ratings.down.sql")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := env.Owner.Exec(ctx, string(down)); err != nil {
		t.Fatal(err)
	}
	if err := env.Owner.QueryRow(ctx, table).Scan(&exists); err != nil || exists {
		t.Fatalf("0019 down left team_rating (%v)", err)
	}
	up, err := fs.ReadFile(migrations.FS, "0019_team_ratings.up.sql")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := env.Owner.Exec(ctx, string(up)); err != nil {
		t.Fatal(err)
	}
	if err := env.Owner.QueryRow(ctx, table).Scan(&exists); err != nil || !exists {
		t.Fatalf("reapplying 0019 did not restore team_rating (%v)", err)
	}
}
