package migrations_test

import (
	"context"
	"testing"

	"github.com/google/uuid"

	"lykeion/server/internal/testutil"
)

func TestEveryTenantTableHasAPolicy(t *testing.T) {
	env := testutil.Setup(t)
	rows, err := env.Owner.Query(context.Background(), `
		SELECT c.relname, c.relrowsecurity, EXISTS (SELECT 1 FROM pg_policy p WHERE p.polrelid = c.oid)
		FROM pg_class c JOIN pg_namespace n ON n.oid = c.relnamespace
		WHERE n.nspname = 'public' AND c.relkind = 'r'
		  AND (c.relname = 'workspace' OR EXISTS (
		    SELECT 1 FROM information_schema.columns col
		    WHERE col.table_schema = 'public' AND col.table_name = c.relname AND col.column_name = 'workspace_id'))`)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	n := 0
	for rows.Next() {
		var name string
		var rls, policy bool
		if err := rows.Scan(&name, &rls, &policy); err != nil {
			t.Fatal(err)
		}
		if !rls || !policy {
			t.Errorf("%s: row-level security %v, policy %v", name, rls, policy)
		}
		n++
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	if n < 7 {
		t.Fatalf("found %d tenant tables, want at least 7", n)
	}
}

func TestDatabaseRefusesCrossWorkspaceRows(t *testing.T) {
	env := testutil.Setup(t)
	ctx := context.Background()
	u, a, b := uuid.New(), uuid.New(), uuid.New()
	for _, q := range []string{
		`INSERT INTO "user" (id, email) VALUES ($1, 'u@x')`,
	} {
		if _, err := env.Owner.Exec(ctx, q, u); err != nil {
			t.Fatal(err)
		}
	}
	for i, ws := range []uuid.UUID{a, b} {
		if _, err := env.Owner.Exec(ctx, `INSERT INTO workspace (id, slug, name) VALUES ($1, $2, 'w')`, ws, []string{"a", "b"}[i]); err != nil {
			t.Fatal(err)
		}
		if _, err := env.Owner.Exec(ctx, `INSERT INTO card (workspace_id, number, title, question, owner_id) VALUES ($1, 1, 't', 'q', $2)`, ws, u); err != nil {
			t.Fatal(err)
		}
	}

	var n int
	if err := env.App.QueryRow(ctx, "SELECT count(*) FROM card").Scan(&n); err != nil {
		t.Fatal(err)
	}
	if n != 0 {
		t.Fatalf("no workspace set: saw %d cards", n)
	}

	tx, err := env.App.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback(ctx)
	if _, err := tx.Exec(ctx, "SELECT set_config('app.workspace_id', $1, true)", a.String()); err != nil {
		t.Fatal(err)
	}
	if err := tx.QueryRow(ctx, "SELECT count(*) FROM card").Scan(&n); err != nil {
		t.Fatal(err)
	}
	if n != 1 {
		t.Fatalf("workspace A sees %d cards, want 1", n)
	}
	if _, err := tx.Exec(ctx, `INSERT INTO card (workspace_id, number, title, question, owner_id) VALUES ($1, 2, 't', 'q', $2)`, b, u); err == nil {
		t.Fatal("inserted a card into workspace B while scoped to A")
	}
}

func TestAppRoleCannotTouchMigrations(t *testing.T) {
	env := testutil.Setup(t)
	if _, err := env.App.Exec(context.Background(), "SELECT * FROM schema_migrations"); err == nil {
		t.Fatal("the app role can read schema_migrations")
	}
}
