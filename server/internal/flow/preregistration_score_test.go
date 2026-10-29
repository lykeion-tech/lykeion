package flow

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/google/uuid"

	"lykeion/server/internal/records"
	"lykeion/server/internal/store"
	db "lykeion/server/pkg/db/generated"
)

const effectPred = `[{"name":"effect","text":"effect > 0.2"}]`

// preregistered creates a working study worked by agent from Experimentation
// to to, with preds as its pre-registration.
func (l *lab) preregistered(t *testing.T, to string, agent uuid.UUID, preds string) db.Card {
	t.Helper()
	typ := "agent"
	var c db.Card
	l.in(t, func(tx *store.Tx) (err error) {
		n, err := tx.Q.NextCardNumber(context.Background(), l.ws)
		if err != nil {
			return err
		}
		c, err = tx.Q.CreateCard(context.Background(), db.CreateCardParams{WorkspaceID: l.ws, Number: n, Title: "t", Question: "Why do cells age?",
			OwnerID: l.owner, FollowsFrom: []uuid.UUID{}, ScopeFrom: "experimentation", ScopeTo: to, AssigneeType: &typ, AssigneeID: &agent,
			BoardColumn: "experimentation", Round: 1, Predicates: []byte(preds)})
		return err
	})
	return c
}

// attempt writes run key with results into the study's current round, names
// it as the results run when named, and declares the column done.
func (l *lab) attempt(t *testing.T, id uuid.UUID, key, results string, named bool) {
	t.Helper()
	l.e.Kick(l.ws, id)
	c := l.card(t, id)
	l.in(t, func(tx *store.Tx) error {
		ctx := context.Background()
		if _, err := tx.Q.InsertRecord(ctx, db.InsertRecordParams{WorkspaceID: l.ws, CardID: id, BoardColumn: c.BoardColumn, Round: c.Round,
			Kind: "run", Role: "server", Key: key, Body: []byte(`{"tool":"run_script","output":"","exit_status":0,"results":` + results + `}`)}); err != nil {
			return err
		}
		if !named {
			return nil
		}
		k, err := records.NextRecordKey(ctx, tx, id)
		if err != nil {
			return err
		}
		_, err = tx.Q.InsertRecord(ctx, db.InsertRecordParams{WorkspaceID: l.ws, CardID: id, BoardColumn: c.BoardColumn, Round: c.Round,
			Kind: "result", Role: "agent", Key: k, Body: []byte(`{"text":"done","results_run":"run:` + key + `"}`)})
		return err
	})
	l.done(t, id)
}

// decisions are the latest preregistration record's decisions.
func (l *lab) decisions(t *testing.T, id uuid.UUID) []Decision {
	t.Helper()
	var raw []byte
	if err := l.env.Owner.QueryRow(context.Background(), `SELECT body FROM record WHERE card_id = $1 AND kind = 'preregistration'
		ORDER BY created_at DESC LIMIT 1`, id).Scan(&raw); err != nil {
		t.Fatal(err)
	}
	var body struct {
		Verdict    string
		Predicates []Decision
	}
	if err := json.Unmarshal(raw, &body); err != nil {
		t.Fatal(err)
	}
	return body.Predicates
}

func (l *lab) preregs(t *testing.T, id uuid.UUID) int {
	return l.count(t, "SELECT count(*) FROM record WHERE card_id = $1 AND kind = 'preregistration'", id)
}

func (s *jevStub) set(score func(string) float64) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.score = score
}

const weakest = "Does it say what it couldn't do?"

func TestAHeldPredicateIsRecordedAndTheStudyMovesOn(t *testing.T) {
	l := newLab(t)
	l.jev(t, 200, levels(nil))
	a := l.agent(t, "Curie", "claude", "", "You are Curie.")
	c := l.preregistered(t, "validation", a, effectPred)
	l.attempt(t, c.ID, "x1", `{"effect":0.3}`, true)
	if n := l.preregs(t, c.ID); n != 1 {
		t.Fatalf("%d preregistration records", n)
	}
	d := l.decisions(t, c.ID)
	if len(d) != 1 || d[0].Outcome != "held" || d[0].Run != "x1" || d[0].Values["effect"] != 0.3 {
		t.Fatalf("decisions %+v", d)
	}
	if got := l.card(t, c.ID); got.BoardColumn != "validation" || got.State != "working" {
		t.Fatalf("after a held predicate: %s %s", got.BoardColumn, got.State)
	}
	if items := l.inbox(t, c.ID, "preregistered"); len(items) != 1 || items[0] != preregisteredText(c.Number, 1, 0) ||
		!strings.Contains(items[0], "1 held, 0 missed") {
		t.Fatalf("preregistered items: %v", items)
	}
}

