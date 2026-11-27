package review

import (
	"encoding/json"
	"strings"
	"testing"

	"lykeion/server/internal/board"
)

const estMeasure = `{"id":"prob_pr_65","name":"predicted probability of PR at 65","unit":"fraction"}`

func estBody(extra string) string {
	return `{"text":"t","prediction":{"shape":"estimate"},"control":{},"cites":["a"],"measure":` + estMeasure +
		`,"method":"logistic regression of PR on age, all patients"` + extra + `}`
}

func TestEstimateHypothesisRecord(t *testing.T) {
	cases := []struct{ name, body, want string }{
		{"valid", estBody(""), ""},
		{"valid with reported", estBody(`,"reported":{"value":0.39,"source":"doi:10.1/a","key":"r3"}`), ""},
		{"no measure", strings.Replace(estBody(""), `,"measure":`+estMeasure, "", 1), "an estimate needs a measure"},
		{"no method", strings.Replace(estBody(""), `"logistic regression of PR on age, all patients"`, `""`, 1), "an estimate needs a method"},
		{"long method", strings.Replace(estBody(""), `"logistic regression of PR on age, all patients"`, `"`+strings.Repeat("x", 2001)+`"`, 1), "method"},
		{"with low", strings.Replace(estBody(""), `{"shape":"estimate"}`, `{"shape":"estimate","low":1}`, 1), "an estimate predicts no value"},
		{"with direction", strings.Replace(estBody(""), `{"shape":"estimate"}`, `{"shape":"estimate","direction":"increase"}`, 1), "an estimate predicts no value"},
		{"with comparison", estBody(`,"comparison":{"threshold":1,"less":"a","equal":"b","greater":"c"}`), "an estimate predicts no value"},
		{"method on direction", `{"text":"t","prediction":{"shape":"direction","direction":"increase"},"control":{"text":"c"},"cites":["a"],"method":"m"}`, "only an estimate has a method"},
		{"direction still needs control", `{"text":"t","prediction":{"shape":"direction","direction":"increase"},"control":{},"cites":["a"]}`, "control needs exactly one of text or none"},
	}
	for _, c := range cases {
		err := Validate(KindHypothesis, board.Hypotheses, []byte(c.body))
		switch {
		case c.want == "" && err != nil:
			t.Errorf("%s: %v", c.name, err)
		case c.want != "" && (err == nil || !strings.Contains(err.Error(), c.want)):
			t.Errorf("%s: got %v, want %q", c.name, err, c.want)
		}
	}
}

// Stored bodies of the three earlier shapes must serialize exactly as before.
func TestOldHypothesisBodiesSerializeUnchanged(t *testing.T) {
	lo, hi := 0.5, 0.8
	for _, c := range []struct {
		b    HypothesisBody
		want string
	}{
		{HypothesisBody{Text: "h", Prediction: Prediction{Shape: "range", Low: &lo, High: &hi, Uncertainty: "90%"}, Control: Control{Text: "c"}, Cites: []string{"r1"}, Measure: &Measure{ID: "m", Name: "M", Unit: "u"}},
			`{"text":"h","prediction":{"shape":"range","low":0.5,"high":0.8,"uncertainty":"90%"},"control":{"text":"c"},"cites":["r1"],"measure":{"id":"m","name":"M","unit":"u"}}`},
		{HypothesisBody{Text: "h", Prediction: Prediction{Shape: "direction", Direction: "increase"}, Control: Control{None: "none"}, Cites: []string{"r1"}},
			`{"text":"h","prediction":{"shape":"direction","direction":"increase"},"control":{"none":"none"},"cites":["r1"]}`},
	} {
		got, _ := json.Marshal(c.b)
		if string(got) != c.want {
			t.Errorf("got  %s\nwant %s", got, c.want)
		}
	}
	if !IsEstimate(HypothesisBody{Prediction: Prediction{Shape: "estimate"}}) || IsEstimate(HypothesisBody{Prediction: Prediction{Shape: "range"}}) {
		t.Error("IsEstimate")
	}
}
