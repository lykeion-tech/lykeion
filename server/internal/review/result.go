package review

import (
	"fmt"
	"strings"
)

const (
	Pass    = "pass"
	Unclear = "unclear"
	Fail    = "fail"
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

type Result struct {
	Column   string   `json:"column"`
	Attempt  int      `json:"attempt"`
	Items    []Item   `json:"items"`
	Missed   []Item   `json:"missed,omitempty"`
	Weakest  *Weakest `json:"weakest,omitempty"`
	Decision string   `json:"decision"`
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
func Decide(r Result, redos int32) string {
	bad := len(r.Items) == 0 || len(r.Missed) > 0
	unclear := false
	for _, it := range r.Items {
		switch it.Status {
		case Fail:
			bad = true
		case Unclear:
			unclear = true
		}
	}
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
			fmt.Fprintf(&b, "- %s: %s\n", it.Label, strings.Join(ids(it.Checks, false, Fail), ", "))
		}
	}
	for _, it := range r.Missed {
		fmt.Fprintf(&b, "- %s: consider this paper (it meets every criterion but was not cited)\n", it.Label)
	}
	b.WriteString("Redo only these. Items not listed stand.")
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
