package handler

import "testing"

const testWorkflow = "mendota-ice-phenology"

func (s *server) createWith(t *testing.T, tok, wsID string, body map[string]any) (int, map[string]any) {
	t.Helper()
	var out map[string]any
	code := s.call(t, "POST", "/api/cards", tok, wsID, body, &out)
	return code, out
}

func TestAWorkflowStudyIsCreated(t *testing.T) {
	s := newServer(t)
	a := s.signIn(t, "a@lab.org")
	w := s.workspace(t, a, "lab")
	agent := s.newAgent(t, a, w.ID, "solo", "claude")
	code, out := s.createWith(t, a, w.ID, map[string]any{"question": "q", "workflow": testWorkflow,
		"attachment_ids": []string{s.uploadData(t, a, w.ID, "data.csv", []byte("a,b\n1,2\n"))},
		"assignee":       map[string]any{"type": "agent", "id": agent},
		"scope":          map[string]string{"from": "experimentation", "to": "experimentation"}})
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
		"range": {map[string]any{"workflow": testWorkflow, "assignee": one, "scope": map[string]string{"from": "grounding", "to": "experimentation"}}, "a workflow study runs Experimentation only"},
	} {
		tc.body["question"] = name
		code, out := s.createWith(t, a, w.ID, tc.body)
		if code != 400 || out["error"] != tc.want {
			t.Errorf("%s: %d %v", name, code, out)
		}
	}
}

func TestWorkflowStudyNeedsAnAttachment(t *testing.T) {
	s := newServer(t)
	a := s.signIn(t, "a@lab.org")
	w := s.workspace(t, a, "lab")
	agent := s.newAgent(t, a, w.ID, "solo", "claude")
	body := map[string]any{"question": "q", "workflow": testWorkflow,
		"assignee": map[string]any{"type": "agent", "id": agent},
		"scope":    map[string]string{"from": "experimentation", "to": "experimentation"}}
	code, out := s.createWith(t, a, w.ID, body)
	if code != 400 || out["error"] != "A Workflow runs its method on your data: attach at least one file." {
		t.Fatalf("no attachment: %d %v", code, out)
	}
	body["attachment_ids"] = []string{s.uploadData(t, a, w.ID, "data.csv", []byte("a,b\n1,2\n"))}
	if code, out = s.createWith(t, a, w.ID, body); code != 201 {
		t.Fatalf("with attachment: %d %v", code, out)
	}
}

func TestNoOpenCardRoute(t *testing.T) {
	s := newServer(t)
	a := s.signIn(t, "a@lab.org")
	w := s.workspace(t, a, "lab")
	_, _, ltk, _ := s.runOnly(t, a, w.ID)
	if code := s.call(t, "POST", "/api/role/cards", ltk, "", map[string]string{"question": "q"}, nil); code != 404 {
		t.Fatalf("open-card route: %d", code)
	}
}

func TestAStudyRecordsTheWorkflowRelease(t *testing.T) {
	s := newServer(t)
	a := s.signIn(t, "a@lab.org")
	w := s.workspace(t, a, "lab")
	agent := s.newAgent(t, a, w.ID, "solo", "claude")
	code, out := s.createWith(t, a, w.ID, map[string]any{"question": "q", "assignee": map[string]any{"type": "agent", "id": agent},
		"scope": map[string]string{"from": "experimentation", "to": "experimentation"}})
	if rel, has := out["workflow_release"]; code != 201 || !has || rel != nil {
		t.Fatalf("%d %v", code, out)
	}
}
