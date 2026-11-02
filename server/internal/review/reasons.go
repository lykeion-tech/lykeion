package review

import (
	"fmt"
	"strings"
)

// Reasons lists, one line each, why a column was sent back or blocked: every
// failed check of every failed item (its question and Jev's percentage, or
// what the server found), every missed paper, and the empty hand-in when
// nothing stood. A redo or a block always has at least one.
func Reasons(r Result) []string {
	var out []string
	standing := 0
	for _, it := range r.Items {
		if it.Status != Withdrawn {
			standing++
		}
		if it.Status != Fail {
			continue
		}
		n := len(out)
		for _, c := range it.Checks {
			if c.Status == Fail && !c.Warning {
				out = append(out, it.Label+": "+reason(c))
			}
		}
		if len(out) == n {
			out = append(out, it.Label+": it failed Jev's review")
		}
	}
	for _, it := range r.Missed {
		out = append(out, it.Label+": consider this paper (it meets every criterion but was not cited)")
	}
	if standing == 0 {
		switch r.Column {
		case "grounding":
			out = append(out, "The Team handed in no papers and no 'nothing qualifies' statement.")
		case "hypotheses":
			out = append(out, "The Team handed in no hypotheses.")
		default:
			out = append(out, "The Team handed in no outputs or values.")
		}
	}
	if len(out) == 0 && Decide(r, 0) == DecideRedo {
		out = append(out, "Jev's review found problems with this column.")
	}
	return out
}

func reason(c Check) string {
	switch {
	case c.Question != "" && c.P != nil && c.Inverted:
		return fmt.Sprintf("\"%s\" (Jev answered yes at %.0f%%, and a yes fails this check)", c.Question, (1-*c.P)*100)
	case c.Question != "" && c.P != nil:
		return fmt.Sprintf("\"%s\" (Jev scored %.0f%%)", c.Question, *c.P*100)
	case c.Detail != "":
		return c.ID + ": " + c.Detail
	}
	return c.ID + " check failed"
}

// AllWithdrawn says whether the Team withdrew every hypothesis it had, so
// nothing is left to test; the owner decides what happens next.
func AllWithdrawn(r Result) bool {
	if r.Column != "hypotheses" || len(r.Items) == 0 {
		return false
	}
	for _, it := range r.Items {
		if it.Status != Withdrawn {
			return false
		}
	}
	return true
}

// WithdrawalReasons are the reasons the Team gave for withdrawing its items.
func WithdrawalReasons(r Result) []string {
	var out []string
	for _, it := range r.Items {
		for _, c := range it.Checks {
			if c.ID == "withdrawn" {
				out = append(out, c.Detail)
			}
		}
	}
	return out
}

// OpenDoubts lists, one entry per item, the Jev questions left unclear and
// their percentages, for the owner of a study that passed with them.
func OpenDoubts(r Result) []string {
	var out []string
	for _, it := range r.Items {
		var qs []string
		for _, c := range it.Checks {
			if c.Status == Unclear && !c.Warning && c.Question != "" && c.P != nil {
				qs = append(qs, fmt.Sprintf("%s, %.0f%%", c.Question, *c.P*100))
			}
		}
		if len(qs) > 0 {
			out = append(out, fmt.Sprintf("%s (%s)", it.Label, strings.Join(qs, "; ")))
		}
	}
	return out
}
