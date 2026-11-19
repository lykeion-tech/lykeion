package flow

import (
	"context"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"

	"lykeion/server/internal/service"
	"lykeion/server/internal/store"
	db "lykeion/server/pkg/db/generated"
)

// askedStudy is a Team study whose leader asked the researcher which
// population to study, the asking run and the open decision's id.
func (l *lab) askedStudy(t *testing.T) (db.Card, db.AgentRun, uuid.UUID) {
	t.Helper()
	c := l.study(t, "hypotheses", true, nil)
	l.e.Kick(l.ws, c.ID)
	lead := l.latestRun(t, c.ID, "leader")
	l.askBy(t, lead, "")
	var item uuid.UUID
	if err := l.env.Owner.QueryRow(context.Background(), "SELECT id FROM inbox_item WHERE card_id = $1 AND kind = 'agent_decision' AND status = 'open'", c.ID).Scan(&item); err != nil {
		t.Fatal(err)
	}
	return c, lead, item
}

// due puts a decision's expiry in the past.
func (l *lab) due(t *testing.T, item uuid.UUID) {
	t.Helper()
	if _, err := l.env.Owner.Exec(context.Background(), "UPDATE inbox_item SET expires_at = $2 WHERE id = $1", item, l.e.now().Add(-time.Minute)); err != nil {
		t.Fatal(err)
	}
}

func TestAnswerResumesTheSameSession(t *testing.T) {
	l := newLab(t)
	c, lead, item := l.askedStudy(t)
	if err := l.e.Decide(context.Background(), l.ws, item, l.owner, "children", "Ages 6 to 12."); err != nil {
		t.Fatal(err)
	}
	if status, chosen, guidance, by := l.decision(t, item); status != "decided" || chosen != "children" || guidance != "Ages 6 to 12." || by == nil || *by != l.owner {
		t.Fatalf("item: %s %s %q %v", status, chosen, guidance, by)
	}
	got := l.card(t, c.ID)
	if got.State != "working" || got.BlockKind != "" || got.Round != c.Round || got.Redos != c.Redos {
		t.Fatalf("after the answer: %s %q round %d redos %d", got.State, got.BlockKind, got.Round, got.Redos)
	}
	q := l.runs(t, c.ID)
	if len(q) != 1 || q[0].Role != "leader" || q[0].Scope != lead.ContinuityScope ||
		!strings.Contains(q[0].Prompt, `The researcher answered your question "Which population?": Children — Ages 6 to 12. — answered by owner@lab.org at `) {
		t.Fatalf("the lead's next run: %+v", q)
	}
	for _, stage := range []string{"experimentation", "validation"} {
		got.BoardColumn = stage
		l.in(t, func(tx *store.Tx) error {
			prompt, _, err := l.e.runPrompt(context.Background(), tx, got, "leader", "")
			if err == nil && (!strings.Contains(prompt, "Decision "+item.String()) ||
				!strings.Contains(prompt, `"Which population?": Children — The study looks at children only. — Ages 6 to 12.`) ||
				!strings.Contains(prompt, "Question context: The question reads two ways.")) {
				t.Errorf("%s omitted the recorded researcher answer: %s", stage, prompt)
			}
			return err
		})
	}
	other := l.study(t, "hypotheses", true, nil)
	l.in(t, func(tx *store.Tx) error {
		prompt, _, err := l.e.runPrompt(context.Background(), tx, other, "leader", "")
		if err == nil && strings.Contains(prompt, "Decision "+item.String()) {
			t.Error("another study received the decision")
		}
		return err
	})
}

// An agent's question accepts only the options the agent offered.
func TestAnswerMustBeAnOfferedOption(t *testing.T) {
	l := newLab(t)
	c, _, item := l.askedStudy(t)
	var opt OptionError
	if err := l.e.Decide(context.Background(), l.ws, item, l.owner, "park", ""); !errors.As(err, &opt) {
		t.Fatalf("an option the agent did not offer: %v", err)
	}
	if status, _, _, _ := l.decision(t, item); status != "open" {
		t.Fatalf("a refused answer changed the item: %s", status)
	}
	if got := l.card(t, c.ID); got.State != "blocked" || got.BlockKind != "decision" {
		t.Fatalf("a refused answer moved the study: %s %q", got.State, got.BlockKind)
	}
}

