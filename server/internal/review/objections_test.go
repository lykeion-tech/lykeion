package review

import (
	"context"
	"maps"
	"strings"
	"testing"
)

func rivals() ([]StatusResult, []HypothesisBody) {
	var hs []HypothesisBody
	var ss []StatusResult
	for _, text := range []string{"Dose raises slope", "Dose lowers slope", "Age raises slope", "Sex changes slope"} {
		hs = append(hs, HypothesisBody{Text: text, Prediction: Prediction{Shape: "direction", Direction: "increase"}, Control: Control{Text: "baseline"}})
		ss = append(ss, StatusResult{Hypothesis: text, Status: Supported, Reasons: []string{"the effect 0.3 is in the predicted direction (increase) and significant (p < 0.05)"}})
	}
	return ss, hs
}

func TestObjectorBriefIsBlind(t *testing.T) {
	ss, hs := rivals()
	p1, l1 := ObjectorBrief(ss, hs, []string{"slope"}, []string{"fit.txt"}, 1)
	_, l2 := ObjectorBrief(ss, hs, []string{"slope"}, []string{"fit.txt"}, 2)
	low := strings.ToLower(p1)
	for _, banned := range []string{"team", "leader", "member", "agent", "claude", "codex", "model", "audit"} {
		if strings.Contains(low, banned) {
			t.Errorf("the brief mentions %q:\n%s", banned, p1)
		}
	}
	for _, want := range []string{"Rival A", "Rival D", "slope", "fit.txt", "supported", "read_output"} {
		if !strings.Contains(p1, want) {
			t.Errorf("the brief lacks %q", want)
		}
	}
	if len(l1) != 4 || maps.Equal(l1, l2) {
		t.Fatalf("labels: seed 1 %v, seed 2 %v", l1, l2)
	}
	if again, l := ObjectorBrief(ss, hs, []string{"slope"}, []string{"fit.txt"}, 1); !maps.Equal(l, l1) || again == "" {
		t.Fatalf("one seed gave two orders: %v %v", l1, l)
	}
}

func TestUnknownTargetOrQuoteIsDropped(t *testing.T) {
	ask := &fakeAsker{}
	texts := map[string]string{"Rival A": "Dose raises slope; measured 0.4"}
	in := []Objection{
		{Label: "Rival Z", Type: "overreach", Problem: "too strong"},
		{Label: "Rival A", Type: "unit_mismatch", Quote: sp("measured 4.0"), Problem: "units"},
	}
	out, err := JudgeObjections(context.Background(), ask, in, texts)
	if err != nil {
		t.Fatal(err)
	}
	if len(ask.noul) != 0 {
		t.Fatalf("Jev was asked %d times", len(ask.noul))
	}
	for i, o := range out {
		if o.Server == "ok" || o.Server == "" || o.Upheld {
			t.Errorf("objection %d: %+v", i+1, o)
		}
	}
}

func TestUpheldNeedsBothChecks(t *testing.T) {
	texts := map[string]string{"Rival A": "Dose raises slope; measured 0.4"}
	for _, c := range []struct {
		wrong, specific float64
		upheld          bool
	}{{0.8, 0.8, true}, {0.8, 0.5, false}, {0.2, 0.9, false}} {
		ask := &fakeAsker{p: func(_, key string) float64 {
			if key == "wrong" {
				return c.wrong
			}
			return c.specific
		}}
		out, err := JudgeObjections(context.Background(), ask, []Objection{{Label: "Rival A", Type: "overreach", Quote: sp("measured 0.4"), Problem: "one run"}}, texts)
		if err != nil {
			t.Fatal(err)
		}
		if len(ask.noul) != 1 || len(out) != 1 {
			t.Fatalf("%v: %d asks, %d objections", c, len(ask.noul), len(out))
		}
		o := out[0]
		if o.Server != "ok" || o.Upheld != c.upheld {
			t.Errorf("%v: %+v", c, o)
		}
		for _, q := range []string{"Is the record wrong in the way stated?", "Is the objection specific to this record rather than generic?"} {
			if !hasValue(ask.noul[0].Questions, q) {
				t.Errorf("question %q not asked", q)
			}
		}
		if !strings.Contains(ask.noul[0].State, "UNTRUSTED") {
			t.Errorf("unfenced state: %s", ask.noul[0].State)
		}
	}
}

func hasValue(m map[string]string, v string) bool {
	for _, x := range m {
		if x == v {
			return true
		}
	}
	return false
}

func TestObjectionQuoteIsWhitespaceTolerantAndNeverEmpty(t *testing.T) {
	texts := map[string]string{"Rival A": "Dose raises\nslope;  measured 0.4"}
	for quote, want := range map[string]bool{"raises slope; measured 0.4": true, "": false, "  ": false} {
		out, err := JudgeObjections(context.Background(), &fakeAsker{}, []Objection{{Label: "Rival A", Type: "overreach", Quote: sp(quote), Problem: "p"}}, texts)
		if err != nil {
			t.Fatal(err)
		}
		if (out[0].Server == "ok") != want {
			t.Errorf("%q: server %q", quote, out[0].Server)
		}
	}
}
