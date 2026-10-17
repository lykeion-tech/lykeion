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
	Column      string          `json:"column"`
	Round       int32           `json:"round"`
	InputDigest string          `json:"input_digest"` // hash of the records the gate read
	Checks      []Check         `json:"checks"`
	Verdict     Verdict         `json:"verdict"`
	Route       *Route          `json:"route,omitempty"`
	Reason      string          `json:"reason,omitempty"`
	Block       string          `json:"block,omitempty"`  // the Blocked kind, set with CannotProceed
	Addons      []AgentRunAddon `json:"addons,omitempty"` // the add-on each judged agent run ran with
}

// AgentRunAddon names an agent run the gate judged, the add-on hash it ran with
// (empty when none), and the daemon's mode and policy digest for it: in
// workspace_continuity the account's own setup shaped the run too.
type AgentRunAddon struct {
	AgentRun     string `json:"agent_run"`
	Role         string `json:"role"`
	Hash         string `json:"addon_hash"`
	ProfileMode  string `json:"profile_mode"`
	PolicyDigest string `json:"policy_digest"`
}

// Bands are the Jev probabilities at or above which a check passes (Yes) and
// below which it fails (No); between them the reviewer settles it.
type Bands struct {
	Yes float64 `json:"yes"`
	No  float64 `json:"no"`
}

var DefaultBands = Bands{Yes: 0.70, No: 0.35}

// Result maps a Jev probability onto a check result.
func (b Bands) Result(p float64) Result {
	switch {
	case p >= b.Yes:
		return Pass
	case p < b.No:
		return Fail
	}
	return Attention
}

// Decide passes only when at least one check ran and every check passed.
// Unsettled attention counts as failure: a missing answer is never approval.
// Among the failing checks, the one whose fix belongs to the earliest column
// sets the route; a tie keeps the first in check order. A target that is not
// a stage, or a stage after the gate's (a failure never routes forward),
// counts as the gate's own column.
func Decide(g *Gate) {
	if g.Verdict == CannotProceed {
		return
	}
	if len(g.Checks) == 0 {
		g.Verdict, g.Reason = VFail, "no checks ran"
		g.Route = &Route{Column: g.Column, Reason: g.Reason}
		return
	}
	g.Verdict, g.Route = VPass, nil
	for _, ch := range g.Checks {
		if ch.Result == Pass {
			continue
		}
		col := g.Column
		if i := StageIndex(ch.To); i >= 0 && i <= StageIndex(g.Column) {
			col = ch.To
		}
		if g.Route != nil && StageIndex(col) >= StageIndex(g.Route.Column) {
			continue
		}
		g.Verdict, g.Reason = VFail, ch.ID+": "+ch.Note
		g.Route = &Route{Column: col, Step: ch.Owner, Reason: g.Reason}
	}
}