func TestExpiredStopIsParked(t *testing.T) {
	l := newLab(t)
	c, item := l.heldStudy(t)
	l.due(t, item)
	if err := l.e.sweepDecisions(context.Background(), l.e.now()); err != nil {
		t.Fatal(err)
	}
	if status, chosen, _, by := l.decision(t, item); status != "expired" || chosen != "park" || by != nil {
		t.Fatalf("item: %s %s %v", status, chosen, by)
	}
	got := l.card(t, c.ID)
	if got.State != "blocked" || got.BlockKind != "parked" || got.Round != c.Round || got.Ending != "" {
		t.Fatalf("after expiry: %s %q round %d ending %q", got.State, got.BlockKind, got.Round, got.Ending)
	}
	if n := l.count(t, "SELECT count(*) FROM event WHERE type = 'inbox.resolved' AND payload->>'inbox_item_id' = $1", item.String()); n != 1 {
		t.Fatalf("%d inbox.resolved events", n)
	}
}

// A stop that expires while its column still holds a run cancels that run,
// as parking by hand does, but as the system.
func TestExpiredStopCancelsTheColumnsRuns(t *testing.T) {
	l := newLab(t)
	c, item := l.expiringWithARun(t)
	if err := l.e.sweepDecisions(context.Background(), l.e.now()); err != nil {
		t.Fatal(err)
	}
	if q := l.runs(t, c.ID); len(q) != 0 {
		t.Fatalf("an expired stop left %d runs queued", len(q))
	}
	if status, _, _, _ := l.decision(t, item); status != "expired" {
		t.Fatalf("item: %s", status)
	}
	if n := l.count(t, "SELECT count(*) FROM event WHERE card_id = $1 AND type = 'agent_run.cancelled' AND actor_type = 'system' AND actor_id IS NULL", c.ID); n != 1 {
		t.Fatalf("%d system cancellations", n)
	}
	if n := l.count(t, "SELECT count(*) FROM event WHERE card_id = $1 AND type = 'agent_run.cancelled'", c.ID); n != 1 {
		t.Fatalf("%d cancellations", n)
	}
}

// An expired park cancels its runs even when the study's owner has left the
// workspace.
func TestExpiredStopCancelsRunsOfAnOwnerWhoLeft(t *testing.T) {
	l := newLab(t)
	c, _ := l.expiringWithARun(t)
	if _, err := l.env.Owner.Exec(context.Background(), "DELETE FROM member WHERE workspace_id = $1 AND user_id = $2", l.ws, l.owner); err != nil {
		t.Fatal(err)
	}
	if err := l.e.sweepDecisions(context.Background(), l.e.now()); err != nil {
		t.Fatal(err)
	}
	if n := l.count(t, "SELECT count(*) FROM agent_run WHERE card_id = $1 AND status IN ('queued', 'dispatched', 'running')", c.ID); n != 0 {
		t.Fatalf("%d open runs after the park", n)
	}
	if got := l.card(t, c.ID); got.BlockKind != "parked" {
		t.Fatalf("after expiry: %s %q", got.State, got.BlockKind)
	}
}

// expiringWithARun is a held study whose stop is due while a member's run is
// still queued in its column.
func (l *lab) expiringWithARun(t *testing.T) (db.Card, uuid.UUID) {
	t.Helper()
	c, item := l.heldStudy(t)
	l.in(t, func(tx *store.Tx) error {
		ctx := context.Background()
		card, err := tx.Q.GetCard(ctx, db.GetCardParams{WorkspaceID: l.ws, ID: c.ID})
		if err != nil {
			return err
		}
		a, err := teamAgent(ctx, tx, l.pack.ID, l.owner, l.man.Members[0].Name)
		if err != nil {
			return err
		}
		_, err = l.e.enqueue(ctx, tx, card, l.man.Members[0].Name, &l.man, a, "still going")
		return err
	})
	l.due(t, item)
	return c, item
}

