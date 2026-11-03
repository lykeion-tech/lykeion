package handin

import (
	"encoding/json"
	"slices"
	"strings"
	"testing"
)

func walk(t *testing.T, path string, n any) {
	m, ok := n.(map[string]any)
	if !ok {
		if a, ok := n.([]any); ok {
			for i, v := range a {
				walk(t, path, v)
				_ = i
			}
		}
		return
	}
	if props, ok := m["properties"].(map[string]any); ok {
		if m["additionalProperties"] != false {
			t.Errorf("%s: additionalProperties is not false", path)
		}
		req, _ := m["required"].([]any)
		if len(req) != len(props) {
			t.Errorf("%s: required has %d keys, properties %d", path, len(req), len(props))
		}
		for _, r := range req {
			if _, ok := props[r.(string)]; !ok {
				t.Errorf("%s: required %v is not a property", path, r)
			}
		}
	}
	for k, v := range m {
		walk(t, path+"."+k, v)
	}
}

func TestSchemasFitStrictMode(t *testing.T) {
	for _, k := range []Kind{Grounding, Hypotheses, Experimentation, Auditor, Autoresearch, Validation} {
		var doc any
		if err := json.Unmarshal([]byte(Schema(k)), &doc); err != nil {
			t.Fatalf("%s: %v", k, err)
		}
		walk(t, string(k), doc)
	}
}

func TestParseRejectsShapeProblems(t *testing.T) {
	long := strings.Repeat("é", 8001)
	cases := []struct{ name, raw, want string }{
		{"no reason", `{"status":"could_not_do","answer":"x","reason":null,"papers":[],"datasets":[],"no_qualifying":null,"replies":[]}`, "reason: say what blocked the work"},
		{"empty answer", `{"status":"done","answer":"","reason":null,"papers":[],"datasets":[],"no_qualifying":null,"replies":[]}`, "answer: say what you did, 1 to 8000 characters"},
		{"unknown", `{"status":"done","answer":"x","bogus":1}`, "bogus: not a field of this hand-in"},
		{"long", `{"status":"done","answer":"` + long + `"}`, "answer: say what you did, 1 to 8000 characters"},
		{"action", `{"status":"done","answer":"x","replies":[{"item":"r1","action":"maybe","reason":"y"}]}`, "replies 1: action must be corrected, withdrawn or declined"},
	}
	for _, c := range cases {
		_, probs := Parse(Grounding, []byte(c.raw))
		if !contains(probs, c.want) {
			t.Errorf("%s: want %q in %q", c.name, c.want, probs)
		}
	}
	if _, probs := Parse(Grounding, []byte(`{"status":"done","answer":"ok","papers":[{"id":"arxiv:2106.01234","why":"w"}]}`)); len(probs) != 0 {
		t.Errorf("valid hand-in: %q", probs)
	}
	dup := `{"status":"done","answer":"ok","values":[{"name":"Slope","value":"1","file":"a.csv"},{"name":" slope ","value":"2","file":"a.csv"}]}`
	if _, probs := Parse(Experimentation, []byte(dup)); !contains(probs, "values 2: another value already has the name  slope ") {
		t.Errorf("two values of one name: %q", probs)
	}
	if _, probs := Parse(Grounding, []byte(`{"status":"done","answer":"ok","concerns":[]}`)); len(probs) == 0 {
		t.Error("a field of another kind must be refused")
	}
}

func contains(l []string, s string) bool {
	for _, x := range l {
		if x == s {
			return true
		}
	}
	return false
}

func TestKindFor(t *testing.T) {
	cases := []struct {
		run, role, col string
		want           Kind
		ok             bool
	}{
		{"role", "searcher", "grounding", Grounding, true},
		{"role", "auditor", "hypotheses", Auditor, true},
		{"autoresearch", "", "", Autoresearch, true},
		{"coresearch", "", "", "", false},
	}
	for _, c := range cases {
		got, ok := KindFor(c.run, c.role, c.col)
		if got != c.want || ok != c.ok {
			t.Errorf("%v: got %q %v", c, got, ok)
		}
	}
}

