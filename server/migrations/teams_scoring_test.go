package migrations_test

import (
	"context"
	"errors"
	"io/fs"
	"strings"
	"testing"
	"testing/fstest"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"

	"lykeion/server/internal/migrate"
	"lykeion/server/internal/testutil"
	"lykeion/server/migrations"
)

func wantCode(t *testing.T, err error, code, what string) {
	t.Helper()
	var pg *pgconn.PgError
	if !errors.As(err, &pg) || pg.Code != code {
		t.Errorf("%s: got %v, want SQLSTATE %s", what, err, code)
	}
}

func TestMigrationDeletesStudiesAndPacks(t *testing.T) {
	env := testutil.Setup(t)
	ctx := context.Background()
	name := "m_" + strings.ReplaceAll(uuid.NewString(), "-", "")
	admin, err := pgx.Connect(ctx, env.OwnerURL)
	if err != nil {
		t.Fatal(err)
	}
	defer admin.Close(ctx)
	if _, err := admin.Exec(ctx, "CREATE DATABASE "+name); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		c, err := pgx.Connect(context.Background(), env.OwnerURL)
		if err != nil {
			t.Errorf("cleanup database: %v", err)
			return
		}
		defer c.Close(context.Background())
		if _, err := c.Exec(context.Background(), "DROP DATABASE IF EXISTS "+name+" WITH (FORCE)"); err != nil {
			t.Errorf("cleanup database: %v", err)
		}
	})
	conn, err := pgx.Connect(ctx, testutil.WithDB(env.OwnerURL, name))
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close(ctx)

	before := fstest.MapFS{}
	files, err := fs.Glob(migrations.FS, "*.up.sql")
	if err != nil {
		t.Fatal(err)
	}
	for _, f := range files {
		if f < "0012" {
			b, err := fs.ReadFile(migrations.FS, f)
			if err != nil {
				t.Fatal(err)
			}
			before[f] = &fstest.MapFile{Data: b}
		}
	}
	if err := migrate.Up(ctx, conn, before); err != nil {
		t.Fatal(err)
	}
	u, ws, card := uuid.New(), uuid.New(), uuid.New()
	for _, q := range []struct {
		sql  string
		args []any
	}{
		{`INSERT INTO "user" (id, email) VALUES ($1, 'u@x')`, []any{u}},
		{`INSERT INTO workspace (id, slug, name) VALUES ($1, 's', 'w')`, []any{ws}},
		{`INSERT INTO card (id, workspace_id, number, title, question, owner_id) VALUES ($1, $2, 1, 't', 'q', $3)`, []any{card, ws, u}},
		{`INSERT INTO comment (workspace_id, card_id, author_id, body) VALUES ($1, $2, $3, 'c')`, []any{ws, card, u}},
		{`INSERT INTO record (workspace_id, card_id, board_column, round, kind, role) VALUES ($1, $2, 'grounding', 0, 'note', 'r')`, []any{ws, card}},
		{`INSERT INTO inbox_item (workspace_id, user_id, card_id, kind, text) VALUES ($1, $2, $3, 'escalated', 't')`, []any{ws, u, card}},
		{`INSERT INTO inbox_item (workspace_id, user_id, kind, text) VALUES ($1, $2, 'out_of_budget', 't')`, []any{ws, u}},
		{`INSERT INTO field_pack (workspace_id, name, version, sha256, manifest, blob_key, source) VALUES ($1, 'p', '1', 'h', '{}', 'k', 'shipped')`, []any{ws}},
	} {
		if _, err := conn.Exec(ctx, q.sql, q.args...); err != nil {
			t.Fatalf("seed %s: %v", q.sql, err)
		}
	}
	if err := migrate.Up(ctx, conn, migrations.FS); err != nil {
		t.Fatal(err)
	}
	for _, table := range []string{"card", "record", "comment", "inbox_item", "field_pack"} {
		var n int
		if err := conn.QueryRow(ctx, `SELECT count(*) FROM `+table).Scan(&n); err != nil {
			t.Fatal(err)
		}
		if n != 0 {
			t.Errorf("%s kept %d rows", table, n)
		}
	}
}