// A question whose lead cannot be woken any more expires all the same, and
// its study is held for its owner instead of staying due.
func TestExpiredQuestionWithNoLeadHoldsTheStudy(t *testing.T) {
	l := newLab(t)
	c, _, item := l.askedStudy(t)
	if _, err := l.env.Owner.Exec(context.Background(), `UPDATE field_pack SET manifest = '"broken"' WHERE id = $1`, l.pack.ID); err != nil {
		t.Fatal(err)
	}
	l.due(t, item)
	for range 2 {
		if err := l.e.sweepDecisions(context.Background(), l.e.now()); err != nil {
			t.Fatal(err)
		}
	}
	if status, _, _, _ := l.decision(t, item); status != "expired" {
		t.Fatalf("item: %s", status)
	}
	if got := l.card(t, c.ID); got.State != "blocked" || got.BlockKind != "" || got.Round != c.Round {
		t.Fatalf("after expiry: %s %q round %d", got.State, got.BlockKind, got.Round)
	}
	if n := l.count(t, "SELECT count(*) FROM inbox_item WHERE card_id = $1 AND kind = 'needs_decision' AND cause = 'could_not_do' AND status = 'open'", c.ID); n != 1 {
		t.Fatalf("%d holds for the owner", n)
	}
	if q := l.runs(t, c.ID); len(q) != 0 {
		t.Fatalf("%d runs queued", len(q))
	}
}

func TestExpiredAgentDecisionWakesTheLead(t *testing.T) {
	l := newLab(t)
	c, lead, item := l.askedStudy(t)
	l.due(t, item)
	if err := l.e.sweepDecisions(context.Background(), l.e.now()); err != nil {
		t.Fatal(err)
	}
	if status, chosen, _, by := l.decision(t, item); status != "expired" || chosen != "" || by != nil {
		t.Fatalf("item: %s %q %v", status, chosen, by)
	}
	got := l.card(t, c.ID)
	if got.State != "working" || got.BlockKind != "" || got.Round != c.Round {
		t.Fatalf("after expiry: %s %q round %d", got.State, got.BlockKind, got.Round)
	}
	q := l.runs(t, c.ID)
	if len(q) != 1 || q[0].Role != "leader" || q[0].Scope != lead.ContinuityScope ||
		!strings.Contains(q[0].Prompt, `Your question "Which population?" expired without an answer.`) ||
		!strings.Contains(q[0].Prompt, "Expiry does not authorize a choice the researcher reserved.") {
		t.Fatalf("the lead's next run: %+v", q)
	}
	if n := l.count(t, "SELECT count(*) FROM event WHERE type = 'inbox.resolved' AND payload->>'inbox_item_id' = $1", item.String()); n != 1 {
		t.Fatalf("%d inbox.resolved events", n)
	}
}

// An answer and the expiry sweep on the same due question: one of them
// closes it, and the lead is woken once.
func TestExpiryAndAnswerRace(t *testing.T) {
	l := newLab(t)
	for range 5 {
		c, _, item := l.askedStudy(t)
		l.due(t, item)
		start := make(chan struct{})
		var decideErr, sweepErr error
		var wg sync.WaitGroup
		wg.Add(2)
		go func() {
			defer wg.Done()
			<-start
			decideErr = l.e.Decide(context.Background(), l.ws, item, l.owner, "adults", "")
		}()
		go func() {
			defer wg.Done()
			<-start
			sweepErr = l.e.sweepDecisions(context.Background(), l.e.now())
		}()
		close(start)
		wg.Wait()
		if sweepErr != nil {
			t.Fatal(sweepErr)
		}
		status, _, _, _ := l.decision(t, item)
		switch {
		case status == "decided" && decideErr == nil:
		case status == "expired" && errors.Is(decideErr, ErrDecisionClosed):
		default:
			t.Fatalf("status %s, answer %v", status, decideErr)
		}
		if q := l.runs(t, c.ID); len(q) != 1 {
			t.Fatalf("the lead was woken %d times", len(q))
		}
		if n := l.count(t, "SELECT count(*) FROM event WHERE type = 'inbox.resolved' AND payload->>'inbox_item_id' = $1", item.String()); n != 1 {
			t.Fatalf("%d inbox.resolved events", n)
		}
	}
}

