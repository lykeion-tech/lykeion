package e2e

import (
	"context"
	"testing"

	"lykeion/server/internal/store"
	"lykeion/server/pkg/agent"
	db "lykeion/server/pkg/db/generated"
)

func (l *boardLab) defaultHarness(t *testing.T, harness string) {
	ctx := context.Background()
	if err := l.st.InWorkspace(ctx, l.ws, l.owner, func(tx *store.Tx) error {
		return tx.Q.SetWorkspaceSettings(ctx, db.SetWorkspaceSettingsParams{ID: l.ws,
			Settings: []byte(`{"jev":{"enabled":true},"harness":{"default":"` + harness + `"}}`)})
	}); err != nil {
		t.Fatal(err)
	}
}

// Every Grounding role of a card runs on cursor, in private mode although the
// workspace is in continuity mode; the card passes its gate, and each agent
// run's usage is priced from the table.
func TestGroundingRunsOnCursor(t *testing.T) {
	l := newBoardLab(t)
	l.defaultHarness(t, "cursor")
	card := l.startCard(t)
	defer l.runOn(t, "cursor", "testdata/fake-cursor.sh")()
	got := l.waitCard(t, card, "Hypotheses", func(c db.Card) bool { return c.BoardColumn == "hypotheses" })
	ctx := context.Background()
	var others, runs, priced int
	must(t, l.env.Owner.QueryRow(ctx, `SELECT count(*) FILTER (WHERE harness <> 'cursor' OR status <> 'completed' OR profile_mode <> 'private'), count(*)
		FROM agent_run WHERE card_id = $1 AND board_column = 'grounding' AND round > 0`, card).Scan(&others, &runs))
	must(t, l.env.Owner.QueryRow(ctx, `SELECT count(*) FROM agent_run_usage u JOIN agent_run t ON t.id = u.agent_run_id
		WHERE t.card_id = $1 AND t.board_column = 'grounding' AND t.round > 0 AND u.model = $2 AND u.cost_usd > 0`, card, agent.CursorModel).Scan(&priced))
	if others != 0 || runs == 0 || priced != runs || got.SpentUsd <= 0 {
		t.Fatalf("runs %d, not on cursor, not completed or not private %d, priced %d, spent %v", runs, others, priced, got.SpentUsd)
	}
}

// A role run on antigravity carries the sources server, which agy cannot be
// given: the agent run fails at once without starting agy, and the card escalates.
func TestAntigravityRefusesARoleThatNeedsSources(t *testing.T) {
	l := newBoardLab(t)
	l.defaultHarness(t, "antigravity")
	card := l.startCard(t)
	defer l.runOn(t, "antigravity", "testdata/fake-antigravity.sh")()
	l.waitCard(t, card, "escalated", func(c db.Card) bool { return c.Flag == "escalated" })
	var refused int
	must(t, l.env.Owner.QueryRow(context.Background(), `SELECT count(*) FROM agent_run WHERE card_id = $1 AND harness = 'antigravity'
		AND status = 'failed' AND attempt = 1 AND failure_reason = 'agent_error' AND error LIKE '%MCP%'`, card).Scan(&refused))
	if refused == 0 {
		t.Fatal("no antigravity run failed once with the MCP refusal")
	}
}
