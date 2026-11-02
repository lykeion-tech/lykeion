package review

import (
	"strings"
	"testing"

	"lykeion/server/internal/board"
)

func TestValidateListsEveryProblem(t *testing.T) {
	body := `{"text":"t","prediction":{"shape":"direction","direction":"increase"},"control":{},"cites":[]}`
	err := Validate(KindHypothesis, board.Hypotheses, []byte(body))
	if err == nil || !strings.Contains(err.Error(), "control") || !strings.Contains(err.Error(), "cites") || !strings.Contains(err.Error(), "; ") {
		t.Fatalf("want both problems joined with \"; \", got %v", err)
	}
}

func TestReasonsNeverEmpty(t *testing.T) {
	p := 0.2
	failed := it("bad", Fail, Check{ID: "falsifiable", By: "jev", Question: "Can it be proven wrong?", P: &p, Status: Fail})
	server := it("ghost", Fail, Check{ID: "exists", By: "server", Detail: "no such paper", Status: Fail})
	cases := []struct {
		name string
		r    Result
	}{
		{"failed items", Result{Column: board.Hypotheses, Items: []Item{failed}}},
		{"server-only failure", Result{Column: board.Grounding, Items: []Item{server}}},
		{"missed only", Result{Column: board.Grounding, Items: []Item{it("a", Pass)}, Missed: []Item{it("arxiv:2106.01234", Fail)}}},
		{"no items grounding", Result{Column: board.Grounding}},
		{"no items hypotheses", Result{Column: board.Hypotheses}},
		{"no items experimentation", Result{Column: board.Experimentation}},
		{"only withdrawn", Result{Column: board.Grounding, Items: []Item{it("w", Withdrawn)}}},
		{"failed item without a failed check", Result{Column: board.Grounding, Items: []Item{it("odd", Fail)}}},
	}
	for _, c := range cases {
		rs := Reasons(c.r)
		if len(rs) == 0 || rs[0] == "" {
			t.Errorf("%s: no reasons", c.name)
			continue
		}
		if !strings.Contains(RedoNote(c.r), rs[0]) {
			t.Errorf("%s: redo note lacks %q", c.name, rs[0])
		}
	}
	if got := Reasons(Result{Column: board.Grounding}); got[0] != "The Team handed in no papers and no 'nothing qualifies' statement." {
		t.Errorf("grounding: %q", got)
	}
	if got := Reasons(Result{Column: board.Hypotheses}); got[0] != "The Team handed in no hypotheses." {
		t.Errorf("hypotheses: %q", got)
	}
	if got := Reasons(Result{Column: board.Experimentation}); got[0] != "The Team handed in no outputs or values." {
		t.Errorf("experimentation: %q", got)
	}
	if got := Reasons(Result{Items: []Item{it("a", Pass)}}); len(got) != 0 {
		t.Errorf("a passing review has reasons: %q", got)
	}
}

func TestWithdrawalReasonsNeverBlank(t *testing.T) {
	w := func(detail string) Item {
		return Item{Status: Withdrawn, Checks: []Check{{ID: "withdrawn", Detail: detail, Status: Withdrawn}}}
	}
	r := Result{Column: board.Hypotheses, Items: []Item{w("too weak"), w("  "), {Status: Withdrawn}}}
	if !AllWithdrawn(r) {
		t.Fatal("not all withdrawn")
	}
	got := strings.Join(WithdrawalReasons(r), "|")
	if got != "too weak|no reason given|no reason given" {
		t.Fatal(got)
	}
	if AllWithdrawn(Result{Column: board.Validation, Items: r.Items}) {
		t.Fatal("validation has no item review")
	}
}

func TestOpenDoubtsShowAnInvertedCheckAsYes(t *testing.T) {
	p := 0.48
	r := Result{Items: []Item{{Label: "h1", Status: Unclear, Checks: []Check{{ID: "q", Question: "Is it known?", Inverted: true, P: &p, Status: Unclear}}}}}
	if got := OpenDoubts(r); len(got) != 1 || got[0] != "h1 (Is it known?, 52%)" {
		t.Fatal(got)
	}
}
