package review

import "fmt"

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
