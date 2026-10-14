package migrations_test

import (
	"context"
	"testing"

	"github.com/google/uuid"

	"lykeion/server/internal/testutil"
)

func TestBoardCandidates(t *testing.T) {
	env := testutil.Setup(t)
	ctx := context.Background()
	u, ws := uuid.New(), uuid.New()
	if _, err := env.Owner.Exec(ctx, `INSERT INTO "user" (id, email) VALUES ($1, 'u@x')`, u); err != nil { t.Fatal(err) }
	if _, err := env.Owner.Exec(ctx, `INSERT INTO workspace (id, slug, name) VALUES ($1, 's', 'w')`, ws); err != nil { t.Fatal(err) }
	ins := `INSERT INTO card (workspace_id, number, title, question, owner_id, board_column, round, flag)
		VALUES ($1, $2, 't', 'q', $3, $4, 1, $5) RETURNING id`
	var backlog, idle, busy, flagged uuid.UUID
	for _, row := range []struct{ number int; column, flag string; id *uuid.UUID }{
		{1, "backlog", "", &backlog}, {2, "grounding", "", &idle}, {3, "grounding", "", &busy}, {4, "grounding", "escalated", &flagged},
	} {
		if err := env.Owner.QueryRow(ctx, ins, ws, row.number, u, row.column, row.flag).Scan(row.id); err != nil { t.Fatal(err) }
	}
	if _, err := env.Owner.Exec(ctx, `INSERT INTO agent_run (workspace_id, card_id, responsible_user_id, board_column, round, role, harness, prompt)
		VALUES ($1, $2, $3, 'grounding', 1, 'scoper', 'claude', 'p')`, ws, busy, u); err != nil { t.Fatal(err) }

	rows, err := env.App.Query(ctx, "SELECT card_id FROM board_candidates()")
	if err != nil { t.Fatal(err) }
	defer rows.Close()
	got := map[uuid.UUID]bool{}
	for rows.Next() {
		var id uuid.UUID
		if err := rows.Scan(&id); err != nil { t.Fatal(err) }
		got[id] = true
	}
	if err := rows.Err(); err != nil { t.Fatal(err) }
	if !got[backlog] || !got[idle] || got[busy] || got[flagged] || len(got) != 2 { t.Fatalf("%v", got) }
}

func TestMarkQueueOffline(t *testing.T) {
	env := testutil.Setup(t)
	ctx := context.Background()
	u, ws := uuid.New(), uuid.New()
	if _, err := env.Owner.Exec(ctx, `INSERT INTO "user" (id, email) VALUES ($1, 'u@x')`, u); err != nil { t.Fatal(err) }
	if _, err := env.Owner.Exec(ctx, `INSERT INTO workspace (id, slug, name) VALUES ($1, 's', 'w')`, ws); err != nil { t.Fatal(err) }
	var card, waiting, broke uuid.UUID
	if err := env.Owner.QueryRow(ctx, `INSERT INTO card (workspace_id, number, title, question, owner_id, board_column, round)
		VALUES ($1, 1, 't', 'q', $2, 'grounding', 1) RETURNING id`, ws, u).Scan(&card); err != nil { t.Fatal(err) }
	ins := `INSERT INTO agent_run (workspace_id, card_id, responsible_user_id, board_column, round, role, harness, prompt, queue_reason)
		VALUES ($1, $2, $3, 'grounding', 1, 'scoper', 'claude', 'p', $4) RETURNING id`
	if err := env.Owner.QueryRow(ctx, ins, ws, card, u, nil).Scan(&waiting); err != nil { t.Fatal(err) }
	if err := env.Owner.QueryRow(ctx, ins, ws, card, u, "budget").Scan(&broke); err != nil { t.Fatal(err) }
	reason := func(id uuid.UUID) string {
		var r *string
		if err := env.Owner.QueryRow(ctx, "SELECT queue_reason FROM agent_run WHERE id = $1", id).Scan(&r); err != nil { t.Fatal(err) }
		if r == nil { return "" }
		return *r
	}
	mark := func() {
		if _, err := env.App.Exec(ctx, "SELECT mark_queue_offline()"); err != nil { t.Fatal(err) }
	}
	mark()
	if reason(waiting) != "runtime_offline" || reason(broke) != "budget" { t.Fatalf("offline: %q %q", reason(waiting), reason(broke)) }
	if _, err := env.Owner.Exec(ctx, `INSERT INTO runtime (workspace_id, owner_id, daemon_id, harness) VALUES ($1, $2, 'd', 'claude')`, ws, u); err != nil { t.Fatal(err) }
	mark()
	if reason(waiting) != "" || reason(broke) != "budget" { t.Fatalf("online: %q %q", reason(waiting), reason(broke)) }
}
