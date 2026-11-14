package migrations_test

import (
	"context"
	"testing"

	"github.com/google/uuid"

	"lykeion/server/internal/testutil"
)

func TestStageAddonColumnsAndAgentRunDefaults(t *testing.T) {
	env := testutil.Setup(t)
	ctx := context.Background()
	u, ws := uuid.New(), uuid.New()
	if _, err := env.Owner.Exec(ctx, `INSERT INTO "user" (id, email) VALUES ($1, 'u@x')`, u); err != nil {
		t.Fatal(err)
	}
	if _, err := env.Owner.Exec(ctx, `INSERT INTO workspace (id, slug, name) VALUES ($1, 's', 'w')`, ws); err != nil {
		t.Fatal(err)
	}
	ins := `INSERT INTO stage_addon (workspace_id, board_column, updated_by) VALUES ($1, $2, $3)`
	if _, err := env.Owner.Exec(ctx, ins, ws, "grounding", u); err != nil {
		t.Fatal(err)
	}
	for _, col := range []string{"backlog", "done", "nope"} {
		if _, err := env.Owner.Exec(ctx, ins, ws, col, u); err == nil {
			t.Fatalf("an add-on for %s was stored", col)
		}
	}
	if _, err := env.Owner.Exec(ctx, `UPDATE stage_addon SET mcp = '{}' WHERE workspace_id = $1`, ws); err == nil {
		t.Fatal("an mcp value that is not an array was stored")
	}

	var card uuid.UUID
	if err := env.Owner.QueryRow(ctx, `INSERT INTO card (workspace_id, number, title, question, owner_id, board_column, round)
		VALUES ($1, 1, 't', 'q', $2, 'grounding', 1) RETURNING id`, ws, u).Scan(&card); err != nil {
		t.Fatal(err)
	}
	var hash, mcp string
	err := env.Owner.QueryRow(ctx, `INSERT INTO agent_run (workspace_id, card_id, responsible_user_id, board_column, round, role, harness, prompt)
		VALUES ($1, $2, $3, 'grounding', 1, 'scoper', 'claude', 'p') RETURNING addon_hash, addon_mcp::text`, ws, card, u).Scan(&hash, &mcp)
	if err != nil || hash != "" || mcp != "[]" {
		t.Fatalf("agent run defaults: %q %q %v", hash, mcp, err)
	}
}
