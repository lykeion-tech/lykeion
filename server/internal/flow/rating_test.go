package flow

import (
	"context"
	"testing"
	"time"

	"github.com/google/uuid"
)

// draws counts a study's rating draws, and how many of them were offers.
func (l *lab) draws(t *testing.T, id uuid.UUID) (all, offered int) {
	t.Helper()
	if err := l.env.Owner.QueryRow(context.Background(),
		"SELECT count(*), count(*) FILTER (WHERE offered) FROM team_rating WHERE card_id = $1", id).Scan(&all, &offered); err != nil {
		t.Fatal(err)
	}
	return all, offered
}

// finish has the study's Team finish its current column and the auditor settle.
func (l *lab) finish(t *testing.T, id uuid.UUID) {
	t.Helper()
	l.done(t, id)
	l.settleAuditor(t, id)
}

func always(hit bool) func() bool { return func() bool { return hit } }

func TestARatingIsDrawnOnceWhenATeamColumnFinishes(t *testing.T) {
	l := newLab(t)
	l.jev(t, 200, levels(nil))
	l.e.RatingDraw = always(true)
	c := l.study(t, "hypotheses", true, nil)
	l.finish(t, c.ID)
	if got := l.card(t, c.ID); got.State != "done" {
		t.Fatalf("the study did not finish: %s %s", got.BoardColumn, got.State)
	}
	var pack, user uuid.UUID
	var column string
	if err := l.env.Owner.QueryRow(context.Background(),
		"SELECT pack_id, user_id, board_column FROM team_rating WHERE card_id = $1 AND offered", c.ID).Scan(&pack, &user, &column); err != nil {
		t.Fatal(err)
	}
	if pack != l.pack.ID || user != l.owner || column != "hypotheses" {
		t.Fatalf("offer: pack %v user %v column %s", pack, user, column)
	}
	if all, _ := l.draws(t, c.ID); all != 1 {
		t.Fatalf("%d draws", all)
	}
}

func TestTheDrawIgnoresTheResult(t *testing.T) {
	l := newLab(t)
	now := time.Now()
	l.e.Now = func() time.Time { return now }
	l.e.RatingDraw = always(true)
	stub := l.jev(t, 200, levels(nil))
	high := l.study(t, "hypotheses", true, nil)
	l.finish(t, high.ID)

	now = now.Add(8 * 24 * time.Hour)
	stub.mu.Lock()
	stub.score = levels(map[string]float64{"Does it say what it couldn't do?": 2})
	stub.mu.Unlock()
	low := l.study(t, "hypotheses", true, nil)
	l.finish(t, low.ID)
	l.finish(t, low.ID)
	if got := l.card(t, low.ID); got.State != "blocked" {
		t.Fatalf("the low study is %s, want blocked", got.State)
	}
	for name, id := range map[string]uuid.UUID{"high": high.ID, "blocked": low.ID} {
		if all, offered := l.draws(t, id); all != 1 || offered != 1 {
			t.Fatalf("%s study: %d draws, %d offered", name, all, offered)
		}
	}
}

func TestTheCooldownHoldsForSevenDays(t *testing.T) {
	l := newLab(t)
	now := time.Now()
	l.e.Now = func() time.Time { return now }
	l.e.RatingDraw = always(true)
	l.jev(t, 200, levels(nil))
	first := l.study(t, "hypotheses", true, nil)
	l.finish(t, first.ID)
	now = now.Add(7*24*time.Hour - time.Minute)
	second := l.study(t, "hypotheses", true, nil)
	l.finish(t, second.ID)
	now = now.Add(2 * time.Minute)
	third := l.study(t, "hypotheses", true, nil)
	l.finish(t, third.ID)
	for i, want := range []int{1, 0, 1} {
		id := []uuid.UUID{first.ID, second.ID, third.ID}[i]
		if all, offered := l.draws(t, id); all != 1 || offered != want {
			t.Fatalf("study %d: %d draws, %d offered, want %d", i+1, all, offered, want)
		}
	}
}

