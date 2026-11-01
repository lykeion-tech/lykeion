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
		{"warning only", Result{Items: []Item{{Label: "a", Status: ItemStatus([]Check{{ID: "w", Status: Fail, Warning: true}})}}}, 0, DecidePass},
	}
	for _, c := range cases {
		if got := Decide(c.r, c.redos); got != c.want {
			t.Errorf("%s: %s want %s", c.name, got, c.want)
		}
	}
}

func TestRedoNoteListsOnlyFailures(t *testing.T) {
	r := Result{Items: []Item{
		it("good", Pass, Check{ID: "exists", Status: Pass}),
		it("bad", Fail, Check{ID: "exists", Status: Pass}, Check{ID: "falsifiable", Status: Fail}),
	}}
	want := "Jev's review sent back these items:\n- bad: falsifiable\nRedo only these. Items not listed stand."
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