func TestAMissedPredicateDoesNotBlock(t *testing.T) {
	l := newLab(t)
	l.jev(t, 200, levels(nil))
	a := l.agent(t, "Curie", "claude", "", "You are Curie.")
	c := l.preregistered(t, "validation", a, effectPred)
	l.attempt(t, c.ID, "x1", `{"effect":0.12}`, true)
	if d := l.decisions(t, c.ID); d[0].Outcome != "missed" {
		t.Fatalf("decisions %+v", d)
	}
	if got := l.card(t, c.ID); got.BoardColumn != "validation" || got.State != "working" {
		t.Fatalf("after a missed predicate: %s %s", got.BoardColumn, got.State)
	}
	if items := l.inbox(t, c.ID, "preregistered"); len(items) != 1 || !strings.Contains(items[0], "0 held, 1 missed") {
		t.Fatalf("preregistered items: %v", items)
	}
}

func TestALowAttemptStillDecides(t *testing.T) {
	l := newLab(t)
	stub := l.jev(t, 200, levels(map[string]float64{weakest: 2}))
	a := l.agent(t, "Curie", "claude", "", "You are Curie.")
	c := l.preregistered(t, "validation", a, effectPred)
	l.attempt(t, c.ID, "x1", `{"effect":0.12}`, true)
	if d := l.decisions(t, c.ID); d[0].Outcome != "missed" || d[0].Round != c.Round {
		t.Fatalf("after a low attempt: %+v", d)
	}
	if got := l.card(t, c.ID); got.State != "working" || got.Redos != 1 || got.Round != c.Round+1 {
		t.Fatalf("after a low attempt: %s redos %d round %d", got.State, got.Redos, got.Round)
	}
	stub.set(levels(nil))
	l.attempt(t, c.ID, "x2", `{"effect":0.5}`, true)
	d := l.decisions(t, c.ID)
	if d[0].Outcome != "missed" || d[0].Run != "x1" || d[0].Values["effect"] != 0.12 {
		t.Fatalf("a later attempt changed the outcome: %+v", d)
	}
	if n := l.preregs(t, c.ID); n != 2 {
		t.Fatalf("%d preregistration records", n)
	}
	if items := l.inbox(t, c.ID, "preregistered"); len(items) != 1 {
		t.Fatalf("preregistered items: %v", items)
	}
	if got := l.card(t, c.ID); got.BoardColumn != "validation" {
		t.Fatalf("after attempt 2: %s %s", got.BoardColumn, got.State)
	}
}

// blockUnconfirmed runs two high attempts that name no run, checking the
// first redoes the column and the second blocks it.
func blockUnconfirmed(t *testing.T, l *lab, c db.Card) {
	t.Helper()
	before := l.card(t, c.ID)
	l.attempt(t, c.ID, "x1", `{"effect":0.3}`, false)
	got := l.card(t, c.ID)
	if got.BoardColumn != "experimentation" || got.State != "working" || got.Round != before.Round+1 || got.Redos != before.Redos {
		t.Fatalf("after one unconfirmed attempt: %s %s round %d redos %d", got.BoardColumn, got.State, got.Round, got.Redos)
	}
	if d := l.decisions(t, c.ID); d[0].Outcome != "unconfirmed" || d[0].Reason != "no results run named" {
		t.Fatalf("decisions %+v", d)
	}
	l.e.Kick(l.ws, c.ID)
	redone := false
	for _, r := range l.runs(t, c.ID) {
		if r.Scope == ContinuityScope(c.ID, "agent", "experimentation", got.Round) &&
			strings.Contains(r.Prompt, "effect") && strings.Contains(r.Prompt, "no results run named") {
			redone = true
		}
	}
	if !redone {
		t.Fatalf("the redo's run does not name the unconfirmed predicate: %+v", l.runs(t, c.ID))
	}
	l.attempt(t, c.ID, "x2", `{"effect":0.3}`, false)
	got = l.card(t, c.ID)
	if got.State != "blocked" || got.BlockKind != "unconfirmed_results" || got.BoardColumn != "experimentation" {
		t.Fatalf("after two unconfirmed attempts: %s %s %s", got.BoardColumn, got.State, got.BlockKind)
	}
}

