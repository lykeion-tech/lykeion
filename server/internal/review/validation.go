package review

import (
	"fmt"
	"math"
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
// values are its measurement and control, the reported test, and the observed
// named outcome.
type Assessment struct {
	Hypothesis  string
	Measurement string
	Control     *string
	Test        *statcheck.Test
	Observed    *string
}

// Measured is a value recomputed by a script run, keyed by value name.
type Measured struct {
	Value      float64
	Text       string
	FromScript bool
	Reproduced bool
	RerunNote  string
	Key        string // the value record's key
	RerunKey   string // the rerun record's key
}

// StatusResult is one hypothesis's status with plain-sentence reasons and the
// record keys it rests on.
type StatusResult struct {
	Hypothesis string
	Status     string
	Reasons    []string
	Cites      []string
}

// Statuses sets each hypothesis's status from its assessment and the measured
// values. Invalid rules come first, then the shape rule, then the no-control cap.
func Statuses(hs []HypothesisBody, as []Assessment, m map[string]Measured) []StatusResult {
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
		if bad := invalidReasons(a, m); len(bad) > 0 {
			r.Status, r.Reasons = Invalid, bad
		} else {
			r.Status, r.Reasons = byShape(h.Prediction, a, m)
		}
		if r.Status == Supported && h.Control.None != "" {
			r.Status = Caveated
			r.Reasons = append(r.Reasons, "no control: "+h.Control.None)
		}
		for _, name := range []*string{&a.Measurement, a.Control} {
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

func invalidReasons(a Assessment, m map[string]Measured) []string {
	var bad []string
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
		case !v.Reproduced:
			s := fmt.Sprintf("the %s %q was not reproduced", role, names[role])
			if v.RerunNote != "" {
				s += ": " + v.RerunNote
			}
			bad = append(bad, s)
		}
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
	return bad
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

func byShape(p Prediction, a Assessment, m map[string]Measured) (string, []string) {
	meas := m[a.Measurement].Value
	switch p.Shape {
	case "range":
		if p.Low == nil || p.High == nil {
			return Inconclusive, []string{"the predicted range has no bounds"}
		}
		if meas >= *p.Low && meas <= *p.High {
			return Supported, []string{fmt.Sprintf("measured %g is inside the predicted range %g–%g", meas, *p.Low, *p.High)}
		}
		return Refuted, []string{fmt.Sprintf("measured %g is outside the predicted range %g–%g", meas, *p.Low, *p.High)}
	case "outcomes":
		if a.Observed == nil {
			return Inconclusive, []string{"no outcome was observed"}
		}
		obs := strings.TrimSpace(*a.Observed)
		if strings.EqualFold(obs, strings.TrimSpace(p.Chosen)) {
			return Supported, []string{fmt.Sprintf("the observed outcome %q is the predicted one", obs)}
		}
		for _, o := range p.Outcomes {
			if strings.EqualFold(obs, strings.TrimSpace(o)) {
				return Refuted, []string{fmt.Sprintf("the observed outcome %q is not the predicted %q", obs, p.Chosen)}
			}
		}
		return Inconclusive, []string{fmt.Sprintf("the observed outcome %q is not one of the listed outcomes", obs)}
	case "direction":
		return byDirection(p.Direction, a, meas, m)
	}
	return Inconclusive, []string{fmt.Sprintf("unknown prediction shape %q", p.Shape)}
}

func byDirection(dir string, a Assessment, meas float64, m map[string]Measured) (string, []string) {
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
	if effect == 0 || math.IsNaN(effect) {
		return Inconclusive, []string{"the measured effect is zero"}
	}
	if (dir == "increase") == (effect > 0) {
		return Supported, []string{fmt.Sprintf("the effect %g is in the predicted direction (%s) and significant (p < 0.05)", effect, dir)}
	}
	return Refuted, []string{fmt.Sprintf("the effect %g is opposite to the predicted direction (%s) and significant (p < 0.05)", effect, dir)}
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
