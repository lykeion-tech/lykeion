package handler

import "testing"

// stagedAgent creates an agent working one stage ("" leaves it unstaged).
func (s *server) stagedAgent(t *testing.T, tok, wsID, stage string) string {
	t.Helper()
	id := s.newAgent(t, tok, wsID, "w-"+stage, "claude")
	if _, err := s.Env.Owner.Exec(t.Context(), `UPDATE agent SET stage = $1 WHERE id = $2`, stage, id); err != nil {
		t.Fatal(err)
	}
	return id
}

func oneAgentStudy(s *server, t *testing.T, tok, wsID, agent, from, to string) (int, map[string]any) {
	t.Helper()
	return s.createWith(t, tok, wsID, map[string]any{"question": "q " + from,
		"assignee": map[string]any{"type": "agent", "id": agent},
		"scope":    map[string]string{"from": from, "to": to}})
}

func TestOneAgentStudyNeedsMatchingStage(t *testing.T) {
	s := newServer(t)
	a := s.signIn(t, "a@lab.org")
	w := s.workspace(t, a, "lab")
	agent := s.stagedAgent(t, a, w.ID, "hypotheses")
	if code, out := oneAgentStudy(s, t, a, w.ID, agent, "hypotheses", "hypotheses"); code != 201 {
		t.Fatalf("%d %v", code, out)
	}
	if code, out := oneAgentStudy(s, t, a, w.ID, agent, "grounding", "grounding"); code != 400 || out["error"] != "this agent works only on Hypotheses" {
		t.Fatalf("%d %v", code, out)
	}
}

func TestOneAgentStudyRefusesWiderScope(t *testing.T) {
	s := newServer(t)
	a := s.signIn(t, "a@lab.org")
	w := s.workspace(t, a, "lab")
	agent := s.stagedAgent(t, a, w.ID, "grounding")
	if code, out := oneAgentStudy(s, t, a, w.ID, agent, "grounding", "validation"); code != 400 || out["error"] != "this agent works only on Grounding" {
		t.Fatalf("%d %v", code, out)
	}
}

func TestUnstagedAgentRefused(t *testing.T) {
	s := newServer(t)
	a := s.signIn(t, "a@lab.org")
	w := s.workspace(t, a, "lab")
	agent := s.stagedAgent(t, a, w.ID, "")
	if code, out := oneAgentStudy(s, t, a, w.ID, agent, "grounding", "grounding"); code != 400 || out["error"] != "this agent has no stage" {
		t.Fatalf("%d %v", code, out)
	}
}

func TestAutoresearchFillerAgentCoversOnlyItsStage(t *testing.T) {
	s := newServer(t)
	a := s.signIn(t, "a@lab.org")
	w := s.workspace(t, a, "lab")
	g := s.addedTeam(t, a, w.ID, "g", "grounding")
	agent := s.stagedAgent(t, a, w.ID, "hypotheses")
	body := func(to string) map[string]any {
		as := teamAssignee(map[string]string{"grounding": g})
		as["agent_id"] = agent
		return map[string]any{"title": "Weekly", "execution_mode": "create_card", "assignee": as,
			"scope": map[string]string{"from": "grounding", "to": to}}
	}
	if code := s.call(t, "POST", "/api/autoresearch", a, w.ID, body("hypotheses"), nil); code != 201 {
		t.Fatalf("accepted: %d", code)
	}
	var e struct{ Error string }
	if code := s.call(t, "POST", "/api/autoresearch", a, w.ID, body("experimentation"), &e); code != 400 || e.Error != "this agent works only on Hypotheses" {
		t.Fatalf("refused: %d %q", code, e.Error)
	}
}

func TestWorkflowStudyNeedsExperimentationAgent(t *testing.T) {
	s := newServer(t)
	a := s.signIn(t, "a@lab.org")
	w := s.workspace(t, a, "lab")
	agent := s.stagedAgent(t, a, w.ID, "grounding")
	code, out := s.createWith(t, a, w.ID, map[string]any{"question": "q", "workflow": testWorkflow,
		"attachment_ids": []string{s.uploadData(t, a, w.ID, "data.csv", []byte("a,b\n1,2\n"))},
		"assignee":       map[string]any{"type": "agent", "id": agent},
		"scope":          map[string]string{"from": "experimentation", "to": "experimentation"}})
	if code != 400 || out["error"] != "this agent works only on Grounding" {
		t.Fatalf("%d %v", code, out)
	}
}
