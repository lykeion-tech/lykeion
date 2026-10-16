package autoresearch

import (
	"encoding/json"
	"errors"
	"fmt"
	"strings"
)

// WebhookPrefix starts every webhook URL token, so a leaked one is easy to
// recognise.
const WebhookPrefix = "lwh_"

// Filter limits a webhook trigger to one event, and to some of its actions
// when Actions is not empty.
type Filter struct {
	Event   string   `json:"event"`
	Actions []string `json:"actions,omitempty"`
}

// ParseFilters checks a trigger's filters. Empty input means none.
func ParseFilters(raw json.RawMessage) ([]Filter, error) {
	fs := []Filter{}
	if len(raw) == 0 || string(raw) == "null" {
		return fs, nil
	}
	dec := json.NewDecoder(strings.NewReader(string(raw)))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&fs); err != nil {
		return nil, fmt.Errorf("filters must be a list of {event, actions}: %v", err)
	}
	if len(fs) > 20 {
		return nil, errors.New("at most 20 filters")
	}
	for i, f := range fs {
		if strings.TrimSpace(f.Event) == "" || len(f.Event) > 200 {
			return nil, fmt.Errorf("filters[%d].event must be 1 to 200 bytes", i)
		}
		for j, a := range f.Actions {
			if strings.TrimSpace(a) == "" || len(a) > 200 {
				return nil, fmt.Errorf("filters[%d].actions[%d] must be 1 to 200 bytes", i, j)
			}
		}
	}
	return fs, nil
}
