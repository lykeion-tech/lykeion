package flow

import (
	"context"
	"strings"
	"testing"

	"github.com/google/uuid"

	"lykeion/server/internal/records"
	"lykeion/server/internal/review"
	"lykeion/server/internal/service"
	"lykeion/server/internal/store"
	db "lykeion/server/pkg/db/generated"
)

// latestRun is the study's newest run of role.
func (l *lab) latestRun(t *testing.T, card uuid.UUID, role string) db.AgentRun {
	t.Helper()
	var id uuid.UUID
	if err := l.env.Owner.QueryRow(context.Background(), "SELECT id FROM agent_run WHERE card_id = $1 AND role = $2 ORDER BY created_at DESC LIMIT 1", card, role).Scan(&id); err != nil {
		t.Fatalf("no %s run: %v", role, err)
	}
	var r db.AgentRun
	l.in(t, func(tx *store.Tx) (err error) {
		r, err = tx.Q.GetAgentRun(context.Background(), db.GetAgentRunParams{WorkspaceID: l.ws, ID: id})
		return err
	})
	return r
}

// settle ends run as its daemon would, with status, failure reason and
// error, and delivers its settlement to the engine.
func (l *lab) settle(t *testing.T, run db.AgentRun, status, reason, errText string) {
	t.Helper()
	ctx := context.Background()
	var fr *string
	if reason != "" {
		fr = &reason
	}
	if _, err := l.env.Owner.Exec(ctx, "UPDATE agent_run SET status = $2, failure_reason = $3, error = $4 WHERE id = $1", run.ID, status, fr, errText); err != nil {
		t.Fatal(err)
	}
	run.Status, run.FailureReason, run.Error = status, fr, errText
	if err := l.e.OnSettlement(ctx, service.Settlement{AgentRun: run}); err != nil {
		t.Fatal(err)
	}
}

func TestMemberSettlementWakesNoOne(t *testing.T) {
	l := newLab(t)
	c := l.study(t, "hypotheses", true, nil)
	l.e.Kick(l.ws, c.ID)
	if _, err := l.env.Owner.Exec(context.Background(), "UPDATE agent_run SET status = 'running' WHERE card_id = $1 AND role = 'leader'", c.ID); err != nil {
		t.Fatal(err)
	}
	l.in(t, func(tx *store.Tx) error {
		ctx := context.Background()
		card, err := tx.Q.GetCard(ctx, db.GetCardParams{WorkspaceID: l.ws, ID: c.ID})
		if err != nil {
			return err
		}
		a, err := teamAgent(ctx, tx, l.pack.ID, l.owner, "member-1")
		if err != nil {
			return err
		}
		_, err = l.e.enqueue(ctx, tx, card, "member-1", &l.man, a, "one")
		return err
	})
	l.settle(t, l.latestRun(t, c.ID, "member-1"), "completed", "", "")
	if q := l.runs(t, c.ID); len(q) != 0 {
		t.Fatalf("a member settling queued %d runs: %+v", len(q), q)
	}
}

