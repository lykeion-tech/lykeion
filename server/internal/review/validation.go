package review

import (
	"fmt"
	"math"
	"math/big"
	"strconv"
	"strings"

	"lykeion/server/internal/statcheck"
)

// Validation statuses. Only code sets them; objections can only lower them.
const (
	Supported    = "supported"
	Refuted      = "refuted"
	Inconclusive = "inconclusive"
	Invalid      = "invalid"
	Caveated     = "caveated"
)

// Assessment is the lead's declaration for one hypothesis: which recorded
// values are its measurement and its control group's raw value (null when
// the measurement is already the effect), the reported test, and, for a
// hypothesis that names outcomes, the recorded value that shows the outcome.
type Assessment struct {
	Hypothesis  string          `json:"hypothesis"`
	Measurement string          `json:"measurement"`
	Control     *string         `json:"control"`
	Test        *statcheck.Test `json:"test"`
	Observed    *string         `json:"observed"`
}

// Measured is a value recomputed by a script run, keyed by value name.
// Number says whether its text is a number, read into Value.
type Measured struct {
	Bound             bool
	BoundValue        string // exact text of the verified numeric field in the replay output
	MeasureID         string
	Value             float64
	Number            bool
	Text              string
	FromScript        bool
	Reproduced        bool
	RerunNote         string
	ReplayUnavailable bool   // an older replay could not preserve this execution's artifact
	Key               string // the value record's key
	RerunKey          string // the rerun record's key
}

// StatusResult is one hypothesis's status with plain-sentence reasons and the
// record keys it rests on.
type StatusResult struct {
	Hypothesis string
	Status     string
	Reasons    []string
	Cites      []string
}

// Statuses sets each hypothesis's status from its assessment, the measured
// values and, by hypothesis text, trimmed, whether the rerun printed the
// test its assessment reports. Invalid rules come first, then the shape
// rule, then the no-control cap.
func Statuses(hs []HypothesisBody, as []Assessment, m map[string]Measured, tests map[string]bool) []StatusResult {
	byText := map[string]Assessment{}
	for _, a := range as {
		byText[strings.TrimSpace(a.Hypothesis)] = a
	}
	out := make([]StatusResult, 0, len(hs))
	for _, h := range hs {
		r := StatusResult{Hypothesis: h.Text}
		a, ok := byText[strings.TrimSpace(h.Text)]
		if !ok {
			r.Status, r.Reasons = Invalid, []string{"not assessed"}
			out = append(out, r)
			continue
		}
		bad, unavailable := assessmentProblems(h, a, m, tests[strings.TrimSpace(h.Text)])
		switch {
		case len(bad) > 0:
			r.Status, r.Reasons = Invalid, bad
		case len(unavailable) > 0:
			r.Status, r.Reasons = Inconclusive, unavailable
		default:
			r.Status, r.Reasons = byShape(h, a, m)
		}
		if none := strings.TrimSpace(h.Control.None); r.Status == Supported && none != "" {
			r.Status = Caveated
			r.Reasons = append(r.Reasons, "no control: "+none)
		}
		for _, name := range []*string{&a.Measurement, a.Control, a.Observed} {
			if name == nil {
				continue
			}
			v := m[*name]
			for _, k := range []string{v.Key, v.RerunKey} {
				if k != "" {
					r.Cites = append(r.Cites, k)
				}
			}
		}
		out = append(out, r)
	}
	return out
}

