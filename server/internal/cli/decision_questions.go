package cli

import (
	"fmt"
	"strings"
)

type decisionQuestionOption struct {
	ID          string `json:"id"`
	Label       string `json:"label"`
	Consequence string `json:"consequence"`
}
type decisionQuestion struct {
	ID         string                   `json:"id"`
	Prompt     string                   `json:"prompt"`
	Mode       string                   `json:"mode"`
	Required   bool                     `json:"required"`
	AllowOther bool                     `json:"allow_other"`
	Options    []decisionQuestionOption `json:"options"`
}
type decisionQuestionSet struct {
	Questions []decisionQuestion `json:"questions"`
}
type decisionAnswer struct {
	QuestionID string   `json:"question_id"`
	OptionIDs  []string `json:"option_ids"`
	Text       *string  `json:"text"`
}

func decisionQuestionLines(set *decisionQuestionSet, answers []decisionAnswer) string {
	if set == nil {
		return ""
	}
	byID := map[string]decisionAnswer{}
	for _, a := range answers {
		byID[a.QuestionID] = a
	}
	var b strings.Builder
	for _, q := range set.Questions {
		required := "optional"
		if q.Required {
			required = "required"
		}
		fmt.Fprintf(&b, "\n  %s (%s; %s)", q.Prompt, q.Mode, required)
		for _, o := range q.Options {
			fmt.Fprintf(&b, "\n    %s: %s — %s", o.ID, o.Label, o.Consequence)
		}
		if q.AllowOther {
			b.WriteString("\n    Something else: custom answer")
		}
		if a, ok := byID[q.ID]; ok {
			if a.Text != nil {
				fmt.Fprintf(&b, "\n    Answer: %s", *a.Text)
			} else {
				fmt.Fprintf(&b, "\n    Answer: %s", strings.Join(a.OptionIDs, ", "))
			}
		}
	}
	return b.String()
}