func TestSingleTeamStudyHasOneColumnScope(t *testing.T) {
	env := testutil.Setup(t)
	_, ws, card := seedWorkspace(t, env)
	_, err := env.Owner.Exec(context.Background(), `UPDATE card SET scope_from = 'hypotheses', scope_to = 'grounding' WHERE id = $1`, card)
	wantCode(t, err, "23514", "scope_from after scope_to")
	_ = ws
}

func TestFollowsFromOnlyOnEndToEnd(t *testing.T) {
	env := testutil.Setup(t)
	_, ws, card := seedWorkspace(t, env)
	ctx := context.Background()
	_, err := env.Owner.Exec(ctx, `UPDATE card SET scope_to = 'grounding', follows_from = ARRAY[$2::uuid] WHERE id = $1`, card, uuid.New())
	wantCode(t, err, "23514", "follows_from on a one-column study")
	if _, err := env.Owner.Exec(ctx, `UPDATE card SET scope_to = 'reporting', follows_from = ARRAY[$2::uuid] WHERE id = $1`, card, uuid.New()); err != nil {
		t.Errorf("follows_from on an end-to-end study: %v", err)
	}
	_ = ws
}

func TestBlockKindIsOnlyLowScore(t *testing.T) {
	env := testutil.Setup(t)
	_, _, card := seedWorkspace(t, env)
	ctx := context.Background()
	_, err := env.Owner.Exec(ctx, `UPDATE card SET block_kind = 'cap' WHERE id = $1`, card)
	wantCode(t, err, "23514", "block_kind cap")
	if _, err := env.Owner.Exec(ctx, `UPDATE card SET block_kind = 'low_score' WHERE id = $1`, card); err != nil {
		t.Errorf("block_kind low_score: %v", err)
	}
}

func TestTeamAssigneeIsCheckedAsAnAgent(t *testing.T) {
	env := testutil.Setup(t)
	_, ws, card := seedWorkspace(t, env)
	ctx := context.Background()
	if _, err := env.Owner.Exec(ctx, `UPDATE card SET assignee_type = 'team', assignee_id = NULL WHERE id = $1`, card); err != nil {
		t.Errorf("a team assignee with no agent: %v", err)
	}
	_, err := env.Owner.Exec(ctx, `UPDATE card SET assignee_type = 'team', assignee_id = $2 WHERE id = $1`, card, uuid.New())
	wantCode(t, err, "23503", "a team assignee that is not an agent")
	_, err = env.Owner.Exec(ctx, `UPDATE card SET assignee_type = 'agent', assignee_id = NULL WHERE id = $1`, card)
	wantCode(t, err, "23514", "an agent assignee with no id")
	_ = ws
}

