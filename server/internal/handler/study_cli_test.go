package handler

import "testing"

func TestCreateStudyCLIs(t *testing.T) {
	s := newServer(t)
	s.builtIns(t)
	s.H.Engine = nil
	auth := s.signIn(t, "cli@lab.org")
	ws := s.workspace(t, auth, "cli-lab")
	for _, tc := range []struct {
		question, worker, auditor string
		want                      int
	}{
		{"valid", "claude", "codex", 201},
		{"same", "codex", "codex", 201},
		{"unknown", "claude", "other", 400},
		{"missing", "claude", "", 400},
	} {
		body := map[string]any{"question": tc.question, "study_cli": tc.worker}
		if tc.auditor != "" {
			body["auditor_cli"] = tc.auditor
		}
		var result map[string]any
		if got := s.call(t, "POST", "/api/cards", auth, ws.ID, body, &result); got != tc.want {
			t.Fatalf("%s: got %d want %d", tc.question, got, tc.want)
		}
		if tc.want == 201 && (result["study_cli"] != tc.worker || result["auditor_cli"] != tc.auditor) {
			t.Fatalf("CLIs: %v", result)
		}
	}
}
