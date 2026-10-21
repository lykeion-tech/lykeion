package handler

import (
	"strings"
	"testing"
)

func TestRuntimesShowCLIVersionsAndActiveRuns(t *testing.T) {
	s := newServer(t)
	s.H.Engine = nil
	jwt := s.signIn(t, "a@lab.org")
	tok := s.personalToken(t, jwt)
	w := s.workspace(t, jwt, "lab")
	reg := map[string]any{"workspace_id": w.ID, "daemon_id": "d1", "device_name": "laptop", "harnesses": []string{"claude", "codex"},
		"versions": map[string]string{"claude": " 2.1.7 (Claude Code)\nupdate available", "codex": strings.Repeat("x", 100)}}
	if code := s.call(t, "POST", "/api/daemon/register", tok, "", reg, nil); code != 200 {
		t.Fatalf("register: %d", code)
	}
	type runtime struct {
		ID         string `json:"id"`
		Harness    string `json:"harness"`
		CLIVersion string `json:"cli_version"`
		ActiveRuns int    `json:"active_runs"`
	}
	list := func() map[string]runtime {
		t.Helper()
		var rts []runtime
		if code := s.call(t, "GET", "/api/runtimes", jwt, w.ID, nil, &rts); code != 200 {
			t.Fatalf("runtimes: %d", code)
		}
		out := map[string]runtime{}
		for _, r := range rts {
			out[r.Harness] = r
		}
		return out
	}
	rts := list()
	if rts["claude"].CLIVersion != "2.1.7 (Claude Code)" || rts["codex"].CLIVersion != strings.Repeat("x", 64) {
		t.Fatalf("versions: %+v", rts)
	}

	c := s.idleCard(t, jwt, w.ID, "q")
	owner := s.userID(t, jwt)
	for range 4 {
		s.queue(t, w.ID, c.ID, owner, "claude")
	}
	// Two runs on the claude runtime are active, one finished there, one still queued.
	if _, err := s.Env.Owner.Exec(t.Context(), `WITH n AS (SELECT id, row_number() OVER (ORDER BY created_at, id) AS i FROM agent_run WHERE card_id = $1)
		UPDATE agent_run SET runtime_id = CASE WHEN n.i < 4 THEN $2::uuid END,
		  status = CASE n.i WHEN 1 THEN 'dispatched' WHEN 2 THEN 'running' WHEN 3 THEN 'completed' ELSE 'queued' END,
		  attempt = CASE WHEN n.i < 4 THEN 1 ELSE 0 END
		FROM n WHERE agent_run.id = n.id`, c.ID, rts["claude"].ID); err != nil {
		t.Fatal(err)
	}
	if rts = list(); rts["claude"].ActiveRuns != 2 || rts["codex"].ActiveRuns != 0 {
		t.Fatalf("active runs: %+v", rts)
	}

	// A daemon that could not read its CLI's version clears the stale one.
	delete(reg, "versions")
	if code := s.call(t, "POST", "/api/daemon/register", tok, "", reg, nil); code != 200 {
		t.Fatalf("re-register: %d", code)
	}
	if rts = list(); rts["claude"].CLIVersion != "" || rts["codex"].CLIVersion != "" {
		t.Fatalf("stale versions kept: %+v", rts)
	}
}
