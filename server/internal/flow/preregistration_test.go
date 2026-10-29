package flow

import (
	"encoding/json"
	"strings"
	"testing"
)

func okRun(key string, res map[string]float64) RunResults {
	return RunResults{Key: key, Tool: "run_script", Results: res}
}

func one(t *testing.T, preds []Predicate, prior []Decision, named string, runs []RunResults, round int32) Decision {
	t.Helper()
	got := Decide(preds, prior, named, runs, round)
	if len(got) != 1 {
		t.Fatalf("got %d decisions", len(got))
	}
	return got[0]
}

var p1 = []Predicate{{Name: "p1", Text: "auc >= 0.8"}}

func TestDecideHeldAndMissed(t *testing.T) {
	runs := []RunResults{okRun("x3", map[string]float64{"auc": 0.9, "extra": 1})}
	d := one(t, p1, nil, "x3", runs, 2)
	if d.Outcome != "held" || d.Round != 2 || d.Run != "x3" || len(d.Values) != 1 || d.Values["auc"] != 0.9 {
		t.Fatalf("%+v", d)
	}
	runs[0].Results["auc"] = 0.5
	d = one(t, p1, nil, "x3", runs, 2)
	if d.Outcome != "missed" || d.Values["auc"] != 0.5 {
		t.Fatalf("%+v", d)
	}
}

func TestDecidePriorStays(t *testing.T) {
	prior := []Decision{{Name: "p1", Text: "auc >= 0.8", Outcome: "missed", Round: 1, Run: "x1", Values: map[string]float64{"auc": 0.1}}}
	runs := []RunResults{okRun("x3", map[string]float64{"auc": 0.9})}
	d := one(t, p1, prior, "x3", runs, 2)
	if d.Outcome != "missed" || d.Round != 1 || d.Run != "x1" || d.Values["auc"] != 0.1 {
		t.Fatalf("%+v", d)
	}
}

func TestDecideUnconfirmed(t *testing.T) {
	good := map[string]float64{"auc": 0.9}
	cases := []struct {
		name, named, reason string
		run                 RunResults
	}{
		{"none", "", "no results run named", okRun("x1", good)},
		{"unknown", "x99", "run x99 is not a run of this column", okRun("x1", good)},
		{"code", "x1", "not a script run", RunResults{Key: "x1", Tool: "run_code", Results: good}},
		{"exit1", "x1", "exited 1", RunResults{Key: "x1", Tool: "run_script", ExitStatus: 1, Results: good}},
		{"timeout", "x1", "timed out", RunResults{Key: "x1", Tool: "run_script", ExitStatus: -1, Results: good}},
		{"error", "x1", "results file: bad json", RunResults{Key: "x1", Tool: "run_script", ResultsError: "bad json"}},
		{"missing", "x1", "missing ci_low", okRun("x1", good)},
	}
	for _, c := range cases {
		preds := p1
		if c.name == "missing" {
			preds = []Predicate{{Name: "p1", Text: "auc >= 0.8 and ci_low > 0"}}
		}
		d := one(t, preds, nil, c.named, []RunResults{c.run}, 1)
		if d.Outcome != "unconfirmed" || d.Round != 0 || !strings.Contains(d.Reason, c.reason) {
			t.Errorf("%s: %+v", c.name, d)
		}
	}
}

func TestDecideOthers(t *testing.T) {
	runs := []RunResults{
		okRun("x2", map[string]float64{"auc": 0.7, "zzz": 1}),
		okRun("x10", map[string]float64{"auc": 0.6}),
		okRun("x3", map[string]float64{"auc": 0.9}),
		okRun("x4", map[string]float64{"other": 1}),
		{Key: "x5", Tool: "run_script", ExitStatus: 1, Results: map[string]float64{"auc": 0.1}},
		{Key: "x6", Tool: "run_code", Results: map[string]float64{"auc": 0.1}},
	}
	d := one(t, p1, nil, "x3", runs, 1)
	if len(d.Others) != 2 || d.Others[0].Run != "x2" || d.Others[1].Run != "x10" {
		t.Fatalf("%+v", d.Others)
	}
	if len(d.Others[0].Values) != 1 || d.Others[0].Values["auc"] != 0.7 {
		t.Fatalf("%+v", d.Others[0])
	}
}

func TestBinding(t *testing.T) {
	body := func(ds ...Decision) []byte {
		b, _ := json.Marshal(map[string]any{"verdict": "v", "predicates": ds})
		return b
	}
	bodies := [][]byte{
		body(Decision{Name: "a", Outcome: "unconfirmed", Reason: "old"}, Decision{Name: "b", Outcome: "unconfirmed", Reason: "old"}),
		[]byte("not json"),
		body(Decision{Name: "a", Outcome: "missed", Round: 2}, Decision{Name: "b", Outcome: "unconfirmed", Reason: "new"}),
		body(Decision{Name: "a", Outcome: "held", Round: 3}, Decision{Name: "b", Outcome: "held", Round: 3}),
	}
	got := Binding(bodies[:3])
	if len(got) != 2 || got[0].Name != "a" || got[0].Outcome != "missed" || got[1].Reason != "new" {
		t.Fatalf("%+v", got)
	}
	got = Binding(bodies)
	if got[0].Outcome != "missed" || got[1].Outcome != "held" {
		t.Fatalf("%+v", got)
	}
}
