package cli

import (
	"strings"
	"testing"
)

func TestDecisionQuestionSetLines(t *testing.T) {
	set := decisionQuestionSet{Questions: []decisionQuestion{{ID: "population", Prompt: "Which population?", Mode: "single", Required: true, Options: []decisionQuestionOption{{ID: "children", Label: "Children"}, {ID: "adults", Label: "Adults"}}}, {ID: "detail", Prompt: "Notes?", Mode: "text"}}}
	lines := decisionQuestionLines(&set, []decisionAnswer{{QuestionID: "population", OptionIDs: []string{"children"}}, {QuestionID: "detail", Text: stringPointer("details")}})
	for _, want := range []string{"Which population?", "children", "Children", "Notes?", "details"} {
		if !strings.Contains(lines, want) {
			t.Errorf("missing %q in %q", want, lines)
		}
	}
}
func stringPointer(s string) *string { return &s }
