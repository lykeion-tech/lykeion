package handin

import (
	"encoding/json"
	"slices"
	"strings"
	"testing"
)

func TestHypothesisMeasureRoundTrip(t *testing.T) {
	const first = `{"text":"Treatment increases accuracy","prediction_shape":"direction","direction":"increase","control":"baseline","cites":["doi:10.1001/a"],"measure":{"id":"accuracy","name":"Accuracy","unit":"percent"}}`
	const second = `{"text":"Treatment decreases accuracy","prediction_shape":"direction","direction":"decrease","control":"baseline","cites":["doi:10.1001/a"],"measure":{"id":"accuracy","name":"Accuracy","unit":"percent"}}`
	handIn := func(hypotheses ...string) string {
		return `{"status":"done","answer":"A testable prediction.","hypotheses":[` + strings.Join(hypotheses, ",") + `]}`
	}
	h := parsed(t, Hypotheses, handIn(first, second))
	recs, probs := Convert(Hypotheses, true, h, studyEvidence(), Review{})
	if len(probs) != 0 || !slices.Equal(kinds(recs), []string{"hypothesis", "hypothesis", "summary"}) {
		t.Fatalf("records %v, problems %v", kinds(recs), probs)
	}
	m, _ := body(t, recs[0])["measure"].(map[string]any)
	if m["id"] != "accuracy" || m["name"] != "Accuracy" || m["unit"] != "percent" {
		t.Fatalf("stored measure = %v", m)
	}
	for _, tc := range []struct {
		name string
		raw  string
		want string
	}{
		{"conflict", handIn(first, strings.Replace(second, "percent", "fraction", 1)), "already declared with a different name or unit"},
		{"invalid id", handIn(strings.Replace(first, `"id":"accuracy"`, `"id":"Accuracy"`, 1)), "measure id"},
		{"invalid name", handIn(strings.Replace(first, `"name":"Accuracy"`, `"name":" Accuracy "`, 1)), "measure name"},
		{"invalid unit", handIn(strings.Replace(first, `"unit":"percent"`, `"unit":" "`, 1)), "measure unit"},
		{"legacy", handIn(strings.Split(first, `,"measure"`)[0] + "}"), ""},
		{"null", handIn(strings.Split(first, `,"measure"`)[0] + `,"measure":null}`), ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_, probs := Parse(Hypotheses, []byte(tc.raw))
			if tc.want == "" && len(probs) != 0 || tc.want != "" && !strings.Contains(strings.Join(probs, "\n"), tc.want) {
				t.Fatalf("problems %q, want %q", probs, tc.want)
			}
		})
	}
}

func TestExperimentationBindingRoundTrip(t *testing.T) {
	const raw = `{"status":"done","answer":"Measured.", "outputs":["outputs/metrics.json"],"values":[
		{"name":"Primary","value":"0.42","file":"outputs/metrics.json","field":"/summary/a~1b/~0c/0","measure":"accuracy","run":"run:x7"},
		{"name":"Root","value":"9","file":"outputs/metrics.json","field":"","measure":"count"},
		{"name":"Partial","value":"0.42","file":"outputs/metrics.json","measure":"accuracy"},
		{"name":"Legacy","value":"4","file":"fit.txt","measure":null,"field":null}]}`
	h := parsed(t, Experimentation, raw)
	recs, probs := Convert(Experimentation, true, h, studyEvidence(), Review{})
	if len(probs) != 0 || !slices.Equal(kinds(recs), []string{"outputs", "value", "value", "value", "value", "summary"}) {
		t.Fatalf("records %v, problems %v", kinds(recs), probs)
	}
	first := body(t, recs[1])
	if first["value"] != "0.42" || first["file"] != "outputs/metrics.json" ||
		first["field"] != "/summary/a~1b/~0c/0" || first["measure"] != "accuracy" ||
		!slices.Equal(first["cites"].([]any), []any{"run:x7"}) {
		t.Fatalf("stored binding = %v", first)
	}
	root := body(t, recs[2])
	if root["field"] != "" || len(root["cites"].([]any)) != 0 {
		t.Fatalf("root pointer or explicit run lost: %v", root)
	}
	for _, rec := range recs[3:5] {
		if _, present := body(t, rec)["field"]; present {
			t.Fatalf("absent pointer acquired a value: %v", rec)
		}
	}
	for _, tc := range []struct{ old, invalid, want string }{
		{`"/summary/a~1b/~0c/0"`, `"summary"`, "field must be a JSON Pointer"},
		{`"/summary/a~1b/~0c/0"`, `"/metric~3"`, "field must be a JSON Pointer"},
		{`"/summary/a~1b/~0c/0"`, `"/metric~"`, "field must be a JSON Pointer"},
		{`"measure":"accuracy"`, `"measure":"Accuracy"`, "measure id"},
	} {
		_, probs := Parse(Experimentation, []byte(strings.Replace(raw, tc.old, tc.invalid, 1)))
		if !strings.Contains(strings.Join(probs, "\n"), tc.want) {
			t.Fatalf("problems %q, want %q", probs, tc.want)
		}
	}
}

func TestBindingSchemaIsStrictAndNullable(t *testing.T) {
	propsOf := func(s map[string]any) map[string]any { return s["properties"].(map[string]any) }
	item := func(k Kind, name string) map[string]any {
		t.Helper()
		var schema map[string]any
		if err := json.Unmarshal([]byte(Schema(k)), &schema); err != nil {
			t.Fatal(err)
		}
		walk(t, string(k), schema)
		return propsOf(schema)[name].(map[string]any)["items"].(map[string]any)
	}
	hypMeasure, ok := propsOf(item(Hypotheses, "hypotheses"))["measure"].(map[string]any)
	if !ok {
		t.Fatal("missing hypotheses measure schema")
	}
	alts := hypMeasure["anyOf"].([]any)
	if len(alts) != 2 || alts[1].(map[string]any)["type"] != "null" {
		t.Fatalf("measure schema = %v", hypMeasure)
	}
	for _, k := range []string{"id", "name", "unit"} {
		if _, ok := propsOf(alts[0].(map[string]any))[k]; !ok {
			t.Errorf("missing measure.%s schema", k)
		}
	}
	props := propsOf(item(Experimentation, "values"))
	for _, k := range []string{"field", "measure"} {
		def, ok := props[k].(map[string]any)
		if !ok || !slices.Equal(def["type"].([]any), []any{"string", "null"}) {
			t.Errorf("values.%s schema = %v", k, props[k])
		}
	}
}