func TestCouldNotDoAsksTheOwner(t *testing.T) {
	l := newLab(t)
	c := l.study(t, "hypotheses", true, nil)
	l.e.Kick(l.ws, c.ID)
	lead := l.latestRun(t, c.ID, "leader")
	const why = "GEO has no aged mice"
	l.in(t, func(tx *store.Tx) error {
		_, err := records.WriteAs(context.Background(), tx, service.Writer{Card: c.ID, AgentRun: lead.ID, Column: "hypotheses", Role: "leader", Round: c.Round},
			"hand_in", map[string]any{"agent_run_id": lead.ID, "attempt": 1, "hand_in": map[string]any{"status": "could_not_do", "answer": "", "reason": why}})
		return err
	})
	l.settle(t, lead, "completed", "", "")
	items := l.inbox(t, c.ID, "needs_decision")
	if len(items) != 1 || items[0] != "Study #1: the agent could not do this column: "+why {
		t.Fatalf("needs_decision items: %q", items)
	}
	if n := l.count(t, "SELECT count(*) FROM inbox_item WHERE card_id = $1 AND cause = 'could_not_do' AND resolved_at IS NULL", c.ID); n != 1 {
		t.Fatalf("%d open could_not_do items", n)
	}
	got := l.card(t, c.ID)
	if got.State != "blocked" || got.BlockKind != "" || got.Redos != c.Redos || got.Round != c.Round {
		t.Fatalf("held study: %s %q redos %d round %d", got.State, got.BlockKind, got.Redos, got.Round)
	}
	if q := l.runs(t, c.ID); len(q) != 0 {
		t.Fatalf("could_not_do queued %d runs", len(q))
	}
	l.e.OnComment(l.ws, c.ID, l.owner, "Use the GTEx data instead.")
	got = l.card(t, c.ID)
	if got.State != "working" || got.Round != c.Round+1 || got.Redos != c.Redos {
		t.Fatalf("after the owner's comment: %s round %d redos %d", got.State, got.Round, got.Redos)
	}
	if n := l.count(t, "SELECT count(*) FROM inbox_item WHERE card_id = $1 AND cause = 'could_not_do' AND resolved_at IS NULL", c.ID); n != 0 {
		t.Fatalf("the comment left %d could_not_do items open", n)
	}
	if q := l.runs(t, c.ID); len(q) != 1 || q[0].Role != "leader" || !strings.Contains(q[0].Prompt, "Use the GTEx data instead.") {
		t.Fatalf("the released column's runs: %+v", q)
	}
}

func TestLeadHandInInvalidRedoesThenBlocks(t *testing.T) {
	l := newLab(t)
	a := l.agent(t, "Curie", "claude", "", "You are Curie.")
	c := l.study(t, "hypotheses", false, &a)
	l.e.Kick(l.ws, c.ID)
	const probs = "hypothesis 1: name a control\nvalue 2: name the saved output or script run that shows the value"
	l.settle(t, l.latestRun(t, c.ID, "agent"), "failed", "hand_in_invalid", probs)
	got := l.card(t, c.ID)
	if got.State != "working" || got.Redos != 1 || got.Round != c.Round+1 {
		t.Fatalf("after the first failure: %s redos %d round %d", got.State, got.Redos, got.Round)
	}
	q := l.runs(t, c.ID)
	if len(q) != 1 || q[0].Role != "agent" || !strings.Contains(q[0].Prompt, "hypothesis 1: name a control") || !strings.Contains(q[0].Prompt, "value 2: name the saved output") {
		t.Fatalf("the redo's run: %+v", q)
	}
	if q[0].Scope != ContinuityScope(c.ID, "agent", "hypotheses", c.Round+1) {
		t.Fatalf("the redo resumed %q", q[0].Scope)
	}
	l.settle(t, l.latestRun(t, c.ID, "agent"), "failed", "hand_in_invalid", probs)
	got = l.card(t, c.ID)
	if got.State != "blocked" || got.BlockKind != "" || got.Round != c.Round+1 {
		t.Fatalf("after the second failure: %s %q round %d", got.State, got.BlockKind, got.Round)
	}
	items := l.inbox(t, c.ID, "blocked")
	if len(items) != 1 || !strings.HasPrefix(items[0], "Study #1's Hypotheses column stopped: ") || !strings.Contains(items[0], "hypothesis 1: name a control") ||
		strings.Contains(items[0], "scored low") {
		t.Fatalf("blocked items: %q", items)
	}
	if q := l.runs(t, c.ID); len(q) != 0 {
		t.Fatalf("a blocked study queued %d runs", len(q))
	}
	l.e.OnComment(l.ws, c.ID, l.owner, "Name a control for every hypothesis.")
	if got = l.card(t, c.ID); got.State != "working" || got.Round != c.Round+2 {
		t.Fatalf("after the owner's comment: %s round %d", got.State, got.Round)
	}
	if n := l.count(t, "SELECT count(*) FROM inbox_item WHERE card_id = $1 AND kind = 'blocked' AND resolved_at IS NULL", c.ID); n != 0 {
		t.Fatalf("the comment left %d blocked items open", n)
	}
}

