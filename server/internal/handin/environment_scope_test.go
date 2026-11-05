package handin

import (
	"encoding/json"
	"testing"
)

func TestEnvironmentBlockerOnlyInStudyHandIns(t *testing.T) {
	for _, tc := range []struct {
		kind Kind
		want bool
	}{
		{Grounding, true}, {Hypotheses, true}, {Experimentation, true}, {Validation, true},
		{Auditor, false}, {Objector, false}, {Autoresearch, false},
	} {
		var schema struct {
			Properties map[string]json.RawMessage `json:"properties"`
		}
		if err := json.Unmarshal([]byte(Schema(tc.kind)), &schema); err != nil {
			t.Fatal(err)
		}
		_, has := schema.Properties["blocked_by_environment"]
		if has != tc.want {
			t.Errorf("%s: blocker field present=%v, want %v", tc.kind, has, tc.want)
		}
	}
}
