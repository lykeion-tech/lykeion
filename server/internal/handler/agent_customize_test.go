package handler

import (
	"testing"

	"lykeion/server/internal/pack"
	"lykeion/server/internal/testutil"
)

func TestCustomizeTeamMemberConnectors(t *testing.T) {
	s := newServer(t)
	a := s.signIn(t, "customize@lab.org")
	w := s.workspace(t, a, "lab")
	var team struct{ ID string }
	s.installPackFixture(t, a, w.ID, testutil.Pack(t, testutil.PackOpts{Name: "editable-customize", Connectors: map[string][]map[string]any{"pi": {{"name": "private-service", "url": "https://example.org/private", "env": map[string]string{"MODE": "scientific"}}}}}), &team)
	connectors := []pack.Connector{{Name: "private-service"}, {Name: "ensembl", Catalogue: "ensembl"}}
	if code := s.call(t, "POST", "/api/teams/"+team.ID+"/add", a, w.ID, nil, nil); code != 200 {
		t.Fatalf("add team: %d", code)
	}
	var agentID string
	if err := s.Env.Owner.QueryRow(t.Context(), `SELECT agent_id::text FROM team_agent WHERE pack_id=$1 AND member='pi'`, team.ID).Scan(&agentID); err != nil {
		t.Fatal(err)
	}
	var view struct {
		TeamMember bool `json:"team_member"`
		Teams      []struct {
			ID         string           `json:"id"`
			Member     string           `json:"member"`
			Connectors []pack.Connector `json:"connectors"`
		} `json:"teams"`
	}
	if code := s.call(t, "GET", "/api/agents/"+agentID+"/customize", a, w.ID, nil, &view); code != 200 || !view.TeamMember || len(view.Teams) != 1 || view.Teams[0].ID != team.ID || view.Teams[0].Member != "pi" {
		t.Fatalf("agent team lookup: %d %+v", code, view)
	}
	if len(view.Teams[0].Connectors) != 1 || view.Teams[0].Connectors[0].URL != "" {
		t.Fatalf("private connector details exposed: %+v", view.Teams[0].Connectors)
	}
	if code := s.call(t, "PUT", "/api/agents/"+agentID+"/customize", a, w.ID, map[string]any{"skills": []string{}, "connectors": []pack.Connector{}}, nil); code != 409 {
		t.Fatalf("team member cannot save standalone: %d", code)
	}
	if code := s.call(t, "POST", "/api/teams/"+team.ID+"/connectors", a, w.ID, map[string]any{"member": "pi", "connectors": connectors}, nil); code != 200 {
		t.Fatalf("save connectors: %d", code)
	}
	var got struct{ Manifest pack.Manifest }
	if code := s.call(t, "GET", "/api/teams/"+team.ID, a, w.ID, nil, &got); code != 200 || len(got.Manifest.Leader.Connectors) != 2 || (got.Manifest.Leader.Connectors[0].URL != "https://example.org/private" || got.Manifest.Leader.Connectors[0].Env["MODE"] != "scientific") || got.Manifest.Leader.Connectors[1].Catalogue != "ensembl" {
		t.Fatalf("stored connectors: %d %#v", code, got.Manifest.Leader.Connectors)
	}
	if code := s.call(t, "POST", "/api/teams/"+team.ID+"/connectors", a, w.ID, map[string]any{"member": "pi", "connectors": []pack.Connector{{Name: "unsafe", Command: []string{"tool"}}}}, nil); code != 400 {
		t.Fatalf("new local command accepted: %d", code)
	}
}

func TestCustomizeStandaloneAgent(t *testing.T) {
	s := newServer(t)
	a := s.signIn(t, "standalone-customize@lab.org")
	w := s.workspace(t, a, "lab")
	id := s.newAgent(t, a, w.ID, "Analyst", "claude")
	colleague := s.member(t, a, w.ID, "customize-colleague@lab.org", "member")
	body := map[string]any{"skills": []string{"aeon"}, "connectors": []pack.Connector{{Name: "ensembl", Catalogue: "ensembl"}}}
	if code := s.call(t, "PUT", "/api/agents/"+id+"/customize", a, w.ID, map[string]any{"skills": []string{"aeon", "aeon"}, "connectors": []pack.Connector{}}, nil); code != 400 {
		t.Fatalf("duplicate skill accepted: %d", code)
	}
	if code := s.call(t, "PUT", "/api/agents/"+id+"/customize", colleague, w.ID, body, nil); code != 403 {
		t.Fatalf("non-owner changed agent capabilities: %d", code)
	}
	var response struct {
		Error string `json:"error"`
	}
	if code := s.call(t, "PUT", "/api/agents/"+id+"/customize", a, w.ID, body, &response); code != 200 {
		t.Fatalf("save customization: %d %s", code, response.Error)
	}
	var got struct {
		Skills     []string         `json:"skills"`
		Connectors []pack.Connector `json:"connectors"`
	}
	if code := s.call(t, "GET", "/api/agents/"+id+"/customize", a, w.ID, nil, &got); code != 200 || len(got.Skills) != 1 || got.Skills[0] != "aeon" || len(got.Connectors) != 1 || got.Connectors[0].Catalogue != "ensembl" {
		t.Fatalf("saved customization: %d %+v", code, got)
	}
	if _, err := s.Env.Owner.Exec(t.Context(), `INSERT INTO agent_run(workspace_id,responsible_user_id,requester_id,kind,board_column,round,role,harness,prompt,agent_id) VALUES($1,$2,$2,'autoresearch','',0,'autoresearch','claude','run',$3)`, w.ID, s.userID(t, a), id); err != nil {
		t.Fatal(err)
	}
	if code := s.call(t, "PUT", "/api/agents/"+id+"/customize", a, w.ID, body, nil); code != 409 {
		t.Fatalf("live run allowed capabilities to change: %d", code)
	}
}