func TestReminderOnce(t *testing.T) {
	l := newLab(t)
	_, _, item := l.askedStudy(t)
	reminders := func() int {
		return l.count(t, "SELECT count(*) FROM event WHERE type = 'decision.reminder' AND payload->>'inbox_item_id' = $1", item.String())
	}
	ctx := context.Background()
	if err := l.e.sweepDecisions(ctx, l.e.now()); err != nil {
		t.Fatal(err)
	}
	if n := reminders(); n != 0 {
		t.Fatalf("a week before expiry: %d reminders", n)
	}
	dayBefore := l.e.now().Add(6*24*time.Hour + 12*time.Hour)
	for range 2 {
		if err := l.e.sweepDecisions(ctx, dayBefore); err != nil {
			t.Fatal(err)
		}
	}
	if n := reminders(); n != 1 {
		t.Fatalf("the day before expiry: %d reminders", n)
	}
	if status, _, _, _ := l.decision(t, item); status != "open" {
		t.Fatalf("a reminder changed the item: %s", status)
	}
}

func TestArchiveAfter90Days(t *testing.T) {
	l := newLab(t)
	ctx := context.Background()
	add := func(status string, kept bool, age time.Duration) uuid.UUID {
		var id uuid.UUID
		if err := l.env.Owner.QueryRow(ctx, `INSERT INTO inbox_item (workspace_id, user_id, kind, text, status, kept, touched_at)
			VALUES ($1, $2, 'done', 'x', $3, $4, $5) RETURNING id`, l.ws, l.owner, status, kept, l.e.now().Add(-age)).Scan(&id); err != nil {
			t.Fatal(err)
		}
		return id
	}
	const day = 24 * time.Hour
	stale := add("decided", false, 91*day)
	kept := add("decided", true, 91*day)
	recent := add("dismissed", false, 89*day)
	open := add("open", false, 91*day)
	if err := l.e.sweepDecisions(ctx, l.e.now()); err != nil {
		t.Fatal(err)
	}
	for id, want := range map[uuid.UUID]bool{stale: true, kept: false, recent: false, open: false} {
		var archived bool
		if err := l.env.Owner.QueryRow(ctx, "SELECT archived_at IS NOT NULL FROM inbox_item WHERE id = $1", id).Scan(&archived); err != nil {
			t.Fatal(err)
		}
		if archived != want {
			t.Errorf("item %s archived %v, want %v", id, archived, want)
		}
	}
}

// A studyless item with an expiry is not the sweep's: it neither breaks the
// pass nor expires.
func TestStudylessItemsStayOutOfTheSweep(t *testing.T) {
	l := newLab(t)
	ctx := context.Background()
	var studyless uuid.UUID
	if err := l.env.Owner.QueryRow(ctx, `INSERT INTO inbox_item (workspace_id, user_id, kind, text, options, default_option, expires_at)
		VALUES ($1, $2, 'needs_decision', 'x', '[{"id":"park","label":"Park","consequence":"p"}]', 'park', $3) RETURNING id`,
		l.ws, l.owner, l.e.now().Add(-2*time.Minute)).Scan(&studyless); err != nil {
		t.Fatal(err)
	}
	_, item := l.heldStudy(t)
	l.due(t, item)
	if err := l.e.sweepDecisions(ctx, l.e.now()); err != nil {
		t.Fatal(err)
	}
	if status, _, _, _ := l.decision(t, item); status != "expired" {
		t.Fatalf("the study's stop: %s", status)
	}
	if status, _, _, _ := l.decision(t, studyless); status != "open" {
		t.Fatalf("the studyless item: %s", status)
	}
}

