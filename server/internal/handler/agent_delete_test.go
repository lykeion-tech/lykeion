package handler

import (
	"context"
	"testing"
	"time"
)

func TestDeleteAgentPreservesFinishedStudyAndRun(t *testing.T) {
	s := newServer(t)
	a := s.signIn(t, "delete@lab.org")
	w := s.workspace(t, a, "lab")
	agent := s.newAgent(t, a, w.ID, "historical agent", "claude")
	card := s.startStudy(t, a, w.ID, agent, "question")
	s.mustUpdate(t, `UPDATE card SET state = 'done' WHERE id = $1`, card)
	s.mustUpdate(t, `INSERT INTO agent_run(workspace_id, card_id, responsible_user_id, board_column, round, role, harness, prompt, status, agent_id) SELECT workspace_id, id, owner_id, 'grounding', 1, 'agent', 'claude', 'test', 'completed', $2 FROM card WHERE id = $1`, card, agent)
	if code := s.call(t, "DELETE", "/api/agents/"+agent, a, w.ID, nil, nil); code != 200 {
		t.Fatalf("delete: %d", code)
	}
	if code := s.call(t, "GET", "/api/agents/"+agent, a, w.ID, nil, nil); code != 404 {
		t.Fatalf("deleted agent visible: %d", code)
	}
	if n := s.count(t, `SELECT count(*) FROM card WHERE id = $1 AND assignee_id = $2`, card, agent); n != 1 {
		t.Fatal("study lost its agent")
	}
	if n := s.count(t, `SELECT count(*) FROM agent_run r JOIN agent a ON a.id = r.agent_id WHERE r.card_id = $1 AND a.name = 'historical agent'`, card); n == 0 {
		t.Fatal("run lost its agent attribution")
	}
	var runs []map[string]any
	if code := s.call(t, "GET", "/api/cards/"+card+"/runs", a, w.ID, nil, &runs); code != 200 || len(runs) == 0 || runs[0]["agent_name"] != "historical agent" {
		t.Fatalf("run history: %d %+v", code, runs)
	}
}

func TestDeleteAgentBlockedWhileStudyUsesIt(t *testing.T) {
	s := newServer(t)
	a := s.signIn(t, "used@lab.org")
	w := s.workspace(t, a, "lab")
	agent := s.newAgent(t, a, w.ID, "busy", "claude")
	s.startStudy(t, a, w.ID, agent, "question")
	if code := s.call(t, "DELETE", "/api/agents/"+agent, a, w.ID, nil, nil); code != 409 {
		t.Fatalf("delete in-use agent: %d", code)
	}
}

func TestDeleteAgentBlockedByLiveRun(t *testing.T) {
	s := newServer(t)
	a := s.signIn(t, "run-delete@lab.org")
	w := s.workspace(t, a, "lab")
	agent := s.newAgent(t, a, w.ID, "running", "claude")
	card := s.startStudy(t, a, w.ID, agent, "question")
	s.mustUpdate(t, `UPDATE card SET state = 'done' WHERE id = $1`, card)
	s.mustUpdate(t, `INSERT INTO agent_run(workspace_id, card_id, responsible_user_id, board_column, round, role, harness, prompt, status, agent_id) SELECT workspace_id, id, owner_id, 'grounding', 1, 'agent', 'claude', 'test', 'queued', $2 FROM card WHERE id = $1`, card, agent)
	if code := s.call(t, "DELETE", "/api/agents/"+agent, a, w.ID, nil, nil); code != 409 {
		t.Fatalf("delete agent with live run: %d", code)
	}
}

func TestDeleteAgentWaitsForStudyAssignment(t *testing.T) {
	s := newServer(t)
	a := s.signIn(t, "race-delete@lab.org")
	w := s.workspace(t, a, "lab")
	agent := s.newAgent(t, a, w.ID, "race", "claude")
	card := s.idleCard(t, a, w.ID, "question")
	tx, err := s.Env.Owner.Begin(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback(context.Background())
	if _, err := tx.Exec(t.Context(), `SELECT 1 FROM agent WHERE id = $1 FOR SHARE`, agent); err != nil {
		t.Fatal(err)
	}
	if _, err := tx.Exec(t.Context(), `UPDATE card SET assignee_type = 'agent', assignee_id = $2 WHERE id = $1`, card.ID, agent); err != nil {
		t.Fatal(err)
	}
	done := make(chan int, 1)
	go func() { done <- s.call(t, "DELETE", "/api/agents/"+agent, a, w.ID, nil, nil) }()
	select {
	case code := <-done:
		t.Fatalf("delete returned %d before assignment committed", code)
	case <-time.After(500 * time.Millisecond):
	}
	if err := tx.Commit(t.Context()); err != nil {
		t.Fatal(err)
	}
	select {
	case code := <-done:
		if code != 409 {
			t.Fatalf("delete after assignment: %d", code)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("delete did not finish")
	}
}

func TestDeleteAgentRequiresOwnerOrAdmin(t *testing.T) {
	s := newServer(t)
	a := s.signIn(t, "owner-delete@lab.org")
	b := s.signIn(t, "member-delete@lab.org")
	w := s.workspace(t, a, "lab")
	s.call(t, "POST", "/api/invitations/"+s.invite(t, a, w.ID, "member-delete@lab.org", "member")+"/accept", b, "", nil, nil)
	agent := s.newAgent(t, a, w.ID, "shared agent", "claude")
	if code := s.call(t, "DELETE", "/api/agents/"+agent, b, w.ID, nil, nil); code != 403 {
		t.Fatalf("member delete: %d", code)
	}
}

func TestDeleteAgentBlockedByTeamMembership(t *testing.T) {
	s := newServer(t)
	s.builtIns(t)
	a := s.signIn(t, "team-delete@lab.org")
	w := s.workspace(t, a, "lab")
	src := s.builtInID(t, w.ID, "grounding")
	var made struct{ ID string }
	if code := s.call(t, "POST", "/api/teams/"+src+"/duplicate", a, w.ID, map[string]string{"name": "Duplicate"}, &made); code != 201 {
		t.Fatalf("duplicate: %d", code)
	}
	var agent string
	if err := s.Env.Owner.QueryRow(t.Context(), `SELECT agent_id FROM team_agent WHERE pack_id = $1 LIMIT 1`, made.ID).Scan(&agent); err != nil {
		t.Fatal(err)
	}
	if code := s.call(t, "DELETE", "/api/agents/"+agent, a, w.ID, nil, nil); code != 409 {
		t.Fatalf("delete Team member: %d", code)
	}
	if n := s.count(t, `SELECT count(*) FROM team_agent WHERE agent_id = $1`, agent); n != 1 {
		t.Fatal("Team member link was removed")
	}
}
