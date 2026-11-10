// Package handin is the structured final answer of a study run: its types,
// strict decoding and shape problems. It must not import internal/flow.
package handin

import (
	"bytes"
	"encoding/json"
	"fmt"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/google/uuid"

	"lykeion/server/internal/board"
	"lykeion/server/internal/review"
	"lykeion/server/internal/statcheck"
)

type Kind string

const (
	Aristotle           Kind = "aristotle"
	ManualDecisionDraft Kind = "manual_decision_draft"
	Grounding           Kind = "grounding"
	Hypotheses          Kind = "hypotheses"
	Experimentation     Kind = "experimentation"
	Auditor             Kind = "auditor"
	Autoresearch        Kind = "autoresearch"
	Validation          Kind = "validation"
	Objector            Kind = "objector"
	Report              Kind = "report"
)

// KindFor is the hand-in kind a run owes; Coresearch and other runs owe none.
func KindFor(runKind, role, column string) (Kind, bool) {
	switch {
	case runKind == "aristotle":
		return Aristotle, true
	case runKind == "autoresearch":
		return Autoresearch, true
	case runKind == "report":
		return Report, true
	case role == "auditor":
		return Auditor, true
	case role == "objector" && column == board.Validation:
		return Objector, true
	}
	switch column {
	case board.Validation:
		return Validation, true
	case board.Grounding:
		return Grounding, true
	case board.Hypotheses:
		return Hypotheses, true
	case board.Experimentation:
		return Experimentation, true
	}
	return "", false
}

// Leads says whether a run's role works its column through to done: a
// Team's leader, or a study's single agent. Only its hand-in becomes items.
func Leads(role string) bool { return role == "leader" || role == "agent" }

type Reply struct {
	Item   string `json:"item"`
	Action string `json:"action"` // corrected | withdrawn | declined
	Reason string `json:"reason"`
}
type Paper struct {
	ID  string `json:"id"`
	Why string `json:"why"`
}
type Dataset struct {
	Accession string `json:"accession"`
	Why       string `json:"why"`
}
type NoQualifying struct {
	Reason string `json:"reason"`
}
type Hypothesis struct {
	Text            string   `json:"text"`
	PredictionShape string   `json:"prediction_shape"`
	Direction       *string  `json:"direction"`
	Outcomes        []string `json:"outcomes"`
	Chosen          *string  `json:"chosen"`
	Low             *float64 `json:"low"`
	High            *float64 `json:"high"`
	Uncertainty     *string  `json:"uncertainty"`
	Control         *string  `json:"control"`
	NoControl       *string  `json:"no_control"` // the reason there is no control
	Cites           []string `json:"cites"`
}
type Value struct {
	Name  string `json:"name"`
	Value string `json:"value"` // text, so decimals stay exact
	File  string `json:"file"`
}
type Concern struct {
	Item    string `json:"item"`
	Concern string `json:"concern"`
}
type Test struct {
	Kind      string       `json:"kind"`
	Statistic json.Number  `json:"statistic"` // kept as written, so its decimals count
	DF1       *json.Number `json:"df1"`
	DF2       *json.Number `json:"df2"`
	P         json.Number  `json:"p"`
	PBound    string       `json:"p_bound"`
}
type Assessment struct {
	Hypothesis  string  `json:"hypothesis"`
	Measurement string  `json:"measurement"`
	Control     *string `json:"control"`
	Test        *Test   `json:"test"`
	Observed    *string `json:"observed"`
}
type Verdict struct {
	Claim   string `json:"claim"`
	Verdict string `json:"verdict"`
	Source  string `json:"source"`
	Quote   string `json:"quote"`
}
type FlaggedError struct {
	Source  string `json:"source"`
	Quote   string `json:"quote"`
	Problem string `json:"problem"`
}

