package migrations_test

import (
	"context"
	"fmt"
	"io/fs"
	"strings"
	"testing"
	"testing/fstest"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"lykeion/server/internal/migrate"
	"lykeion/server/internal/testutil"
	"lykeion/server/migrations"
)

func TestUI1FieldsDefaultAndAreChecked(t *testing.T) {
	env := testutil.Setup(t)
	ctx := context.Background()
	u, ws := uuid.New(), uuid.New()
	if _, err := env.Owner.Exec(ctx, `INSERT INTO "user" (id, email) VALUES ($1, 'u@x')`, u); err != nil {
		t.Fatal(err)
	}
	if _, err := env.Owner.Exec(ctx, `INSERT INTO workspace (id, slug, name) VALUES ($1, 's', 'w')`, ws); err != nil {
		t.Fatal(err)
	}
	var desc, rc, prefix, logo, stage string
	var onboarded *string
	if err := env.Owner.QueryRow(ctx, `SELECT description, research_context, card_prefix, logo_blob FROM workspace WHERE id = $1`, ws).
		Scan(&desc, &rc, &prefix, &logo); err != nil {
		t.Fatal(err)
	}
	if err := env.Owner.QueryRow(ctx, `SELECT career_stage, onboarded_at::text FROM "user" WHERE id = $1`, u).Scan(&stage, &onboarded); err != nil {
		t.Fatal(err)
	}
	if desc != "" || rc != "" || prefix != "" || logo != "" || stage != "" || onboarded != nil {
		t.Fatalf("defaults: %q %q %q %q %q %v", desc, rc, prefix, logo, stage, onboarded)
	}
	for _, p := range []string{"", "AB", "ABCDEF"} {
		if _, err := env.Owner.Exec(ctx, `UPDATE workspace SET card_prefix = $1 WHERE id = $2`, p, ws); err != nil {
			t.Fatalf("prefix %q refused: %v", p, err)
		}
	}
	for _, p := range []string{"A", "ab", "ABCDEFG", "AB1", "A-B"} {
		if _, err := env.Owner.Exec(ctx, `UPDATE workspace SET card_prefix = $1 WHERE id = $2`, p, ws); err == nil {
			t.Fatalf("prefix %q accepted", p)
		}
	}
	for _, s := range []string{"", "undergraduate", "masters", "phd", "postdoc", "research_staff", "faculty", "industry", "other"} {
		if _, err := env.Owner.Exec(ctx, `UPDATE "user" SET career_stage = $1 WHERE id = $2`, s, u); err != nil {
			t.Fatalf("stage %q refused: %v", s, err)
		}
	}
	if _, err := env.Owner.Exec(ctx, `UPDATE "user" SET career_stage = 'wizard' WHERE id = $1`, u); err == nil {
		t.Fatal("unknown career stage accepted")
	}
	var card uuid.UUID
	if err := env.Owner.QueryRow(ctx, `INSERT INTO card (workspace_id, number, title, question, owner_id, board_column) VALUES ($1, 1, 't', 'q', $2, 'grounding') RETURNING id`, ws, u).Scan(&card); err != nil {
		t.Fatal(err)
	}
	var resolved *string
	if err := env.Owner.QueryRow(ctx, `INSERT INTO inbox_item (workspace_id, card_id, user_id, kind, text) VALUES ($1, $2, $3, 'escalated', 'x') RETURNING resolved_at::text`, ws, card, u).Scan(&resolved); err != nil || resolved != nil {
		t.Fatalf("inbox item opens unresolved: %v %v", resolved, err)
	}
	var version string
	if err := env.Owner.QueryRow(ctx, `INSERT INTO runtime (workspace_id, owner_id, daemon_id, harness) VALUES ($1, $2, 'd', 'claude') RETURNING cli_version`, ws, u).Scan(&version); err != nil || version != "" {
		t.Fatalf("runtime cli_version: %q %v", version, err)
	}
}

func TestBacklogPositionsAreBackfilledInNumberOrder(t *testing.T) {
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
		if f < "0006" {
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
	u, a, b := uuid.New(), uuid.New(), uuid.New()
	if _, err := conn.Exec(ctx, `INSERT INTO "user" (id, email) VALUES ($1, 'u@x')`, u); err != nil {
		t.Fatal(err)
	}
	if _, err := conn.Exec(ctx, `INSERT INTO workspace (id, slug, name) VALUES ($1, 'a', 'a'), ($2, 'b', 'b')`, a, b); err != nil {
		t.Fatal(err)
	}
	ins := `INSERT INTO card (workspace_id, number, title, question, owner_id, board_column) VALUES ($1, $2, 't', 'q', $3, $4)`
	for _, c := range []struct {
		ws     uuid.UUID
		number int
		column string
	}{{a, 3, "backlog"}, {a, 1, "backlog"}, {a, 2, "grounding"}, {a, 4, "backlog"}, {b, 1, "backlog"}} {
		if _, err := conn.Exec(ctx, ins, c.ws, c.number, u, c.column); err != nil {
			t.Fatal(err)
		}
	}
	// Later migrations delete studies, so stop at the one under test.
	upToUI1 := fstest.MapFS{}
	for name, f := range before {
		upToUI1[name] = f
	}
	for _, f := range files {
		if f < "0007" {
			b, err := fs.ReadFile(migrations.FS, f)
			if err != nil {
				t.Fatal(err)
			}
			upToUI1[f] = &fstest.MapFile{Data: b}
		}
	}
	if err := migrate.Up(ctx, conn, upToUI1); err != nil {
		t.Fatal(err)
	}
	rows, err := conn.Query(ctx, `SELECT w.slug, c.number, c.position FROM card c JOIN workspace w ON w.id = c.workspace_id ORDER BY w.slug, c.number`)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	var got []string
	for rows.Next() {
		var slug string
		var number int
		var position float64
		if err := rows.Scan(&slug, &number, &position); err != nil {
			t.Fatal(err)
		}
		got = append(got, fmt.Sprintf("%s:%d=%g", slug, number, position))
	}
	want := "a:1=1 a:2=0 a:3=2 a:4=3 b:1=1"
	if strings.Join(got, " ") != want {
		t.Fatalf("positions %v, want %s", got, want)
	}
}
