package handler

import (
	"testing"

	"lykeion/server/internal/pack"
)

func TestCreateWorkspaceInstallsLaboratory(t *testing.T) {
	s := newServer(t)
	s.H.Shipped = pack.Shipped()
	a := s.signIn(t, "a@lab.org")
	w := s.workspace(t, a, "lab-one")
	if n := s.count(t, `SELECT count(*) FROM agent WHERE workspace_id=$1 AND laboratory = 'built-in'`, w.ID); n != 18 {
		t.Fatalf("%d laboratory agents, want 18", n)
	}
	if n := s.count(t, `SELECT count(*) FROM workspace WHERE id=$1 AND laboratory='built-in'`, w.ID); n != 1 {
		t.Fatalf("workspace.laboratory is not built-in")
	}
}

func TestCreateWorkspaceRefusesUnknownLaboratory(t *testing.T) {
	s := newServer(t)
	s.H.Shipped = pack.Shipped()
	a := s.signIn(t, "a@lab.org")
	var out map[string]any
	if c := s.call(t, "POST", "/api/workspaces", a, "", map[string]string{"name": "x", "slug": "acme-lab", "laboratory": "acme/lab"}, &out); c != 400 || out["error"] != "unknown laboratory" {
		t.Fatalf("unknown laboratory: %d %v", c, out)
	}
	if n := s.count(t, `SELECT count(*) FROM workspace WHERE slug='acme-lab'`); n != 0 {
		t.Fatalf("%d workspaces created", n)
	}
}

func TestListLaboratories(t *testing.T) {
	s := newServer(t)
	s.H.Shipped = pack.Shipped()
	a := s.signIn(t, "a@lab.org")
	var labs []struct {
		ID, Name string
		Rooms    []struct {
			Stage, Team string
			Agents      []struct{ Name, Role string }
		}
	}
	if c := s.call(t, "GET", "/api/laboratories", a, "", nil, &labs); c != 200 || len(labs) != 1 || labs[0].ID != "built-in" {
		t.Fatalf("%d %+v", c, labs)
	}
	stages := []string{"grounding", "hypotheses", "experimentation", "validation"}
	teams := []string{"Grounding Team", "Hypotheses Team", "Experimentation Team", "Validation Team"}
	total := 0
	if len(labs[0].Rooms) != 4 {
		t.Fatalf("%+v", labs[0].Rooms)
	}
	for i, r := range labs[0].Rooms {
		if r.Stage != stages[i] || r.Team != teams[i] {
			t.Fatalf("room %d: %s %s", i, r.Stage, r.Team)
		}
		roles := map[string]int{}
		for _, ag := range r.Agents {
			roles[ag.Role]++
		}
		if roles["leader"] != 1 || roles["auditor"] != 1 {
			t.Fatalf("room %d roles %v", i, roles)
		}
		total += len(r.Agents)
	}
	if total != 18 {
		t.Fatalf("%d agents", total)
	}
}
