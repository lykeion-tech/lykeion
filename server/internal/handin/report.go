package handin

import (
	"bytes"
	"encoding/json"
	"fmt"
	"slices"
	"strings"
	"unicode/utf8"
)

// What a report run hands in: concepts and facts about the study's own text,
// and links from phrases of its hand-in to the graph's nodes.
const (
	MaxReportConcepts = 40
	MaxReportFacts    = 80
	MaxReportLinks    = 60
	MaxQuoteBytes     = 600
	MaxPhraseBytes    = 200
)

// ReportRelations are the fixed relations a fact may state; mentioned_in is
// the only one whose object is a graph node.
var ReportRelations = []string{"is_a", "part_of", "associated_with", "increases", "decreases", "measured_by", "mentioned_in"}

// ReportConceptTypes are the kinds of thing a concept may be.
var ReportConceptTypes = []string{"gene", "protein", "drug", "disease", "organism", "cohort", "measure", "method", "other"}

type ReportConcept struct {
	Key   string `json:"key"`
	Label string `json:"label"`
	Type  string `json:"type"`
}

type ReportFact struct {
	Subject    string `json:"subject"`
	Relation   string `json:"relation"`
	Object     string `json:"object"`
	Quote      string `json:"quote"`
	SourceNode string `json:"source_node"`
}

// ReportLink makes the Occurrence-th (1-based) occurrence of Phrase in the
// study's hand-in a link to Node.
type ReportLink struct {
	Phrase     string `json:"phrase"`
	Occurrence int    `json:"occurrence"`
	Node       string `json:"node"`
}

type ReportHandIn struct {
	Status   string          `json:"status"` // done | could_not_do
	Reason   *string         `json:"reason"`
	Concepts []ReportConcept `json:"concepts"`
	Facts    []ReportFact    `json:"facts"`
	Links    []ReportLink    `json:"links"`
}

func reportSchema() string {
	return strictSchema(
		"status", enum("done", "could_not_do"),
		"reason", nullStr(),
		"concepts", list(object("key", str(), "label", str(), "type", enum(ReportConceptTypes...))),
		"facts", list(object("subject", str(), "relation", enum(ReportRelations...), "object", str(),
			"quote", desc(str(), "Words copied exactly from the source node's text."), "source_node", str())),
		"links", list(object("phrase", desc(str(), "Words copied exactly from the study's hand-in."),
			"occurrence", obj{"type": "integer"}, "node", str())))
}

// parseReport decodes a report hand-in strictly and lists what is wrong with it.
func parseReport(raw []byte) (ReportHandIn, []string) {
	var h ReportHandIn
	var keys map[string]json.RawMessage
	if err := json.Unmarshal(raw, &keys); err != nil {
		return h, []string{"the hand-in is not one JSON object"}
	}
	var probs []string
	var allowed struct{ Properties map[string]any }
	_ = json.Unmarshal([]byte(Schema(Report)), &allowed)
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
	add(h.Status != "could_not_do" || strings.TrimSpace(deref(h.Reason)) != "", "reason: say what blocked the work")
	add(len(h.Concepts) <= MaxReportConcepts, "concepts: at most %d concepts", MaxReportConcepts)
	add(len(h.Facts) <= MaxReportFacts, "facts: at most %d facts", MaxReportFacts)
	add(len(h.Links) <= MaxReportLinks, "links: at most %d links", MaxReportLinks)
	for i, c := range h.Concepts {
		add(strings.TrimSpace(c.Key) != "" && within(c.Label, 200), "concepts %d: key is required and label must be 1 to 200 characters", i+1)
		add(slices.Contains(ReportConceptTypes, c.Type), "concepts %d: type must be one of %s", i+1, strings.Join(ReportConceptTypes, ", "))
	}
	for i, f := range h.Facts {
		add(slices.Contains(ReportRelations, f.Relation), "facts %d: relation must be one of %s", i+1, strings.Join(ReportRelations, ", "))
		add(f.Subject != "" && f.Object != "" && f.SourceNode != "", "facts %d: subject, object and source_node are all required", i+1)
		add(strings.TrimSpace(f.Quote) != "" && len(f.Quote) <= MaxQuoteBytes && utf8.ValidString(f.Quote), "facts %d: quote must be 1 to %d bytes", i+1, MaxQuoteBytes)
	}
	for i, l := range h.Links {
		add(strings.TrimSpace(l.Phrase) != "" && len(l.Phrase) <= MaxPhraseBytes && utf8.ValidString(l.Phrase), "links %d: phrase must be 1 to %d bytes", i+1, MaxPhraseBytes)
		add(l.Occurrence >= 1, "links %d: occurrence counts from 1", i+1)
		add(l.Node != "", "links %d: node is required", i+1)
	}
	return h, probs
}

// ParseReport decodes a report run's hand-in and lists what is wrong with its shape.
func ParseReport(raw []byte) (ReportHandIn, []string) { return parseReport(raw) }
