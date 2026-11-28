// Package review holds the typed items agents declare for the stage review.
package review

import (
	"bytes"
	"encoding/json"
	"math"
	"regexp"
	"slices"
	"strings"
	"unicode/utf8"

	"lykeion/server/internal/board"
	"lykeion/server/internal/service"
	db "lykeion/server/pkg/db/generated"
)

const (
	KindPaper        = "paper"         // grounding
	KindDataset      = "dataset"       // grounding
	KindNoQualifying = "no_qualifying" // grounding: "no paper qualifies"
	KindHypothesis   = "hypothesis"    // hypotheses
	KindOutputs      = "outputs"       // experimentation: the files the run must produce
	KindWithdrawn    = "withdrawn"     // any reviewed column: an item sent back and dropped
	KindAssessment   = "assessment"    // validation: a hypothesis checked against recorded values
	KindVerdict      = "verdict"       // validation: a claim judged against a source
	KindFlaggedError = "flagged_error" // validation: an error found in a source
	// KindDeclined is a review's item for a missed paper the Team declined
	// and Jev did not reject; it is no record kind an agent declares.
	KindDeclined = "declined"
)

type PaperBody struct {
	ID    string   `json:"id"`    // "arxiv:2106.01234", "doi:10.…" or "pmid:12345"
	Text  string   `json:"text"`  // what the paper contributes, one or two sentences
	Cites []string `json:"cites"` // the call records it was fetched in
}
type DatasetBody struct {
	Accession string   `json:"accession"` // e.g. "GSE12345", or an https link
	Text      string   `json:"text"`
	Cites     []string `json:"cites"`
}
type NoQualifyingBody struct {
	Text string `json:"text"`
}
type Prediction struct {
	Shape       string   `json:"shape"`               // "direction" | "outcomes" | "range" | "estimate"
	Direction   string   `json:"direction,omitempty"` // "increase" | "decrease" | "no_change"
	Outcomes    []string `json:"outcomes,omitempty"`  // 2+ named outcomes
	Chosen      string   `json:"chosen,omitempty"`    // one of Outcomes
	Low         *float64 `json:"low,omitempty"`       // range bounds, Low < High
	High        *float64 `json:"high,omitempty"`
	Uncertainty string   `json:"uncertainty,omitempty"` // required for "range"
}
type Control struct {
	Text string `json:"text,omitempty"` // the control or baseline
	None string `json:"none,omitempty"` // the reason there is none
}

// Measure names the quantity that an accepted hypothesis predicts.
type Measure struct {
	ID   string `json:"id"`
	Name string `json:"name"`
	Unit string `json:"unit"`
}

type HypothesisBody struct {
	Text       string             `json:"text"`
	Prediction Prediction         `json:"prediction"`
	Control    Control            `json:"control"`
	Cites      []string           `json:"cites"` // at least one
	Measure    *Measure           `json:"measure,omitempty"`
	Comparison *OutcomeComparison `json:"comparison,omitempty"`
	// An estimate's method and any value a source already reports for it.
	Method   string         `json:"method,omitempty"`
	Reported *ReportedValue `json:"reported,omitempty"`
}

// ReportedValue is a value an earlier source reports for an estimate's
// measure: Source is the id as handed in, Key the record it resolved to.
type ReportedValue struct {
	Value  float64 `json:"value"`
	Source string  `json:"source"`
	Key    string  `json:"key"`
}

// IsEstimate is whether a hypothesis measures a quantity instead of
// predicting it.
func IsEstimate(b HypothesisBody) bool { return b.Prediction.Shape == "estimate" }

type OutputsBody struct {
	Files []string `json:"files"` // names of the study's saved output files
}

// ValueBody is a reported value and the evidence it names.
type ValueBody struct {
	Name      string   `json:"name"`
	Value     string   `json:"value"`
	File      string   `json:"file"`
	Cites     []string `json:"cites"`
	ScriptRun bool     `json:"script_run,omitempty"`
	Field     *string  `json:"field,omitempty"`
	Measure   *string  `json:"measure,omitempty"`
}

type VerdictBody struct {
	Claim   string `json:"claim"`
	Verdict string `json:"verdict"` // "supported" | "refuted" | "not_enough_info"
	Source  string `json:"source"`  // an attachment name or a recorded source id
	Quote   string `json:"quote"`
}
type FlaggedErrorBody struct {
	Source  string `json:"source"`
	Quote   string `json:"quote"`
	Problem string `json:"problem"`
}
type WithdrawnBody struct {
	Item   string `json:"item"` // the key of the item record withdrawn, e.g. "r3"
	Reason string `json:"reason"`
}

var columns = map[string]string{
	KindPaper:        board.Grounding,
	KindDataset:      board.Grounding,
	KindNoQualifying: board.Grounding,
	KindHypothesis:   board.Hypotheses,
	KindOutputs:      board.Experimentation,
	KindAssessment:   board.Validation,
	KindVerdict:      board.Validation,
	KindFlaggedError: board.Validation,
}

// withdrawnIn are the columns reviewed item by item, where an item may be withdrawn.
var withdrawnIn = []string{board.Grounding, board.Hypotheses, board.Experimentation, board.Validation}

