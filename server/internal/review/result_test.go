package review

import (
	"strings"
	"testing"
)

func it(label, status string, cs ...Check) Item {
	return Item{Label: label, Status: status, Checks: cs}
}

func TestStatusThresholds(t *testing.T) {
	for p, want := range map[float64]string{0.70: Pass, 0.6999: Unclear, 0.35: Unclear, 0.3499: Fail} {
		if got := StatusOf(p); got != want {
			t.Errorf("StatusOf(%v)=%s want %s", p, got, want)
		}
	}
}

func TestDecide(t *testing.T) {
	ok := it("a", Pass)
	cases := []struct {
		name  string
		r     Result
		redos int32
		want  string
	}{
		{"no items", Result{}, 0, DecideRedo},
		{"no items again", Result{}, 1, DecideBlock},
		{"failed", Result{Items: []Item{ok, it("b", Fail)}}, 0, DecideRedo},
		{"missed", Result{Items: []Item{ok}, Missed: []Item{it("p", Fail)}}, 0, DecideRedo},
		{"unclear", Result{Items: []Item{ok, it("b", Unclear)}}, 0, DecideCarry},
		{"pass", Result{Items: []Item{ok}}, 0, DecidePass},
		{"withdrawn neither passes nor fails", Result{Items: []Item{ok, it("b", Withdrawn)}}, 0, DecidePass},
		{"only withdrawn", Result{Items: []Item{it("b", Withdrawn)}}, 1, DecideBlock},
		{"warning only", Result{Items: []Item{{Label: "a", Status: ItemStatus([]Check{{ID: "w", Status: Fail, Warning: true}})}}}, 0, DecidePass},
	}
	for _, c := range cases {
		if got := Decide(c.r, c.redos); got != c.want {
			t.Errorf("%s: %s want %s", c.name, got, c.want)
		}
	}
}

const redoAnswer = "Items not listed stand. In your hand-in's replies, answer each listed item by the name shown: corrected (it is in this hand-in again, fixed, with the same id or text), " +
	"withdrawn (it no longer stands) or, for a paper the review found missing, declined, each with a reason. " +
	"A replacement with a different id or text is a new item, so also withdraw the old one. " +
	"Items you leave unanswered count as failed, and a second failure blocks the study."

func TestRedoNoteListsOnlyFailures(t *testing.T) {
	bad := it("bad", Fail, Check{ID: "exists", Status: Pass}, Check{ID: "falsifiable", Status: Fail})
	bad.Key = "r4"
	r := Result{Items: []Item{it("good", Pass, Check{ID: "exists", Status: Pass}), bad}}
	want := "The review sent back these items:\n- bad: falsifiable check failed\n" + redoAnswer
	if got := RedoNote(r); got != want {
		t.Errorf("got %q", got)
	}
}

func TestRedoNoteShowsJevsOwnAnswerForInvertedChecks(t *testing.T) {
	p := 0.1 // stored after inversion: Jev answered yes at 0.9
	bad := it("bad", Fail, Check{ID: "lookup", By: "jev", Question: "Does it already contain the answer?", P: &p, Inverted: true, Status: Fail})
	want := "The review sent back these items:\n- bad: \"Does it already contain the answer?\" (Jev answered yes at 90%, and a yes fails this check)\n" + redoAnswer
	if got := RedoNote(Result{Items: []Item{bad}}); got != want {
		t.Errorf("got %q", got)
	}
}

func TestRedoNoteShowsQuestionsAndDetails(t *testing.T) {
	p := 0.126
	bad := it("bad", Fail,
		Check{ID: "supported", By: "jev", Question: "Does the cited evidence support making this prediction?", P: &p, Status: Fail},
		Check{ID: "cited", By: "server", Detail: "no citation was found", Status: Fail},
		Check{ID: "ok", By: "jev", Question: "fine?", Status: Pass})
	bad.Key = "r4"
	want := "The review sent back these items:\n" +
		"- bad: \"Does the cited evidence support making this prediction?\" (Jev scored 13%)\n" +
		"- bad: cited: no citation was found\n" + redoAnswer
	if got := RedoNote(Result{Items: []Item{bad}}); got != want {
		t.Errorf("got %q", got)
	}
}

func TestRedoNoteOffersMissedPapers(t *testing.T) {
	r := Result{Items: []Item{it("a", Pass)}, Missed: []Item{it("doi:10.1/m", Fail, Check{ID: "criterion:1", Status: Pass}, Check{ID: "missed", Status: Fail})}}
	want := "The review sent back these items:\n- doi:10.1/m: consider this paper (it meets every criterion but was not cited)\n" + redoAnswer
	if got := RedoNote(r); got != want {
		t.Errorf("got %q", got)
	}
}

func TestCarryNote(t *testing.T) {
	if CarryNote(Result{Column: "grounding", Items: []Item{it("a", Pass)}}) != "" {
		t.Error("want empty")
	}
	r := Result{Column: "grounding", Items: []Item{it("a", Unclear, Check{ID: "criterion:2", Status: Unclear})}}
	want := "Unclear in Grounding (carried forward, check them as you work):\n- a: criterion:2"
	if got := CarryNote(r); got != want {
		t.Errorf("got %q", got)
	}
}

func TestCarryNoteIncludesWarnings(t *testing.T) {
	r := Result{Column: "experimentation", Items: []Item{
		it("run", Pass, Check{ID: "seed", Status: Fail, Warning: true}, Check{ID: "ok", Status: Pass, Warning: true}),
	}}
	got := CarryNote(r)
	if got != "Unclear in Experimentation (carried forward, check them as you work):\n- run: seed" {
		t.Errorf("got %q", got)
	}
	if Decide(r, 0) != DecidePass {
		t.Error("warning must not affect Decide")
	}
}

// Live (bix-29): a direction prediction for an odds-ratio question failed
// "supported", and the redo withdrew it instead of turning it into an
// estimate, which blocked the study. The redo note now says so.
func TestRedoNotePointsUnsupportedPredictionsToAnEstimate(t *testing.T) {
	unsupported := Result{Column: "hypotheses", Items: []Item{{Key: "h1", Kind: KindHypothesis, Label: "BCG lowers severity", Status: Fail,
		Checks: []Check{{ID: "supported", By: "jev", Status: Fail, Question: "Does the cited evidence support making this prediction?"}}}}}
	if n := RedoNote(unsupported); !strings.Contains(n, "`estimate`") || !strings.Contains(n, "instead of withdrawing") {
		t.Fatalf("no estimate hint:\n%s", n)
	}
	other := Result{Column: "hypotheses", Items: []Item{{Key: "h1", Kind: KindHypothesis, Label: "x", Status: Fail,
		Checks: []Check{{ID: "falsifiable", By: "jev", Status: Fail}}}}}
	grounding := Result{Column: "grounding", Items: []Item{{Key: "p1", Kind: KindPaper, Label: "doi:10.1/a", Status: Fail,
		Checks: []Check{{ID: "supported", By: "jev", Status: Fail}}}}}
	for _, r := range []Result{other, grounding} {
		if strings.Contains(RedoNote(r), "`estimate`") {
			t.Fatalf("estimate hint where it does not apply:\n%s", RedoNote(r))
		}
	}
}