// A lead that completes with no summary and no could_not_do hand-in counts
// as a failed run: its column is redone once, then held for its owner, so
// the study never waits on a run that will not come.
func TestLeadCompletedWithoutHandInCountsAsFailed(t *testing.T) {
	l := newLab(t)
	c := l.study(t, "hypotheses", true, nil)
	l.e.Kick(l.ws, c.ID)
	l.settle(t, l.latestRun(t, c.ID, "leader"), "completed", "", "")
	got := l.card(t, c.ID)
	if got.State != "working" || got.Redos != 1 || got.Round != c.Round+1 {
		t.Fatalf("after the first run: %s redos %d round %d", got.State, got.Redos, got.Round)
	}
	q := l.runs(t, c.ID)
	if len(q) != 1 || q[0].Role != "leader" || !strings.Contains(q[0].Prompt, "the run ended without a hand-in") {
		t.Fatalf("the redo's run: %+v", q)
	}
	l.settle(t, l.latestRun(t, c.ID, "leader"), "completed", "", "")
	if got = l.card(t, c.ID); got.State != "blocked" || got.BlockKind != "" {
		t.Fatalf("after the second run: %s %q", got.State, got.BlockKind)
	}
	items := l.inbox(t, c.ID, "blocked")
	if len(items) != 1 || !strings.HasPrefix(items[0], "Study #1's Hypotheses column stopped: ") || !strings.Contains(items[0], "the run ended without a hand-in") {
		t.Fatalf("blocked items: %q", items)
	}
}

func TestAWorkingStudysCommentQueuesNoRun(t *testing.T) {
	l := newLab(t)
	c := l.study(t, "hypotheses", true, nil)
	l.e.Kick(l.ws, c.ID)
	if _, err := l.env.Owner.Exec(context.Background(), "UPDATE agent_run SET status = 'running' WHERE card_id = $1 AND role = 'leader'", c.ID); err != nil {
		t.Fatal(err)
	}
	before := l.count(t, "SELECT count(*) FROM agent_run WHERE card_id = $1", c.ID)
	l.e.OnComment(l.ws, c.ID, l.owner, "How is it going?")
	if n := l.count(t, "SELECT count(*) FROM agent_run WHERE card_id = $1", c.ID); n != before {
		t.Fatalf("a comment on a working study queued %d runs", n-before)
	}
}

func TestRunBookkeepingNeverReachesJev(t *testing.T) {
	for _, legacy := range []bool{true, false} {
		l := newLab(t)
		l.legacy = legacy
		stub := l.jev(t, 200, levels(nil))
		c := l.study(t, "hypotheses", true, nil)
		l.e.Kick(l.ws, c.ID)
		note := l.groundingPaper(t, c.ID, paperA)
		l.item(t, c.ID, review.KindHypothesis, review.HypothesisBody{Text: "Telomere length predicts senescence onset.",
			Prediction: review.Prediction{Shape: "direction", Direction: "decrease"}, Control: review.Control{Text: "Young cells"}, Cites: []string{note}})
		for _, kind := range []string{"hand_in", "declined", "comment_delivery"} {
			l.item(t, c.ID, kind, map[string]any{"text": "SECRET-" + kind, "quote": "SECRET-" + kind + " quote", "cites": []string{"call:k1"}})
		}
		l.done(t, c.ID)
		l.settleAuditor(t, c.ID)
		calls := stub.calls()
		if len(calls) == 0 {
			t.Fatalf("legacy %v: Jev was not asked", legacy)
		}
		for _, call := range calls {
			if strings.Contains(call.State, "SECRET-") {
				t.Fatalf("legacy %v: run bookkeeping reached Jev:\n%s", legacy, call.State)
			}
		}
	}
}