// Assessment is the review item this assessment becomes; a test's digits are
// the decimals its numbers were written with.
func (a Assessment) Assessment() review.Assessment {
	r := review.Assessment{Hypothesis: a.Hypothesis, Measurement: a.Measurement, Control: a.Control, Observed: a.Observed}
	if t := a.Test; t != nil {
		r.Test = &statcheck.Test{Kind: t.Kind, PBound: t.PBound, StatDigits: digits(t.Statistic), PDigits: digits(t.P)}
		r.Test.Statistic, _ = t.Statistic.Float64()
		r.Test.P, _ = t.P.Float64()
		if t.DF1 != nil {
			r.Test.DF1, _ = t.DF1.Float64()
		}
		if t.DF2 != nil {
			r.Test.DF2, _ = t.DF2.Float64()
		}
	}
	return r
}

// digits are the decimals of a JSON number's text: 2.310 has 3, 4e-3 has 3.
func digits(n json.Number) int {
	s := strings.ToLower(string(n))
	mant, exp, _ := strings.Cut(s, "e")
	d := 0
	if _, frac, ok := strings.Cut(mant, "."); ok {
		d = len(frac)
	}
	if e, err := strconv.Atoi(exp); err == nil {
		d -= e
	}
	return max(d, 0)
}

// ObjectionIn is one objection as the objector files it: target is a label
// from its brief, a value name or a saved file's name.
type ObjectionIn struct {
	Target  string  `json:"target"`
	Type    string  `json:"type"`
	Quote   *string `json:"quote"`
	Problem string  `json:"problem"`
}

// ObjectionTypes are the kinds of error an objection can name.
var ObjectionTypes = []string{"wrong_test", "leakage", "unit_mismatch", "missing_control", "overreach"}

// DecisionKinds are the questions an agent may put to the researcher.
var DecisionKinds = []string{"scope", "change_question", "ethics", "cost", "access"}

// MinDecisionWait and MaxDecisionWait bound how far ahead an agent may set
// its decision's expiry.
const (
	MinDecisionWait = time.Hour
	MaxDecisionWait = 30 * 24 * time.Hour
)

// OptionIn is one answer an agent offers the researcher.
type OptionIn struct {
	ID          string `json:"id"`
	Label       string `json:"label"`
	Consequence string `json:"consequence"`
}

// DecisionIn is the question an ask hand-in puts to the researcher; a lead
// raising a member's question names that member in FromMember.
type DecisionIn struct {
	Kind        string         `json:"kind"`
	Title       string         `json:"title"`
	Body        string         `json:"body"`
	Options     []OptionIn     `json:"options"`
	QuestionSet *QuestionSetIn `json:"question_set"`
	ExpiresAt   *time.Time     `json:"expires_at"`
	FromMember  *string        `json:"from_member"`
}

// QuestionSetIn is a group of questions raised as one decision.
type QuestionSetIn struct {
	Questions []QuestionIn `json:"questions"`
}

type QuestionIn struct {
	ID         string     `json:"id"`
	Prompt     string     `json:"prompt"`
	HelpText   *string    `json:"help_text"`
	Required   *bool      `json:"required"`
	Mode       string     `json:"mode"`
	Options    []OptionIn `json:"options"`
	AllowOther *bool      `json:"allow_other"`
}

var optionID = regexp.MustCompile(`^[a-z0-9_]{1,40}$`)

var localAnswerPath = regexp.MustCompile(`(?i)(^|[\s(\[<"'\x60])(?:file://|[a-z]:[\\/]|\\\\[a-z0-9._-]+\\[^\\\s]+|~[\\/]|/(?:users|home|private|tmp|var|mnt|media|opt|root|workspace|workspaces|data|srv|run|volumes|etc|usr|proc|dev)/)`)
var absoluteMarkdownDestination = regexp.MustCompile(`\]\(\s*<?(/[^\s)>]+)`)
var absoluteFilePath = regexp.MustCompile(`(?i)(^|[\s(\[<"'\x60])(/(?:[^/\s<>"'\x60]+/)+[^/\s<>"'\x60]+\.[a-z0-9][a-z0-9_-]*)`)

func appRoute(path string) bool {
	root := strings.SplitN(strings.TrimPrefix(path, "/"), "/", 2)[0]
	if i := strings.IndexAny(root, "?#"); i >= 0 {
		root = root[:i]
	}
	return slices.Contains([]string{"studies", "my-studies", "agents", "teams", "projects", "coresearch", "decisions", "settings", "colleagues", "workflows", "autoresearch", "machines", "inbox", "updates", "signin", "invite", "onboarding", "api", "auth", "ws", "doi"}, root)
}

