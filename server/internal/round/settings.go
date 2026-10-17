// Package round advances cards: it queues each phase's role runs, runs the
// column's gate on the server, queues the reviewer, and applies the result.
package round

import "encoding/json"

type Harness struct {
	Default string            `json:"default"`
	Roles   map[string]string `json:"roles,omitempty"`
}

type JevSettings struct {
	Enabled bool `json:"enabled"` // sending text to TypeSafe is an owner's opt-in
}

// Settings live in workspace.settings; missing fields take the defaults.
type Settings struct {
	WIPLimit int64       `json:"wip_limit"`
	Harness  Harness     `json:"harness"`
	Jev      JevSettings `json:"jev"`
}

func ParseSettings(raw json.RawMessage) Settings {
	s := Settings{WIPLimit: 2, Harness: Harness{Default: "claude"}}
	json.Unmarshal(raw, &s)
	return s
}

func (s Settings) HarnessFor(role string) string {
	if h := s.Harness.Roles[role]; h != "" {
		return h
	}
	return s.Harness.Default
}