var measureID = regexp.MustCompile(`^[a-z][a-z0-9_-]{0,63}$`)

// ValidMeasureID reports whether id can name a measure.
func ValidMeasureID(id string) bool { return measureID.MatchString(id) }

// Valid reports whether the declaration can be used in a value binding.
func (m Measure) Valid() bool { return measureProblem(&m) == "" }

var itemKey = regexp.MustCompile(`^r[0-9]+$`)

var paperID = regexp.MustCompile(`^(arxiv:\d{4}\.\d{4,5}|arxiv:[a-z-]+/\d{7}|doi:10\.\d{4,9}/\S+|pmid:\d{1,9})$`)

// ColumnOf is the only column a kind may be written in.
func ColumnOf(kind string) (column string, ok bool) {
	column, ok = columns[kind]
	return
}

// Validate refuses a malformed item body or an item written outside its column.
// Structured values can only be written through hand-ins. It returns service.Invalid.
func Validate(kind, column string, body []byte) error {
	want, ok := ColumnOf(kind)
	switch {
	case kind == "value":
		var fields map[string]json.RawMessage
		if json.Unmarshal(body, &fields) == nil {
			for key, raw := range fields {
				if (strings.EqualFold(key, "field") || strings.EqualFold(key, "measure")) && !bytes.Equal(raw, []byte("null")) {
					return service.Invalid("submit structured values through an Experimentation hand-in")
				}
			}
		}
		return nil
	case kind == KindWithdrawn:
		if !slices.Contains(withdrawnIn, column) {
			return service.Invalid("withdrawn records are written in the Grounding, Hypotheses, Experimentation or Validation column")
		}
	case !ok:
		return nil
	case column != want:
		return service.Invalid(kind + " records are written in the " + strings.ToUpper(want[:1]) + want[1:] + " column")
	}
	dec := json.NewDecoder(bytes.NewReader(body))
	var msg string
	switch kind {
	case KindPaper:
		var b PaperBody
		if msg = decode(dec, &b); msg == "" {
			msg = all(check(paperID.MatchString(b.ID), "id must be arxiv:…, doi:10.… or pmid:…"), text(b.Text))
		}
	case KindDataset:
		var b DatasetBody
		if msg = decode(dec, &b); msg == "" {
			msg = all(check(b.Accession != "" && utf8.RuneCountInString(b.Accession) <= 200, "accession must be 1 to 200 characters"), text(b.Text))
		}
	case KindNoQualifying:
		var b NoQualifyingBody
		if msg = decode(dec, &b); msg == "" {
			msg = text(b.Text)
		}
	case KindHypothesis:
		var b HypothesisBody
		if msg = decode(dec, &b); msg == "" {
			msg = all(text(b.Text), prediction(b.Prediction), estimateProblem(b),
				check(len(b.Cites) >= 1, "a hypothesis cites at least one record"), measureProblem(b.Measure), comparisonProblem(b))
		}
	case KindOutputs:
		var b OutputsBody
		if msg = decode(dec, &b); msg == "" {
			msg = check(len(b.Files) >= 1 && len(b.Files) <= 50, "outputs lists 1 to 50 file names")
		}
	case KindAssessment:
		var b Assessment
		if msg = decode(dec, &b); msg == "" {
			msg = check(strings.TrimSpace(b.Hypothesis) != "" && b.Measurement != "", "an assessment names its hypothesis and measurement")
		}
	case KindVerdict:
		var b VerdictBody
		if msg = decode(dec, &b); msg == "" {
			msg = all(check(slices.Contains([]string{"supported", "refuted", "not_enough_info"}, b.Verdict), "verdict must be supported, refuted or not_enough_info"),
				check(b.Claim != "" && b.Source != "" && b.Quote != "", "a verdict needs its claim, source and quote"))
		}
	case KindFlaggedError:
		var b FlaggedErrorBody
		if msg = decode(dec, &b); msg == "" {
			msg = check(b.Source != "" && b.Quote != "" && b.Problem != "", "a flagged error needs its source, quote and problem")
		}
	case KindWithdrawn:
		var b WithdrawnBody
		if msg = decode(dec, &b); msg == "" {
			n := utf8.RuneCountInString(b.Reason)
			msg = all(check(itemKey.MatchString(b.Item), "item must be the key of the item record withdrawn, such as r3"),
				check(n >= 1 && n <= 2000, "reason must be 1 to 2000 characters"))
		}
	}
	if msg != "" {
		return service.Invalid(kind + ": " + msg)
	}
	return nil
}

func decode(dec *json.Decoder, v any) string {
	if err := dec.Decode(v); err != nil {
		return "body does not match: " + err.Error()
	}
	return ""
}

func check(ok bool, msg string) string {
	if ok {
		return ""
	}
	return msg
}

// all joins every problem found, so one refusal names them all.
func all(msgs ...string) string {
	var out []string
	for _, m := range msgs {
		if m != "" {
			out = append(out, m)
		}
	}
	return strings.Join(out, "; ")
}

func text(s string) string {
	n := utf8.RuneCountInString(s)
	return check(n >= 1 && n <= 2000, "text must be 1 to 2000 characters")
}

