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
		{"name":"Legacy","value":"4","file":"fit.txt","measure":null,"field":null}]}`
	h := parsed(t, Experimentation, raw)
	recs, probs := Convert(Experimentation, true, h, studyEvidence(), Review{})
	if len(probs) != 0 || !slices.Equal(kinds(recs), []string{"outputs", "value", "value", "value", "summary"}) {
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
	if _, present := body(t, recs[3])["field"]; present {
		t.Fatalf("absent pointer acquired a value: %v", recs[3])
	}
	for _, tc := range []struct{ old, invalid, want string }{
		{`"/summary/a~1b/~0c/0"`, `"summary"`, "field must be a JSON Pointer"},
		{`"/summary/a~1b/~0c/0"`, `"/metric~3"`, "field must be a JSON Pointer"},
		{`"/summary/a~1b/~0c/0"`, `"/metric~"`, "field must be a JSON Pointer"},
		{`"measure":"accuracy"`, `"measure":"Accuracy"`, "measure id"},
		// Review fails a half binding every time, so it is refused at hand-in,
		// where the agent can still fix it in the same attempt.
		{`,"measure":"accuracy"`, ``, "field and measure go together"},
		{`"field":"/summary/a~1b/~0c/0",`, ``, "field and measure go together"},
		{`"value":"0.42"`, `"value":"steel"`, "bound value must be a number"},
		{`"value":"0.42"`, `"value":"NaN"`, "bound value must be a number"},
		// Review reads only these forms as numbers (flow numberText).
		{`"value":"0.42"`, `"value":"+0.42"`, "bound value must be a number"},
		{`"value":"0.42"`, `"value":".42"`, "bound value must be a number"},
		{`"value":"0.42"`, `"value":"42."`, "bound value must be a number"},
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

func TestOutcomeComparisonRoundTrip(t *testing.T) {
	const hypothesis = `{"text":"Treatment improves accuracy","prediction_shape":"outcomes","outcomes":["higher","no gain"],"chosen":"higher","control":"baseline","cites":["doi:10.1001/a"],"measure":{"id":"accuracy","name":"Accuracy","unit":"fraction"}`
	const comparison = `"comparison":{"threshold":0.50000000000000000001,"control":null,"less":"no gain","equal":"no gain","greater":"higher"}`
	wrap := func(h string) string {
		return `{"status":"done","answer":"A testable prediction.","hypotheses":[` + h + `}]}`
	}
	raw := wrap(hypothesis + "," + comparison)
	h := parsed(t, Hypotheses, raw)
	recs, probs := Convert(Hypotheses, true, h, studyEvidence(), Review{})
	if len(probs) != 0 || !slices.Equal(kinds(recs), []string{"hypothesis", "summary"}) {
		t.Fatalf("records %v, problems %v", kinds(recs), probs)
	}
	var stored struct {
		Comparison struct {
			Threshold json.Number `json:"threshold"`
			Equal     string      `json:"equal"`
		} `json:"comparison"`
	}
	encoded, err := json.Marshal(recs[0].Body)
	if err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(encoded, &stored); err != nil || stored.Comparison.Threshold != "0.50000000000000000001" || stored.Comparison.Equal != "no gain" {
		t.Fatalf("frozen comparison changed during conversion: %+v (%v)", stored, err)
	}
	for _, tc := range []struct{ before, after, want string }{
		{`"control":null`, `"control":"baseline value"`, "exactly one of threshold or control"},
		{`0.50000000000000000001`, `1e99999999`, "finite decimal number"},
		{`"equal":"no gain"`, `"equal":"unknown"`, "registered outcomes"},
		{`"prediction_shape":"outcomes"`, `"prediction_shape":"direction","direction":"increase"`, "needs an outcomes prediction"},
	} {
		_, probs := Parse(Hypotheses, []byte(strings.Replace(raw, tc.before, tc.after, 1)))
		if !strings.Contains(strings.Join(probs, "\n"), tc.want) {
			t.Fatalf("problems %q, want %q", probs, tc.want)
		}
	}
	parsed(t, Hypotheses, wrap(hypothesis))
	parsed(t, Hypotheses, wrap(hypothesis+`,"comparison":null`))
}

func TestEstimateHandInRoundTrip(t *testing.T) {
	const est = `{"text":"PR probability at 65","prediction_shape":"estimate","measure":{"id":"prob_pr_65","name":"PR probability at 65","unit":"fraction"},"method":"logistic regression of PR on age, all patients","reported":{"value":0.39,"cite":"doi:10.1001/a"},"control":null,"no_control":null,"cites":["doi:10.1001/a"]}`
	wrap := func(h string) string {
		return `{"status":"done","answer":"A measurement plan.","hypotheses":[` + h + `]}`
	}
	h := parsed(t, Hypotheses, wrap(est))
	recs, probs := Convert(Hypotheses, true, h, studyEvidence(), Review{})
	if len(probs) != 0 || !slices.Equal(kinds(recs), []string{"hypothesis", "summary"}) {
		t.Fatalf("records %v, problems %v", kinds(recs), probs)
	}
	b := body(t, recs[0])
	rep, _ := b["reported"].(map[string]any)
	if b["prediction"].(map[string]any)["shape"] != "estimate" || b["method"] != "logistic regression of PR on age, all patients" ||
		rep["key"] != "r2" || rep["source"] != "doi:10.1001/a" || rep["value"] != 0.39 {
		t.Fatalf("stored estimate = %v", b)
	}
	_, probs = Convert(Hypotheses, true, parsed(t, Hypotheses, wrap(strings.Replace(est, `"cite":"doi:10.1001/a"`, `"cite":"10.8888/made-up"`, 1))), studyEvidence(), Review{})
	if !strings.Contains(strings.Join(probs, "\n"), "hypothesis 1: reported cite 10.8888/made-up is neither") {
		t.Fatalf("unresolved reported cite: %q", probs)
	}
	var schema map[string]any
	if err := json.Unmarshal([]byte(Schema(Hypotheses)), &schema); err != nil {
		t.Fatal(err)
	}
	item := schema["properties"].(map[string]any)["hypotheses"].(map[string]any)["items"].(map[string]any)
	props := item["properties"].(map[string]any)
	if !slices.Contains(props["prediction_shape"].(map[string]any)["enum"].([]any), any("estimate")) {
		t.Errorf("prediction_shape enum lacks estimate")
	}
	for _, k := range []string{"method", "reported"} {
		if _, ok := props[k]; !ok || !slices.Contains(item["required"].([]any), any(k)) {
			t.Errorf("%s is not a required schema field", k)
		}
	}
}
