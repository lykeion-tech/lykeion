// Package handin is the structured final answer of a study run: its types,
// strict decoding and shape problems. It must not import internal/flow.
package handin

import (
	"bytes"
	"encoding/json"
	"fmt"
	"slices"
	"strings"
	"unicode/utf8"

	"lykeion/server/internal/board"
	"lykeion/server/internal/review"
)

type Kind string

const (
	Grounding       Kind = "grounding"
	Hypotheses      Kind = "hypotheses"
	Experimentation Kind = "experimentation"
	Auditor         Kind = "auditor"
	Autoresearch    Kind = "autoresearch"
)

// KindFor is the hand-in kind a run owes; Validation, Coresearch and other
// runs owe none.
func KindFor(runKind, role, column string) (Kind, bool) {
	switch {
	case runKind == "autoresearch":
		return Autoresearch, true
	case role == "auditor" && column != board.Validation:
		return Auditor, true
	}
	switch column {
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
type NewStudy struct {
	Title    string `json:"title"`
	Question string `json:"question"`
}

// HandIn holds every field of every kind; Parse refuses the ones a kind does
// not have.
type HandIn struct {
	Status       string        `json:"status"` // done | could_not_do
	Answer       string        `json:"answer"`
	Reason       *string       `json:"reason"`
	Replies      []Reply       `json:"replies"`
	Papers       []Paper       `json:"papers"`
	Datasets     []Dataset     `json:"datasets"`
	NoQualifying *NoQualifying `json:"no_qualifying"`
	Hypotheses   []Hypothesis  `json:"hypotheses"`
	Outputs      []string      `json:"outputs"`
	Values       []Value       `json:"values"`
	Concerns     []Concern     `json:"concerns"`
	Report       *string       `json:"report"`
	NewStudies   []NewStudy    `json:"new_studies"`
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
	var keys map[string]json.RawMessage
	if err := json.Unmarshal(raw, &keys); err != nil {
		return h, []string{"the hand-in is not one JSON object"}
	}
	var probs []string
	var allowed struct{ Properties map[string]any }
	_ = json.Unmarshal([]byte(Schema(k)), &allowed)
	for key := range keys {
		if _, ok := allowed.Properties[key]; !ok {
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
	add(h.Status == "done" || h.Status == "could_not_do", "status: must be done or could_not_do")
	add(within(h.Answer, 8000), "answer: say what you did, 1 to 8000 characters")
	add(h.Status != "could_not_do" || strings.TrimSpace(deref(h.Reason)) != "", "reason: say what blocked the work")
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
		if (deref(x.Control) != "") == (deref(x.NoControl) != "") {
			probs = append(probs, fmt.Sprintf("hypotheses %d: give either a control or the reason there is none", i+1))
			continue
		}
		b, _ := json.Marshal(x.Body(x.Cites))
		if err := review.Validate(review.KindHypothesis, board.Hypotheses, b); err != nil {
			probs = append(probs, fmt.Sprintf("hypotheses %d: %s", i+1, strings.TrimPrefix(err.Error(), "hypothesis: ")))
		}
	}
	add(len(h.Outputs) <= 50, "outputs: list at most 50 file names")
	for i, o := range h.Outputs {
		add(o != "", "outputs %d: a file name cannot be empty", i+1)
	}
	names := map[string]bool{}
	for i, v := range h.Values {
		add(v.Name != "" && v.Value != "", "values %d: name and value are both required", i+1)
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
	return h, probs
}

func within(s string, max int) bool {
	n := utf8.RuneCountInString(strings.TrimSpace(s))
	return n >= 1 && utf8.RuneCountInString(s) <= max
}