func assessmentProblems(h HypothesisBody, a Assessment, m map[string]Measured, testPrinted bool) (bad, unavailable []string) {
	names := map[string]string{"measurement": a.Measurement}
	order := []string{"measurement"}
	if a.Control != nil {
		names["control"] = *a.Control
		order = append(order, "control")
	}
	for _, role := range order {
		v, ok := m[names[role]]
		switch {
		case !ok:
			bad = append(bad, fmt.Sprintf("the %s %q has no recorded value", role, names[role]))
		case !v.FromScript:
			bad = append(bad, fmt.Sprintf("the %s %q was not computed by a script run", role, names[role]))
		case (h.Prediction.Shape == "direction" || h.Prediction.Shape == "range") && v.Bound && (h.Measure == nil || h.Measure.ID != v.MeasureID):
			bad = append(bad, fmt.Sprintf("the %s %q is bound to a different hypothesis measure", role, names[role]))
		case !v.Reproduced:
			s := fmt.Sprintf("the %s %q was not reproduced", role, names[role])
			if v.ReplayUnavailable && h.Comparison == nil {
				unavailable = append(unavailable, fmt.Sprintf("the %s %q could not be checked: %s", role, names[role], v.RerunNote))
				continue
			}
			if v.RerunNote != "" {
				s += ": " + v.RerunNote
			}
			bad = append(bad, s)
		case !v.Number && h.Prediction.Shape != "outcomes":
			bad = append(bad, fmt.Sprintf("the %s %q is %q, which is not a number", role, names[role], strings.TrimSpace(v.Text)))
		}
	}
	if a.Test != nil && !testPrinted {
		bad = append(bad, "the reported test was not found in any recorded run")
	}
	if a.Test != nil {
		ok, p, err := statcheck.Consistent(*a.Test)
		if err != nil {
			bad = append(bad, err.Error())
		} else if !ok {
			t := a.Test
			df := fmt.Sprint(t.DF1)
			if t.DF2 != 0 {
				df += "," + fmt.Sprint(t.DF2)
			}
			bad = append(bad, fmt.Sprintf("reported p %g does not match %s(%s)=%g, p≈%.2g", t.P, t.Kind, df, t.Statistic, p))
		}
	}
	return bad, unavailable
}

// significant reports whether a consistent reported test has p < 0.05.
func significant(t statcheck.Test) bool {
	switch t.PBound {
	case "=":
		return t.P < 0.05
	case "<":
		return t.P <= 0.05
	}
	return false
}

func byShape(h HypothesisBody, a Assessment, m map[string]Measured) (string, []string) {
	p := h.Prediction
	meas := m[a.Measurement].Value
	switch p.Shape {
	case "range":
		if p.Low == nil || p.High == nil {
			return Inconclusive, []string{"the predicted range has no bounds"}
		}
		inside, shown := meas >= *p.Low && meas <= *p.High, fmt.Sprint(meas)
		if measurement := m[a.Measurement]; measurement.Bound {
			value, ok := new(big.Rat).SetString(measurement.BoundValue)
			// The registered bounds use float64. Compare their shortest decimal forms to the replayed decimal.
			low, lowOK := new(big.Rat).SetString(fmt.Sprint(*p.Low))
			high, highOK := new(big.Rat).SetString(fmt.Sprint(*p.High))
			if !ok || !lowOK || !highOK {
				return Invalid, []string{"the bound measurement or its predicted range could not be read as a number"}
			}
			inside, shown = value.Cmp(low) >= 0 && value.Cmp(high) <= 0, measurement.BoundValue
		}
		if inside {
			return Supported, []string{fmt.Sprintf("measured %s is inside the predicted range %g–%g", shown, *p.Low, *p.High)}
		}
		return Refuted, []string{fmt.Sprintf("measured %s is outside the predicted range %g–%g", shown, *p.Low, *p.High)}
	case "outcomes":
		if h.Comparison != nil {
			return byNumericOutcome(h, a, m)
		}
		// The outcome is a recorded value the rerun reproduced, never the
		// lead's own words.
		if a.Observed == nil {
			return Inconclusive, []string{"no outcome was observed"}
		}
		name := strings.TrimSpace(*a.Observed)
		v, ok := m[name]
		switch {
		case !ok:
			return Inconclusive, []string{fmt.Sprintf("the observed outcome %q names no recorded value", name)}
		case !v.FromScript || !v.Reproduced:
			return Inconclusive, []string{fmt.Sprintf("the observed value %q was not reproduced by a script run", name)}
		}
		obs := strings.TrimSpace(v.Text)
		if strings.EqualFold(obs, strings.TrimSpace(p.Chosen)) {
			return Supported, []string{fmt.Sprintf("the observed value %q is %q, the predicted outcome", name, obs)}
		}
		for _, o := range p.Outcomes {
			if strings.EqualFold(obs, strings.TrimSpace(o)) {
				return Refuted, []string{fmt.Sprintf("the observed value %q is %q, not the predicted %q", name, obs, p.Chosen)}
			}
		}
		return Inconclusive, []string{fmt.Sprintf("the observed value %q is %q, which is not one of the named outcomes", name, obs)}
	case "direction":
		return byDirection(h, a, meas, m)
	}
	return Inconclusive, []string{fmt.Sprintf("unknown prediction shape %q", p.Shape)}
}

