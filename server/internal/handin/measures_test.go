package handin

import (
	"testing"

	"lykeion/server/internal/board"
	"lykeion/server/internal/review"
	db "lykeion/server/pkg/db/generated"
)

func TestAcceptedMeasuresUseTheCarriedDeclaration(t *testing.T) {
	original := rec(board.Hypotheses, review.KindHypothesis, "h1", map[string]any{
		"text":    "Treatment improves accuracy",
		"measure": review.Measure{ID: "accuracy", Name: "Accuracy", Unit: "percent"},
	})
	original.Role, original.Round = "leader", 1
	edit := rec(board.Hypotheses, review.KindHypothesis, "h2", map[string]any{
		"text":    "Treatment  improves accuracy",
		"measure": review.Measure{ID: "accuracy", Name: "Edited accuracy", Unit: "fraction"},
	})
	edit.Role, edit.Round = "leader", 2
	result := rec(board.Hypotheses, "review", "review", map[string]any{
		"decision": review.DecideCarry,
		"items":    []review.Item{{Kind: review.KindHypothesis, Key: original.Key, Status: review.Unclear, Carried: true}},
	})
	result.Role, result.Round = "server", 2
	outcome := rec(board.Hypotheses, "outcome", "outcome", map[string]any{"verdict": review.DecideCarry})
	outcome.Role, outcome.Round = "server", 2

	study := []db.Record{original, edit, result, outcome}
	want := review.Measure{ID: "accuracy", Name: "Accuracy", Unit: "percent"}
	if got := AcceptedMeasures(study); len(got) != 1 || got[want.ID] != want {
		t.Fatalf("accepted measures = %+v, want %+v", got, want)
	}
}

func TestAcceptedMeasuresExcludeWithdrawnDeclaration(t *testing.T) {
	hypothesis := rec(board.Hypotheses, review.KindHypothesis, "h1", map[string]any{
		"text":    "Treatment improves accuracy",
		"measure": review.Measure{ID: "accuracy", Name: "Accuracy", Unit: "percent"},
	})
	hypothesis.Role, hypothesis.Round = "leader", 1
	result := rec(board.Hypotheses, "review", "review", map[string]any{
		"decision": review.DecidePass,
		"items":    []review.Item{{Kind: review.KindHypothesis, Key: hypothesis.Key, Status: review.Pass}},
	})
	result.Role, result.Round = "server", 1
	outcome := rec(board.Hypotheses, "outcome", "outcome", map[string]any{"verdict": review.DecidePass})
	outcome.Role, outcome.Round = "server", 1
	withdrawal := rec(board.Hypotheses, review.KindWithdrawn, "w1", map[string]any{"item": hypothesis.Key})
	withdrawal.Role, withdrawal.Round = "leader", 2
	if got := AcceptedMeasures([]db.Record{hypothesis, result, outcome, withdrawal}); len(got) != 0 {
		t.Fatalf("accepted measures after withdrawal = %+v", got)
	}
}
