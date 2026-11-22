package handler

import (
	"errors"
	"testing"

	"github.com/google/uuid"
	"lykeion/server/internal/store"
)

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
	input := map[string]any{"agent_id": agent, "prompt": "Study the evidence for sleep and memory", "research_id": project.ID, "start_date": "2026-11-03T00:00:00Z", "deadline": "2026-11-05T23:59:59Z", "priority": "high"}
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
	if n := s.count(t, `SELECT count(*) FROM study_draft_request WHERE workspace_id=$1 AND research_id=$2 AND priority='high'`, ws.ID, project.ID); n != 1 {
		t.Fatalf("priority not saved: %d", n)
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

func TestDraftStudyWithoutRequest(t *testing.T) {
	s := newServer(t)
	token := s.signIn(t, "nohttp@lab.org")
	ws := s.workspace(t, token, "nohttp-lab")
	agent := uuid.MustParse(s.stagedAgent(t, token, ws.ID, "hypotheses"))
	s.call(t, "POST", "/api/daemon/register", s.personalToken(t, token), "", map[string]any{"workspace_id": ws.ID, "daemon_id": "d", "harnesses": []string{"claude"}}, nil)
	wsID, user := uuid.MustParse(ws.ID), uuid.MustParse(s.userID(t, token))
	id, err := s.H.DraftStudy(t.Context(), wsID, user, StudyDraftIn{AgentID: agent, Prompt: "Study sleep", Priority: "none"})
	if err != nil || id == uuid.Nil {
		t.Fatalf("draft: %v %v", id, err)
	}
	if n := s.count(t, `SELECT count(*) FROM study_draft_request WHERE autoresearch_id=$1`, id); n != 1 {
		t.Fatalf("draft request count: %d", n)
	}
	var priv struct{ ID string }
	if c := s.call(t, "POST", "/api/agents", token, ws.ID, map[string]string{"name": "Priv", "harness": "claude", "instructions": "x", "stage": "grounding", "visibility": "private"}, &priv); c != 201 {
		t.Fatalf("private agent: %d", c)
	}
	err = s.H.Store.InWorkspace(t.Context(), wsID, user, func(tx *store.Tx) error {
		if err := DraftingAgentUsable(t.Context(), tx, wsID, agent); err != nil {
			t.Errorf("workspace agent refused: %v", err)
		}
		return DraftingAgentUsable(t.Context(), tx, wsID, uuid.MustParse(priv.ID))
	})
	var inputErr cardInputError
	if !errors.As(err, &inputErr) {
		t.Fatalf("private agent: %v", err)
	}
}
