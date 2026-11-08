package handler

import "testing"

func draftBody(agent string) map[string]any {
	return map[string]any{"agent_id": agent, "prompt": "Study sleep and memory."}
}

func TestStudyDraftRefusesUnstagedAgent(t *testing.T) {
	s := newServer(t)
	a := s.signIn(t, "a@lab.org")
	w := s.workspace(t, a, "lab")
	agent := s.stagedAgent(t, a, w.ID, "")
	var out map[string]any
	if code := s.call(t, "POST", "/api/study-drafts", a, w.ID, draftBody(agent), &out); code != 400 || out["error"] != "this agent has no stage" {
		t.Fatalf("%d %v", code, out)
	}
}

func TestStudyDraftScopeIsAgentStage(t *testing.T) {
	s := newServer(t)
	a := s.signIn(t, "a@lab.org")
	w := s.workspace(t, a, "lab")
	agent := s.stagedAgent(t, a, w.ID, "hypotheses")
	s.call(t, "POST", "/api/daemon/register", s.personalToken(t, a), "", map[string]any{"workspace_id": w.ID, "daemon_id": "d", "harnesses": []string{"claude"}}, nil)
	var out map[string]any
	if code := s.call(t, "POST", "/api/study-drafts", a, w.ID, draftBody(agent), &out); code != 202 {
		t.Fatalf("%d %v", code, out)
	}
	if n := s.count(t, `SELECT count(*) FROM autoresearch WHERE assignee_id = $1 AND scope_from = 'hypotheses' AND scope_to = 'hypotheses'`, agent); n != 1 {
		t.Fatalf("draft scope is not the agent's stage")
	}
}
