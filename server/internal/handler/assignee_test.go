package handler

import "testing"

func TestNoOneReassignsAStudyButAnyoneFilesIt(t *testing.T) {
	s := newServer(t)
	s.builtIns(t)
	a, b := s.signIn(t, "a@lab.org"), s.signIn(t, "b@lab.org")
	w := s.workspace(t, a, "lab")
	s.call(t, "POST", "/api/invitations/"+s.invite(t, a, w.ID, "b@lab.org", "member")+"/accept", b, "", nil, nil)
	var mine map[string]any
	s.call(t, "POST", "/api/agents", a, w.ID, map[string]any{"stage": "experimentation", "name": "Ada", "harness": "codex", "visibility": "private"}, &mine)
	var card map[string]any
	if code := s.call(t, "POST", "/api/cards", a, w.ID, map[string]any{"question": "Why do cells age?"}, &card); code != 201 {
		t.Fatalf("create: %d", code)
	}
	path := "/api/cards/" + card["id"].(string) + "/properties"
	for _, body := range []map[string]any{
		{"assignee_type": "agent", "assignee_id": mine["id"]},
		{"assignee_type": "member", "assignee_id": s.userID(t, a)},
		{"assignee_type": nil, "assignee_id": nil},
	} {
		if code := s.call(t, "PATCH", path, a, w.ID, body, nil); code != 400 {
			t.Fatalf("the owner reassigned the study (%v): %d", body, code)
		}
	}
	// The research a study belongs to stays anyone's to change.
	var res map[string]any
	s.call(t, "POST", "/api/researches", b, w.ID, map[string]any{"name": "Ageing"}, &res)
	if code := s.call(t, "PATCH", path, b, w.ID, map[string]any{"research_id": res["id"]}, &card); code != 200 || card["research_id"] != res["id"] || card["assignee_type"] != "team" {
		t.Fatalf("a colleague filed the study: %d %v", code, card)
	}
}
