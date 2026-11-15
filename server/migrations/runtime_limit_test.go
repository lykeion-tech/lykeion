package migrations_test

import (
	"io/fs"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"lykeion/server/internal/testutil"
	"lykeion/server/migrations"
)

// Upgrading lifts the old default limit of 2 and keeps any limit someone set;
// rolling back gives every unlimited runtime the old default again.
func TestRuntimeLimitOptionalUpgradeAndRollback(t *testing.T) {
	env := testutil.Setup(t)
	ctx := t.Context()
	conn, err := pgx.Connect(ctx, env.OwnerURL)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close(ctx)
	read := func(name string) string {
		t.Helper()
		b, err := fs.ReadFile(migrations.FS, name)
		if err != nil {
			t.Fatal(err)
		}
		return string(b)
	}
	exec := func(sql string, args ...any) {
		t.Helper()
		if _, err := conn.Exec(ctx, sql, args...); err != nil {
			t.Fatal(err)
		}
	}
	limit := func(daemon string) *int32 {
		t.Helper()
		var n *int32
		if err := conn.QueryRow(ctx, `SELECT max_concurrent FROM runtime WHERE daemon_id = $1`, daemon).Scan(&n); err != nil {
			t.Fatal(err)
		}
		return n
	}

	exec(read("0074_runtime_limit_optional.down.sql"))
	exec(`DELETE FROM schema_migrations WHERE version = 74`)
	u, ws := uuid.New(), uuid.New()
	exec(`INSERT INTO "user" (id, email) VALUES ($1, 'u@x')`, u)
	exec(`INSERT INTO workspace (id, slug, name) VALUES ($1, 's', 'w')`, ws)
	exec(`INSERT INTO runtime (workspace_id, owner_id, daemon_id, harness) VALUES ($1, $2, 'old-default', 'claude')`, ws, u)
	exec(`INSERT INTO runtime (workspace_id, owner_id, daemon_id, harness, max_concurrent) VALUES ($1, $2, 'chosen', 'claude', 5)`, ws, u)

	exec(read("0074_runtime_limit_optional.up.sql"))
	if n := limit("old-default"); n != nil {
		t.Fatalf("the old default stayed: %d", *n)
	}
	if n := limit("chosen"); n == nil || *n != 5 {
		t.Fatalf("a chosen limit was lost: %v", n)
	}
	exec(`INSERT INTO runtime (workspace_id, owner_id, daemon_id, harness) VALUES ($1, $2, 'new', 'claude')`, ws, u)
	if n := limit("new"); n != nil {
		t.Fatalf("a new runtime has a limit: %d", *n)
	}

	exec(read("0074_runtime_limit_optional.down.sql"))
	for daemon, want := range map[string]int32{"old-default": 2, "new": 2, "chosen": 5} {
		if n := limit(daemon); n == nil || *n != want {
			t.Fatalf("after rollback %s has %v, want %d", daemon, n, want)
		}
	}
}
