package handin

import (
	"encoding/json"
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
	for _, k := range []Kind{Grounding, Hypotheses, Experimentation, Auditor, Autoresearch} {
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
		{"role", "leader", "validation", "", false},
		{"role", "searcher", "grounding", Grounding, true},
		{"role", "auditor", "hypotheses", Auditor, true},
		{"role", "auditor", "validation", "", false},
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