func TestHypothesisFlatFieldsRoundTrip(t *testing.T) {
	raw := `{"status":"done","answer":"a","hypotheses":[
	{"text":"t1","prediction_shape":"direction","direction":"increase","outcomes":[],"chosen":null,"low":null,"high":null,"uncertainty":null,"control":null,"no_control":"a descriptive study","cites":["r1"]},
	{"text":"t2","prediction_shape":"range","direction":null,"outcomes":[],"chosen":null,"low":1.5,"high":2.5,"uncertainty":"wide","control":"placebo","no_control":null,"cites":["r1"]}]}`
	h, probs := Parse(Hypotheses, []byte(raw))
	if len(probs) != 0 {
		t.Fatal(probs)
	}
	a := h.Hypotheses[0].Body([]string{"r1"})
	if a.Prediction.Shape != "direction" || a.Prediction.Direction != "increase" || a.Control.None != "a descriptive study" || a.Control.Text != "" || a.Cites[0] != "r1" {
		t.Errorf("%+v", a)
	}
	b := h.Hypotheses[1].Body(nil)
	if *b.Prediction.Low != 1.5 || *b.Prediction.High != 2.5 || b.Prediction.Uncertainty != "wide" || b.Control.Text != "placebo" || b.Control.None != "" {
		t.Errorf("%+v", b)
	}
}

func TestHypothesisNeedsExactlyOneOfControlAndReason(t *testing.T) {
	for _, c := range []string{`"control":null,"no_control":null`, `"control":"x","no_control":"y"`} {
		raw := `{"status":"done","answer":"a","hypotheses":[{"text":"t","prediction_shape":"direction","direction":"increase","outcomes":[],"chosen":null,"low":null,"high":null,"uncertainty":null,` + c + `,"cites":["r1"]}]}`
		_, probs := Parse(Hypotheses, []byte(raw))
		if !contains(probs, "hypotheses 1: give either a control or the reason there is none") {
			t.Errorf("%s: %q", c, probs)
		}
	}
}

// A hypothesis missing its control is told every other problem in the same
// re-ask, and a blank no_control is no reason at all.
func TestHypothesisControlProblemDoesNotHideOthers(t *testing.T) {
	raw := `{"status":"done","answer":"a","hypotheses":[{"text":"t","prediction_shape":"direction","direction":null,"outcomes":[],"chosen":null,"low":null,"high":null,"uncertainty":null,"control":null,"no_control":"  ","cites":["r1"]}]}`
	_, probs := Parse(Hypotheses, []byte(raw))
	if len(probs) != 2 || probs[0] != "hypotheses 1: give either a control or the reason there is none" || !strings.Contains(probs[1], "direction") {
		t.Errorf("%q", probs)
	}
	raw = `{"status":"done","answer":"a","hypotheses":[{"text":"t","prediction_shape":"direction","direction":"increase","outcomes":[],"chosen":null,"low":null,"high":null,"uncertainty":null,"control":null,"no_control":null,"cites":["r1"]}]}`
	if _, probs = Parse(Hypotheses, []byte(raw)); len(probs) != 1 {
		t.Errorf("the control problem is told twice: %q", probs)
	}
}

func TestValidationSchemaIsStrict(t *testing.T) {
	var doc map[string]any
	if err := json.Unmarshal([]byte(Schema(Validation)), &doc); err != nil {
		t.Fatal(err)
	}
	walk(t, "validation", doc)
	props := doc["properties"].(map[string]any)
	for _, k := range []string{"status", "answer", "reason", "replies", "assessments", "verdicts", "flagged_errors"} {
		if _, ok := props[k]; !ok {
			t.Errorf("no %s", k)
		}
	}
}

func TestKindForValidation(t *testing.T) {
	cases := []struct {
		role string
		want Kind
	}{
		{"leader", Validation},
		{"agent", Validation},
		{"replicator", Validation},
		{"auditor", Auditor},
	}
	for _, c := range cases {
		if got, ok := KindFor("role", c.role, "validation"); !ok || got != c.want {
			t.Errorf("%s: got %q %v", c.role, got, ok)
		}
	}
	if got, ok := KindFor("role", "auditor", "hypotheses"); !ok || got != Auditor {
		t.Errorf("hypotheses auditor: %q %v", got, ok)
	}
}

