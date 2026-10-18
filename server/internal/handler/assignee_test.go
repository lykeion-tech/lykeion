package handler

import (
	"testing"
	"time"
)

func TestOnlyTheOwnerReassignsAStudyAndOnlyInBacklog(t *testing.T) {
	s := newServer(t)
	a, b := s.signIn(t, "a@lab.org"), s.signIn(t, "b@lab.org")
	w := s.workspace(t, a, "lab")
	s.call(t, "POST", "/api/invitations/"+s.invite(t, a, w.ID, "b@lab.org", "member")+"/accept", b, "", nil, nil)
	var mine, theirs map[string]any
	s.call(t, "POST", "/api/agents", a, w.ID, map[string]any{"name": "Ada", "harness": "codex", "visibility": "private"}, &mine)
	s.call(t, "POST", "/api/agents", b, w.ID, map[string]any{"name": "Bob", "harness": "codex", "visibility": "workspace"}, &theirs)
	var card map[string]any
	s.call(t, "POST", "/api/cards", a, w.ID, map[string]any{"question": "Why do cells age?"}, &card)
	id := card["id"].(string)
	path := "/api/cards/" + id + "/properties"

	// A colleague's agent would run its instructions on the owner's daemon.
	if code := s.call(t, "PATCH", path, b, w.ID, map[string]any{"assignee_type": "agent", "assignee_id": theirs["id"]}, nil); code != 403 {
		t.Fatalf("a colleague reassigned the study: %d", code)
	}
	if code := s.call(t, "PATCH", path, a, w.ID, map[string]any{"assignee_type": "team", "assignee_id": mine["id"]}, nil); code != 400 {
		t.Fatalf("a Team was assigned after creation: %d", code)
	}
	if code := s.call(t, "PATCH", path, a, w.ID, map[string]any{"assignee_type": "member", "assignee_id": s.userID(t, b)}, &card); code != 200 || card["board_column"] != "backlog" {
		t.Fatalf("a member assignment: %d %v", code, card["board_column"])
	}
	if code := s.call(t, "PATCH", path, a, w.ID, map[string]any{"assignee_type": "agent", "assignee_id": mine["id"]}, &card); code != 200 {
		t.Fatalf("the owner's agent assignment: %d", code)
	}
	if card["board_column"] != "grounding" || card["round"] != float64(1) {
		t.Fatalf("an agent assigned in Backlog did not start the study: %v round %v", card["board_column"], card["round"])
	}
	deadline := time.Now().Add(10 * time.Second)
	for s.count(t, `SELECT count(*) FROM agent_run WHERE card_id = $1 AND role = 'agent' AND board_column = 'grounding'`, id) != 1 {
		if time.Now().After(deadline) {
			t.Fatal("the started study queued no run of its agent")
		}
		time.Sleep(50 * time.Millisecond)
	}
	for _, body := range []map[string]any{
		{"assignee_type": nil, "assignee_id": nil},
		{"assignee_type": "member", "assignee_id": s.userID(t, a)},
	} {
		if code := s.call(t, "PATCH", path, a, w.ID, body, nil); code != 409 {
			t.Fatalf("a started study's assignee changed (%v): %d", body, code)
		}
	}
	// The research a study belongs to stays anyone's to change.
	var res map[string]any
	s.call(t, "POST", "/api/researches", b, w.ID, map[string]any{"name": "Ageing"}, &res)
	if code := s.call(t, "PATCH", path, b, w.ID, map[string]any{"research_id": res["id"]}, nil); code != 200 {
		t.Fatalf("a colleague filed the study: %d", code)
	}
}
