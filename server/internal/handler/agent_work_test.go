package handler

import (
	"reflect"
	"testing"
)

func TestAgentWorkShowsItsTeamRunsInTheCurrentWorkspace(t *testing.T) {
	s := newServer(t)
	s.H.Engine = nil
	a := s.signIn(t, "agent-work@lab.org")
	w := s.workspace(t, a, "agent-work")
	id := s.newAgent(t, a, w.ID, "Leader", "codex")
	otherID := s.newAgent(t, a, w.ID, "Another member", "codex")
	if code := s.call(t, "PATCH", "/api/agents/"+id, a, w.ID, map[string]string{"visibility": "private"}, nil); code != 200 {
		t.Fatal(code)
	}
	active := s.idleCard(t, a, w.ID, "Study with active leader")
	earlier := s.idleCard(t, a, w.ID, "Study with earlier work")
	neither := s.idleCard(t, a, w.ID, "Only the other agent's work")
	for _, card := range []string{active.ID, earlier.ID, neither.ID} {
		s.mustUpdate(t, `UPDATE card SET assignee_type = 'team', assignee_id = NULL WHERE id = $1`, card)
	}
	insert := func(card, agent, role, status, kind string) {
		s.mustUpdate(t, `INSERT INTO agent_run(workspace_id, card_id, responsible_user_id, board_column, round, role, harness, prompt, status, kind, agent_id)
SELECT workspace_id, id, owner_id, 'grounding', 1, $3, 'codex', 'test', $4, $5, $2 FROM card WHERE id = $1`, card, agent, role, status, kind)
	}
	insert(active.ID, id, "leader", "running", "role")
	insert(active.ID, id, "member", "completed", "role")
	insert(earlier.ID, id, "leader", "completed", "role")
	insert(earlier.ID, otherID, "auditor", "running", "role")
	insert(neither.ID, otherID, "leader", "running", "role")
	insert(neither.ID, id, "reporter", "running", "report")
	var work []struct {
		CardID     string `json:"card_id"`
		ActiveRole string `json:"active_role"`
	}
	path := "/api/agents/" + id + "/work"
	if code := s.call(t, "GET", path, a, w.ID, nil, &work); code != 200 {
		t.Fatalf("get work: %d", code)
	}
	got := make(map[string]string)
	for _, row := range work {
		got[row.CardID] = row.ActiveRole
	}
	if len(work) != 2 || !reflect.DeepEqual(got, map[string]string{active.ID: "leader", earlier.ID: ""}) {
		t.Fatalf("work: %+v", work)
	}
	colleague := s.member(t, a, w.ID, "agent-work-colleague@lab.org", "member")
	if code := s.call(t, "GET", path, colleague, w.ID, nil, nil); code != 404 {
		t.Fatalf("private agent work visible to colleague: %d", code)
	}
	otherWorkspace := s.workspace(t, a, "other-agent-work")
	if code := s.call(t, "GET", path, a, otherWorkspace.ID, nil, nil); code != 404 {
		t.Fatalf("cross-workspace agent work visible: %d", code)
	}
}
