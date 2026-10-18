package flow

import (
	"context"
	"strings"
	"testing"

	"lykeion/server/internal/store"
	db "lykeion/server/pkg/db/generated"
)

func TestASummaryMayCiteACallAsCallKeyOrAsKey(t *testing.T) {
	l := newLab(t)
	l.jev(t, 200, levels(nil))
	a := l.agent(t, "Curie", "claude", "", "You are Curie.")
	for _, cite := range []string{"call:c1", "c1"} {
		c := l.scoped(t, "grounding", "grounding", a)
		l.in(t, func(tx *store.Tx) error {
			_, err := tx.Q.InsertRecord(context.Background(), db.InsertRecordParams{WorkspaceID: l.ws, CardID: c.ID, BoardColumn: "grounding",
				Round: c.Round, Kind: "call", Role: "agent", Key: "c1", Body: []byte(`{}`)})
			return err
		})
		if err := l.declare(t, c.ID, "call:c9"); err == nil || !strings.Contains(err.Error(), "call:c9") {
			t.Fatalf("an unknown call: %v", err)
		}
		if err := l.declare(t, c.ID, cite); err != nil {
			t.Fatalf("%s refused: %v", cite, err)
		}
	}
}

func TestACallCitedOnlyInTheSummaryReachesTheScorer(t *testing.T) {
	l := newLab(t)
	js := l.jev(t, 200, levels(nil))
	a := l.agent(t, "Curie", "claude", "", "You are Curie.")
	c := l.scoped(t, "grounding", "grounding", a)
	l.in(t, func(tx *store.Tx) error {
		for _, r := range []db.InsertRecordParams{
			{Kind: "call", Key: "c1", Body: []byte(`{"answer":"MARKER-FROM-THE-CALL"}`)},
			{Kind: "evidence", Key: "r3", Body: []byte(`{"claim":"x"}`)},
		} {
			r.WorkspaceID, r.CardID, r.BoardColumn, r.Round, r.Role = l.ws, c.ID, "grounding", c.Round, "agent"
			if _, err := tx.Q.InsertRecord(context.Background(), r); err != nil {
				return err
			}
		}
		return nil
	})
	if err := l.declare(t, c.ID, "call:r3"); err == nil {
		t.Fatal("call:r3 passed for a call")
	}
	l.done(t, c.ID, "call:c1")
	var seen bool
	for _, r := range js.calls() {
		seen = seen || strings.Contains(r.State, "MARKER-FROM-THE-CALL")
	}
	if !seen {
		t.Fatalf("the cited call never reached Jev: %d requests", len(js.calls()))
	}
}
