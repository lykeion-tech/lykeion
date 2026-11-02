package handler

import (
	"slices"
	"testing"
)

func TestBuiltInTeamReturnsOwnMemberAgents(t *testing.T) {
	s := newServer(t)
	s.builtIns(t)
	s.H.Engine = nil
	auth := s.signIn(t, "team-cli@lab.org")
	ws := s.workspace(t, auth, "team-cli")
	id := s.builtInID(t, ws.ID, "grounding")
	if code := s.call(t, "POST", "/api/cards", auth, ws.ID, map[string]any{"question": "Question"}, nil); code != 201 {
		t.Fatalf("study %d", code)
	}
	var detail struct {
		MyAgents map[string]struct {
			ID      string `json:"id"`
			Harness string `json:"harness"`
		} `json:"my_agents"`
	}
	if code := s.call(t, "GET", "/api/teams/"+id, auth, ws.ID, nil, &detail); code != 200 {
		t.Fatalf("team %d", code)
	}
	if len(detail.MyAgents) == 0 {
		t.Fatal("no own member agents")
	}
	other := s.member(t, auth, ws.ID, "other-cli@lab.org", "member")
	var otherDetail struct {
		MyAgents map[string]any `json:"my_agents"`
	}
	if code := s.call(t, "GET", "/api/teams/"+id, other, ws.ID, nil, &otherDetail); code != 200 || len(otherDetail.MyAgents) != 0 {
		t.Fatalf("other user's agents: status %d, agents %v", code, otherDetail.MyAgents)
	}
	for _, member := range detail.MyAgents {
		var updated struct {
			Harness string `json:"harness"`
		}
		if code := s.call(t, "PATCH", "/api/agents/"+member.ID, auth, ws.ID, map[string]string{"harness": "codex", "model": ""}, &updated); code != 200 {
			t.Fatalf("update %d", code)
		}
		if updated.Harness != "codex" {
			t.Fatalf("harness %q", updated.Harness)
		}
		break
	}
	var teams []struct {
		ID        string   `json:"id"`
		AgentCLIs []string `json:"agent_clis"`
	}
	if code := s.call(t, "GET", "/api/teams", auth, ws.ID, nil, &teams); code != 200 {
		t.Fatalf("teams %d", code)
	}
	for _, team := range teams {
		if team.ID == id && !slices.Contains(team.AgentCLIs, "codex") {
			t.Fatalf("own CLIs %v", team.AgentCLIs)
		}
	}
	teams = nil
	if code := s.call(t, "GET", "/api/teams", other, ws.ID, nil, &teams); code != 200 {
		t.Fatalf("other teams %d", code)
	}
	for _, team := range teams {
		if team.ID == id && len(team.AgentCLIs) != 0 {
			t.Fatalf("other user's CLIs: %v", team.AgentCLIs)
		}
	}
}
