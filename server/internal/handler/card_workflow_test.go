package handler

import "testing"

const testWorkflow = "mendota-ice-phenology"

func TestAWorkflowStudyNeedsNoPreRegistration(t *testing.T) {
	s := newServer(t)
	a := s.signIn(t, "a@lab.org")
	w := s.workspace(t, a, "lab")
	agent := s.newAgent(t, a, w.ID, "solo", "claude")
	code, out := s.createWith(t, a, w.ID, map[string]any{"question": "q", "workflow": testWorkflow,
		"assignee": map[string]any{"type": "agent", "id": agent},
		"scope":    map[string]string{"from": "experimentation", "to": "experimentation"}})
	if code != 201 || out["workflow"] != testWorkflow || out["workflow_release"] != "v0.1.0" {
		t.Fatalf("%d %v", code, out)
	}
}

func TestAWorkflowStudyIsRefused(t *testing.T) {
	s := newServer(t)
	s.builtIns(t)
	a := s.signIn(t, "a@lab.org")
	w := s.workspace(t, a, "lab")
	agent := s.newAgent(t, a, w.ID, "solo", "claude")
	one := map[string]any{"type": "agent", "id": agent}
	exp := map[string]string{"from": "experimentation", "to": "experimentation"}
	for name, tc := range map[string]struct {
		body map[string]any
		want string
	}{
		"unknown": {map[string]any{"workflow": "nope", "assignee": one, "scope": exp}, "no workflow named nope"},
		"team": {map[string]any{"workflow": testWorkflow, "scope": exp,
			"assignee": map[string]any{"type": "team", "teams": map[string]string{"experimentation": s.builtInID(t, w.ID, "experimentation")}}}, "a workflow study is worked by one agent"},
		"range":      {map[string]any{"workflow": testWorkflow, "assignee": one, "scope": map[string]string{"from": "grounding", "to": "experimentation"}}, "a workflow study runs Experimentation only"},
		"predicates": {map[string]any{"workflow": testWorkflow, "assignee": one, "scope": exp, "predicates": testPredicates}, "a workflow study has no pre-registered results"},
	} {
		tc.body["question"] = name
		code, out := s.createWith(t, a, w.ID, tc.body)
		if code != 400 || out["error"] != tc.want {
			t.Errorf("%s: %d %v", name, code, out)
		}
	}
}

func TestAStudyWithoutAWorkflowStillPreRegisters(t *testing.T) {
	s := newServer(t)
	a := s.signIn(t, "a@lab.org")
	w := s.workspace(t, a, "lab")
	agent := s.newAgent(t, a, w.ID, "solo", "claude")
	code, out := s.createWith(t, a, w.ID, map[string]any{"question": "q", "assignee": map[string]any{"type": "agent", "id": agent},
		"scope": map[string]string{"from": "experimentation", "to": "experimentation"}})
	if code != 400 || out["error"] != "a study that runs Experimentation needs at least one pre-registered result" {
		t.Fatalf("%d %v", code, out)
	}
}

func TestAStudyRecordsTheWorkflowRelease(t *testing.T) {
	s := newServer(t)
	a := s.signIn(t, "a@lab.org")
	w := s.workspace(t, a, "lab")
	agent := s.newAgent(t, a, w.ID, "solo", "claude")
	code, out := s.createWith(t, a, w.ID, map[string]any{"question": "q", "assignee": map[string]any{"type": "agent", "id": agent},
		"scope": map[string]string{"from": "experimentation", "to": "experimentation"}, "predicates": testPredicates})
	if rel, has := out["workflow_release"]; code != 201 || !has || rel != nil {
		t.Fatalf("%d %v", code, out)
	}
}
