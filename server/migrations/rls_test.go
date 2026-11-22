package migrations_test

import (
	"context"
	"errors"
	"github.com/jackc/pgx/v5/pgconn"
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
	// Keep the count explicit so a missing table cannot make the policy check pass.
	if n != 90 {
		t.Fatalf("found %d tenant tables, want 90", n)
	}
}

func TestAgentCapabilitiesCannotReferenceAnotherWorkspace(t *testing.T) {
	env := testutil.Setup(t)
	ctx := t.Context()
	user, first, second, agent := uuid.New(), uuid.New(), uuid.New(), uuid.New()
	for _, q := range []struct {
		sql  string
		args []any
	}{
		{`INSERT INTO "user"(id,email) VALUES($1,'agent-capability-rls@test')`, []any{user}},
		{`INSERT INTO workspace(id,slug,name) VALUES($1,'agent-cap-a','a'),($2,'agent-cap-b','b')`, []any{first, second}},
		{`INSERT INTO agent(id,workspace_id,name,harness,owner_id) VALUES($1,$2,'agent','claude',$3)`, []any{agent, second, user}},
	} {
		if _, err := env.Owner.Exec(ctx, q.sql, q.args...); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := env.Owner.Exec(ctx, `INSERT INTO agent_capabilities(workspace_id,agent_id) VALUES($1,$2)`, first, agent); err == nil {
		t.Fatal("accepted agent from another workspace")
	}
	if _, err := env.Owner.Exec(ctx, `INSERT INTO agent_capabilities(workspace_id,agent_id) VALUES($1,$2)`, second, agent); err != nil {
		t.Fatalf("own workspace: %v", err)
	}
}

func TestCoresearchConnectorCannotReferenceAnotherWorkspacesRun(t *testing.T) {
	env := testutil.Setup(t)
	ctx := t.Context()
	user, first, second, card, run := uuid.New(), uuid.New(), uuid.New(), uuid.New(), uuid.New()
	queries := []struct {
		sql  string
		args []any
	}{
		{`INSERT INTO "user"(id,email) VALUES($1,'connector-rls@test')`, []any{user}},
		{`INSERT INTO workspace(id,slug,name) VALUES($1,'connector-a','a'),($2,'connector-b','b')`, []any{first, second}},
		{`INSERT INTO card(id,workspace_id,number,title,question,owner_id,board_column) VALUES($1,$2,1,'x','x',$3,'grounding')`, []any{card, second, user}},
		{`INSERT INTO agent_run(id,workspace_id,card_id,responsible_user_id,board_column,round,role,harness,prompt) VALUES($1,$2,$3,$4,'grounding',1,'worker','claude','x')`, []any{run, second, card, user}},
	}
	for _, q := range queries {
		if _, err := env.Owner.Exec(ctx, q.sql, q.args...); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := env.Owner.Exec(ctx, `INSERT INTO coresearch_run_connector(workspace_id,agent_run_id,connector_id) VALUES($1,$2,$3)`, first, run, uuid.New()); err == nil {
		t.Fatal("accepted a connector selection referencing another workspace's run")
	}
	if _, err := env.Owner.Exec(ctx, `INSERT INTO coresearch_run_connector(workspace_id,agent_run_id,connector_id) VALUES($1,$2,$3)`, second, run, uuid.New()); err != nil {
		t.Fatalf("own workspace: %v", err)
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
		if _, err := env.Owner.Exec(ctx, `INSERT INTO card (workspace_id, number, title, question, owner_id, board_column) VALUES ($1, 1, 't', 'q', $2, 'grounding')`, ws, u); err != nil {
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
	if _, err := tx.Exec(ctx, `INSERT INTO card (workspace_id, number, title, question, owner_id, board_column) VALUES ($1, 2, 't', 'q', $2, 'grounding')`, b, u); err == nil {
		t.Fatal("inserted a card into workspace B while scoped to A")
	}
}

func TestAppRoleCannotTouchMigrations(t *testing.T) {
	env := testutil.Setup(t)
	if _, err := env.App.Exec(context.Background(), "SELECT * FROM schema_migrations"); err == nil {
		t.Fatal("the app role can read schema_migrations")
	}
}

func TestCardAssociationsCannotCrossWorkspaces(t *testing.T) {
	env := testutil.Setup(t)
	ctx := context.Background()
	user, a, b, cardA, cardB := uuid.New(), uuid.New(), uuid.New(), uuid.New(), uuid.New()
	if _, err := env.Owner.Exec(ctx, `INSERT INTO "user" (id, email) VALUES ($1, 'association@x')`, user); err != nil {
		t.Fatal(err)
	}
	for i, ws := range []uuid.UUID{a, b} {
		if _, err := env.Owner.Exec(ctx, `INSERT INTO workspace (id, slug, name) VALUES ($1, $2, 'w')`, ws, []string{"a", "b"}[i]); err != nil {
			t.Fatal(err)
		}
		if _, err := env.Owner.Exec(ctx, `INSERT INTO card (id, workspace_id, number, title, question, owner_id, board_column) VALUES ($1, $2, 1, 't', 'q', $3, 'grounding')`, []uuid.UUID{cardA, cardB}[i], ws, user); err != nil {
			t.Fatal(err)
		}
	}
	for _, tc := range []struct{ name, query string }{
		{"comment", `INSERT INTO comment (workspace_id, card_id, author_id, body) VALUES ($1, $2, $3, 'body')`},
		{"inbox_item", `INSERT INTO inbox_item (workspace_id, card_id, user_id, kind, text) VALUES ($1, $2, $3, 'blocked', 'text')`},
		{"event", `INSERT INTO event (workspace_id, card_id, actor_id, type, actor_type) VALUES ($1, $2, $3, 'card.created', 'user')`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			tx, err := env.App.Begin(ctx)
			if err != nil {
				t.Fatal(err)
			}
			defer tx.Rollback(ctx)
			if _, err := tx.Exec(ctx, "SELECT set_config('app.workspace_id', $1, true)", a.String()); err != nil {
				t.Fatal(err)
			}
			if _, err := tx.Exec(ctx, tc.query, a, cardA, user); err != nil {
				t.Fatalf("same-workspace association: %v", err)
			}
			_, err = tx.Exec(ctx, tc.query, a, cardB, user)
			var pgerr *pgconn.PgError
			if !errors.As(err, &pgerr) || pgerr.Code != "23503" {
				t.Fatalf("cross-workspace association must fail foreign key, got %v", err)
			}
		})
	}
}

func TestDeletingCardPreservesEventWithoutCardAssociation(t *testing.T) {
	env := testutil.Setup(t)
	ctx := context.Background()
	user, ws, card := uuid.New(), uuid.New(), uuid.New()
	for _, tc := range []struct {
		query string
		args  []any
	}{
		{`INSERT INTO "user" (id, email) VALUES ($1, 'delete@x')`, []any{user}},
		{`INSERT INTO workspace (id, slug, name) VALUES ($1, 'delete', 'w')`, []any{ws}},
		{`INSERT INTO card (id, workspace_id, number, title, question, owner_id, board_column) VALUES ($1, $2, 1, 't', 'q', $3, 'grounding')`, []any{card, ws, user}},
		{`INSERT INTO event (workspace_id, card_id, type, actor_type) VALUES ($1, $2, 'created', 'system')`, []any{ws, card}},
		{`DELETE FROM card WHERE id = $1`, []any{card}},
	} {
		if _, err := env.Owner.Exec(ctx, tc.query, tc.args...); err != nil {
			t.Fatal(err)
		}
	}
	var n int
	if err := env.Owner.QueryRow(ctx, `SELECT count(*) FROM event WHERE workspace_id = $1 AND card_id IS NULL`, ws).Scan(&n); err != nil {
		t.Fatal(err)
	}
	if n != 1 {
		t.Fatalf("retained unlinked events: %d, want 1", n)
	}
}