func TestParseValidationKeepsNumberText(t *testing.T) {
	raw := `{"status":"done","answer":"a","reason":null,"replies":[],"verdicts":[],"flagged_errors":[],"assessments":[
		{"hypothesis":"h","measurement":"slope","control":null,"observed":null,
		 "test":{"kind":"t","statistic":2.310,"df1":28,"df2":null,"p":0.03,"p_bound":"="}},
		{"hypothesis":"h2","measurement":"slope","control":"base","observed":"up",
		 "test":{"kind":"z","statistic":1.2e-1,"df1":null,"df2":null,"p":4E-3,"p_bound":"<"}}]}`
	h, probs := Parse(Validation, []byte(raw))
	if len(probs) > 0 {
		t.Fatal(probs)
	}
	a := h.Assessments[0].Assessment()
	if a.Test == nil || a.Test.Statistic != 2.31 || a.Test.StatDigits != 3 || a.Test.DF1 != 28 || a.Test.P != 0.03 || a.Test.PDigits != 2 || a.Test.PBound != "=" {
		t.Errorf("%+v", a.Test)
	}
	b := h.Assessments[1].Assessment()
	if b.Test.StatDigits != 2 || b.Test.PDigits != 3 || *b.Control != "base" || *b.Observed != "up" {
		t.Errorf("%+v", b.Test)
	}
	dfs := `{"status":"done","answer":"a","assessments":[
		{"hypothesis":"h","measurement":"m","control":null,"observed":null,"test":{"kind":"t","statistic":2,"df1":null,"df2":null,"p":0.05,"p_bound":"="}},
		{"hypothesis":"h","measurement":"m","control":null,"observed":null,"test":{"kind":"F","statistic":2,"df1":2,"df2":null,"p":0.05,"p_bound":"="}},
		{"hypothesis":"h","measurement":"m","control":null,"observed":null,"test":{"kind":"z","statistic":2,"df1":null,"df2":null,"p":0.05,"p_bound":"="}}]}`
	_, probs = Parse(Validation, []byte(dfs))
	if want := []string{"assessments 1: a t test needs df1", "assessments 2: an F test needs df1 and df2"}; !slices.Equal(probs, want) {
		t.Errorf("dfs: got %q want %q", probs, want)
	}
	long := strings.Repeat("x", 2001)
	lens := `{"status":"done","answer":"a",
		"assessments":[{"hypothesis":"  ","measurement":"m","control":null,"observed":"` + long + `","test":null}],
		"verdicts":[{"claim":"` + long + `","verdict":"supported","source":" ","quote":"q"}],
		"flagged_errors":[{"source":"s","quote":"q","problem":"` + long + `"}]}`
	_, probs = Parse(Validation, []byte(lens))
	for _, want := range []string{
		"assessments 1: hypothesis and measurement must each be 1 to 2000 characters",
		"assessments 1: observed must be 1 to 2000 characters",
		"verdicts 1: claim, source and quote must each be 1 to 2000 characters",
		"flagged_errors 1: source, quote and problem must each be 1 to 2000 characters",
	} {
		if !contains(probs, want) {
			t.Errorf("want %q in %q", want, probs)
		}
	}
	bad := `{"status":"done","answer":"a","verdicts":[{"claim":"c","verdict":"maybe","source":"s","quote":"q"}],
		"assessments":[{"hypothesis":"","measurement":"","control":null,"observed":null,"test":{"kind":"w","statistic":1,"df1":null,"df2":null,"p":0.5,"p_bound":"~"}}],
		"flagged_errors":[{"source":"","quote":"q","problem":"p"}]}`
	_, probs = Parse(Validation, []byte(bad))
	for _, want := range []string{
		"assessments 1: hypothesis and measurement must each be 1 to 2000 characters",
		"assessments 1: test kind must be t, F, chi2, r or z, and p_bound =, < or >",
		"verdicts 1: verdict must be supported, refuted or not_enough_info",
		"flagged_errors 1: source, quote and problem must each be 1 to 2000 characters",
	} {
		if !contains(probs, want) {
			t.Errorf("want %q in %q", want, probs)
		}
	}
}

func TestObjectorHandIn(t *testing.T) {
	var doc map[string]any
	if err := json.Unmarshal([]byte(Schema(Objector)), &doc); err != nil {
		t.Fatal(err)
	}
	walk(t, "objector", doc)
	if got, ok := KindFor("role", "objector", "validation"); !ok || got != Objector {
		t.Errorf("objector: %q %v", got, ok)
	}
	raw := `{"status":"done","answer":"a","reason":null,"objections":[
		{"target":"Rival A","type":"overreach","quote":null,"problem":"one run"},
		{"target":"slope","type":"guess","quote":"","problem":""}]}`
	_, probs := Parse(Objector, []byte(raw))
	for _, want := range []string{
		"objections 2: type must be wrong_test, leakage, unit_mismatch, missing_control or overreach",
		"objections 2: quote must be null or 1 to 2000 characters",
		"objections 2: target and problem must each be 1 to 2000 characters",
	} {
		if !contains(probs, want) {
			t.Errorf("want %q in %q", want, probs)
		}
	}
	if len(probs) != 3 {
		t.Errorf("problems %q", probs)
	}
	if _, probs := Parse(Objector, []byte(`{"status":"done","answer":"a","replies":[]}`)); !contains(probs, "replies: not a field of this hand-in") {
		t.Errorf("replies accepted: %q", probs)
	}
}
