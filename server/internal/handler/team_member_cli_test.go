package handler

import (
	"testing"
)

func TestBuiltInTeamReturnsSharedMemberAgents(t *testing.T) {
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
		Manifest struct {
			Leader struct {
				Name string `json:"name"`
			} `json:"leader"`
		} `json:"manifest"`
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
		MyAgents map[string]struct {
			ID string `json:"id"`
		} `json:"my_agents"`
	}
	if code := s.call(t, "GET", "/api/teams/"+id, other, ws.ID, nil, &otherDetail); code != 200 || len(otherDetail.MyAgents) != len(detail.MyAgents) {
		t.Fatalf("other user's agents: status %d, agents %v", code, otherDetail.MyAgents)
	}
	for name, a := range detail.MyAgents {
		if otherDetail.MyAgents[name].ID != a.ID {
			t.Fatalf("%s: the other member sees agent %s, want the shared %s", name, otherDetail.MyAgents[name].ID, a.ID)
		}
	}
	for name, member := range detail.MyAgents {
		if name != detail.Manifest.Leader.Name {
			continue
		}
		if code := s.call(t, "PATCH", "/api/agents/"+member.ID, auth, ws.ID, map[string]string{"harness": "codex", "model": ""}, nil); code != 403 {
			t.Fatalf("laboratory agent update %d, want 403", code)
		}
		break
	}
}
