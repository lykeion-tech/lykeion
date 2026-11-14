package handler

import (
	"testing"

	"lykeion/server/internal/testutil"
)

func TestDuplicateAgentCopiesSettingsAndCapabilities(t *testing.T) {
	s := newServer(t)
	a := s.signIn(t, "agent-duplicate@lab.org")
	w := s.workspace(t, a, "lab")
	id := s.newAgent(t, a, w.ID, "Original", "claude")
	if code := s.call(t, "PATCH", "/api/agents/"+id, a, w.ID, map[string]any{"avatar_url": "emoji:🧪", "visibility": "private"}, nil); code != 200 {
		t.Fatalf("update source: %d", code)
	}
	colleague := s.member(t, a, w.ID, "duplicate-colleague@lab.org", "member")
	if code := s.call(t, "POST", "/api/agents/"+id+"/duplicate", colleague, w.ID, map[string]string{"name": "Secret copy"}, nil); code != 404 {
		t.Fatalf("private agent visible: %d", code)
	}
	if code := s.call(t, "PUT", "/api/agents/"+id+"/customize", a, w.ID, map[string]any{"skills": []string{"aeon"}, "connectors": []any{map[string]string{"name": "ensembl", "catalogue": "ensembl"}}}, nil); code != 200 {
		t.Fatalf("customize: %d", code)
	}
	var copy struct {
		ID, Name, Instructions, Visibility string
		AvatarURL                          string `json:"avatar_url"`
	}
	if code := s.call(t, "POST", "/api/agents/"+id+"/duplicate", a, w.ID, map[string]string{"name": "Copy"}, &copy); code != 201 {
		t.Fatalf("duplicate: %d", code)
	}
	if copy.ID == id || copy.Name != "Copy" || copy.Instructions != "Be Original" || copy.Visibility != "private" || copy.AvatarURL != "emoji:🧪" {
		t.Fatalf("copy: %+v", copy)
	}
	var caps struct {
		Skills     []string
		Connectors []struct{ Catalogue string }
	}
	if code := s.call(t, "GET", "/api/agents/"+copy.ID+"/customize", a, w.ID, nil, &caps); code != 200 || len(caps.Skills) != 1 || caps.Skills[0] != "aeon" || len(caps.Connectors) != 1 || caps.Connectors[0].Catalogue != "ensembl" {
		t.Fatalf("capabilities: %d %+v", code, caps)
	}
}

func TestDuplicateTeamMemberLeavesTeamOnlyCapabilities(t *testing.T) {
	s := newServer(t)
	a := s.signIn(t, "duplicate-team-member@lab.org")
	w := s.workspace(t, a, "lab")
	var team struct{ ID string }
	s.installPackFixture(t, a, w.ID, testutil.Pack(t, testutil.PackOpts{Name: "copy-team", Catalogue: map[string][]string{"pi": {"ensembl"}}, Connectors: map[string][]map[string]any{"pi": {{"name": "private-tool", "url": "https://example.org/private"}}}}), &team)
	if code := s.call(t, "POST", "/api/teams/"+team.ID+"/add", a, w.ID, nil, nil); code != 200 {
		t.Fatalf("add Team: %d", code)
	}
	var agent string
	if err := s.Env.Owner.QueryRow(t.Context(), `SELECT agent_id::text FROM team_agent WHERE pack_id=$1 AND member='pi'`, team.ID).Scan(&agent); err != nil {
		t.Fatal(err)
	}
	var copy struct{ ID, Instructions string }
	if code := s.call(t, "POST", "/api/agents/"+agent+"/duplicate", a, w.ID, map[string]string{"name": "Copy"}, &copy); code != 201 {
		t.Fatalf("duplicate: %d", code)
	}
	if copy.Instructions != "You are pi." {
		t.Fatalf("instructions: %q", copy.Instructions)
	}
	var caps struct {
		Skills     []string
		Connectors []struct{ Catalogue string }
		TeamMember bool `json:"team_member"`
	}
	if code := s.call(t, "GET", "/api/agents/"+copy.ID+"/customize", a, w.ID, nil, &caps); code != 200 || caps.TeamMember || len(caps.Skills) != 0 || len(caps.Connectors) != 1 || caps.Connectors[0].Catalogue != "ensembl" {
		t.Fatalf("copy capabilities: %d %+v", code, caps)
	}
}

func TestCannotDuplicateBuiltInAgent(t *testing.T) {
	s := newServer(t)
	a := s.signIn(t, "builtin-duplicate@lab.org")
	w := s.workspace(t, a, "lab")
	id := s.newAgent(t, a, w.ID, "Memory", "claude")
	if _, err := s.Env.Owner.Exec(t.Context(), `UPDATE agent SET visibility = 'private', built_in_key = 'aristotle' WHERE id = $1`, id); err != nil {
		t.Fatal(err)
	}
	if code := s.call(t, "POST", "/api/agents/"+id+"/duplicate", a, w.ID, map[string]string{"name": "Copy"}, nil); code != 403 {
		t.Fatalf("duplicate built-in agent: %d", code)
	}
}
