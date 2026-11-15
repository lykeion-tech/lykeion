package flow

import (
	"context"
	"testing"

	"lykeion/server/internal/review"
)

func TestFailedCallIsNotHypothesisEvidence(t *testing.T) {
	l := newLab(t)
	a := l.agent(t, "Curie", "claude", "", "")
	c := l.study(t, "hypotheses", false, &a)
	l.item(t, c.ID, "call", map[string]any{"answer": "false evidence", "error": "429"})
	l.item(t, c.ID, review.KindHypothesis, map[string]any{"text": "Hypothesis", "cites": []string{"call:r1"}})
	rv, err := l.e.readReview(context.Background(), l.ws, c)
	if err != nil {
		t.Fatal(err)
	}
	if len(rv.cited) != 0 {
		t.Fatalf("failed call counted as evidence: %+v", rv.cited)
	}
}
