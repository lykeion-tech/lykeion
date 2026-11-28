package flow

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"

	"lykeion/server/internal/jev"
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

// Outside the planning columns a could_not_do holds the study at once;
// planning columns redo once first (TestPlanningCouldNotDoIsRedoneOnce).
func TestCouldNotDoAsksTheOwner(t *testing.T) {
	l := newLab(t)
	c := l.study(t, "experimentation", true, nil)
	l.e.Kick(l.ws, c.ID)
	lead := l.latestRun(t, c.ID, "leader")
	const why = "GEO has no aged mice"
	l.in(t, func(tx *store.Tx) error {
		_, err := records.WriteAs(context.Background(), tx, service.Writer{Card: c.ID, AgentRun: lead.ID, Column: "experimentation", Role: "leader", Round: c.Round},
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
	l.wantStopDecision(t, c.ID)
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
	l.wantStopDecision(t, c.ID)
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

func TestFileComposerDuringRunQueuesSuccessorBeforeReview(t *testing.T) {
	l := newLab(t)
	a := l.agent(t, "Curie", "codex", "", "Curie")
	c := l.study(t, "hypotheses", false, &a)
	l.e.Kick(l.ws, c.ID)
	old := l.latestRun(t, c.ID, "agent")
	if _, err := l.env.Owner.Exec(t.Context(), "UPDATE agent_run SET status='running' WHERE id=$1", old.ID); err != nil {
		t.Fatal(err)
	}
	comment := l.composer(t, c, "Please inspect this data", nil)
	file := uuid.New()
	if _, err := l.env.Owner.Exec(t.Context(), `INSERT INTO attachment(id, workspace_id, uploader_id, comment_id, name, content_type, size_bytes, sha256, blob_key) VALUES($1,$2,$3,$4,'data.csv','text/csv',4,'hash','test-blob')`, file, l.ws, l.owner, comment.ID); err != nil {
		t.Fatal(err)
	}
	l.e.OnComment(l.ws, c.ID, l.owner, comment.Body)
	if q := l.runs(t, c.ID); len(q) != 0 {
		t.Fatalf("queued while run active: %+v", q)
	}
	if err := l.declare(t, c.ID); err != nil {
		t.Fatal(err)
	}
	l.settle(t, old, "completed", "", "")
	q := l.runs(t, c.ID)
	if len(q) != 1 || q[0].Role != "agent" || !strings.Contains(q[0].Prompt, comment.Body) {
		t.Fatalf("missing follow-up: %+v", q)
	}
	next := l.latestRun(t, c.ID, "agent")
	if n := l.count(t, `SELECT count(*) FROM comment_reservation WHERE comment_id=$1 AND agent_run_id=$2`, comment.ID, next.ID); n != 1 {
		t.Fatalf("file-bearing comment not reserved to follow-up: %d", n)
	}
	if got := l.card(t, c.ID); got.State != "working" || got.BoardColumn != c.BoardColumn {
		t.Fatalf("study advanced before input: %+v", got)
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

func TestComposerFollowUpKeepsCurrentScope(t *testing.T) {
	for _, teamed := range []bool{false, true} {
		t.Run(fmt.Sprint(teamed), func(t *testing.T) {
			l := newLab(t)
			a := l.agent(t, "Curie", "claude", "", "You are Curie.")
			c := l.study(t, "hypotheses", teamed, &a)
			role := "agent"
			if teamed {
				role = "leader"
			}
			l.e.Kick(l.ws, c.ID)
			old := l.latestRun(t, c.ID, role)
			if _, err := l.env.Owner.Exec(t.Context(), "UPDATE agent_run SET status='completed' WHERE id=$1", old.ID); err != nil {
				t.Fatal(err)
			}
			l.in(t, func(tx *store.Tx) error {
				_, err := tx.Q.CreateComment(t.Context(), db.CreateCommentParams{WorkspaceID: l.ws, CardID: c.ID, AuthorID: l.owner, Body: "Check the independent control cohort."})
				return err
			})
			l.e.OnComment(l.ws, c.ID, l.owner, "Check the independent control cohort.")
			q := l.runs(t, c.ID)
			if len(q) != 1 || q[0].Role != role || !strings.Contains(q[0].Prompt, "Check the independent control cohort.") {
				t.Fatalf("follow-up runs: %+v", q)
			}
			next := l.latestRun(t, c.ID, role)
			if next.Round != old.Round || next.BoardColumn != old.BoardColumn || next.ContinuityScope != old.ContinuityScope {
				t.Fatalf("changed scope: %+v", q[0])
			}
			for range 3 {
				if err := l.e.Tick(t.Context()); err != nil {
					t.Fatal(err)
				}
			}
			if q := l.runs(t, c.ID); len(q) != 1 {
				t.Fatalf("duplicate follow-ups: %+v", q)
			}
		})
	}
}

func (l *lab) composer(t *testing.T, c db.Card, body string, reply *uuid.UUID) db.Comment {
	t.Helper()
	var out db.Comment
	l.in(t, func(tx *store.Tx) (err error) {
		out, err = tx.Q.CreateComment(t.Context(), db.CreateCommentParams{WorkspaceID: l.ws, CardID: c.ID, AuthorID: l.owner, Body: body, ReplyRunID: reply})
		if err != nil {
			return err
		}
		return tx.Emit(t.Context(), "comment.created", &c.ID, store.User(l.owner), map[string]string{"comment_id": out.ID.String()})
	})
	return out
}

func TestComposerFollowUpRecoveryAndFrozenInput(t *testing.T) {
	l := newLab(t)
	a := l.agent(t, "Curie", "codex", "", "Curie")
	c := l.study(t, "hypotheses", false, &a)
	first := l.composer(t, c, "First frozen instruction.", nil)
	l.e.Kick(l.ws, c.ID)
	old := l.latestRun(t, c.ID, "agent")
	late := l.composer(t, c, "Later instruction stays pending.", nil)
	l.e.OnComment(l.ws, c.ID, l.owner, late.Body)
	if q := l.runs(t, c.ID); len(q) != 1 || strings.Contains(q[0].Prompt, late.Body) {
		t.Fatalf("mutated frozen queue: %+v", q)
	}
	// A finished run with no launch acknowledgement leaves both messages pending.
	if _, err := l.env.Owner.Exec(t.Context(), "UPDATE agent_run SET status='completed' WHERE id=$1", old.ID); err != nil {
		t.Fatal(err)
	}
	var wg sync.WaitGroup
	for range 8 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if err := l.e.Advance(t.Context(), l.ws, c.ID); err != nil {
				t.Error(err)
			}
		}()
	}
	wg.Wait()
	next := l.latestRun(t, c.ID, "agent")
	if next.ID == old.ID || !strings.Contains(next.Prompt, first.Body) || !strings.Contains(next.Prompt, late.Body) {
		t.Fatalf("lost recovery input: %+v", next)
	}
	if q := l.runs(t, c.ID); len(q) != 1 {
		t.Fatalf("parallel follow-ups: %+v", q)
	}
	if n := l.count(t, "SELECT count(*) FROM record WHERE card_id=$1 AND kind='comment_delivery'", c.ID); n != 0 {
		t.Fatalf("false delivery receipts: %d", n)
	}
	// A delayed old failure settlement must not redo or block the new run.
	l.settle(t, old, "completed", "", "")
	if got := l.card(t, c.ID); got.Round != c.Round || got.State != "working" {
		t.Fatalf("stale settlement: %+v", got)
	}
}

func TestComposerFollowUpRespectsClosedAndCancelledWork(t *testing.T) {
	for _, mode := range []string{"reply", "cancelled_comment", "done", "stopped", "blocked", "parked", "access_revoked"} {
		t.Run(mode, func(t *testing.T) {
			l := newLab(t)
			a := l.agent(t, "Curie", "claude", "", "Curie")
			c := l.study(t, "hypotheses", false, &a)
			l.e.Kick(l.ws, c.ID)
			old := l.latestRun(t, c.ID, "agent")
			if _, err := l.env.Owner.Exec(t.Context(), "UPDATE agent_run SET status='completed' WHERE id=$1", old.ID); err != nil {
				t.Fatal(err)
			}
			var reply *uuid.UUID
			if mode == "reply" {
				reply = &old.ID
			}
			msg := l.composer(t, c, "Do more work.", reply)
			var setupErr error
			switch mode {
			case "done":
				_, setupErr = l.env.Owner.Exec(t.Context(), "UPDATE card SET state='done' WHERE id=$1", c.ID)
			case "stopped":
				_, setupErr = l.env.Owner.Exec(t.Context(), "UPDATE card SET ending='stopped' WHERE id=$1", c.ID)
			case "blocked":
				_, setupErr = l.env.Owner.Exec(t.Context(), "UPDATE card SET state='blocked' WHERE id=$1", c.ID)
			case "parked":
				_, setupErr = l.env.Owner.Exec(t.Context(), "UPDATE card SET state='blocked', block_kind='parked' WHERE id=$1", c.ID)
			case "cancelled_comment":
				l.in(t, func(tx *store.Tx) error {
					_, err := tx.CancelStudyComment(t.Context(), c.ID, msg.ID, l.owner)
					return err
				})
			case "access_revoked":
				_, setupErr = l.env.Owner.Exec(t.Context(), "UPDATE agent_run SET status='cancelled',cancel_reason='runtime_access_revoked' WHERE id=$1", old.ID)
			}
			if setupErr != nil {
				t.Fatal(setupErr)
			}
			if err := l.e.Tick(t.Context()); err != nil {
				t.Fatal(err)
			}
			if q := l.runs(t, c.ID); len(q) != 0 {
				t.Fatalf("ineligible work restarted: %+v", q)
			}
		})
	}
}

func TestComposerResumesOnlyAfterAUserStopsTheLeaderAndSendsNewInput(t *testing.T) {
	l := newLab(t)
	c := l.study(t, "hypotheses", true, nil)
	l.e.Kick(l.ws, c.ID)
	lead := l.latestRun(t, c.ID, "leader")
	old := l.composer(t, c, "Earlier message", nil)
	if _, err := l.env.Owner.Exec(t.Context(), "UPDATE comment SET created_at=now()-interval '1 minute' WHERE id=$1", old.ID); err != nil {
		t.Fatal(err)
	}
	var sibling db.AgentRun
	l.in(t, func(tx *store.Tx) error {
		_, m, _, err := l.e.worker(t.Context(), tx, c)
		if err != nil {
			return err
		}
		member := l.man.Members[0].Name
		a, err := teamAgent(t.Context(), tx, l.pack.ID, l.owner, member)
		if err != nil {
			return err
		}
		sibling, err = l.e.enqueue(t.Context(), tx, c, member, m, a, "Independent member work")
		return err
	})
	if _, err := l.env.Owner.Exec(t.Context(), "UPDATE agent_run SET status='running' WHERE id=$1", sibling.ID); err != nil {
		t.Fatal(err)
	}
	if err := l.e.AgentRuns.Cancel(t.Context(), l.ws, l.owner, lead.ID); err != nil {
		t.Fatal(err)
	}
	if err := l.e.Advance(t.Context(), l.ws, c.ID); err != nil {
		t.Fatal(err)
	}
	if q := l.runs(t, c.ID); len(q) != 0 {
		t.Fatalf("old pending input restarted stopped work: %+v", q)
	}
	msg := l.composer(t, c, "Please continue with a new plan", nil)
	// A transaction can start before the stop and create its comment after it.
	if _, err := l.env.Owner.Exec(t.Context(), "UPDATE comment SET created_at=now()-interval '1 minute' WHERE id=$1", msg.ID); err != nil {
		t.Fatal(err)
	}
	l.e.OnComment(l.ws, c.ID, l.owner, msg.Body)
	if err := l.e.Advance(t.Context(), l.ws, c.ID); err != nil {
		t.Fatal(err)
	}
	if q := l.runs(t, c.ID); len(q) != 0 {
		t.Fatalf("restarted while member still active: %+v", q)
	}
	var status string
	if err := l.env.Owner.QueryRow(t.Context(), "SELECT status FROM agent_run WHERE id=$1", sibling.ID).Scan(&status); err != nil || status != "running" {
		t.Fatalf("sibling changed: %s %v", status, err)
	}
	if _, err := l.env.Owner.Exec(t.Context(), "UPDATE agent_run SET status='completed' WHERE id=$1", sibling.ID); err != nil {
		t.Fatal(err)
	}
	var wg sync.WaitGroup
	for range 8 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if err := l.e.Advance(t.Context(), l.ws, c.ID); err != nil {
				t.Error(err)
			}
		}()
	}
	wg.Wait()
	q := l.runs(t, c.ID)
	if len(q) != 1 || q[0].Role != "leader" || !strings.Contains(q[0].Prompt, msg.Body) {
		t.Fatalf("new input did not resume leader: %+v", q)
	}
	if strings.Contains(q[0].Prompt, old.Body) {
		t.Fatal("pre-stop pending message replayed in resumed run")
	}
	if err := l.e.Advance(t.Context(), l.ws, c.ID); err != nil {
		t.Fatal(err)
	}
	if q := l.runs(t, c.ID); len(q) != 1 {
		t.Fatalf("duplicate follow-up: %+v", q)
	}
}

func TestUserStoppedLeadDoesNotRestartWithoutMessage(t *testing.T) {
	l := newLab(t)
	a := l.agent(t, "Curie", "codex", "", "Curie")
	c := l.study(t, "hypotheses", false, &a)
	l.e.Kick(l.ws, c.ID)
	lead := l.latestRun(t, c.ID, "agent")
	if err := l.e.AgentRuns.Cancel(t.Context(), l.ws, l.owner, lead.ID); err != nil {
		t.Fatal(err)
	}
	if err := l.e.Advance(t.Context(), l.ws, c.ID); err != nil {
		t.Fatal(err)
	}
	if q := l.runs(t, c.ID); len(q) != 0 {
		t.Fatalf("stopped work restarted without a message: %+v", q)
	}
}

func TestComposerTransactionStartedBeforeStopResumesAfterStop(t *testing.T) {
	l := newLab(t)
	a := l.agent(t, "Curie", "codex", "", "Curie")
	c := l.study(t, "hypotheses", false, &a)
	l.e.Kick(l.ws, c.ID)
	lead := l.latestRun(t, c.ID, "agent")
	started, proceed := make(chan struct{}), make(chan struct{})
	result := make(chan error, 1)
	go func() {
		result <- l.e.Store.InWorkspace(t.Context(), l.ws, l.owner, func(tx *store.Tx) error {
			// now() is fixed here, before the cancellation transaction.
			close(started)
			<-proceed
			if _, err := tx.Q.LockWorkspaceExecution(t.Context(), l.ws); err != nil {
				return err
			}
			if _, err := tx.Q.LockCard(t.Context(), db.LockCardParams{WorkspaceID: l.ws, ID: c.ID}); err != nil {
				return err
			}
			comment, err := tx.Q.CreateComment(t.Context(), db.CreateCommentParams{WorkspaceID: l.ws, CardID: c.ID, AuthorID: l.owner, Body: "Continue after the stop"})
			if err != nil {
				return err
			}
			return tx.Emit(t.Context(), "comment.created", &c.ID, store.User(l.owner), map[string]string{"comment_id": comment.ID.String()})
		})
	}()
	<-started
	if err := l.e.AgentRuns.Cancel(t.Context(), l.ws, l.owner, lead.ID); err != nil {
		t.Fatal(err)
	}
	close(proceed)
	if err := <-result; err != nil {
		t.Fatal(err)
	}
	if err := l.e.Advance(t.Context(), l.ws, c.ID); err != nil {
		t.Fatal(err)
	}
	if q := l.runs(t, c.ID); len(q) != 1 || !strings.Contains(q[0].Prompt, "Continue after the stop") {
		t.Fatalf("later serialized comment did not resume: %+v", q)
	}
}

func TestComposerDoesNotResumeSystemCancelledLead(t *testing.T) {
	l := newLab(t)
	a := l.agent(t, "Curie", "codex", "", "Curie")
	c := l.study(t, "hypotheses", false, &a)
	l.e.Kick(l.ws, c.ID)
	lead := l.latestRun(t, c.ID, "agent")
	l.in(t, func(tx *store.Tx) error { return l.e.AgentRuns.CancelInTx(t.Context(), tx, uuid.Nil, lead) })
	msg := l.composer(t, c, "Please continue", nil)
	if _, err := l.env.Owner.Exec(t.Context(), "UPDATE comment SET created_at=now()+interval '1 second' WHERE id=$1", msg.ID); err != nil {
		t.Fatal(err)
	}
	if err := l.e.Advance(t.Context(), l.ws, c.ID); err != nil {
		t.Fatal(err)
	}
	if q := l.runs(t, c.ID); len(q) != 0 {
		t.Fatalf("system cancellation was bypassed: %+v", q)
	}
}

func TestComposerFollowUpRequiresFreshHandInAndAudit(t *testing.T) {
	l := newLab(t)
	c := l.study(t, "hypotheses", true, nil)
	l.e.Kick(l.ws, c.ID)
	old := l.latestRun(t, c.ID, "leader")
	l.in(t, func(tx *store.Tx) error {
		_, err := records.WriteAs(t.Context(), tx, service.Writer{Card: c.ID, AgentRun: old.ID, Column: c.BoardColumn, Role: "leader", Round: c.Round}, "summary", map[string]any{"text": "Old answer", "cites": []string{}})
		return err
	})
	if _, err := l.env.Owner.Exec(t.Context(), "UPDATE agent_run SET status='completed' WHERE id=$1", old.ID); err != nil {
		t.Fatal(err)
	}
	l.composer(t, c, "Recheck the conclusion.", nil)
	l.e.OnComment(l.ws, c.ID, l.owner, "Recheck the conclusion.")
	next := l.latestRun(t, c.ID, "leader")
	if next.ID == old.ID {
		t.Fatal("no continuation")
	}
	l.in(t, func(tx *store.Tx) error {
		if _, ok, err := roundSummary(t.Context(), tx, c); err != nil {
			return err
		} else if ok {
			t.Fatal("old summary used for new work")
		}
		_, err := openColumn(t.Context(), tx, service.Writer{Card: c.ID, AgentRun: next.ID, Column: c.BoardColumn, Role: "leader", Round: c.Round})
		return err
	})
	if err := l.e.Advance(t.Context(), l.ws, c.ID); err != nil {
		t.Fatal(err)
	}
	if n := l.count(t, "SELECT count(*) FROM agent_run WHERE card_id=$1 AND role='auditor'", c.ID); n != 0 {
		t.Fatal("old answer started audit")
	}
	l.in(t, func(tx *store.Tx) error {
		_, err := records.WriteAs(t.Context(), tx, service.Writer{Card: c.ID, AgentRun: next.ID, Column: c.BoardColumn, Role: "leader", Round: c.Round}, "summary", map[string]any{"text": "New answer", "cites": []string{}})
		return err
	})
	// Model the successful launch acknowledgement; handler coverage uses the real API.
	if _, err := l.env.Owner.Exec(t.Context(), "UPDATE comment_reservation SET acknowledged_at=now() WHERE agent_run_id=$1", next.ID); err != nil {
		t.Fatal(err)
	}
	l.settle(t, next, "completed", "", "")
	if q := l.runs(t, c.ID); len(q) != 1 || q[0].Role != "auditor" || !strings.Contains(q[0].Prompt, "New answer") {
		t.Fatalf("normal fresh audit missing: %+v", q)
	}
}

func TestComposerFollowUpSendRacesSettlement(t *testing.T) {
	l := newLab(t)
	a := l.agent(t, "Curie", "codex", "", "Curie")
	c := l.study(t, "hypotheses", false, &a)
	l.e.Kick(l.ws, c.ID)
	old := l.latestRun(t, c.ID, "agent")
	l.in(t, func(tx *store.Tx) error {
		_, err := records.WriteAs(t.Context(), tx, service.Writer{Card: c.ID, AgentRun: old.ID, Column: c.BoardColumn, Role: "agent", Round: c.Round}, "summary", map[string]any{"text": "Earlier result", "cites": []string{}})
		return err
	})
	var wg sync.WaitGroup
	wg.Add(3)
	go func() { defer wg.Done(); l.settle(t, old, "completed", "", "") }()
	for _, body := range []string{"Concurrent first instruction.", "Concurrent second instruction."} {
		go func() { defer wg.Done(); l.composer(t, c, body, nil); l.e.OnComment(l.ws, c.ID, l.owner, body) }()
	}
	wg.Wait()
	if err := l.e.Tick(t.Context()); err != nil {
		t.Fatal(err)
	}
	if q := l.runs(t, c.ID); len(q) != 1 {
		t.Fatalf("parallel or lost follow-ups: %+v", q)
	}
	// A send after enqueue stays pending, whether or not it fit the first snapshot.
	l.in(t, func(tx *store.Tx) error {
		pending, err := tx.Q.UndeliveredComments(t.Context(), db.UndeliveredCommentsParams{WorkspaceID: l.ws, CardID: c.ID, Since: time.Time{}})
		if err != nil {
			return err
		}
		n := l.count(t, "SELECT count(*) FROM comment_reservation WHERE agent_run_id=$1", l.latestRun(t, c.ID, "agent").ID)
		if n+len(pending) != 2 {
			t.Fatalf("lost message: reserved %d pending %d", n, len(pending))
		}
		return nil
	})
}

func TestComposerFollowUpFailedLaunchUsesNormalRetry(t *testing.T) {
	l := newLab(t)
	a := l.agent(t, "Curie", "codex", "", "Curie")
	c := l.study(t, "hypotheses", false, &a)
	msg := l.composer(t, c, "Retry this instruction.", nil)
	l.e.Kick(l.ws, c.ID)
	old := l.latestRun(t, c.ID, "agent")
	l.settle(t, old, "failed", "agent_error", "could not launch")
	next := l.latestRun(t, c.ID, "agent")
	if next.ID == old.ID || next.Round != old.Round+1 || !strings.Contains(next.Prompt, msg.Body) {
		t.Fatalf("normal retry lost input: %+v", next)
	}
	if n := l.count(t, "SELECT count(*) FROM record WHERE card_id=$1 AND kind='comment_delivery'", c.ID); n != 0 {
		t.Fatalf("false delivery %d", n)
	}
}

func TestComposerFollowUpFencesExpiredScoring(t *testing.T) {
	l := newLab(t)
	a := l.agent(t, "Curie", "codex", "", "Curie")
	c := l.study(t, "hypotheses", false, &a)
	l.e.Kick(l.ws, c.ID)
	old := l.latestRun(t, c.ID, "agent")
	l.in(t, func(tx *store.Tx) error {
		_, err := records.WriteAs(t.Context(), tx, service.Writer{Card: c.ID, AgentRun: old.ID, Column: c.BoardColumn, Role: "agent", Round: c.Round}, "summary", map[string]any{"text": "Old result", "cites": []string{}})
		return err
	})
	if _, err := l.env.Owner.Exec(t.Context(), "UPDATE agent_run SET status='completed' WHERE id=$1", old.ID); err != nil {
		t.Fatal(err)
	}
	in, ok, err := l.e.readScoring(t.Context(), l.ws, c.ID, false)
	if err != nil || !ok {
		t.Fatalf("scoring snapshot: %v %v", ok, err)
	}
	if _, err := l.env.Owner.Exec(t.Context(), "UPDATE card SET gating_until=now()-interval '1 second' WHERE id=$1", c.ID); err != nil {
		t.Fatal(err)
	}
	l.composer(t, c, "New input after scoring lease expired.", nil)
	l.e.OnComment(l.ws, c.ID, l.owner, "New input after scoring lease expired.")
	if q := l.runs(t, c.ID); len(q) != 1 {
		t.Fatalf("no follow-up: %+v", q)
	}
	// A stale scoring callback cannot record an outcome or clear a new lease.
	if _, err := l.env.Owner.Exec(t.Context(), "UPDATE card SET gating_until=now()+interval '1 hour' WHERE id=$1", c.ID); err != nil {
		t.Fatal(err)
	}
	if moved, err := l.e.applyScore(t.Context(), l.ws, in, nil, fmt.Errorf("old scoring error")); err != nil || moved {
		t.Fatalf("stale scoring applied: %v %v", moved, err)
	}
	if got := l.card(t, c.ID); got.GatingUntil == nil || got.State != "working" {
		t.Fatalf("stale scoring changed current work: %+v", got)
	}
	if n := l.count(t, "SELECT count(*) FROM record WHERE card_id=$1 AND kind='outcome'", c.ID); n != 0 {
		t.Fatal("old outcome applied")
	}
}

func TestComposerDuringActiveScoringStaysInCurrentColumn(t *testing.T) {
	for _, gate := range []string{"score", "review", "validation"} {
		for _, last := range []bool{true, false} {
			if gate == "validation" && !last {
				continue
			}
			t.Run(fmt.Sprintf("%s/last=%v", gate, last), func(t *testing.T) {
				l := newLab(t)
				a := l.agent(t, "Curie", "codex", "", "Curie")
				column := "hypotheses"
				if gate == "validation" {
					column = "validation"
				}
				c := l.study(t, column, false, &a)
				if !last {
					if _, err := l.env.Owner.Exec(t.Context(), "UPDATE card SET scope_to='validation' WHERE id=$1", c.ID); err != nil {
						t.Fatal(err)
					}
				}
				l.e.Kick(l.ws, c.ID)
				old := l.latestRun(t, c.ID, "agent")
				l.in(t, func(tx *store.Tx) error {
					_, err := records.WriteAs(t.Context(), tx, service.Writer{Card: c.ID, AgentRun: old.ID, Column: c.BoardColumn, Role: "agent", Round: c.Round}, "summary", map[string]any{"text": "Old completed answer", "cites": []string{}})
					return err
				})
				if _, err := l.env.Owner.Exec(t.Context(), "UPDATE agent_run SET status='completed' WHERE id=$1", old.ID); err != nil {
					t.Fatal(err)
				}
				// Pause at the real asynchronous scoring boundary, with its lease still live.
				in, ok, err := l.e.readScoring(t.Context(), l.ws, c.ID, false)
				if err != nil || !ok {
					t.Fatalf("snapshot: %v %v", ok, err)
				}
				msg := l.composer(t, c, "Recheck before leaving this column.", nil)
				l.e.OnComment(l.ws, c.ID, l.owner, msg.Body)
				answers := map[string]jev.ScoreAnswer{}
				for _, cr := range in.criteria {
					answers[cr.ID] = jev.ScoreAnswer{Score: 4}
				}
				switch gate {
				case "score":
					_, err = l.e.applyScore(t.Context(), l.ws, in, answers, nil)
				case "review":
					_, err = l.e.applyReview(t.Context(), l.ws, in, review.Result{}, nil)
				case "validation":
					_, err = l.e.applyValidation(t.Context(), l.ws, in, validating{}, nil, nil)
				}
				if err != nil {
					t.Fatal(err)
				}
				got := l.card(t, c.ID)
				if got.State != "working" || got.BoardColumn != c.BoardColumn || got.Round != c.Round {
					t.Fatalf("pending comment crossed gate: state=%s column=%s round=%d", got.State, got.BoardColumn, got.Round)
				}
				next := l.latestRun(t, c.ID, "agent")
				if next.ID == old.ID || !strings.Contains(next.Prompt, msg.Body) {
					t.Fatalf("message stranded: %+v", next)
				}
				if n := l.count(t, "SELECT count(*) FROM record WHERE card_id=$1 AND kind='outcome'", c.ID); n != 0 {
					t.Fatal("old scoring outcome applied")
				}
			})
		}
	}
}

func TestComposerPreservesEveryRoleAccessHold(t *testing.T) {
	for _, role := range []string{"auditor", "member-1"} {
		for _, reason := range []string{"runtime_access_revoked", "runtime_access_denied"} {
			for _, recovered := range []bool{false, true} {
				t.Run(fmt.Sprintf("%s/%s/recovered=%v", role, reason, recovered), func(t *testing.T) {
					l := newLab(t)
					c := l.study(t, "hypotheses", true, nil)
					l.e.Kick(l.ws, c.ID)
					lead := l.latestRun(t, c.ID, "leader")
					if _, err := l.env.Owner.Exec(t.Context(), "UPDATE agent_run SET status='completed' WHERE id=$1", lead.ID); err != nil {
						t.Fatal(err)
					}
					enqueue := func() db.AgentRun {
						var r db.AgentRun
						l.in(t, func(tx *store.Tx) error {
							_, m, a, err := l.e.worker(t.Context(), tx, c)
							if err != nil {
								return err
							}
							r, err = l.e.enqueue(t.Context(), tx, c, role, m, a, "")
							return err
						})
						return r
					}
					l.in(t, func(tx *store.Tx) error {
						_, err := records.WriteAs(t.Context(), tx, service.Writer{Card: c.ID, AgentRun: lead.ID, Column: c.BoardColumn, Role: "leader", Round: c.Round}, "summary", map[string]any{"text": "Previous completed answer", "cites": []string{}})
						return err
					})
					held := enqueue()
					if reason == "runtime_access_revoked" {
						_, err := l.env.Owner.Exec(t.Context(), "UPDATE agent_run SET status='cancelled',cancel_reason=$2 WHERE id=$1", held.ID, reason)
						if err != nil {
							t.Fatal(err)
						}
					} else {
						_, err := l.env.Owner.Exec(t.Context(), "UPDATE agent_run SET status='failed',failure_reason=$2 WHERE id=$1", held.ID, reason)
						if err != nil {
							t.Fatal(err)
						}
					}
					if recovered {
						replacement := enqueue()
						if _, err := l.env.Owner.Exec(t.Context(), "UPDATE agent_run SET status='completed' WHERE id=$1", replacement.ID); err != nil {
							t.Fatal(err)
						}
					}
					msg := l.composer(t, c, "Continue only after machine recovery.", nil)
					l.e.OnComment(l.ws, c.ID, l.owner, msg.Body)
					if err := l.e.Tick(t.Context()); err != nil {
						t.Fatal(err)
					}
					want := 0
					if recovered {
						want = 1
					}
					if q := l.runs(t, c.ID); len(q) != want {
						t.Fatalf("access hold bypassed or superseded hold retained: runs=%+v", q)
					}
				})
			}
		}
	}
}
