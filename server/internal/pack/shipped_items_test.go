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
			if !strings.Contains(m.Leader.Agent.Instructions, "`"+f+"`") {
				t.Errorf("%s's leader is not told the hand-in field %s", name, f)
			}
		}
	}
}
