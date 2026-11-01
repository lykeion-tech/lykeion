package review

import (
	"fmt"
	"math"
	"strings"
)

const (
	Pass    = "pass"
	Unclear = "unclear"
	Fail    = "fail"
	// Withdrawn is an item the agents dropped after it failed; it neither
	// passes nor fails the column.
	Withdrawn = "withdrawn"
)

const (
	DecidePass  = "pass"
	DecideCarry = "carry"
	DecideRedo  = "redo"
	DecideBlock = "block"
)

type Check struct {
	ID       string   `json:"id"`                 // e.g. "exists", "criterion:2", "falsifiable"
	By       string   `json:"by"`                 // "server" | "jev"
	Question string   `json:"question,omitempty"` // the exact Jev instructions
	Detail   string   `json:"detail,omitempty"`   // server checks: what was found
	P        *float64 `json:"p,omitempty"`        // Jev's probability of yes, after any inversion
	Status   string   `json:"status"`
	Warning  bool     `json:"warning,omitempty"` // counts toward nothing but the Review surface
}

type Item struct {
	Key   string `json:"key"` // record key; "" for a missed paper
	Kind  string `json:"kind"`
	Label string `json:"label"` // the paper id, accession, or the first 80 characters of the text
	// Identity is what matches the item across declarations and attempts;
	// the label is for display only.
	Identity string  `json:"identity,omitempty"`
	Status   string  `json:"status"`
	Carried  bool    `json:"carried,omitempty"`
	Checks   []Check `json:"checks"`
}

type Weakest struct {
	Key string  `json:"key"`
	P   float64 `json:"p"`
}

// Recall is what the server's search for uncited papers found: the query
// it last sent, the hits it returned and how many Jev screened.
type Recall struct {
	Query    string `json:"query"`
	Hits     int    `json:"hits"`
	Screened int    `json:"screened"`
}

type Result struct {
	Column   string   `json:"column"`
	Attempt  int      `json:"attempt"`
	Items    []Item   `json:"items"`
	Missed   []Item   `json:"missed,omitempty"`
	Recall   *Recall  `json:"recall,omitempty"`
	Weakest  *Weakest `json:"weakest,omitempty"`
	Decision string   `json:"decision"`
}

// stored is p as a review keeps it, to 3 places; statuses are taken from p
// as Jev gave it.
func stored(p float64) float64 {
	return math.Round(p*1000) / 1000
}

// StatusOf maps Jev's probability of yes to a status.
func StatusOf(p float64) string {
	switch {
	case p >= 0.70:
		return Pass
	case p < 0.35:
		return Fail
	}
	return Unclear
}

// ItemStatus is the worst status among the non-warning checks.
func ItemStatus(cs []Check) string {
	s := Pass
	for _, c := range cs {
		if c.Warning {
			continue
		}
		if c.Status == Fail {
			return Fail
		}
		if c.Status != Pass {
			s = Unclear
		}
	}
	return s
}

// Decide: any item failing, any missed paper, or no items at all is redo
// while no redo was used, else block; any unclear item is carry; else pass.
// Withdrawn items do not count.
func Decide(r Result, redos int32) string {
	bad := len(r.Missed) > 0
	unclear, standing := false, 0
	for _, it := range r.Items {
		switch it.Status {
		case Fail:
			bad = true
		case Unclear:
			unclear = true
		case Withdrawn:
			continue
		}
		standing++
	}
	bad = bad || standing == 0
	switch {
	case bad && redos == 0:
		return DecideRedo
	case bad:
		return DecideBlock
	case unclear:
		return DecideCarry
	}
	return DecidePass
}

// ids lists the ids of checks with one of the statuses; warnings only when asked.
func ids(cs []Check, warnings bool, statuses ...string) []string {
	var out []string
	for _, c := range cs {
		if c.Warning && !warnings {
			continue
		}
		for _, s := range statuses {
			if c.Status == s {
				out = append(out, c.ID)
				break
			}
		}
	}
	return out
}

func RedoNote(r Result) string {
	var b strings.Builder
	b.WriteString("Jev's review sent back these items:\n")
	for _, it := range r.Items {
		if it.Status == Fail {
			name := it.Label
			if it.Key != "" {
				name += " (" + it.Key + ")"
			}
			fmt.Fprintf(&b, "- %s:\n", name)
			for _, c := range it.Checks {
				if c.Status != Fail || c.Warning {
					continue
				}
				switch {
				case c.Question != "" && c.P != nil:
					fmt.Fprintf(&b, "  - %s: \"%s\" (scored %.2f)\n", c.ID, c.Question, *c.P)
				case c.Detail != "":
					fmt.Fprintf(&b, "  - %s: %s\n", c.ID, c.Detail)
				default:
					fmt.Fprintf(&b, "  - %s\n", c.ID)
				}
			}
		}
	}
	for _, it := range r.Missed {
		fmt.Fprintf(&b, "- %s: consider this paper (it meets every criterion but was not cited)\n", it.Label)
	}
	b.WriteString("Items not listed stand. Answer each listed item: declare it again with the same id or text once it is fixed, or withdraw it with " +
		"`lyk record put withdrawn --content '{\"item\":\"rN\",\"reason\":\"…\"}'`. " +
		"A replacement with a different id or text is a new item, so also withdraw the old one. " +
		"Items you leave unanswered count as failed, and a second failure blocks the study.")
	return b.String()
}

// CarryNote lists unclear checks, and warning checks that failed or were
// unclear, so the next stage's agents see them; warnings never reach Decide.
func CarryNote(r Result) string {
	var b strings.Builder
	for _, it := range r.Items {
		l := append(ids(it.Checks, false, Unclear), warnIDs(it.Checks)...)
		if len(l) > 0 {
			fmt.Fprintf(&b, "- %s: %s\n", it.Label, strings.Join(l, ", "))
		}
	}
	if b.Len() == 0 {
		return ""
	}
	col := r.Column
	if col != "" {
		col = strings.ToUpper(col[:1]) + col[1:]
	}
	return fmt.Sprintf("Unclear in %s (carried forward, check them as you work):\n%s", col, strings.TrimSuffix(b.String(), "\n"))
}

func warnIDs(cs []Check) []string {
	var out []string
	for _, c := range cs {
		if c.Warning && (c.Status == Fail || c.Status == Unclear) {
			out = append(out, c.ID)
		}
	}
	return out
}
