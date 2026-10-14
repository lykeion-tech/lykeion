package migrations_test

import (
	"context"
	"testing"

	"github.com/google/uuid"

	"lykeion/server/internal/testutil"
)

func TestSweepFunctionsRunAsTheApp(t *testing.T) {
	env := testutil.Setup(t)
	ctx := context.Background()
	u, ws := uuid.New(), uuid.New()
	if _, err := env.Owner.Exec(ctx, `INSERT INTO "user" (id, email) VALUES ($1, 'u@x')`, u); err != nil {
		t.Fatal(err)
	}
	if _, err := env.Owner.Exec(ctx, `INSERT INTO workspace (id, slug, name) VALUES ($1, 's', 'w')`, ws); err != nil {
		t.Fatal(err)
	}
	if _, err := env.Owner.Exec(ctx, `INSERT INTO runtime (workspace_id, owner_id, daemon_id, harness, last_seen_at)
		VALUES ($1, $2, 'd', 'claude', now() - interval '5 minutes')`, ws, u); err != nil {
		t.Fatal(err)
	}
	var n int
	if err := env.App.QueryRow(ctx, "SELECT sweep_runtimes('60 seconds')").Scan(&n); err != nil || n != 1 {
		t.Fatalf("sweep_runtimes as app: %d %v", n, err)
	}
	if _, err := env.App.Exec(ctx, "SELECT * FROM sweep_agent_runs()"); err != nil {
		t.Fatal(err)
	}
	var rows int
	if err := env.App.QueryRow(ctx, "SELECT count(*) FROM agent_run_token_lookup('nope')").Scan(&rows); err != nil {
		t.Fatal(err)
	}
	if rows != 0 {
		t.Fatal("lookup of an unknown token returned rows")
	}
}