// withHeldRun gives a held study a member's run that its machines kept
// failing, held for its owner, and the open machine_failures decision that
// asks about it; it returns the run and the decision.
func (l *lab) withHeldRun(t *testing.T, c db.Card) (uuid.UUID, uuid.UUID) {
	t.Helper()
	var run uuid.UUID
	l.in(t, func(tx *store.Tx) error {
		ctx := context.Background()
		card, err := tx.Q.GetCard(ctx, db.GetCardParams{WorkspaceID: l.ws, ID: c.ID})
		if err != nil {
			return err
		}
		a, err := teamAgent(ctx, tx, l.pack.ID, l.owner, l.man.Members[0].Name)
		if err != nil {
			return err
		}
		r, err := l.e.enqueue(ctx, tx, card, l.man.Members[0].Name, &l.man, a, "still going")
		if err != nil {
			return err
		}
		run = r.ID
		if err := tx.Q.HoldAgentRun(ctx, db.HoldAgentRunParams{WorkspaceID: l.ws, ID: run, HeldUntil: &service.HeldUntil}); err != nil {
			return err
		}
		return service.HoldForDecision(ctx, tx, card, "needs_decision", "machine_failures", "kept failing", l.e.now())
	})
	if _, err := l.env.Owner.Exec(context.Background(), "UPDATE agent_run SET infra_failures = 5 WHERE id = $1", run); err != nil {
		t.Fatal(err)
	}
	var item uuid.UUID
	if err := l.env.Owner.QueryRow(context.Background(), "SELECT id FROM inbox_item WHERE card_id = $1 AND cause = 'machine_failures' AND status = 'open'", c.ID).Scan(&item); err != nil {
		t.Fatal(err)
	}
	return run, item
}

// The owner's redo answers every stop of the study: the machine_failures
// decision is dismissed and the old round's held run cancelled, so its
// later expiry cannot park the study that moved on.
func TestRedoClosesMachineFailures(t *testing.T) {
	l := newLab(t)
	c, _ := l.heldStudy(t)
	run, machine := l.withHeldRun(t, c)
	l.e.OnComment(l.ws, c.ID, l.owner, "Use the GTEx data instead.")
	if status, _, _, _ := l.decision(t, machine); status != "dismissed" {
		t.Fatalf("machine_failures item after the redo: %s", status)
	}
	if n := l.count(t, "SELECT count(*) FROM agent_run WHERE id = $1 AND status = 'cancelled'", run); n != 1 {
		t.Fatal("the old round's held run was not cancelled")
	}
	l.due(t, machine)
	if err := l.e.sweepDecisions(context.Background(), l.e.now()); err != nil {
		t.Fatal(err)
	}
	if got := l.card(t, c.ID); got.State != "working" || got.BlockKind != "" {
		t.Fatalf("after the old item's due date: %s %q", got.State, got.BlockKind)
	}
}

