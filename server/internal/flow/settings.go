package flow

import "encoding/json"

type Harness struct {
	Default string            `json:"default"`
	Roles   map[string]string `json:"roles,omitempty"`
}

// Settings live in workspace.settings; missing fields take the defaults.
type Settings struct {
	Harness Harness `json:"harness"`
}

func ParseSettings(raw json.RawMessage) Settings {
	s := Settings{Harness: Harness{Default: "claude"}}
	json.Unmarshal(raw, &s)
	return s
}

func (s Settings) HarnessFor(role string) string {
	if h := s.Harness.Roles[role]; h != "" {
		return h
	}
	return s.Harness.Default
}
