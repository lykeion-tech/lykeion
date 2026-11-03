package flow

import (
	"context"
	"encoding/json"
	"errors"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"

	"lykeion/server/internal/records"
	"lykeion/server/internal/service"
	"lykeion/server/internal/store"
	db "lykeion/server/pkg/db/generated"
)

// wantStopDecision checks the one open item a study's stop raised: it offers
// redo, close and park, defaults to park, and applies the default in a week.
// Every stop's scenario test calls it.
func (l *lab) wantStopDecision(t *testing.T, card uuid.UUID) {
	t.Helper()
	rows, err := l.env.Owner.Query(context.Background(),
		"SELECT options, default_option, expires_at FROM inbox_item WHERE card_id = $1 AND resolved_at IS NULL AND kind IN ('blocked', 'needs_decision')", card)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	n := 0
	for rows.Next() {
		var raw []byte
		var def string
		var expires *time.Time
		if err := rows.Scan(&raw, &def, &expires); err != nil {
			t.Fatal(err)
		}
		n++
		var opts []struct{ ID, Label, Consequence string }
		if err := json.Unmarshal(raw, &opts); err != nil {
			t.Fatal(err)
		}
		var ids []string
		for _, o := range opts {
			if o.Label == "" || o.Consequence == "" {
				t.Errorf("option %q lacks its words", o.ID)
			}
			ids = append(ids, o.ID)
		}
		if !slices.Equal(ids, []string{"redo", "close", "park"}) || def != "park" {
			t.Errorf("options %v default %q", ids, def)
		}
		if expires == nil || expires.Sub(l.e.now().Add(7*24*time.Hour)).Abs() > time.Minute {
			t.Errorf("expires_at %v", expires)
		}
	}
	if n != 1 {
		t.Fatalf("%d open stop items, want 1", n)
	}
}

// heldStudy is a study whose lead could not do Hypotheses, held for its
// owner's decision, and the id of that decision.
func (l *lab) heldStudy(t *testing.T) (db.Card, uuid.UUID) {
	t.Helper()
	c := l.study(t, "hypotheses", true, nil)
	l.e.Kick(l.ws, c.ID)
	lead := l.latestRun(t, c.ID, "leader")
	l.in(t, func(tx *store.Tx) error {
		_, err := records.WriteAs(context.Background(), tx, service.Writer{Card: c.ID, AgentRun: lead.ID, Column: "hypotheses", Role: "leader", Round: c.Round},
			"hand_in", map[string]any{"agent_run_id": lead.ID, "attempt": 1, "hand_in": map[string]any{"status": "could_not_do", "answer": "", "reason": "no data"}})
		return err
	})
	l.settle(t, lead, "completed", "", "")
	var item uuid.UUID
	if err := l.env.Owner.QueryRow(context.Background(), "SELECT id FROM inbox_item WHERE card_id = $1 AND cause = 'could_not_do' AND status = 'open'", c.ID).Scan(&item); err != nil {
		t.Fatal(err)
	}
	return l.card(t, c.ID), item
}

func (l *lab) decision(t *testing.T, id uuid.UUID) (status, chosen, guidance string, by *uuid.UUID) {
	t.Helper()
	if err := l.env.Owner.QueryRow(context.Background(), "SELECT status, chosen_option, guidance, decided_by FROM inbox_item WHERE id = $1", id).Scan(&status, &chosen, &guidance, &by); err != nil {
		t.Fatal(err)
	}
	return
}

func TestDecideRedoRestartsTheColumn(t *testing.T) {
	l := newLab(t)
	c, item := l.heldStudy(t)
	if err := l.e.Decide(context.Background(), l.ws, item, l.owner, "redo", "Use the GTEx data instead."); err != nil {
		t.Fatal(err)
	}
	got := l.card(t, c.ID)
	if got.State != "working" || got.Round != c.Round+1 || !strings.Contains(got.HandoffNote, "Use the GTEx data instead.") {
		t.Fatalf("after redo: %s round %d note %q", got.State, got.Round, got.HandoffNote)
	}
	if status, chosen, guidance, by := l.decision(t, item); status != "decided" || chosen != "redo" || guidance != "Use the GTEx data instead." || by == nil || *by != l.owner {
		t.Fatalf("item: %s %s %q %v", status, chosen, guidance, by)
	}
	if q := l.runs(t, c.ID); len(q) != 1 || q[0].Role != "leader" || !strings.Contains(q[0].Prompt, "Use the GTEx data instead.") {
		t.Fatalf("the redone column's runs: %+v", q)
	}
}

func TestDecideParkKeepsTheStudyParked(t *testing.T) {
	l := newLab(t)
	c, item := l.heldStudy(t)
	if err := l.e.Decide(context.Background(), l.ws, item, l.owner, "park", ""); err != nil {
		t.Fatal(err)
	}
	got := l.card(t, c.ID)
	if got.State != "blocked" || got.BlockKind != "parked" || got.Round != c.Round || got.Ending != "" {
		t.Fatalf("after park: %s %q round %d ending %q", got.State, got.BlockKind, got.Round, got.Ending)
	}
	if status, chosen, _, _ := l.decision(t, item); status != "decided" || chosen != "park" {
		t.Fatalf("item: %s %s", status, chosen)
	}
	l.e.Kick(l.ws, c.ID)
	if q := l.runs(t, c.ID); len(q) != 0 {
		t.Fatalf("a parked study queued %d runs", len(q))
	}
}