// A stop whose study has moved on no longer holds it: its expiry only closes
// the item, and answering it is refused.
func TestStaleStopLeavesTheStudyAlone(t *testing.T) {
	l := newLab(t)
	c, stop := l.heldStudy(t)
	_, machine := l.withHeldRun(t, c)
	// The study works again, and its held run is gone, without either item
	// being closed.
	if _, err := l.env.Owner.Exec(context.Background(), "UPDATE card SET state = 'working', block_kind = '' WHERE id = $1", c.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := l.env.Owner.Exec(context.Background(), "UPDATE agent_run SET status = 'cancelled' WHERE card_id = $1 AND status = 'queued'", c.ID); err != nil {
		t.Fatal(err)
	}
	if err := l.e.Decide(context.Background(), l.ws, stop, l.owner, "park", ""); !errors.Is(err, ErrNotWaiting) {
		t.Fatalf("parking through a stale stop: %v", err)
	}
	if err := l.e.Decide(context.Background(), l.ws, machine, l.owner, "park", ""); !errors.Is(err, ErrNotWaiting) {
		t.Fatalf("parking through a stale machine_failures item: %v", err)
	}
	l.due(t, stop)
	l.due(t, machine)
	if err := l.e.sweepDecisions(context.Background(), l.e.now()); err != nil {
		t.Fatal(err)
	}
	for _, id := range []uuid.UUID{stop, machine} {
		if status, _, _, _ := l.decision(t, id); status != "expired" {
			t.Fatalf("item %s: %s", id, status)
		}
	}
	if got := l.card(t, c.ID); got.State != "working" || got.BlockKind != "" || got.Ending != "" {
		t.Fatalf("a stale stop moved the study: %s %q %q", got.State, got.BlockKind, got.Ending)
	}
}

// An answer whose lead cannot be woken any more still closes the question,
// and the study is held for its owner, its brief kept.
func TestAnswerWithNoLeadHoldsTheStudy(t *testing.T) {
	l := newLab(t)
	c, _, item := l.askedStudy(t)
	if _, err := l.env.Owner.Exec(context.Background(), `UPDATE field_pack SET manifest = '"broken"' WHERE id = $1`, l.pack.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := l.env.Owner.Exec(context.Background(), `UPDATE card SET handoff_note = 'the brief' WHERE id = $1`, c.ID); err != nil {
		t.Fatal(err)
	}
	if err := l.e.Decide(context.Background(), l.ws, item, l.owner, "children", ""); err != nil {
		t.Fatalf("answering: %v", err)
	}
	if status, chosen, _, _ := l.decision(t, item); status != "decided" || chosen != "children" {
		t.Fatalf("item: %s %s", status, chosen)
	}
	if got := l.card(t, c.ID); got.State != "blocked" || got.BlockKind != "" || got.HandoffNote != "the brief" {
		t.Fatalf("after the answer: %s %q note %q", got.State, got.BlockKind, got.HandoffNote)
	}
	if n := l.count(t, "SELECT count(*) FROM inbox_item WHERE card_id = $1 AND cause = 'could_not_do' AND status = 'open'", c.ID); n != 1 {
		t.Fatalf("%d holds for the owner", n)
	}
}

// Asking blocks the study without losing the brief its lead was working to.
func TestAskKeepsTheBrief(t *testing.T) {
	l := newLab(t)
	c := l.study(t, "hypotheses", true, nil)
	l.e.Kick(l.ws, c.ID)
	lead := l.latestRun(t, c.ID, "leader")
	if _, err := l.env.Owner.Exec(context.Background(), `UPDATE card SET handoff_note = 'the brief' WHERE id = $1`, c.ID); err != nil {
		t.Fatal(err)
	}
	l.askBy(t, lead, "")
	if got := l.card(t, c.ID); got.BlockKind != "decision" || got.HandoffNote != "the brief" {
		t.Fatalf("after the ask: %q note %q", got.BlockKind, got.HandoffNote)
	}
}

// A stop raised again gets a fresh week, and so a fresh reminder.
func TestRaisedAgainRemindsAgain(t *testing.T) {
	l := newLab(t)
	c, item := l.heldStudy(t)
	if _, err := l.env.Owner.Exec(context.Background(), "UPDATE inbox_item SET reminded_at = now() WHERE id = $1", item); err != nil {
		t.Fatal(err)
	}
	l.in(t, func(tx *store.Tx) error {
		return holdForDecision(context.Background(), tx, c, "needs_decision", "could_not_do", "again", l.e.now())
	})
	if n := l.count(t, "SELECT count(*) FROM inbox_item WHERE id = $1 AND reminded_at IS NULL AND text = 'again'", item); n != 1 {
		t.Fatal("the refreshed stop kept its old reminder")
	}
}