func TestCardTeamIsTenantScoped(t *testing.T) {
	env := testutil.Setup(t)
	ctx := context.Background()
	u, ws, card := seedWorkspace(t, env)
	var pack, agent uuid.UUID
	if err := env.Owner.QueryRow(ctx, `INSERT INTO field_pack (workspace_id, name, version, sha256, manifest, blob_key, source)
		VALUES ($1, 'p', '1', 'h', '{}', 'k', 'shipped') RETURNING id`, ws).Scan(&pack); err != nil {
		t.Fatal(err)
	}
	if err := env.Owner.QueryRow(ctx, `INSERT INTO agent (workspace_id, name, harness, owner_id) VALUES ($1, 'a', 'claude', $2) RETURNING id`, ws, u).Scan(&agent); err != nil {
		t.Fatal(err)
	}
	if _, err := env.Owner.Exec(ctx, `INSERT INTO card_team (workspace_id, card_id, board_column, pack_id) VALUES ($1, $2, 'grounding', $3)`, ws, card, pack); err != nil {
		t.Fatal(err)
	}
	if _, err := env.Owner.Exec(ctx, `INSERT INTO team_agent (workspace_id, pack_id, user_id, member, agent_id) VALUES ($1, $2, $3, 'pi', $4)`, ws, pack, u, agent); err != nil {
		t.Fatal(err)
	}
	other := uuid.New()
	if _, err := env.Owner.Exec(ctx, `INSERT INTO workspace (id, slug, name) VALUES ($1, 'o', 'o')`, other); err != nil {
		t.Fatal(err)
	}
	if _, err := env.Owner.Exec(ctx, `INSERT INTO card_team (workspace_id, card_id, board_column, pack_id) VALUES ($1, $2, 'hypotheses', $3)`, other, card, pack); err == nil {
		t.Error("card_team linked another workspace's card and pack")
	}
	if _, err := env.Owner.Exec(ctx, `INSERT INTO team_agent (workspace_id, pack_id, user_id, member, agent_id) VALUES ($1, $2, $3, 'x', $4)`, other, pack, u, agent); err == nil {
		t.Error("team_agent linked another workspace's pack and agent")
	}

	tx, err := env.App.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback(ctx)
	if _, err := tx.Exec(ctx, "SELECT set_config('app.workspace_id', $1, true)", other.String()); err != nil {
		t.Fatal(err)
	}
	for _, table := range []string{"card_team", "team_agent"} {
		var n int
		if err := tx.QueryRow(ctx, `SELECT count(*) FROM `+table).Scan(&n); err != nil {
			t.Fatal(err)
		}
		if n != 0 {
			t.Errorf("another workspace saw %d %s rows", n, table)
		}
	}
	if _, err := tx.Exec(ctx, "SELECT set_config('app.workspace_id', $1, true)", ws.String()); err != nil {
		t.Fatal(err)
	}
	for _, table := range []string{"card_team", "team_agent"} {
		var n int
		if err := tx.QueryRow(ctx, `SELECT count(*) FROM `+table).Scan(&n); err != nil {
			t.Fatal(err)
		}
		if n != 1 {
			t.Errorf("the owning workspace saw %d %s rows, want 1", n, table)
		}
	}
}

func TestPackNameIsGone(t *testing.T) {
	env := testutil.Setup(t)
	_, err := env.Owner.Exec(context.Background(), `SELECT pack_name FROM "user"`)
	wantCode(t, err, "42703", "pack_name")
}

func TestScienceRowsRefuseMalformedValues(t *testing.T) {
	env := testutil.Setup(t)
	ctx := context.Background()
	u, ws, _ := seedWorkspace(t, env)
	for _, c := range []struct {
		name string
		sql  string
		args []any
	}{
		{"a connector name that is not a slug", `INSERT INTO workspace_connector (workspace_id, name, updated_by) VALUES ($1, '../x', $2)`, []any{ws, u}},
		{"a key ciphertext too short to be sealed", `INSERT INTO workspace_connector (workspace_id, name, key_ciphertext, updated_by) VALUES ($1, 'omim', '\x00', $2)`, []any{ws, u}},
		{"a licence confirmed by nobody", `INSERT INTO workspace_connector (workspace_id, name, licence_confirmed_at, updated_by) VALUES ($1, 'omim', now(), $2)`, []any{ws, u}},
		{"a request hash that is not sha256", `INSERT INTO connector_fetch (workspace_id, request_sha256, connector, blob_key, truncated) VALUES ($1, 'abc', 'ensembl', 'k', false)`, []any{ws}},
	} {
		if _, err := env.Owner.Exec(ctx, c.sql, c.args...); err == nil {
			t.Errorf("%s was stored", c.name)
		}
	}
	if _, err := env.Owner.Exec(ctx, `INSERT INTO workspace_connector (workspace_id, name, enabled, updated_by) VALUES ($1, '13c-like-name', false, $2)`, ws, u); err != nil {
		t.Fatalf("a valid row: %v", err)
	}
}