func TestRedoReopensAParkedStudy(t *testing.T) {
	l := newLab(t)
	c, item := l.heldStudy(t)
	if err := l.e.Decide(context.Background(), l.ws, item, l.owner, "park", ""); err != nil {
		t.Fatal(err)
	}
	l.e.OnComment(l.ws, c.ID, l.owner, "Pick it up again with the GTEx data.")
	got := l.card(t, c.ID)
	if got.State != "working" || got.BlockKind != "" || got.Round != c.Round+1 || !strings.Contains(got.HandoffNote, "GTEx") {
		t.Fatalf("after the owner's comment: %s %q round %d", got.State, got.BlockKind, got.Round)
	}
}

func TestDecideCloseStopsTheStudy(t *testing.T) {
	l := newLab(t)
	c, item := l.heldStudy(t)
	if err := l.e.Decide(context.Background(), l.ws, item, l.owner, "close", "aged mice data does not exist"); err != nil {
		t.Fatal(err)
	}
	got := l.card(t, c.ID)
	if got.State != "stopped" || got.Ending != "stopped" || !strings.Contains(got.EndReason, "negative result") || !strings.Contains(got.EndReason, "aged mice") {
		t.Fatalf("after close: %s %q %q", got.State, got.Ending, got.EndReason)
	}
	if status, chosen, _, _ := l.decision(t, item); status != "decided" || chosen != "close" {
		t.Fatalf("item: %s %s", status, chosen)
	}
}

// Two people answering at once: one answer applies, the other is told the
// decision is closed, and the study moves once.
func TestDecideTwiceConflicts(t *testing.T) {
	l := newLab(t)
	c, item := l.heldStudy(t)
	start := make(chan struct{})
	errs := make(chan error, 2)
	var wg sync.WaitGroup
	for _, answer := range [][2]string{{"redo", "Try GTEx."}, {"park", ""}} {
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			errs <- l.e.Decide(context.Background(), l.ws, item, l.owner, answer[0], answer[1])
		}()
	}
	close(start)
	wg.Wait()
	close(errs)
	var ok, closed int
	for err := range errs {
		switch {
		case err == nil:
			ok++
		case errors.Is(err, ErrDecisionClosed):
			closed++
		default:
			t.Fatalf("unexpected: %v", err)
		}
	}
	if ok != 1 || closed != 1 {
		t.Fatalf("%d succeeded, %d closed", ok, closed)
	}
	got := l.card(t, c.ID)
	if got.Round > c.Round+1 {
		t.Fatalf("round moved twice: %d -> %d", c.Round, got.Round)
	}
	_, chosen, _, _ := l.decision(t, item)
	if chosen == "redo" && (got.State != "working" || got.Round != c.Round+1) || chosen == "park" && (got.BlockKind != "parked" || got.Round != c.Round) {
		t.Fatalf("chosen %s, card %s %q round %d", chosen, got.State, got.BlockKind, got.Round)
	}
}

func TestDecideRefusesWhatTheDecisionDoesNotOffer(t *testing.T) {
	l := newLab(t)
	_, item := l.heldStudy(t)
	var opt OptionError
	if err := l.e.Decide(context.Background(), l.ws, item, l.owner, "try_again", ""); !errors.As(err, &opt) {
		t.Fatalf("an option the stop does not offer: %v", err)
	}
	if err := l.e.Decide(context.Background(), l.ws, item, l.owner, "redo", "  "); !errors.As(err, &opt) {
		t.Fatalf("redo without guidance: %v", err)
	}
	if status, _, _, _ := l.decision(t, item); status != "open" {
		t.Fatalf("a refused answer changed the item: %s", status)
	}
}

// colleague adds a plain member to the lab's workspace.
func (l *lab) colleague(t *testing.T, email string) uuid.UUID {
	t.Helper()
	u, err := l.st.Global().UpsertUserByEmail(context.Background(), email)
	if err != nil {
		t.Fatal(err)
	}
	l.in(t, func(tx *store.Tx) error {
		return tx.Q.AddMember(context.Background(), db.AddMemberParams{WorkspaceID: l.ws, UserID: u.ID, Role: "member"})
	})
	return u.ID
}

func TestAMemberMayReopenAParkedStudy(t *testing.T) {
	l := newLab(t)
	b := l.colleague(t, "b@lab.org")
	c, item := l.heldStudy(t)
	if err := l.e.Decide(context.Background(), l.ws, item, l.owner, "park", ""); err != nil {
		t.Fatal(err)
	}
	l.e.OnComment(l.ws, c.ID, b, "I'll take it from here with GTEx.")
	got := l.card(t, c.ID)
	if got.State != "working" || got.BlockKind != "" || got.Round != c.Round+1 || !strings.Contains(got.HandoffNote, "GTEx") {
		t.Fatalf("after a colleague's comment: %s %q round %d note %q", got.State, got.BlockKind, got.Round, got.HandoffNote)
	}
}

func TestAColleagueCannotRedoALowScoreBlock(t *testing.T) {
	l := newLab(t)
	b := l.colleague(t, "b@lab.org")
	c, _ := l.heldStudy(t)
	if _, err := l.env.Owner.Exec(context.Background(), "UPDATE card SET block_kind = 'low_score' WHERE id = $1", c.ID); err != nil {
		t.Fatal(err)
	}
	l.e.OnComment(l.ws, c.ID, b, "Redo it.")
	if got := l.card(t, c.ID); got.State != "blocked" || got.Round != c.Round {
		t.Fatalf("a colleague's comment moved a low-score block: %s round %d", got.State, got.Round)
	}
}

func TestACommentThatRedoesDecidesTheStop(t *testing.T) {
	l := newLab(t)
	c, item := l.heldStudy(t)
	l.e.OnComment(l.ws, c.ID, l.owner, "Use the GTEx data instead.")
	if status, chosen, guidance, by := l.decision(t, item); status != "decided" || chosen != "redo" || guidance != "Use the GTEx data instead." || by == nil || *by != l.owner {
		t.Fatalf("item after the owner's comment: %s %s %q %v", status, chosen, guidance, by)
	}
}