func TestUnconfirmedRedoesOnceThenBlocks(t *testing.T) {
	l := newLab(t)
	l.jev(t, 200, levels(nil))
	a := l.agent(t, "Curie", "claude", "", "You are Curie.")
	// Experimentation is the study's last column: unconfirmed must not finish it.
	c := l.preregistered(t, "experimentation", a, effectPred)
	blockUnconfirmed(t, l, c)
	if items := l.inbox(t, c.ID, "blocked"); len(items) != 1 || items[0] != unconfirmedText(c.Number, []string{"effect"}) {
		t.Fatalf("blocked items: %v", items)
	}
	for _, kind := range []string{"done", "middle_score", "preregistered"} {
		if items := l.inbox(t, c.ID, kind); len(items) != 0 {
			t.Fatalf("%s items: %v", kind, items)
		}
	}
}

func TestALowScoreThenUnconfirmedStillGetsItsRedo(t *testing.T) {
	l := newLab(t)
	stub := l.jev(t, 200, levels(map[string]float64{weakest: 2}))
	a := l.agent(t, "Curie", "claude", "", "You are Curie.")
	c := l.preregistered(t, "validation", a, effectPred)
	l.attempt(t, c.ID, "x1", `{"effect":0.3}`, false)
	if got := l.card(t, c.ID); got.State != "working" || got.Redos != 1 {
		t.Fatalf("after a low attempt: %s redos %d", got.State, got.Redos)
	}
	stub.set(levels(nil))
	l.attempt(t, c.ID, "x2", `{"effect":0.3}`, false)
	if got := l.card(t, c.ID); got.State != "working" || got.BoardColumn != "experimentation" || got.Round != c.Round+2 {
		t.Fatalf("after a high but unconfirmed attempt: %s %s round %d", got.BoardColumn, got.State, got.Round)
	}
	l.attempt(t, c.ID, "x3", `{"effect":0.3}`, false)
	if got := l.card(t, c.ID); got.State != "blocked" || got.BlockKind != "unconfirmed_results" {
		t.Fatalf("after a second unconfirmed attempt: %s %s", got.State, got.BlockKind)
	}
}

func TestUnblockGivesOneMoreAttempt(t *testing.T) {
	l := newLab(t)
	l.jev(t, 200, levels(nil))
	a := l.agent(t, "Curie", "claude", "", "You are Curie.")
	c := l.preregistered(t, "experimentation", a, effectPred)
	blockUnconfirmed(t, l, c)
	blocked := l.card(t, c.ID)
	l.e.OnComment(l.ws, c.ID, l.owner, "Write effect to the results file.")
	if got := l.card(t, c.ID); got.State != "working" || got.Round != blocked.Round+1 {
		t.Fatalf("after the comment: %s round %d", got.State, got.Round)
	}
	l.attempt(t, c.ID, "x3", `{"effect":0.3}`, false)
	if got := l.card(t, c.ID); got.State != "blocked" || got.BlockKind != "unconfirmed_results" {
		t.Fatalf("after the extra attempt: %s %s", got.State, got.BlockKind)
	}
}

func TestJevCannotChangeAnOutcome(t *testing.T) {
	l := newLab(t)
	l.jev(t, 200, func(string) float64 { return 4 }) // "Fully" on every criterion
	a := l.agent(t, "Curie", "claude", "", "You are Curie.")
	c := l.preregistered(t, "experimentation", a, effectPred)
	l.attempt(t, c.ID, "x1", `{"effect":0.12}`, true)
	if d := l.decisions(t, c.ID); d[0].Outcome != "missed" {
		t.Fatalf("decisions %+v", d)
	}
	if got := l.card(t, c.ID); got.State != "done" {
		t.Fatalf("after a missed predicate in the last column: %s", got.State)
	}
}

func TestStudiesWithoutPredicatesScoreAsBefore(t *testing.T) {
	l := newLab(t)
	l.jev(t, 200, levels(nil))
	a := l.agent(t, "Curie", "claude", "", "You are Curie.")
	c := l.scoped(t, "experimentation", "experimentation", a)
	l.done(t, c.ID)
	if n := l.preregs(t, c.ID); n != 0 {
		t.Fatalf("%d preregistration records", n)
	}
	if got := l.card(t, c.ID); got.State != "done" {
		t.Fatalf("state %s", got.State)
	}
}