func hasLocalAnswerPath(answer string) bool {
	// Code examples can legitimately mention local paths; check the prose around them.
	var prose strings.Builder
	var fence byte
	fenceLen := 0
	for _, line := range strings.Split(answer, "\n") {
		trimmed := strings.TrimLeft(line, " ")
		if len(line)-len(trimmed) <= 3 && len(trimmed) >= 3 && (trimmed[0] == '`' || trimmed[0] == '~') {
			n := 0
			for n < len(trimmed) && trimmed[n] == trimmed[0] {
				n++
			}
			if n >= 3 && fence == 0 {
				fence, fenceLen = trimmed[0], n
				continue
			}
			if fence != 0 && trimmed[0] == fence && n >= fenceLen && strings.TrimSpace(trimmed[n:]) == "" {
				fence = 0
				continue
			}
		}
		if fence == 0 {
			prose.WriteString(line)
			prose.WriteByte('\n')
		}
	}
	answer = prose.String()
	if localAnswerPath.MatchString(answer) {
		return true
	}
	for _, match := range absoluteFilePath.FindAllStringSubmatch(answer, -1) {
		if !appRoute(match[2]) {
			return true
		}
	}
	for _, match := range absoluteMarkdownDestination.FindAllStringSubmatch(answer, -1) {
		dest := match[1]
		if strings.HasPrefix(dest, "//") {
			continue
		}
		if !appRoute(dest) {
			return true
		}
	}
	return false
}

type NewStudy struct {
	Title    string `json:"title"`
	Question string `json:"question"`
}

// Statement is one marked claim or gap in a lab answer.
type Statement struct {
	Kind  string   `json:"kind"`
	Text  string   `json:"text"`
	Cites []string `json:"cites"`
}

// HandIn holds every field of every kind; Parse refuses fields outside the chosen schema.
type ManualDraft struct {
	Title    string  `json:"title"`
	Context  string  `json:"context"`
	Deadline *string `json:"deadline"`
	CardID   *string `json:"card_id"`
}

type HandIn struct {
	Message              string         `json:"message"`
	Draft                *ManualDraft   `json:"draft"`
	Statements           []Statement    `json:"statements"`
	Status               string         `json:"status"` // done | could_not_do | ask
	Decision             *DecisionIn    `json:"decision"`
	Answer               string         `json:"answer"`
	Reason               *string        `json:"reason"`
	BlockedByEnvironment *string        `json:"blocked_by_environment"`
	BlockedByMemberRun   *string        `json:"blocked_by_member_run"`
	Replies              []Reply        `json:"replies"`
	Papers               []Paper        `json:"papers"`
	Datasets             []Dataset      `json:"datasets"`
	NoQualifying         *NoQualifying  `json:"no_qualifying"`
	Hypotheses           []Hypothesis   `json:"hypotheses"`
	Outputs              []string       `json:"outputs"`
	Values               []Value        `json:"values"`
	Concerns             []Concern      `json:"concerns"`
	Report               *string        `json:"report"`
	NewStudies           []NewStudy     `json:"new_studies"`
	Assessments          []Assessment   `json:"assessments"`
	Verdicts             []Verdict      `json:"verdicts"`
	FlaggedErrs          []FlaggedError `json:"flagged_errors"`
	Objections           []ObjectionIn  `json:"objections"`
}

// Body is the review item this hypothesis becomes, citing cites.
func (h Hypothesis) Body(cites []string) review.HypothesisBody {
	p := review.Prediction{Shape: h.PredictionShape, Outcomes: h.Outcomes, Low: h.Low, High: h.High}
	p.Direction, p.Chosen, p.Uncertainty = deref(h.Direction), deref(h.Chosen), deref(h.Uncertainty)
	if len(p.Outcomes) == 0 {
		p.Outcomes = nil
	}
	c := review.Control{Text: deref(h.Control), None: deref(h.NoControl)}
	return review.HypothesisBody{Text: h.Text, Prediction: p, Control: c, Cites: cites}
}

func deref(s *string) string {
	if s == nil {
		return ""
	}
	return *s
}

