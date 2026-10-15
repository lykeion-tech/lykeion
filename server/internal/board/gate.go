package board

type Judge string

const (
	Code     Judge = "code"
	Jev      Judge = "jev"
	Reviewer Judge = "reviewer"
)

type Result string

const (
	Pass      Result = "pass"
	Fail      Result = "fail"
	Attention Result = "attention"
)

type Verdict string

const (
	VPass         Verdict = "pass"
	VFail         Verdict = "fail"
	CannotProceed Verdict = "cannot_proceed"
)

type Check struct {
	ID       string   `json:"id"`
	Judge    Judge    `json:"judge"`
	Owner    string   `json:"owner,omitempty"` // role that redoes its part; empty means all the stage roles
	To       string   `json:"to,omitempty"`    // set when the fix belongs to an earlier column
	Result   Result   `json:"result"`
	Evidence []string `json:"evidence,omitempty"`
	Model    string   `json:"model,omitempty"`
	Question string   `json:"question,omitempty"`
	P        *float64 `json:"p,omitempty"`
	Choice   string   `json:"choice,omitempty"`
	Note     string   `json:"note,omitempty"`
}

type Route struct {
	Column string `json:"column"`
	Step   string `json:"step,omitempty"`
	Reason string `json:"reason"`
}

type Gate struct {
	Column      string  `json:"column"`
	Round       int32   `json:"round"`
	InputDigest string  `json:"input_digest"` // hash of the records the gate read
	Checks      []Check `json:"checks"`
	Verdict     Verdict `json:"verdict"`
	Route       *Route  `json:"route,omitempty"`
	Reason      string  `json:"reason,omitempty"`
}

// FromP maps a Jev probability onto a check result.
func FromP(p float64) Result {
	switch {
	case p >= 0.70:
		return Pass
	case p < 0.35:
		return Fail
	}
	return Attention
}

// Decide passes only when at least one check ran and every check passed.
// Unsettled attention counts as failure: a missing answer is never approval.
func Decide(g *Gate) {
	if g.Verdict == CannotProceed {
		return
	}
	if len(g.Checks) == 0 {
		g.Verdict, g.Reason = VFail, "no checks ran"
		g.Route = &Route{Column: g.Column, Reason: g.Reason}
		return
	}
	for _, ch := range g.Checks {
		if ch.Result == Pass {
			continue
		}
		g.Verdict = VFail
		g.Reason = ch.ID + ": " + ch.Note
		col := g.Column
		if ch.To != "" {
			col = ch.To
		}
		g.Route = &Route{Column: col, Step: ch.Owner, Reason: g.Reason}
		return
	}
	g.Verdict, g.Route = VPass, nil
}
