package handler

import "testing"

func TestAgentAndTeamDeletionBothHideTheirAgents(t *testing.T) {
	s := newServer(t)
	s.builtIns(t)
	a := s.signIn(t, "deletion-integration@lab.org")
	w := s.workspace(t, a, "lab")
	solo := s.newAgent(t, a, w.ID, "solo to delete", "claude")
	team := s.addedTeam(t, a, w.ID, "team to delete", "grounding")
	var copyID string
	if err := s.Env.Owner.QueryRow(t.Context(), `SELECT agent_id FROM team_agent WHERE pack_id = $1 LIMIT 1`, team).Scan(&copyID); err != nil {
		t.Fatal(err)
	}
	if code := s.call(t, "DELETE", "/api/agents/"+solo, a, w.ID, nil, nil); code != 200 {
		t.Fatalf("delete Agent: %d", code)
	}
	if code := s.call(t, "DELETE", "/api/teams/"+team, a, w.ID, nil, nil); code != 200 {
		t.Fatalf("delete Team: %d", code)
	}
	var agents []struct{ ID string }
	if code := s.call(t, "GET", "/api/agents", a, w.ID, nil, &agents); code != 200 {
		t.Fatalf("list Agents: %d", code)
	}
	for _, agent := range agents {
		if agent.ID == solo || agent.ID == copyID {
			t.Fatalf("deleted Agent %s is still listed", agent.ID)
		}
	}
	for _, id := range []string{solo, copyID} {
		if code := s.call(t, "GET", "/api/agents/"+id, a, w.ID, nil, nil); code != 404 {
			t.Fatalf("deleted Agent %s remains visible: %d", id, code)
		}
	}
}
