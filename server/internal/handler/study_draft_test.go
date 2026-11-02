package handler

import "testing"

func TestCreateStudyDraftQueuesOneShotAgent(t *testing.T) {
	s := newServer(t)
	token := s.signIn(t, "draft@lab.org")
	ws := s.workspace(t, token, "draft-lab")
	agent := s.newAgent(t, token, ws.ID, "Drafter", "claude")
	personal := s.personalToken(t, token)
	var runtimes []struct {
		ID string `json:"id"`
	}
	if code := s.call(t, "POST", "/api/daemon/register", personal, "", map[string]any{"workspace_id": ws.ID, "daemon_id": "draft-device", "harnesses": []string{"claude"}}, &runtimes); code != 200 || len(runtimes) != 1 {
		t.Fatalf("register: %d %+v", code, runtimes)
	}
	var project struct {
		ID string `json:"id"`
	}
	if code := s.call(t, "POST", "/api/researches", token, ws.ID, map[string]any{"name": "Draft project"}, &project); code != 201 {
		t.Fatalf("project: %d", code)
	}
	var result struct {
		RunID string `json:"run_id"`
	}
	input := map[string]any{"agent_id": agent, "prompt": "Study the evidence for sleep and memory", "research_id": project.ID, "start_date": "2026-11-03T00:00:00Z", "deadline": "2026-11-05T23:59:59Z"}
	if code := s.call(t, "POST", "/api/study-drafts", token, ws.ID, input, &result); code != 202 || result.RunID == "" {
		t.Fatalf("draft: %d %+v", code, result)
	}
	var rules []map[string]any
	if code := s.call(t, "GET", "/api/autoresearch", token, ws.ID, nil, &rules); code != 200 || len(rules) != 0 {
		t.Fatalf("draft appeared as an automation: %d %+v", code, rules)
	}
	if n := s.count(t, `SELECT count(*) FROM study_draft_request WHERE workspace_id=$1 AND research_id=$2`, ws.ID, project.ID); n != 1 {
		t.Fatalf("draft request count: %d", n)
	}
	if n := s.count(t, `SELECT count(*) FROM agent_run WHERE workspace_id=$1 AND kind='autoresearch' AND status='queued'`, ws.ID); n != 1 {
		t.Fatalf("queued agent runs: %d", n)
	}
	ref := map[string]any{"workspace_id": ws.ID, "runtime_id": runtimes[0].ID}
	var claimed []struct {
		AgentRun struct {
			ID      string `json:"id"`
			Attempt int32  `json:"attempt"`
		} `json:"agent_run"`
		Token string `json:"token"`
	}
	if code := s.call(t, "POST", "/api/daemon/agent-runs/claim", personal, "", map[string]any{"runtimes": []any{ref}, "slots": 1}, &claimed); code != 200 || len(claimed) != 1 {
		t.Fatalf("claim: %d %+v", code, claimed)
	}
	ref["attempt"] = claimed[0].AgentRun.Attempt
	if code := s.call(t, "POST", "/api/daemon/agent-runs/"+claimed[0].AgentRun.ID+"/start", personal, "", ref, nil); code != 204 {
		t.Fatalf("start: %d", code)
	}
	var created struct {
		ID         string  `json:"id"`
		ResearchID *string `json:"research_id"`
	}
	if code := s.call(t, "POST", "/api/role/cards", claimed[0].Token, "", map[string]string{"title": "Sleep and memory", "question": "Does sleep affect memory?"}, &created); code != 201 || created.ResearchID == nil || *created.ResearchID != project.ID {
		t.Fatalf("agent create: %d %+v", code, created)
	}
	if code := s.call(t, "POST", "/api/role/cards", claimed[0].Token, "", map[string]string{"title": "A second study", "question": "Another question"}, nil); code != 409 {
		t.Fatalf("second create: %d", code)
	}
}

func TestStudyDraftRejectsUnavailableAgentAndInvalidDates(t *testing.T) {
	s := newServer(t)
	token := s.signIn(t, "draft-offline@lab.org")
	ws := s.workspace(t, token, "draft-offline")
	agent := s.newAgent(t, token, ws.ID, "Offline", "claude")
	for _, input := range []map[string]any{
		{"agent_id": agent, "prompt": "Draft a study"},
		{"agent_id": agent, "prompt": "Draft a study", "start_date": "2026-11-08T00:00:00Z", "deadline": "2026-11-05T23:59:59Z"},
	} {
		if code := s.call(t, "POST", "/api/study-drafts", token, ws.ID, input, nil); code != 400 {
			t.Fatalf("accepted invalid request: %d", code)
		}
	}
	if n := s.count(t, `SELECT count(*) FROM study_draft_request WHERE workspace_id=$1`, ws.ID); n != 0 {
		t.Fatalf("invalid request persisted: %d", n)
	}
}
