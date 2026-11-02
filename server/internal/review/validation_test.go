package review

import (
	"strings"
	"testing"

	"lykeion/server/internal/statcheck"
)

func fp(f float64) *float64 { return &f }
func sp(s string) *string   { return &s }

func good(v float64) Measured {
	return Measured{Value: v, FromScript: true, Reproduced: true, Key: "v", RerunKey: "r"}
}

func dirHyp(dir string) HypothesisBody {
	return HypothesisBody{Text: "h", Prediction: Prediction{Shape: "direction", Direction: dir}, Control: Control{Text: "baseline"}}
}

// sigT is t(28)=2.20 reported as p = .04 (consistent: p is about .036).
func sigT() *statcheck.Test {
	return &statcheck.Test{Kind: "t", Statistic: 2.20, StatDigits: 2, DF1: 28, P: 0.04, PDigits: 2, PBound: "="}
}
func nsT() *statcheck.Test {
	return &statcheck.Test{Kind: "t", Statistic: 1.0, StatDigits: 2, DF1: 28, P: 0.33, PDigits: 2, PBound: "="}
}

func run(h HypothesisBody, a *Assessment, m map[string]Measured) StatusResult {
	var as []Assessment
	if a != nil {
		as = []Assessment{*a}
	}
	r := Statuses([]HypothesisBody{h}, as, m)
	if len(r) != 1 || len(r[0].Reasons) == 0 {
		panic("one result with reasons expected")
	}
	return r[0]
}

func TestStatusesInvalidFirst(t *testing.T) {
	h := dirHyp("increase")
	ok := map[string]Measured{"m": good(2), "c": good(1)}
	a := func(test *statcheck.Test) *Assessment {
		return &Assessment{Hypothesis: " h ", Measurement: "m", Control: sp("c"), Test: test}
	}
	if r := run(h, nil, ok); r.Status != Invalid || !strings.Contains(r.Reasons[0], "not assessed") {
		t.Fatalf("no assessment: %+v", r)
	}
	notRep := good(2)
	notRep.Reproduced = false
	if r := run(h, a(sigT()), map[string]Measured{"m": notRep, "c": good(1)}); r.Status != Invalid {
		t.Fatalf("not reproduced: %+v", r)
	}
	if r := run(h, a(sigT()), map[string]Measured{"m": good(2), "c": notRep}); r.Status != Invalid {
		t.Fatalf("control not reproduced: %+v", r)
	}
	noScript := good(2)
	noScript.FromScript = false
	if r := run(h, a(sigT()), map[string]Measured{"m": noScript, "c": good(1)}); r.Status != Invalid {
		t.Fatalf("not from script: %+v", r)
	}
	bad := &statcheck.Test{Kind: "t", Statistic: 2.20, StatDigits: 2, DF1: 28, P: 0.01, PDigits: 2, PBound: "="}
	r := run(h, a(bad), ok)
	if r.Status != Invalid || !strings.Contains(r.Reasons[0], "reported p 0.01 does not match t(28)=2.2, p≈0.036") {
		t.Fatalf("inconsistent: %+v", r)
	}
	if got := run(h, a(sigT()), ok); got.Status != Supported || len(got.Cites) != 4 {
		t.Fatalf("cites: %+v", got)
	}
}

func TestStatusesRange(t *testing.T) {
	h := HypothesisBody{Text: "h", Prediction: Prediction{Shape: "range", Low: fp(0.5), High: fp(0.8)}, Control: Control{Text: "c"}}
	for v, want := range map[float64]string{0.62: Supported, 0.5: Supported, 0.8: Supported, 0.9: Refuted} {
		r := run(h, &Assessment{Hypothesis: "h", Measurement: "m"}, map[string]Measured{"m": good(v)})
		if r.Status != want {
			t.Fatalf("%v: %+v", v, r)
		}
	}
}

func TestStatusesDirection(t *testing.T) {
	cases := []struct {
		dir  string
		m, c float64
		test *statcheck.Test
		want string
	}{
		{"increase", 2, 1, sigT(), Supported},
		{"increase", 1, 2, sigT(), Refuted},
		{"increase", 2, 1, nsT(), Inconclusive},
		{"increase", 2, 1, nil, Inconclusive},
		{"decrease", 1, 2, sigT(), Supported},
		{"decrease", 2, 1, sigT(), Refuted},
		{"no_change", 2, 1, sigT(), Refuted},
		{"no_change", 2, 1, nsT(), Inconclusive},
	}
	for _, c := range cases {
		r := run(dirHyp(c.dir), &Assessment{Hypothesis: "h", Measurement: "m", Control: sp("c"), Test: c.test},
			map[string]Measured{"m": good(c.m), "c": good(c.c)})
		if r.Status != c.want {
			t.Errorf("%+v: got %+v", c, r)
		}
	}
}

func TestStatusesOutcomes(t *testing.T) {
	h := HypothesisBody{Text: "h", Prediction: Prediction{Shape: "outcomes", Outcomes: []string{"A", "B"}, Chosen: "A"}, Control: Control{Text: "c"}}
	for obs, want := range map[string]string{" a ": Supported, "B": Refuted, "C": Inconclusive} {
		r := run(h, &Assessment{Hypothesis: "h", Measurement: "m", Observed: sp(obs)}, map[string]Measured{"m": good(1)})
		if r.Status != want {
			t.Fatalf("%q: %+v", obs, r)
		}
	}
	if r := run(h, &Assessment{Hypothesis: "h", Measurement: "m"}, map[string]Measured{"m": good(1)}); r.Status != Inconclusive {
		t.Fatalf("nil observed: %+v", r)
	}
}

func TestStatusesControlNoneCaps(t *testing.T) {
	h := HypothesisBody{Text: "h", Prediction: Prediction{Shape: "range", Low: fp(0), High: fp(1)}, Control: Control{None: "nothing to compare"}}
	r := run(h, &Assessment{Hypothesis: "h", Measurement: "m"}, map[string]Measured{"m": good(0.5)})
	if r.Status != Caveated || r.Reasons[len(r.Reasons)-1] != "no control: nothing to compare" {
		t.Fatalf("%+v", r)
	}
	r = run(h, &Assessment{Hypothesis: "h", Measurement: "m"}, map[string]Measured{"m": good(5)})
	if r.Status != Refuted {
		t.Fatalf("%+v", r)
	}
}

func TestLower(t *testing.T) {
	all := []string{Supported, Caveated, Inconclusive, Refuted, Invalid}
	rank := map[string]int{Invalid: 0, Inconclusive: 1, Refuted: 1, Caveated: 2, Supported: 3}
	for _, ty := range []string{"wrong_test", "leakage", "unit_mismatch", "missing_control"} {
		for _, s := range all {
			if Lower(s, ty) != Invalid {
				t.Fatalf("%s %s", ty, s)
			}
		}
	}
	for s, want := range map[string]string{Supported: Caveated, Caveated: Inconclusive, Refuted: Inconclusive, Invalid: Invalid, Inconclusive: Inconclusive} {
		if got := Lower(s, "overreach"); got != want {
			t.Fatalf("overreach %s: %s", s, got)
		}
	}
	for _, ty := range []string{"wrong_test", "overreach", "other", ""} {
		for _, s := range all {
			if rank[Lower(s, ty)] > rank[s] {
				t.Fatalf("raised %s %s", ty, s)
			}
		}
	}
}
