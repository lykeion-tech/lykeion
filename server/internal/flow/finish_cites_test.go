package flow

import (
	"context"
	"encoding/json"
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

func TestASummaryCitingAHugeCallIsStillScored(t *testing.T) {
	l := newLab(t)
	js := l.jev(t, 200, levels(nil))
	js.maxState = 40 << 10
	a := l.agent(t, "Curie", "claude", "", "You are Curie.")
	c := l.scoped(t, "grounding", "hypotheses", a)
	huge := `{"answer":"` + strings.Repeat(`{\"id\":\"P12345\",\"score\":0.91},`, 7000) + `"}`
	l.in(t, func(tx *store.Tx) error {
		_, err := tx.Q.InsertRecord(context.Background(), db.InsertRecordParams{WorkspaceID: l.ws, CardID: c.ID, BoardColumn: "grounding",
			Round: c.Round, Kind: "call", Role: "agent", Key: "c1", Body: []byte(huge)})
		return err
	})
	l.done(t, c.ID, "call:c1")
	got := l.card(t, c.ID)
	if got.BoardColumn != "hypotheses" {
		t.Fatalf("a too-long state left the study unscored in %s (%d Jev requests)", got.BoardColumn, len(js.calls()))
	}
	if items := l.inbox(t, c.ID, "scoring_unavailable"); len(items) != 0 {
		t.Fatalf("scoring_unavailable items: %v", items)
	}
}

func TestASummaryMayCiteARun(t *testing.T) {
	l := newLab(t)
	l.jev(t, 200, levels(nil))
	a := l.agent(t, "Curie", "claude", "", "You are Curie.")
	c := l.scoped(t, "grounding", "grounding", a)
	l.in(t, func(tx *store.Tx) error {
		_, err := tx.Q.InsertRecord(context.Background(), db.InsertRecordParams{WorkspaceID: l.ws, CardID: c.ID, BoardColumn: "grounding",
			Round: c.Round, Kind: "run", Role: "agent", Key: "x1", Body: []byte(`{}`)})
		return err
	})
	if err := l.declare(t, c.ID, "run:x9"); err == nil {
		t.Fatal("an unknown run passed")
	}
	if err := l.declare(t, c.ID, "run:x1"); err != nil {
		t.Fatalf("run:x1 refused: %v", err)
	}
	var got []byte
	l.in(t, func(tx *store.Tx) error {
		recs, err := tx.Q.ListRecords(context.Background(), db.ListRecordsParams{WorkspaceID: l.ws, CardID: c.ID, BoardColumn: "grounding"})
		for _, r := range recs {
			if r.Kind == "summary" {
				got = r.Body
			}
		}
		return err
	})
	var body struct{ Cites []string }
	if json.Unmarshal(got, &body); len(body.Cites) != 1 || body.Cites[0] != "x1" {
		t.Fatalf("summary %s", got)
	}
}