func TestAMissIsRecordedAndNotShown(t *testing.T) {
	l := newLab(t)
	l.jev(t, 200, levels(nil))
	l.e.RatingDraw = always(false)
	c := l.study(t, "hypotheses", true, nil)
	l.finish(t, c.ID)
	if all, offered := l.draws(t, c.ID); all != 1 || offered != 0 {
		t.Fatalf("%d draws, %d offered", all, offered)
	}
}

func TestARedoneColumnDrawsOnce(t *testing.T) {
	l := newLab(t)
	l.e.RatingDraw = always(true)
	stub := l.jev(t, 200, levels(map[string]float64{"Does it say what it couldn't do?": 2}))
	c := l.study(t, "hypotheses", true, nil)
	l.finish(t, c.ID)
	if all, _ := l.draws(t, c.ID); all != 0 {
		t.Fatalf("the first low score, which redoes the column, drew %d", all)
	}
	l.finish(t, c.ID)
	if got := l.card(t, c.ID); got.State != "blocked" {
		t.Fatalf("the study is %s, want blocked", got.State)
	}
	stub.mu.Lock()
	stub.score = levels(nil)
	stub.mu.Unlock()
	l.e.OnComment(l.ws, c.ID, l.owner, "Say what the data could not show.")
	l.finish(t, c.ID)
	if got := l.card(t, c.ID); got.State != "done" {
		t.Fatalf("the redone study is %s, want done", got.State)
	}
	if all, _ := l.draws(t, c.ID); all != 1 {
		t.Fatalf("%d draws for one column", all)
	}
}

func TestAColumnWithoutATeamNeverDraws(t *testing.T) {
	l := newLab(t)
	l.jev(t, 200, levels(nil))
	l.e.RatingDraw = always(true)
	a := l.agent(t, "Curie", "claude", "", "You are Curie.")
	c := l.study(t, "hypotheses", false, &a)
	l.done(t, c.ID)
	if got := l.card(t, c.ID); got.State != "done" {
		t.Fatalf("the study did not finish: %s", got.State)
	}
	if all, _ := l.draws(t, c.ID); all != 0 {
		t.Fatalf("%d draws without a Team", all)
	}
}

func TestScoringUnavailableDrawsNothing(t *testing.T) {
	l := newLab(t)
	l.jev(t, 503, nil)
	l.e.RatingDraw = always(true)
	c := l.study(t, "hypotheses", true, nil)
	l.finish(t, c.ID)
	if got := l.card(t, c.ID); got.State != "working" {
		t.Fatalf("the study moved without Jev: %s", got.State)
	}
	if all, _ := l.draws(t, c.ID); all != 0 {
		t.Fatalf("%d draws without a score", all)
	}
}

func TestTheDefaultDrawHitsAboutOneInFive(t *testing.T) {
	hits := 0
	for range 10000 {
		if defaultRatingDraw() {
			hits++
		}
	}
	if hits < 1800 || hits > 2200 {
		t.Fatalf("%d hits in 10000", hits)
	}
}

func TestAFailedDrawStillMovesTheStudy(t *testing.T) {
	l := newLab(t)
	l.jev(t, 200, levels(nil))
	l.e.RatingDraw = always(true)
	if _, err := l.env.Owner.Exec(context.Background(), "REVOKE INSERT ON team_rating FROM lykeion_app"); err != nil {
		t.Fatal(err)
	}
	c := l.study(t, "hypotheses", true, nil)
	l.finish(t, c.ID)
	if got := l.card(t, c.ID); got.State != "done" {
		t.Fatalf("a failed rating draw left the study %s %s", got.BoardColumn, got.State)
	}
	if n := l.count(t, "SELECT count(*) FROM record WHERE card_id = $1 AND kind = 'outcome'", c.ID); n != 1 {
		t.Fatalf("%d outcome records", n)
	}
}
