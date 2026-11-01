// Package review holds the typed items agents declare for the stage review.
package review

import (
	"bytes"
	"encoding/json"
	"regexp"
	"slices"
	"strings"
	"unicode/utf8"

	"lykeion/server/internal/board"
	"lykeion/server/internal/service"
)

const (
	KindPaper        = "paper"         // grounding
	KindDataset      = "dataset"       // grounding
	KindNoQualifying = "no_qualifying" // grounding: "no paper qualifies"
	KindHypothesis   = "hypothesis"    // hypotheses
	KindOutputs      = "outputs"       // experimentation: the files the run must produce
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
	Shape       string   `json:"shape"`               // "direction" | "outcomes" | "range"
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
type HypothesisBody struct {
	Text       string     `json:"text"`
	Prediction Prediction `json:"prediction"`
	Control    Control    `json:"control"`
	Cites      []string   `json:"cites"` // at least one
}
type OutputsBody struct {
	Files []string `json:"files"` // file names as saved with `lyk output put`
}

var columns = map[string]string{
	KindPaper:        board.Grounding,
	KindDataset:      board.Grounding,
	KindNoQualifying: board.Grounding,
	KindHypothesis:   board.Hypotheses,
	KindOutputs:      board.Experimentation,
}

var paperID = regexp.MustCompile(`^(arxiv:\d{4}\.\d{4,5}|arxiv:[a-z-]+/\d{7}|doi:10\.\d{4,9}/\S+|pmid:\d{1,9})$`)

// ColumnOf is the only column a kind may be written in.
func ColumnOf(kind string) (column string, ok bool) {
	column, ok = columns[kind]
	return
}

// Validate refuses a malformed item body, or an item written outside its
// column; kinds that are not items pass unchecked. It returns service.Invalid.
func Validate(kind, column string, body []byte) error {
	want, ok := ColumnOf(kind)
	if !ok {
		return nil
	}
	if column != want {
		return service.Invalid(kind + " records are written in the " + strings.ToUpper(want[:1]) + want[1:] + " column")
	}
	dec := json.NewDecoder(bytes.NewReader(body))
	var msg string
	switch kind {
	case KindPaper:
		var b PaperBody
		if msg = decode(dec, &b); msg == "" {
			msg = first(check(paperID.MatchString(b.ID), "id must be arxiv:…, doi:10.… or pmid:…"), text(b.Text))
		}
	case KindDataset:
		var b DatasetBody
		if msg = decode(dec, &b); msg == "" {
			msg = first(check(b.Accession != "" && utf8.RuneCountInString(b.Accession) <= 200, "accession must be 1 to 200 characters"), text(b.Text))
		}
	case KindNoQualifying:
		var b NoQualifyingBody
		if msg = decode(dec, &b); msg == "" {
			msg = text(b.Text)
		}
	case KindHypothesis:
		var b HypothesisBody
		if msg = decode(dec, &b); msg == "" {
			msg = first(text(b.Text), prediction(b.Prediction), control(b.Control), check(len(b.Cites) >= 1, "a hypothesis cites at least one record"))
		}
	case KindOutputs:
		var b OutputsBody
		if msg = decode(dec, &b); msg == "" {
			msg = check(len(b.Files) >= 1 && len(b.Files) <= 50, "outputs lists 1 to 50 file names")
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

func first(msgs ...string) string {
	for _, m := range msgs {
		if m != "" {
			return m
		}
	}
	return ""
}

func text(s string) string {
	n := utf8.RuneCountInString(s)
	return check(n >= 1 && n <= 2000, "text must be 1 to 2000 characters")
}

func prediction(p Prediction) string {
	switch p.Shape {
	case "direction":
		return check(slices.Contains([]string{"increase", "decrease", "no_change"}, p.Direction), "a direction prediction needs direction: increase, decrease or no_change")
	case "outcomes":
		return check(len(p.Outcomes) >= 2 && slices.Contains(p.Outcomes, p.Chosen), "an outcomes prediction needs 2 or more outcomes and a chosen one among them")
	case "range":
		return first(check(p.Low != nil && p.High != nil && *p.Low < *p.High, "a range prediction needs low < high"), check(p.Uncertainty != "", "a range prediction needs an uncertainty"))
	}
	return "prediction shape must be direction, outcomes or range"
}

func control(c Control) string {
	return check((c.Text != "") != (c.None != ""), "control needs exactly one of text or none")
}
