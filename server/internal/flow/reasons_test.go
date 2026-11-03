package flow

import (
	"strings"
	"testing"
)

func TestEveryBlockPathNamesAReason(t *testing.T) {
	check := func(name, text string) {
		t.Helper()
		if text == "" || strings.HasSuffix(strings.TrimSpace(text), ":") {
			t.Errorf("%s: block text %q ends without a reason", name, text)
		}
	}
	check("review", reviewBlockedText(1, "grounding", []string{"- arxiv:2106.01234: no such paper"}))
	if got := reviewBlockedText(1, "grounding", []string{"- a: b"}); !strings.Contains(got, "- a: b") {
		t.Errorf("review block text lacks its reason line: %q", got)
	}
	check("review, no reasons given", reviewBlockedText(1, "grounding", nil))
	check("legacy", blockedText(1, "grounding", ""))
	check("legacy named", blockedText(1, "grounding", "“x” (level 1 of 5)"))
	check("hand-in invalid, no detail", runFailedText(1, "hypotheses", "hand_in_invalid", ""))
	check("hand-in invalid", runFailedText(1, "hypotheses", "hand_in_invalid", "hypothesis 1: name a control"))
}