// Parse decodes a hand-in strictly and lists what is wrong with its shape,
// one plain sentence each, led by where it is. Nothing wrong means no problems.
func Parse(k Kind, raw []byte) (HandIn, []string) {
	var h HandIn
	if k == Report {
		_, probs := parseReport(raw)
		return h, probs
	}
	var keys map[string]json.RawMessage
	if err := json.Unmarshal(raw, &keys); err != nil {
		return h, []string{"the hand-in is not one JSON object"}
	}
	var probs []string
	var allowed struct{ Properties map[string]any }
	_ = json.Unmarshal([]byte(Schema(k)), &allowed)
	for key := range keys {
		if _, ok := allowed.Properties[key]; !ok {
			if k == Aristotle {
				probs = append(probs, "only statements are allowed in this hand-in")
				break
			}
			probs = append(probs, key+": not a field of this hand-in")
		}
	}
	if len(probs) > 0 {
		slices.Sort(probs)
		return h, probs
	}
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&h); err != nil {
		return h, []string{"the hand-in does not match its shape: " + err.Error()}
	}
	add := func(ok bool, format string, a ...any) {
		if !ok {
			probs = append(probs, fmt.Sprintf(format, a...))
		}
	}
	if k == ManualDecisionDraft {
		return parseManualDraft(h, keys)
	}
	if k == Aristotle {
		add(len(h.Statements) >= 1 && len(h.Statements) <= 40, "statements: give 1 to 40 statements")
		for i, s := range h.Statements {
			add(slices.Contains([]string{"fact", "lesson", "gap"}, s.Kind), "statement %d: kind must be fact, lesson or gap", i+1)
			add(within(s.Text, 2000), "statement %d: text must be 1 to 2000 characters", i+1)
			add(s.Cites != nil && !slices.Contains(s.Cites, ""), "statement %d: cites must be an array of references", i+1)
		}
		return h, probs
	}
	// The auditor, the objector and Autoresearch work alone, with no lead to
	// ask for them, so they never ask.
	if slices.Contains([]Kind{Auditor, Objector, Autoresearch}, k) {
		add(h.Status == "done" || h.Status == "could_not_do", "status: must be done or could_not_do")
	} else {
		add(slices.Contains([]string{"done", "could_not_do", "ask"}, h.Status), "status: must be done, could_not_do or ask")
	}
	add(within(h.Answer, 8000), "answer: say what you did, 1 to 8000 characters")
	if k != Autoresearch {
		if k == Auditor || k == Objector {
			add(!hasLocalAnswerPath(h.Answer), "answer: do not include local file paths; refer to files by name and describe unavailable evidence")
		} else {
			add(!hasLocalAnswerPath(h.Answer), "answer: do not include local file paths; put files to keep in outputs/ and refer to them by name")
		}
	}
	add(h.Status != "could_not_do" || strings.TrimSpace(deref(h.Reason)) != "", "reason: say what blocked the work")
	if h.BlockedByEnvironment != nil {
		add(slices.Contains([]string{"python", "r", "julia"}, *h.BlockedByEnvironment), "blocked_by_environment: must be python, r, julia or null")
		add(h.Status == "could_not_do", "blocked_by_environment: only a could_not_do hand-in can name an environment")
	}
	if h.BlockedByMemberRun != nil {
		_, err := uuid.Parse(*h.BlockedByMemberRun)
		add(err == nil, "blocked_by_member_run: must be a member ticket UUID or null")
		add(h.BlockedByEnvironment != nil, "blocked_by_member_run: name the blocked environment")
	}
	add(h.Status != "ask" || h.Decision != nil, "decision: an ask needs a decision")
	add(h.Status == "ask" || h.Decision == nil, "decision: only an ask carries a decision; leave it null")
	if d := h.Decision; d != nil {
		add(slices.Contains(DecisionKinds, d.Kind), "decision: kind must be scope, change_question, ethics, cost or access")
		add(within(d.Title, 200) && !strings.Contains(d.Title, "\n"), "decision: title must be one line of 1 to 200 characters")
		add(within(d.Body, 4000), "decision: body must be 1 to 4000 characters")
		validateOptions := func(options []OptionIn, prefix string) {
			add(len(options) >= 2 && len(options) <= 5, "%s: give 2 to 5 options", prefix)
			ids := map[string]bool{}
			for i, o := range options {
				add(optionID.MatchString(o.ID), "%s options %d: id must be 1 to 40 lowercase letters, digits or underscores", prefix, i+1)
				add(!ids[o.ID], "%s options %d: another option already has the id %s", prefix, i+1, o.ID)
				ids[o.ID] = true
				add(within(o.Label, 80), "%s options %d: label must be 1 to 80 characters", prefix, i+1)
				add(within(o.Consequence, 200), "%s options %d: consequence must say in 1 to 200 characters what the option leads to", prefix, i+1)
			}
		}
		if d.QuestionSet == nil {
			validateOptions(d.Options, "decision")
		} else {
			add(len(d.Options) == 0, "decision: give options or a question set, not both")
			var shape struct {
				QuestionSet struct {
					Questions []map[string]json.RawMessage `json:"questions"`
				} `json:"question_set"`
			}
			_ = json.Unmarshal(keys["decision"], &shape)
			for i, q := range shape.QuestionSet.Questions {
				_, ok := q["help_text"]
				add(ok, "decision question %d: help_text must be present, or null", i+1)
			}
			qs := d.QuestionSet.Questions
			add(len(qs) >= 1 && len(qs) <= 64, "decision: give 1 to 64 questions")
			ids := map[string]bool{}
			for i, q := range qs {
				prefix := fmt.Sprintf("decision question %d", i+1)
				add(optionID.MatchString(q.ID), "%s: id must be 1 to 40 lowercase letters, digits or underscores", prefix)
				add(!ids[q.ID], "%s: another question already has the id %s", prefix, q.ID)
				ids[q.ID] = true
				add(within(q.Prompt, 4000), "%s: prompt must be 1 to 4000 characters", prefix)
				add(q.HelpText == nil || within(*q.HelpText, 4000), "%s: help text must be null or 1 to 4000 characters", prefix)
				add(q.Required != nil, "%s: required must be a boolean", prefix)
				add(q.AllowOther != nil, "%s: allow_other must be a boolean", prefix)
				add(q.Options != nil, "%s: options must be an array", prefix)
				switch q.Mode {
				case "single", "multiple":
					validateOptions(q.Options, prefix)
				case "text":
					add(len(q.Options) == 0, "%s: text questions cannot have options", prefix)
					add(q.AllowOther == nil || !*q.AllowOther, "%s: text questions cannot allow another answer", prefix)
				default:
					add(false, "%s: mode must be single, multiple or text", prefix)
				}
			}
		}
		if e := d.ExpiresAt; e != nil {
			now := time.Now()
			add(!e.Before(now.Add(MinDecisionWait)) && !e.After(now.Add(MaxDecisionWait)), "decision: expires_at must be at least 1 hour and at most 30 days ahead")
		}
		add(d.FromMember == nil || strings.TrimSpace(*d.FromMember) != "", "decision: from_member must name a Team member, or be null")
	}
	for i, r := range h.Replies {
		add(r.Item != "", "replies %d: item must name the item replied to", i+1)
		add(slices.Contains([]string{"corrected", "withdrawn", "declined"}, r.Action), "replies %d: action must be corrected, withdrawn or declined", i+1)
		add(within(r.Reason, 2000), "replies %d: reason must be 1 to 2000 characters", i+1)
	}
	for i, p := range h.Papers {
		add(within(p.ID, 200), "papers %d: id must be 1 to 200 characters", i+1)
		add(within(p.Why, 2000), "papers %d: why must be 1 to 2000 characters", i+1)
	}
	for i, d := range h.Datasets {
		add(within(d.Accession, 200), "datasets %d: accession must be 1 to 200 characters", i+1)
		add(within(d.Why, 2000), "datasets %d: why must be 1 to 2000 characters", i+1)
	}
	if n := h.NoQualifying; n != nil {
		add(within(n.Reason, 2000), "no_qualifying: reason must be 1 to 2000 characters")
	}
	for i, x := range h.Hypotheses {
		// The control problem is told in the hand-in's own words; review's
		// version of it is dropped, and any other problem is still told.
		badControl := (strings.TrimSpace(deref(x.Control)) != "") == (strings.TrimSpace(deref(x.NoControl)) != "")
		add(!badControl, "hypotheses %d: give either a control or the reason there is none", i+1)
		b, _ := json.Marshal(x.Body(x.Cites))
		if err := review.Validate(review.KindHypothesis, board.Hypotheses, b); err != nil {
			msg := strings.TrimPrefix(err.Error(), "hypothesis: ")
			add(badControl && strings.HasPrefix(msg, "control "), "hypotheses %d: %s", i+1, msg)
		}
	}
	add(len(h.Outputs) <= 50, "outputs: list at most 50 file names")
	for i, o := range h.Outputs {
		add(o != "", "outputs %d: a file name cannot be empty", i+1)
	}
	names := map[string]bool{}
	for i, v := range h.Values {
		add(v.Name != "" && v.Value != "", "values %d: name and value are both required", i+1)
		add(v.Value == "" || within(v.Value, 2000), "values %d: value must be 1 to 2000 characters", i+1)
		n := strings.ToLower(strings.Join(strings.Fields(v.Name), " "))
		add(n == "" || !names[n], "values %d: another value already has the name %s", i+1, v.Name)
		names[n] = true
	}
	for i, c := range h.Concerns {
		add(c.Item != "" && within(c.Concern, 2000), "concerns %d: name the item and give a concern of 1 to 2000 characters", i+1)
	}
	for i, s := range h.NewStudies {
		add(s.Title != "" && s.Question != "", "new_studies %d: title and question are both required", i+1)
	}
	for i, a := range h.Assessments {
		add(within(a.Hypothesis, 2000) && within(a.Measurement, 2000), "assessments %d: hypothesis and measurement must each be 1 to 2000 characters", i+1)
		add(a.Control == nil || within(*a.Control, 2000), "assessments %d: control must be 1 to 2000 characters", i+1)
		add(a.Observed == nil || within(*a.Observed, 2000), "assessments %d: observed must be 1 to 2000 characters", i+1)
		if t := a.Test; t != nil {
			add(slices.Contains([]string{"t", "F", "chi2", "r", "z"}, t.Kind) && slices.Contains([]string{"=", "<", ">"}, t.PBound),
				"assessments %d: test kind must be t, F, chi2, r or z, and p_bound =, < or >", i+1)
			_, e1 := t.Statistic.Float64()
			_, e2 := t.P.Float64()
			add(e1 == nil && e2 == nil, "assessments %d: test statistic and p must be numbers", i+1)
			switch t.Kind {
			case "F":
				add(t.DF1 != nil && t.DF2 != nil, "assessments %d: an F test needs df1 and df2", i+1)
			case "t", "chi2", "r":
				add(t.DF1 != nil, "assessments %d: a %s test needs df1", i+1, t.Kind)
			}
		}
	}
	for i, v := range h.Verdicts {
		add(slices.Contains([]string{"supported", "refuted", "not_enough_info"}, v.Verdict), "verdicts %d: verdict must be supported, refuted or not_enough_info", i+1)
		add(within(v.Claim, 2000) && within(v.Source, 2000) && within(v.Quote, 2000), "verdicts %d: claim, source and quote must each be 1 to 2000 characters", i+1)
	}
	for i, f := range h.FlaggedErrs {
		add(within(f.Source, 2000) && within(f.Quote, 2000) && within(f.Problem, 2000), "flagged_errors %d: source, quote and problem must each be 1 to 2000 characters", i+1)
	}
	for i, o := range h.Objections {
		add(slices.Contains(ObjectionTypes, o.Type), "objections %d: type must be wrong_test, leakage, unit_mismatch, missing_control or overreach", i+1)
		add(o.Quote == nil || within(*o.Quote, 2000), "objections %d: quote must be null or 1 to 2000 characters", i+1)
		add(within(o.Target, 2000) && within(o.Problem, 2000), "objections %d: target and problem must each be 1 to 2000 characters", i+1)
	}
	return h, probs
}

func within(s string, max int) bool {
	n := utf8.RuneCountInString(strings.TrimSpace(s))
	return n >= 1 && utf8.RuneCountInString(s) <= max
}
