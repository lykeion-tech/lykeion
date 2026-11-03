package pack_test

import (
	"encoding/json"
	"strings"
	"testing"

	"lykeion/server/internal/handin"
	"lykeion/server/internal/pack"
)

// The reviewed stage Teams carry no criteria of their own, and each leader's
// instructions name every field of its column's hand-in.
func TestShippedTeamsDescribeTheHandIn(t *testing.T) {
	kinds := map[string]handin.Kind{
		"Grounding Team":       handin.Grounding,
		"Hypotheses Team":      handin.Hypotheses,
		"Experimentation Team": handin.Experimentation,
		"Validation Team":      handin.Validation,
	}
	for name, k := range kinds {
		_, p, err := pack.ReadShipped(pack.Shipped(), name)
		if err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		m := p.Manifest
		if len(m.Criteria) != 0 {
			t.Errorf("%s still has criteria: %v", name, m.Criteria)
		}
		var s struct{ Required []string }
		if err := json.Unmarshal([]byte(handin.Schema(k)), &s); err != nil || len(s.Required) == 0 {
			t.Fatalf("%s schema: %v", k, err)
		}
		if k == handin.Experimentation && !strings.Contains(m.Leader.Agent.Instructions, "At least one is required") {
			t.Errorf("%s's leader is not told an output file is required", name)
		}
		for _, f := range s.Required {
			// The protocol every lead reads describes the decision an ask carries.
			if f == "decision" {
				continue
			}
			if !strings.Contains(m.Leader.Agent.Instructions, "`"+f+"`") {
				t.Errorf("%s's leader is not told the hand-in field %s", name, f)
			}
		}
	}
}

// The Validation Team is told how an assessment names its values: a control
// value only when the measurement is the treated group's raw value, and the
// outcome by the value that shows it.
func TestTheValidationTeamNamesValuesAsTheReviewReadsThem(t *testing.T) {
	_, p, err := pack.ReadShipped(pack.Shipped(), "Validation Team")
	if err != nil {
		t.Fatal(err)
	}
	lead := p.Manifest.Leader.Agent.Instructions
	for _, want := range []string{
		"null when `measurement` is already the effect",
		"`df2` (null when the test has none)",
		"the name of the recorded value whose text is the outcome seen",
		"a test no run printed makes the hypothesis invalid",
		"in Mode B judge the claims and the work; in Mode A name what was measured and judge nothing",
	} {
		if !strings.Contains(lead, want) {
			t.Errorf("the leader is not told %q", want)
		}
	}
	for _, m := range p.Manifest.Members {
		if m.Name == "checker" && !strings.Contains(m.Agent.Instructions, "the files Experimentation saved") {
			t.Errorf("the checker is not told it may read Experimentation's saved files")
		}
	}
	skill := string(p.Skills["checker/claim-check"]["SKILL.md"])
	if !strings.Contains(skill, "The\n   verdict the hand-in carries is always `supported`, `refuted` or `not_enough_info`") {
		t.Errorf("claim-check does not say which verdicts the hand-in carries:\n%s", skill)
	}
}
