package review

import (
	"encoding/json"
	"strings"
	"testing"
)

func numericOutcomeHypothesis(t *testing.T, comparison string) HypothesisBody {
	t.Helper()
	raw := `{
		"text":"Anchor-first accuracy exceeds the baseline.",
		"prediction":{"shape":"outcomes","outcomes":["above","equal","below"],"chosen":"above"},
		"measure":{"id":"accuracy","name":"Accuracy","unit":"fraction"},
		"control":{"text":"Matched baseline"},
		"cites":["r1"],
		"comparison":` + comparison + `
	}`
	var h HypothesisBody
	if err := json.Unmarshal([]byte(raw), &h); err != nil {
		t.Fatal(err)
	}
	return h
}

func TestNumericOutcomeRejectsWrongReproducedCategory(t *testing.T) {
	h := numericOutcomeHypothesis(t, `{"threshold":0.50,"control":null,"less":"below","equal":"equal","greater":"above"}`)
	measurement := good(0.5833)
	measurement.Bound, measurement.BoundValue, measurement.MeasureID = true, "0.5833333333333334", "accuracy"
	got := run(t, h, &Assessment{Hypothesis: h.Text, Measurement: "value", Observed: sp("label")}, map[string]Measured{
		"value": measurement, "label": {Text: "below", FromScript: true, Reproduced: true, Key: "r2", RerunKey: "r3"},
	})
	if got.Status != Invalid || !strings.Contains(strings.Join(got.Reasons, " "), "derives") {
		t.Fatalf("wanted an invalid label with the derived outcome in the reason, got %+v", got)
	}
}

func TestNumericOutcomeUsesExactFrozenThreshold(t *testing.T) {
	h := numericOutcomeHypothesis(t, `{"threshold":0.50000000000000000001,"control":null,"less":"below","equal":"equal","greater":"above"}`)
	for _, tt := range []struct {
		value, label, want string
	}{
		{"0.50000000000000000002", "above", Supported},
		{"0.50000000000000000001", "equal", Refuted},
		{"0.50000000000000000000", "below", Refuted},
	} {
		t.Run(tt.label, func(t *testing.T) {
			v := good(0.5)
			v.Bound, v.BoundValue, v.MeasureID = true, tt.value, "accuracy"
			got := run(t, h, &Assessment{Hypothesis: h.Text, Measurement: "value"}, map[string]Measured{"value": v})
			if got.Status != tt.want || !strings.Contains(strings.Join(got.Reasons, " "), `"`+tt.label+`"`) {
				t.Fatalf("wanted exact comparison to derive %s and %s, got %+v", tt.label, tt.want, got)
			}
		})
	}
}

func TestNumericOutcomeUsesFrozenBoundControl(t *testing.T) {
	h := numericOutcomeHypothesis(t, `{"threshold":null,"control":"baseline","less":"below","equal":"equal","greater":"above"}`)
	measure := good(0.5)
	measure.Key = "r1"
	measure.Bound, measure.BoundValue, measure.MeasureID = true, "0.50000000000000000001", "accuracy"
	baseline := good(0.5)
	baseline.Key = "r2"
	baseline.Bound, baseline.BoundValue, baseline.MeasureID = true, "0.50000000000000000002", "accuracy"
	assessment := Assessment{Hypothesis: h.Text, Measurement: "treatment", Control: sp("baseline")}
	values := map[string]Measured{"treatment": measure, "baseline": baseline, "other": baseline}
	if got := run(t, h, &assessment, values); got.Status != Refuted || !strings.Contains(got.Reasons[0], `"below"`) {
		t.Fatalf("wanted exact below-control result, got %+v", got)
	}
	assessment.Control = sp("other")
	if got := run(t, h, &assessment, values); got.Status != Invalid {
		t.Fatalf("wanted wrong control to fail, got %+v", got)
	}
	assessment.Control = sp("baseline")
	baseline.MeasureID = "different"
	values["baseline"] = baseline
	if got := run(t, h, &assessment, values); got.Status != Invalid {
		t.Fatalf("wanted control with a different measure to fail, got %+v", got)
	}
}

func TestCategoricalOutcomeWithoutDefinitionPreservesLegacy(t *testing.T) {
	for _, tt := range []struct {
		name, chosen, reversed, observed, original, flipped string
	}{
		{"length-compliance", "Below perfect compliance", "Perfect compliance", "Below perfect compliance", Supported, Refuted},
		{"length-contrast", "Positive matched gap", "Zero or negative matched gap", "Positive matched gap", Supported, Refuted},
		{"premise-contrast", "First exceeds last", "First trails last", "First ties last", Refuted, Refuted},
		{"premise-accuracy", "Above fixed-label baseline", "Below fixed-label baseline", "Above fixed-label baseline", Supported, Refuted},
		{"awareness-auc", "Above 0.5", "At or below 0.5", "At or below 0.5", Refuted, Supported},
		{"awareness-brier", "Below 0.25", "At or above 0.25", "At or above 0.25", Refuted, Supported},
		{"genuinely categorical", "normal stop", "abstention", "normal stop", Supported, Refuted},
	} {
		t.Run(tt.name, func(t *testing.T) {
			for _, bound := range []bool{true, false} {
				h := HypothesisBody{Text: "h", Control: Control{Text: "baseline"},
					Measure:    &Measure{ID: "metric", Name: "Metric", Unit: "fraction"},
					Prediction: Prediction{Shape: "outcomes", Outcomes: []string{tt.chosen, tt.reversed, tt.observed}, Chosen: tt.chosen}}
				a := &Assessment{Hypothesis: h.Text, Measurement: "measurement", Observed: sp("observed")}
				v := good(0.5)
				if bound {
					v.Bound, v.BoundValue, v.MeasureID = true, "0.5", "metric"
				}
				m := map[string]Measured{"measurement": v, "observed": {Text: tt.observed, FromScript: true, Reproduced: true}}
				if got := run(t, h, a, m); got.Status != tt.original {
					t.Fatalf("original with binding %t: %+v", bound, got)
				}
				h.Prediction.Chosen = tt.reversed
				if got := run(t, h, a, m); got.Status != tt.flipped {
					t.Fatalf("reversed with binding %t: %+v", bound, got)
				}
			}
		})
	}
}
