package review

import (
	"context"
	"strings"
	"testing"

	"lykeion/server/internal/jev"
	"lykeion/server/internal/statcheck"
	db "lykeion/server/pkg/db/generated"
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

func run(t *testing.T, h HypothesisBody, a *Assessment, m map[string]Measured) StatusResult {
	var as []Assessment
	if a != nil {
		as = []Assessment{*a}
	}
	r := Statuses([]HypothesisBody{h}, as, m)
	t.Helper()
	if len(r) != 1 || len(r[0].Reasons) == 0 {
		t.Fatalf("one result with reasons expected, got %+v", r)
	}
	return r[0]
}

func TestStatusesInvalidFirst(t *testing.T) {
	h := dirHyp("increase")
	ok := map[string]Measured{"m": good(2), "c": good(1)}
	a := func(test *statcheck.Test) *Assessment {
		return &Assessment{Hypothesis: " h ", Measurement: "m", Control: sp("c"), Test: test}
	}
	if r := run(t, h, nil, ok); r.Status != Invalid || !strings.Contains(r.Reasons[0], "not assessed") {
		t.Fatalf("no assessment: %+v", r)
	}
	notRep := good(2)
	notRep.Reproduced = false
	if r := run(t, h, a(sigT()), map[string]Measured{"m": notRep, "c": good(1)}); r.Status != Invalid {
		t.Fatalf("not reproduced: %+v", r)
	}
	if r := run(t, h, a(sigT()), map[string]Measured{"m": good(2), "c": notRep}); r.Status != Invalid {
		t.Fatalf("control not reproduced: %+v", r)
	}
	noScript := good(2)
	noScript.FromScript = false
	if r := run(t, h, a(sigT()), map[string]Measured{"m": noScript, "c": good(1)}); r.Status != Invalid {
		t.Fatalf("not from script: %+v", r)
	}
	bad := &statcheck.Test{Kind: "t", Statistic: 2.20, StatDigits: 2, DF1: 28, P: 0.01, PDigits: 2, PBound: "="}
	r := run(t, h, a(bad), ok)
	if r.Status != Invalid || !strings.Contains(r.Reasons[0], "reported p 0.01 does not match t(28)=2.2, p≈0.036") {
		t.Fatalf("inconsistent: %+v", r)
	}
	if got := run(t, h, a(sigT()), ok); got.Status != Supported || len(got.Cites) != 4 {
		t.Fatalf("cites: %+v", got)
	}
}

func TestStatusesRange(t *testing.T) {
	h := HypothesisBody{Text: "h", Prediction: Prediction{Shape: "range", Low: fp(0.5), High: fp(0.8)}, Control: Control{Text: "c"}}
	for v, want := range map[float64]string{0.62: Supported, 0.5: Supported, 0.8: Supported, 0.9: Refuted} {
		r := run(t, h, &Assessment{Hypothesis: "h", Measurement: "m"}, map[string]Measured{"m": good(v)})
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
		r := run(t, dirHyp(c.dir), &Assessment{Hypothesis: "h", Measurement: "m", Control: sp("c"), Test: c.test},
			map[string]Measured{"m": good(c.m), "c": good(c.c)})
		if r.Status != c.want {
			t.Errorf("%+v: got %+v", c, r)
		}
	}
}

func TestStatusesOutcomes(t *testing.T) {
	h := HypothesisBody{Text: "h", Prediction: Prediction{Shape: "outcomes", Outcomes: []string{"A", "B"}, Chosen: "A"}, Control: Control{Text: "c"}}
	for obs, want := range map[string]string{" a ": Supported, "B": Refuted, "C": Inconclusive} {
		r := run(t, h, &Assessment{Hypothesis: "h", Measurement: "m", Observed: sp(obs)}, map[string]Measured{"m": good(1)})
		if r.Status != want {
			t.Fatalf("%q: %+v", obs, r)
		}
	}
	if r := run(t, h, &Assessment{Hypothesis: "h", Measurement: "m"}, map[string]Measured{"m": good(1)}); r.Status != Inconclusive {
		t.Fatalf("nil observed: %+v", r)
	}
}

func TestStatusesControlNoneCaps(t *testing.T) {
	h := HypothesisBody{Text: "h", Prediction: Prediction{Shape: "range", Low: fp(0), High: fp(1)}, Control: Control{None: "nothing to compare"}}
	r := run(t, h, &Assessment{Hypothesis: "h", Measurement: "m"}, map[string]Measured{"m": good(0.5)})
	if r.Status != Caveated || r.Reasons[len(r.Reasons)-1] != "no control: nothing to compare" {
		t.Fatalf("%+v", r)
	}
	r = run(t, h, &Assessment{Hypothesis: "h", Measurement: "m"}, map[string]Measured{"m": good(5)})
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

func sigMeasured() map[string]Measured {
	return map[string]Measured{"m": good(2), "c": good(1)}
}

func dirAssess(test *statcheck.Test) *Assessment {
	return &Assessment{Hypothesis: "h", Measurement: "m", Control: sp("c"), Test: test}
}

func TestSignificanceBounds(t *testing.T) {
	at := func(stat float64, p float64, bound string) *statcheck.Test {
		return &statcheck.Test{Kind: "t", Statistic: stat, StatDigits: 2, DF1: 28, P: p, PDigits: 2, PBound: bound}
	}
	cases := []struct {
		name string
		test *statcheck.Test
		want string
	}{
		{"less-than 0.05", at(2.20, 0.05, "<"), Supported},
		{"equals 0.05", at(2.05, 0.05, "="), Inconclusive},
		{"greater-than never", at(1.0, 0.04, ">"), Inconclusive},
	}
	for _, c := range cases {
		if r := run(t, dirHyp("increase"), dirAssess(c.test), sigMeasured()); r.Status != c.want {
			t.Errorf("%s: %+v", c.name, r)
		}
	}
}

func TestStatusesConsistencyError(t *testing.T) {
	bad := &statcheck.Test{Kind: "t", Statistic: 2.2, StatDigits: 2, DF1: 0, P: 0.04, PDigits: 2, PBound: "="}
	_, _, err := statcheck.Consistent(*bad)
	if err == nil {
		t.Fatal("expected a Consistent error for DF1=0")
	}
	r := run(t, dirHyp("increase"), dirAssess(bad), sigMeasured())
	if r.Status != Invalid || r.Reasons[0] != err.Error() {
		t.Fatalf("%+v", r)
	}
}

func TestStatusesMissingValue(t *testing.T) {
	r := run(t, dirHyp("increase"), dirAssess(sigT()), map[string]Measured{"c": good(1)})
	if r.Status != Invalid || !strings.Contains(r.Reasons[0], "has no recorded value") {
		t.Fatalf("%+v", r)
	}
}

func TestStatusesUnknownShape(t *testing.T) {
	h := HypothesisBody{Text: "h", Prediction: Prediction{Shape: "bogus"}, Control: Control{Text: "c"}}
	if r := run(t, h, &Assessment{Hypothesis: "h", Measurement: "m"}, map[string]Measured{"m": good(1)}); r.Status != Inconclusive {
		t.Fatalf("%+v", r)
	}
}

func TestStatusesZeroEffect(t *testing.T) {
	r := run(t, dirHyp("increase"), dirAssess(sigT()), map[string]Measured{"m": good(1), "c": good(1)})
	if r.Status != Inconclusive {
		t.Fatalf("%+v", r)
	}
}

func TestStatusesBlankControlNoneIsNoCap(t *testing.T) {
	h := HypothesisBody{Text: "h", Prediction: Prediction{Shape: "range", Low: fp(0), High: fp(1)}, Control: Control{None: "  "}}
	if r := run(t, h, &Assessment{Hypothesis: "h", Measurement: "m"}, map[string]Measured{"m": good(0.5)}); r.Status != Supported {
		t.Fatalf("%+v", r)
	}
}

func claimSources() map[string]string {
	return map[string]string{"s1": "The drug lowered blood pressure by 10 mmHg in all arms."}
}

func verdictRec(t *testing.T, key, verdict, quote string) db.Record {
	return rec(t, key, KindVerdict, VerdictBody{Claim: "The drug lowers pressure", Verdict: verdict, Source: "s1", Quote: quote})
}

func TestClaimsVerdicts(t *testing.T) {
	const q = "lowered blood pressure by 10 mmHg"
	cases := []struct {
		name, verdict, jev string
		p                  float64
		want               string
	}{
		{"same pick", "supported", "supported", 0.9, Pass},
		{"same pick barely leaning", "supported", "supported", 0.3, Unclear},
		{"different at 0.70", "refuted", "supported", 0.70, Fail},
		{"different at 0.69", "refuted", "supported", 0.69, Unclear},
		{"different at 0.35", "refuted", "supported", 0.35, Unclear},
		{"different below 0.35", "refuted", "supported", 0.34, Unclear},
	}
	for _, c := range cases {
		a := &fakeAsker{choice: c.jev, choiceP: c.p}
		r, err := Claims(context.Background(), a, ClaimsInput{Items: []db.Record{verdictRec(t, "r1", c.verdict, q)}, Sources: claimSources()})
		if err != nil {
			t.Fatal(err)
		}
		if got := r.Items[0].Status; got != c.want {
			t.Errorf("%s: %s want %s", c.name, got, c.want)
		}
		// Jev is blind to the Team's verdict.
		if len(a.choices) != 1 || strings.Contains(a.choices[0].State, "Team") {
			t.Errorf("%s: %+v", c.name, a.choices)
		}
	}
}

func TestClaimsQuoteNotInSourceIsNotAsked(t *testing.T) {
	a := &fakeAsker{choice: "supported", choiceP: 0.9}
	bad := rec(t, "r2", KindVerdict, VerdictBody{Claim: "c", Verdict: "supported", Source: "nope", Quote: "lowered"})
	r, err := Claims(context.Background(), a, ClaimsInput{
		Items:   []db.Record{verdictRec(t, "r1", "supported", "raised blood pressure"), bad},
		Sources: claimSources(),
	})
	if err != nil {
		t.Fatal(err)
	}
	for _, it := range r.Items {
		if it.Status != Fail || it.Checks[0].By != "server" {
			t.Errorf("%s: %+v", it.Key, it)
		}
	}
	if len(a.choices)+len(a.noul) != 0 || r.Weakest != nil {
		t.Fatalf("jev was asked: %+v %+v", a.choices, a.noul)
	}
}

func TestClaimsFlaggedError(t *testing.T) {
	fe := rec(t, "r1", KindFlaggedError, FlaggedErrorBody{Source: "s1", Quote: "by 10 mmHg", Problem: "wrong unit"})
	a := &fakeAsker{p: func(_, k string) float64 {
		if k == "specific" {
			return 0.5
		}
		return 0.9
	}}
	r, err := Claims(context.Background(), a, ClaimsInput{Items: []db.Record{fe}, Sources: claimSources(), Attempt: 2})
	if err != nil {
		t.Fatal(err)
	}
	if r.Attempt != 2 || r.Items[0].Status != Unclear || len(a.noul) != 1 || len(a.noul[0].Questions) != 2 {
		t.Fatalf("%+v %+v", r, a.noul)
	}
	a = &fakeAsker{p: func(_, k string) float64 { return map[string]float64{"wrong": 0.1, "specific": 0.9}[k] }}
	r, _ = Claims(context.Background(), a, ClaimsInput{Items: []db.Record{fe}, Sources: claimSources()})
	if r.Items[0].Status != Fail {
		t.Fatalf("one failed question must fail the item: %+v", r.Items[0])
	}
	fe = rec(t, "r1", KindFlaggedError, FlaggedErrorBody{Source: "s1", Quote: "not there", Problem: "p"})
	a = &fakeAsker{}
	r, _ = Claims(context.Background(), a, ClaimsInput{Items: []db.Record{fe}, Sources: claimSources()})
	if r.Items[0].Status != Fail || len(a.noul) != 0 {
		t.Fatalf("passage is checked first: %+v", r.Items[0])
	}
}

// byInstructions answers the verdict question with one pick and the
// weakest-item question with another.
type byInstructions struct {
	fakeAsker
	verdict, weakest string
}

func (b *byInstructions) Choice(ctx context.Context, state, instr string, opts map[string]string) (jev.Answer, error) {
	pick := b.verdict
	if instr == claimsWeakest {
		pick = b.weakest
	}
	b.choices = append(b.choices, choiceCall{state, instr, opts})
	return jev.Answer{Choice: pick, Probabilities: map[string]float64{pick: 0.8}}, nil
}

func TestClaimsWeakest(t *testing.T) {
	a := &byInstructions{verdict: "supported", weakest: "claim:2"}
	r, err := Claims(context.Background(), a, ClaimsInput{
		Items:   []db.Record{verdictRec(t, "r1", "supported", "lowered"), verdictRec(t, "r2", "supported", "blood pressure")},
		Sources: claimSources(),
		Carried: []Item{{Key: "r9", Status: Unclear}},
	})
	if err != nil {
		t.Fatal(err)
	}
	if r.Weakest == nil || r.Weakest.Key != "r2" || r.Weakest.P != 0.8 {
		t.Fatalf("%+v", r.Weakest)
	}
	if len(r.Items) != 3 || !r.Items[2].Carried {
		t.Fatalf("%+v", r.Items)
	}
	if last := a.choices[len(a.choices)-1]; last.Instructions != claimsWeakest || strings.Contains(last.State, "r2") {
		t.Fatalf("%+v", last)
	}
}
