package round

import (
	"encoding/json"
	"strings"

	"lykeion/server/internal/service"
)

// ResultKinds are what a role records when it ends without its usual records:
// nothing_found is a result the gate judges, failed escalates the card.
var ResultKinds = []string{"failed", "nothing_found"}

func needReason(raw json.RawMessage) error {
	var b struct {
		Reason string `json:"reason"`
	}
	if err := json.Unmarshal(raw, &b); err != nil {
		return service.Invalid("bad body: " + err.Error())
	}
	if strings.TrimSpace(b.Reason) == "" {
		return service.Invalid("a reason is required")
	}
	return nil
}

// Kinds is every record kind roles may write: each stage's roles' own, plus
// the result kinds, which any role of any stage may write.
func Kinds() map[string]service.Kind {
	out := map[string]service.Kind{}
	var roles []string
	for _, t := range StageRoles {
		for name, k := range t.Kinds() {
			out[name] = k
		}
		for _, p := range t.Phases {
			roles = append(roles, p...)
		}
		roles = append(roles, t.Reviewer)
	}
	for _, name := range ResultKinds {
		out[name] = service.Kind{Roles: roles, Validate: needReason}
	}
	return out
}