// signed are the tests whose statistic's sign gives the effect's direction.
var signed = map[string]bool{"t": true, "z": true, "r": true}

// byDirection reads the effect as the measurement less the control's raw
// value, or as the measurement itself when the assessment names no control
// value. A hypothesis that has a control but whose effect is read alone
// must have a signed test agree with the predicted direction.
func byDirection(h HypothesisBody, a Assessment, meas float64, m map[string]Measured) (string, []string) {
	dir := h.Prediction.Direction
	if a.Test == nil {
		return Inconclusive, []string{"no test reported"}
	}
	sig := significant(*a.Test)
	if dir == "no_change" {
		if sig {
			return Refuted, []string{"a significant difference (p < 0.05) was reported where no change was predicted"}
		}
		return Inconclusive, []string{"no significant difference was found, which does not show there is none"}
	}
	effect := meas
	if a.Control != nil {
		effect = meas - m[*a.Control].Value
	}
	if !sig {
		return Inconclusive, []string{"the reported test is not significant (p ≥ 0.05)"}
	}
	measurement := m[a.Measurement]
	var control Measured
	if a.Control != nil {
		control = m[*a.Control]
	}
	positive, zero := effect > 0, effect == 0 || math.IsNaN(effect)
	if measurement.Bound || control.Bound {
		exactEffect, ok := directionEffect(measurement, control, a.Control != nil)
		if !ok {
			return Inconclusive, []string{"the measured effect could not be read as a number"}
		}
		positive, zero = exactEffect.Sign() > 0, exactEffect.Sign() == 0
		effect, _ = exactEffect.Float64()
	}
	if zero {
		return Inconclusive, []string{"the measured effect is zero"}
	}
	shown := roundedLike(effect, measurement.Text)
	if measurement.Bound || control.Bound {
		shown = fmt.Sprintf("%g", effect)
	}
	if a.Control == nil && strings.TrimSpace(h.Control.Text) != "" && signed[a.Test.Kind] && (a.Test.Statistic > 0) != (dir == "increase") {
		return Inconclusive, []string{fmt.Sprintf("the effect %s was read without its control value, and the test statistic %g does not agree with the predicted direction (%s)", shown, a.Test.Statistic, dir)}
	}
	if (dir == "increase") == positive {
		return Supported, []string{fmt.Sprintf("the effect %s is in the predicted direction (%s) and significant (p < 0.05)", shown, dir)}
	}
	return Refuted, []string{fmt.Sprintf("the effect %s is opposite to the predicted direction (%s) and significant (p < 0.05)", shown, dir)}
}

// directionEffect subtracts the replayed fields without rounding away a
// small difference between two separately reported measurements.
func directionEffect(measurement, control Measured, hasControl bool) (*big.Rat, bool) {
	read := func(m Measured) (*big.Rat, bool) {
		if m.Bound {
			return new(big.Rat).SetString(m.BoundValue)
		}
		return new(big.Rat).SetString(strings.TrimSpace(m.Text))
	}
	m, ok := read(measurement)
	if !ok || !hasControl {
		return m, ok
	}
	c, ok := read(control)
	if !ok {
		return nil, false
	}
	return m.Sub(m, c), true
}

// roundedLike writes x to the decimals the reported text states.
func roundedLike(x float64, text string) string {
	text = strings.TrimSpace(text)
	if strings.ContainsAny(text, "eE") {
		return fmt.Sprintf("%g", x)
	}
	places := 0
	if _, frac, ok := strings.Cut(text, "."); ok {
		places = len(frac)
	}
	return strconv.FormatFloat(x, 'f', places, 64)
}

// Lower applies an objection to a status. It never raises one.
func Lower(status, objectionType string) string {
	switch objectionType {
	case "wrong_test", "leakage", "unit_mismatch", "missing_control":
		return Invalid
	case "overreach":
		switch status {
		case Supported:
			return Caveated
		case Caveated, Refuted:
			return Inconclusive
		}
	}
	return status
}
