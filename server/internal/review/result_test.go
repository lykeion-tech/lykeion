package review

import "testing"

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

const redoAnswer = "Items not listed stand. Answer each listed item: declare a corrected version, or withdraw it with " +
	"`lyk record put withdrawn --content '{\"item\":\"rN\",\"reason\":\"…\"}'`. " +
	"Items you leave unanswered count as failed, and a second failure blocks the study."

func TestRedoNoteListsOnlyFailures(t *testing.T) {
	bad := it("bad", Fail, Check{ID: "exists", Status: Pass}, Check{ID: "falsifiable", Status: Fail})
	bad.Key = "r4"
	r := Result{Items: []Item{it("good", Pass, Check{ID: "exists", Status: Pass}), bad}}
	want := "Jev's review sent back these items:\n- bad (r4): falsifiable\n" + redoAnswer
	if got := RedoNote(r); got != want {
		t.Errorf("got %q", got)
	}
}

func TestRedoNoteOffersMissedPapers(t *testing.T) {
	r := Result{Missed: []Item{it("doi:10.1/m", Fail, Check{ID: "criterion:1", Status: Pass}, Check{ID: "missed", Status: Fail})}}
	want := "Jev's review sent back these items:\n- doi:10.1/m: consider this paper (it meets every criterion but was not cited)\n" + redoAnswer
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
