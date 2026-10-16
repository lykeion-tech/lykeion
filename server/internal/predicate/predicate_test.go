package predicate

import (
	"fmt"
	"math"
	"strings"
	"testing"
)

func must(t *testing.T, s string) *Pred {
	t.Helper()
	p, err := Parse(s)
	if err != nil {
		t.Fatalf("%q: %v", s, err)
	}
	return p
}

func TestParseRefuses(t *testing.T) {
	for _, s := range []string{"", "effect == 0", "effect = 0", "Effect > 0", "effect > x", "effect >", "0 < effect",
		"effect > 0 and", "(effect > 0", "effect > 0)", "effect > inf", "effect > NaN", "effect > 1e400", "and > 0",
		strings.Repeat("(", 40) + "effect > 0" + strings.Repeat(")", 40), strings.Repeat("effect > 0 and ", 40) + "effect > 0"} {
		if _, err := Parse(s); err == nil {
			t.Errorf("%q parsed", s)
		}
	}
}

func TestEval(t *testing.T) {
	p := must(t, "effect > 0.5 and not (ci_low <= 0 or ci_high > 3)")
	for r, want := range map[[3]float64]bool{{1, 0.1, 2}: true, {1, 0, 2}: false, {0.5, 0.1, 2}: false, {1, 0.1, 4}: false} {
		got, err := p.Eval(map[string]float64{"effect": r[0], "ci_low": r[1], "ci_high": r[2]})
		if err != nil || got != want {
			t.Errorf("%v: got %v %v, want %v", r, got, err, want)
		}
	}
	if _, err := p.Eval(map[string]float64{"effect": 1}); err == nil {
		t.Fatal("a missing key must be an error, never false")
	}
	if k := strings.Join(p.Keys(), ","); k != "ci_high,ci_low,effect" {
		t.Fatal(k)
	}
}

func TestJointFindsBoundaryOverlap(t *testing.T) {
	a, b := must(t, "effect >= 0.5"), must(t, "effect <= 0.5")
	pt, ok, err := Joint(nil, a, b)
	if err != nil || !ok || pt["effect"] != 0.5 {
		t.Fatalf("%v %v %v", pt, ok, err)
	}
	if _, ok, _ := Joint(nil, must(t, "effect > 0.5"), b); ok {
		t.Fatal("a strict and a non-strict bound at one threshold do not overlap")
	}
}

func TestJointRespectsOrder(t *testing.T) {
	ord := []string{"ci_low", "effect", "ci_high"}
	a, b := must(t, "ci_low > 0.2"), must(t, "ci_high < 0.2")
	if _, ok, _ := Joint(nil, a, b); !ok {
		t.Fatal("unordered keys are independent")
	}
	if pt, ok, _ := Joint(ord, a, b); ok {
		t.Fatalf("found an outcome with ci_low above ci_high: %v", pt)
	}
	// An interval inside [-0.2, 0.2] keeps the estimate between its bounds.
	null := must(t, "ci_low >= -0.2 and ci_high <= 0.2")
	if pt, ok, _ := Joint(ord, null, Not(Within("effect", -0.2, 0.2))); ok {
		t.Fatalf("effect escaped an interval that contains it: %v", pt)
	}
	if _, ok, _ := Joint(ord, null); !ok {
		t.Fatal("the null is a possible outcome")
	}
}

func TestJointRefusesHugeSearches(t *testing.T) {
	var ps []*Pred
	for i, k := range []string{"a", "b", "c", "d", "e", "f"} {
		var parts []string
		for j := 0; j < 10; j++ {
			parts = append(parts, fmt.Sprintf("%s > %d", k, i*10+j))
		}
		ps = append(ps, must(t, strings.Join(parts, " or ")))
	}
	if _, _, err := Joint(nil, ps...); err == nil {
		t.Fatal("a search past the probe bound must be refused, not run")
	}
}

func TestJointFindsLargeFiniteInterval(t *testing.T) {
	pt, ok, err := Joint(nil, must(t, "x > 1e308"), must(t, "x < 1.1e308"))
	if err != nil || !ok || !(pt["x"] > 1e308 && pt["x"] < 1.1e308) {
		t.Fatalf("large finite interval witness: %v, %v, %v", pt, ok, err)
	}
	pt, ok, err = Joint(nil, must(t, "x > -1.1e308"), must(t, "x < -1e308"))
	if err != nil || !ok || !(pt["x"] > -1.1e308 && pt["x"] < -1e308) {
		t.Fatalf("large negative finite interval witness: %v, %v, %v", pt, ok, err)
	}
	pt, ok, err = Joint(nil, must(t, "x > 0"), must(t, "x < 1e-323"))
	if err != nil || !ok || !(pt["x"] > 0 && pt["x"] < 1e-323) {
		t.Fatalf("subnormal interval witness: %v, %v, %v", pt, ok, err)
	}
}

func TestJointUsesAdjacentRepresentableFloat(t *testing.T) {
	lo := 1.0
	hi := math.Nextafter(lo, math.Inf(1))
	ps := []*Pred{Within("x", lo, hi)}
	pt, ok, err := Joint(nil, ps...)
	if err != nil || !ok || pt["x"] < lo || pt["x"] > hi {
		t.Fatalf("adjacent finite endpoints: %v, %v, %v", pt, ok, err)
	}
	if _, ok, err := Joint(nil, must(t, "x > 1"), must(t, fmt.Sprintf("x < %.17g", hi))); err != nil || ok {
		t.Fatalf("no representable value between adjacent floats: ok=%v err=%v", ok, err)
	}
}

func TestJointOrderedBoundaryOverlap(t *testing.T) {
	pt, ok, err := Joint([]string{"low", "high"}, must(t, "low >= 0.5"), must(t, "high <= 0.5"))
	if err != nil || !ok || pt["low"] != 0.5 || pt["high"] != 0.5 {
		t.Fatalf("ordered equality boundary: %v, %v, %v", pt, ok, err)
	}
}

func TestValidKey(t *testing.T) {
	for _, key := range []string{"a", "ci_low", "x9", strings.Repeat("a", 32)} {
		if !ValidKey(key) {
			t.Errorf("ValidKey(%q) = false", key)
		}
	}
	for _, key := range []string{"", "9x", "A", "a-b", strings.Repeat("a", 33), "and", "or", "not"} {
		if ValidKey(key) {
			t.Errorf("ValidKey(%q) = true", key)
		}
	}
}
