package review

import (
	"encoding/json"
	"fmt"
	"math/big"
	"slices"
	"strconv"
	"strings"
)

// OutcomeComparison defines how a frozen threshold in measure units or a
// named control value maps the comparison to the registered outcome labels.
type OutcomeComparison struct {
	Threshold *json.Number `json:"threshold"`
	Control   *string      `json:"control"`
	Less      string       `json:"less"`
	Equal     string       `json:"equal"`
	Greater   string       `json:"greater"`
}

func comparisonProblem(h HypothesisBody) string {
	c := h.Comparison
	if c == nil {
		return ""
	}
	if h.Prediction.Shape != "outcomes" || h.Measure == nil {
		return "a comparison needs an outcomes prediction and a declared measure"
	}
	if (c.Threshold == nil) == (c.Control == nil) {
		return "a comparison needs exactly one of threshold or control"
	}
	if c.Threshold != nil && exactThreshold(*c.Threshold) == nil {
		return "a comparison threshold must be a finite decimal number"
	}
	if c.Control != nil && (h.Control.Text == "" || *c.Control == "" || strings.TrimSpace(*c.Control) != *c.Control) {
		return "a control comparison needs a declared control and its planned value name"
	}

	labels := []string{c.Less, c.Equal, c.Greater}
	for _, label := range labels {
		if strings.TrimSpace(label) == "" || !slices.Contains(h.Prediction.Outcomes, label) {
			return "comparison labels must be registered outcomes"
		}
	}
	for _, label := range h.Prediction.Outcomes {
		if !slices.Contains(labels, label) {
			return "every registered outcome must be mapped by the comparison"
		}
	}
	return ""
}

func exactThreshold(n json.Number) *big.Rat {
	s := string(n)
	if len(s) == 0 || len(s) > 256 || !strings.ContainsRune("-0123456789", rune(s[0])) || !json.Valid([]byte(s)) {
		return nil
	}
	if i := strings.IndexAny(s, "eE"); i >= 0 {
		exponent, err := strconv.Atoi(s[i+1:])
		if err != nil || exponent < -9999 || exponent > 9999 {
			return nil
		}
	}
	r, _ := new(big.Rat).SetString(s)
	return r
}

func byNumericOutcome(h HypothesisBody, a Assessment, m map[string]Measured) (string, []string) {
	if problem := comparisonProblem(h); problem != "" {
		return Invalid, []string{problem}
	}
	c := h.Comparison
	measurement := m[a.Measurement]
	if !measurement.Bound || measurement.MeasureID != h.Measure.ID {
		return Invalid, []string{fmt.Sprintf("the measurement %q needs a verified binding to measure %q", a.Measurement, h.Measure.ID)}
	}
	value, ok := directionEffect(measurement, Measured{}, false)
	if !ok {
		return Invalid, []string{"the bound measurement could not be read as a number"}
	}

	var baseline *big.Rat
	var against string
	if c.Control != nil {
		name := *c.Control
		if a.Control == nil || *a.Control != name || a.Measurement == name {
			return Invalid, []string{fmt.Sprintf("the assessment needs the frozen control %q as a separate value", name)}
		}
		control := m[name]
		if !control.Bound || control.MeasureID != h.Measure.ID {
			return Invalid, []string{fmt.Sprintf("the control %q needs a verified binding to measure %q", name, h.Measure.ID)}
		}
		baseline, ok = directionEffect(control, Measured{}, false)
		if !ok {
			return Invalid, []string{"the bound control could not be read as a number"}
		}
		against = fmt.Sprintf("frozen control %q (%s)", name, control.BoundValue)
	} else {
		if a.Control != nil {
			return Invalid, []string{"the assessment must use the frozen threshold without substituting a control"}
		}
		baseline = exactThreshold(*c.Threshold)
		against = "frozen threshold " + c.Threshold.String()
	}

	label := c.Equal
	switch value.Cmp(baseline) {
	case -1:
		label = c.Less
	case 1:
		label = c.Greater
	}
	reason := fmt.Sprintf("comparing bound measurement %q (%s) with %s derives %q", a.Measurement, measurement.BoundValue, against, label)
	if a.Observed != nil {
		name := strings.TrimSpace(*a.Observed)
		observed := m[name]
		if !observed.FromScript || !observed.Reproduced {
			return Invalid, []string{reason, fmt.Sprintf("the reported outcome %q was not reproduced by a script run", name)}
		}
		if !strings.EqualFold(strings.TrimSpace(observed.Text), strings.TrimSpace(label)) {
			return Invalid, []string{reason, fmt.Sprintf("the reported outcome %q is %q and disagrees with the comparison", name, strings.TrimSpace(observed.Text))}
		}
	}
	if label == h.Prediction.Chosen {
		return Supported, []string{reason}
	}
	return Refuted, []string{reason, fmt.Sprintf("the predicted outcome was %q", h.Prediction.Chosen)}
}
