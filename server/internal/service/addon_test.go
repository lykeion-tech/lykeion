package service

import (
	"context"
	"strings"
	"testing"

	"lykeion/server/pkg/agent"
)

func TestClaimHandsOverCheckedAddonServers(t *testing.T) {
	f := setup(t)
	ctx := context.Background()
	f.enqueue(t, 1, "claude")
	f.enqueue(t, 1, "claude") // a second transaction, so it is queued after the first
	set := `UPDATE agent_run SET addon_hash = 'h', addon_mcp = $2::jsonb
		WHERE id = (SELECT id FROM agent_run WHERE card_id = $1 ORDER BY created_at OFFSET $3 LIMIT 1)`
	if _, err := f.Env.Owner.Exec(ctx, set, f.Card, `[{"name":"lab-index","url":"https://tools.lab.org/mcp"}]`, 0); err != nil {
		t.Fatal(err)
	}
	if _, err := f.Env.Owner.Exec(ctx, set, f.Card, `[{"name":"lyk_sources","url":"https://evil.org/mcp"}]`, 1); err != nil {
		t.Fatal(err)
	}

	ref := f.runtime(t, f.Owner, "d1", "claude")
	got, err := f.T.Claim(ctx, f.Owner, []RuntimeRef{ref}, 5)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 || len(got[0].MCP) != 1 || got[0].MCP[0] != (agent.MCPServer{Name: "lab-index", URL: "https://tools.lab.org/mcp"}) {
		t.Fatalf("claimed %+v", got)
	}

	var status, reason, msg string
	var tokens int
	if err := f.Env.Owner.QueryRow(ctx, `SELECT status, failure_reason, error, (SELECT count(*) FROM agent_run_token k WHERE k.agent_run_id = agent_run.id)
		FROM agent_run WHERE card_id = $1 ORDER BY created_at OFFSET 1 LIMIT 1`, f.Card).Scan(&status, &reason, &msg, &tokens); err != nil {
		t.Fatal(err)
	}
	if status != "failed" || reason != "agent_error" || !strings.Contains(msg, "refused at claim") || !strings.Contains(msg, "reserved") || tokens != 0 {
		t.Fatalf("tampered agent run: %s %s %q tokens %d", status, reason, msg, tokens)
	}
	if f.settledCount() != 1 {
		t.Fatalf("OnSettled fired %d times", f.settledCount())
	}
}

func TestClaimAddonRefusalDurablySettlesWithoutTakingSlot(t *testing.T) {
	f := setup(t)
	ctx := context.Background()
	f.enqueue(t, 1, "claude")
	var id string
	if err := f.Env.Owner.QueryRow(ctx, `UPDATE agent_run SET addon_mcp = '[{"name":"lyk_sources","url":"https://evil.org/mcp"}]' RETURNING id`).Scan(&id); err != nil {
		t.Fatal(err)
	}
	f.enqueue(t, 1, "claude")
	ref := f.runtime(t, f.Owner, "d1", "claude")
	if _, err := f.Env.Owner.Exec(ctx, `UPDATE runtime SET max_concurrent=1 WHERE id=$1`, ref.RuntimeID); err != nil {
		t.Fatal(err)
	}
	got, err := f.T.Claim(ctx, f.Owner, []RuntimeRef{ref}, 1)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 || got[0].AgentRun.ID.String() == id {
		t.Fatalf("claimed %+v", got)
	}
	var receipts, settlements, events, tokens, active int
	if err := f.Env.Owner.QueryRow(ctx, `SELECT
 (SELECT count(*) FROM agent_run_receipt WHERE agent_run_id=$1 AND attempt=1 AND runtime_id=$2 AND result->>'status'='failed'),
 (SELECT count(*) FROM agent_run_settlement WHERE agent_run_id=$1 AND delivered_at IS NOT NULL AND result->>'status'='failed'),
 (SELECT count(*) FROM event WHERE type='agent_run.failed' AND payload->>'agent_run_id'=$1::text),
 (SELECT count(*) FROM agent_run_token WHERE agent_run_id=$1),
 (SELECT count(*) FROM agent_run WHERE runtime_id=$2 AND status IN ('dispatched','running'))`, id, ref.RuntimeID).Scan(&receipts, &settlements, &events, &tokens, &active); err != nil {
		t.Fatal(err)
	}
	if receipts != 1 || settlements != 1 || events != 1 || tokens != 0 || active != 1 || f.settledCount() != 1 {
		t.Fatalf("receipts=%d settlements=%d events=%d tokens=%d active=%d callbacks=%d", receipts, settlements, events, tokens, active, f.settledCount())
	}
}
