package flow

import (
	"strings"
	"testing"

	"lykeion/server/internal/review"
)

func TestHypothesisLineEstimate(t *testing.T) {
	e := review.HypothesisBody{Text: "PR at 65.", Prediction: review.Prediction{Shape: "estimate"},
		Measure: &review.Measure{ID: "prob_pr_65", Name: "PR probability at 65", Unit: "fraction"}, Method: "logistic regression on age"}
	want := "PR at 65. Estimates PR probability at 65 by logistic regression on age. Measure prob_pr_65: PR probability at 65 (fraction)."
	if got := hypothesisLine(e, nil); got != want {
		t.Errorf("got  %q\nwant %q", got, want)
	}
	e.Reported = &review.ReportedValue{Value: 0.39, Source: "doi:10.1/a", Key: "r2"}
	if got := hypothesisLine(e, nil); !strings.Contains(got, " Published: 0.39 (doi:10.1/a).") {
		t.Errorf("published value missing: %q", got)
	}
	lo, hi := 0.5, 0.8
	r := review.HypothesisBody{Text: "h", Prediction: review.Prediction{Shape: "range", Low: &lo, High: &hi, Uncertainty: "90%"},
		Control: review.Control{Text: "c"}, Measure: &review.Measure{ID: "m", Name: "M", Unit: "u"}}
	if got := hypothesisLine(r, nil); got != "h Predicts 0.5 to 0.8 (90%). Measure m: M (u). Control: c." {
		t.Errorf("range line changed: %q", got)
	}
}

func TestHypothesesPromptExplainsEstimates(t *testing.T) {
	p := Protocol("leader", "hypotheses", "", nil)
	for _, w := range []string{"`estimate`", "only when the question asks for a quantity", "Never invent a predicted range"} {
		if !strings.Contains(p, w) {
			t.Errorf("hypotheses protocol lacks %q", w)
		}
	}
	if p := Protocol("leader", "validation", "", nil); !strings.Contains(p, "For an estimate, name only `measurement`") {
		t.Error("validation protocol does not say how to assess an estimate")
	}
}