func measureProblem(m *Measure) string {
	if m == nil {
		return ""
	}
	return all(
		check(ValidMeasureID(m.ID), "measure id must be 1 to 64 lowercase letters, digits, hyphens or underscores and start with a letter"),
		check(m.Name != "" && strings.TrimSpace(m.Name) == m.Name && utf8.RuneCountInString(m.Name) <= 120, "measure name must be 1 to 120 characters without surrounding whitespace"),
		check(m.Unit != "" && strings.TrimSpace(m.Unit) == m.Unit && utf8.RuneCountInString(m.Unit) <= 64, "measure unit must be 1 to 64 characters without surrounding whitespace"),
	)
}

func prediction(p Prediction) string {
	switch p.Shape {
	case "direction":
		return check(slices.Contains([]string{"increase", "decrease", "no_change"}, p.Direction), "a direction prediction needs direction: increase, decrease or no_change")
	case "outcomes":
		return check(len(p.Outcomes) >= 2 && slices.Contains(p.Outcomes, p.Chosen), "an outcomes prediction needs 2 or more outcomes and a chosen one among them")
	case "range":
		return all(check(p.Low != nil && p.High != nil && *p.Low < *p.High, "a range prediction needs low < high"), check(p.Uncertainty != "", "a range prediction needs an uncertainty"))
	case "estimate":
		return check(p.Direction == "" && len(p.Outcomes) == 0 && p.Chosen == "" && p.Low == nil && p.High == nil && p.Uncertainty == "", "an estimate predicts no value: leave direction, outcomes, low, high and uncertainty empty")
	}
	return "prediction shape must be direction, outcomes, range or estimate"
}

// estimateProblem holds an estimate to its measure and method, without a
// control or comparison, and every other shape to its control and no method.
func estimateProblem(b HypothesisBody) string {
	if !IsEstimate(b) {
		return all(control(b.Control), check(b.Method == "" && b.Reported == nil, "only an estimate has a method or a reported value"))
	}
	m := strings.TrimSpace(b.Method)
	return all(check(b.Measure != nil, "an estimate needs a measure"),
		check(m != "", "an estimate needs a method"),
		check(utf8.RuneCountInString(m) <= 2000, "an estimate's method must be at most 2000 characters"),
		check(b.Comparison == nil, "an estimate predicts no value: leave comparison null"),
		check(b.Reported == nil || !math.IsNaN(b.Reported.Value) && !math.IsInf(b.Reported.Value, 0), "a reported value must be a finite number"))
}

func control(c Control) string {
	return check((c.Text != "") != (c.None != ""), "control needs exactly one of text or none")
}

// itemText is what LabelOf and IdentityOf read of an item record's body.
type itemText struct {
	ID         string `json:"id"`
	Accession  string `json:"accession"`
	Text       string `json:"text"`
	Hypothesis string `json:"hypothesis"`
	Claim      string `json:"claim"`
	Source     string `json:"source"`
	Quote      string `json:"quote"`
}

// LabelOf is the label the reviewers give an item record: a paper's id, a
// dataset's accession, an assessment's hypothesis, a verdict's claim, a
// flagged error's source and quote, otherwise its text, cut to fit 80
// characters.
func LabelOf(rec db.Record) string {
	var b itemText
	_ = json.Unmarshal(rec.Body, &b)
	switch rec.Kind {
	case KindPaper:
		return b.ID
	case KindDataset:
		return b.Accession
	case KindAssessment:
		return clip(strings.TrimSpace(b.Hypothesis))
	case KindVerdict:
		return clip(strings.TrimSpace(b.Claim))
	case KindFlaggedError:
		return clip(strings.TrimSpace(b.Source) + ": " + strings.TrimSpace(b.Quote))
	}
	return clip(b.Text)
}

// IdentityOf is what makes two item records the same item: a paper's id or a
// dataset's accession without case, the outputs list as one item, a flagged
// error's source and quote each collapsed and joined by a line break,
// otherwise the whole text (an assessment's hypothesis, a verdict's claim)
// with its spacing collapsed, without case. Labels are cut short for display,
// so they never match items.
func IdentityOf(rec db.Record) string {
	var b itemText
	_ = json.Unmarshal(rec.Body, &b)
	switch rec.Kind {
	case KindPaper:
		return strings.ToLower(b.ID)
	case KindDataset:
		return strings.ToLower(b.Accession)
	case KindOutputs:
		return KindOutputs
	case KindAssessment:
		return collapse(b.Hypothesis)
	case KindVerdict:
		return collapse(b.Claim)
	case KindFlaggedError:
		return collapse(b.Source) + "\n" + collapse(b.Quote)
	}
	return collapse(b.Text)
}

func collapse(s string) string { return strings.ToLower(strings.Join(strings.Fields(s), " ")) }

// GroundingSource says whether r is a paper or dataset Grounding declared:
// with the sources server's calls, the only records a hypothesis may cite.
func GroundingSource(r db.Record) bool {
	return r.BoardColumn == board.Grounding && (r.Kind == KindPaper || r.Kind == KindDataset)
}
