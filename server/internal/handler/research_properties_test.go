package handler

import (
	"github.com/google/uuid"
	"testing"
)

type projectProperties struct {
	ID       string  `json:"id"`
	Name     string  `json:"name"`
	Status   string  `json:"status"`
	Priority string  `json:"priority"`
	LeadType *string `json:"lead_type"`
	LeadID   *string `json:"lead_id"`
}

func TestProjectPropertiesPersistAndClear(t *testing.T) {
	s := newServer(t)
	a := s.signIn(t, "projects@lab.org")
	w := s.workspace(t, a, "projects")
	var p projectProperties
	if code := s.call(t, "POST", "/api/researches", a, w.ID, map[string]any{"name": "Defaults"}, &p); code != 201 {
		t.Fatalf("create: %d", code)
	}
	if p.Status != "planned" || p.Priority != "none" || p.LeadID != nil || p.LeadType != nil {
		t.Fatalf("defaults: %+v", p)
	}
	member := s.member(t, a, w.ID, "lead@lab.org", "member")
	lead := s.userID(t, member)
	if code := s.call(t, "PATCH", "/api/researches/"+p.ID, a, w.ID, map[string]any{"status": "in_progress", "priority": "high", "lead_type": "member", "lead_id": lead}, &p); code != 200 {
		t.Fatalf("set properties: %d", code)
	}
	if code := s.call(t, "PATCH", "/api/researches/"+p.ID, a, w.ID, map[string]any{"name": "Renamed"}, &p); code != 200 {
		t.Fatalf("rename: %d", code)
	}
	s.call(t, "GET", "/api/researches/"+p.ID, a, w.ID, nil, &p)
	if p.Name != "Renamed" || p.Status != "in_progress" || p.Priority != "high" || p.LeadID == nil || *p.LeadID != lead {
		t.Fatalf("persist: %+v", p)
	}
	if code := s.call(t, "DELETE", "/api/workspaces/"+w.ID+"/members/"+lead, a, "", nil, nil); code != 204 {
		t.Fatalf("remove lead: %d", code)
	}
	p = projectProperties{ID: p.ID}
	s.call(t, "GET", "/api/researches/"+p.ID, a, w.ID, nil, &p)
	if p.LeadID != nil || p.LeadType != nil {
		t.Fatalf("departed lead retained: %+v", p)
	}
	agent := s.newAgent(t, a, w.ID, "Project lead", "claude")
	if code := s.call(t, "PATCH", "/api/researches/"+p.ID, a, w.ID, map[string]any{"lead_type": "agent", "lead_id": agent}, &p); code != 200 {
		t.Fatalf("agent lead: %d", code)
	}
	if code := s.call(t, "PATCH", "/api/researches/"+p.ID, a, w.ID, map[string]any{"lead_type": nil, "lead_id": nil}, &p); code != 200 {
		t.Fatalf("clear lead: %d", code)
	}
	if p.LeadID != nil || p.LeadType != nil {
		t.Fatalf("clear: %+v", p)
	}
}

func TestProjectPropertiesRejectInvalidAndForeignLeads(t *testing.T) {
	s := newServer(t)
	a := s.signIn(t, "projects@lab.org")
	w := s.workspace(t, a, "projects")
	b := s.signIn(t, "elsewhere@lab.org")
	other := s.workspace(t, b, "elsewhere")
	foreignAgent := s.newAgent(t, b, other.ID, "Foreign lead", "claude")
	var p projectProperties
	s.call(t, "POST", "/api/researches", a, w.ID, map[string]any{"name": "Project"}, &p)
	for _, fields := range []map[string]any{
		{"status": "backlog"}, {"status": nil}, {"priority": "critical"}, {"priority": nil},
		{"lead_type": "member"}, {"lead_id": s.userID(t, a)}, {"lead_type": nil, "lead_id": s.userID(t, a)},
		{"lead_type": "team", "lead_id": uuid.NewString()}, {"lead_type": "member", "lead_id": s.userID(t, b)},
		{"lead_type": "agent", "lead_id": foreignAgent}, {"lead_type": "agent", "lead_id": uuid.NewString()},
	} {
		if code := s.call(t, "PATCH", "/api/researches/"+p.ID, a, w.ID, fields, nil); code != 400 {
			t.Fatalf("accepted invalid properties %+v: %d", fields, code)
		}
		fields["name"] = "Invalid"
		if code := s.call(t, "POST", "/api/researches", a, w.ID, fields, nil); code != 400 {
			t.Fatalf("create accepted invalid properties %+v: %d", fields, code)
		}
	}
	if code := s.call(t, "PATCH", "/api/researches/"+p.ID, b, other.ID, map[string]any{"status": "completed"}, nil); code != 404 {
		t.Fatalf("foreign project: %d", code)
	}
	for _, sql := range []string{
		`UPDATE research SET status='bogus' WHERE id=$1`,
		`UPDATE research SET priority='bogus' WHERE id=$1`,
		`UPDATE research SET lead_type='member' WHERE id=$1`,
		`UPDATE research SET lead_type='agent',lead_id='` + foreignAgent + `' WHERE id=$1`,
	} {
		if _, err := s.Env.Owner.Exec(t.Context(), sql, p.ID); err == nil {
			t.Fatalf("database accepted %s", sql)
		}
	}
}

func TestProjectLeadAgentPermissionsAndHistoricalSelection(t *testing.T) {
	s := newServer(t)
	a := s.signIn(t, "projects@lab.org")
	w := s.workspace(t, a, "projects")
	b := s.member(t, a, w.ID, "other@lab.org", "member")
	own := s.newAgent(t, a, w.ID, "Own private lead", "claude")
	other := s.newAgent(t, b, w.ID, "Others private lead", "claude")
	for _, pair := range [][2]string{{own, a}, {other, b}} {
		if code := s.call(t, "PATCH", "/api/agents/"+pair[0], pair[1], w.ID, map[string]any{"visibility": "private"}, nil); code != 200 {
			t.Fatalf("make private: %d", code)
		}
	}
	if code := s.call(t, "POST", "/api/researches", a, w.ID, map[string]any{"name": "Forbidden", "lead_type": "agent", "lead_id": other}, nil); code != 400 {
		t.Fatalf("another person's private lead: %d", code)
	}
	var p projectProperties
	if code := s.call(t, "POST", "/api/researches", a, w.ID, map[string]any{"name": "Private lead", "lead_type": "agent", "lead_id": own}, &p); code != 201 {
		t.Fatalf("own private lead: %d", code)
	}
	if code := s.call(t, "POST", "/api/agents/"+own+"/archive", a, w.ID, nil, nil); code != 200 {
		t.Fatalf("archive: %d", code)
	}
	if code := s.call(t, "PATCH", "/api/researches/"+p.ID, a, w.ID, map[string]any{"name": "Archived lead retained", "lead_type": "agent", "lead_id": own}, &p); code != 200 {
		t.Fatalf("unchanged archived lead: %d", code)
	}
	if p.LeadID == nil || *p.LeadID != own {
		t.Fatal("historical lead lost")
	}
	if code := s.call(t, "POST", "/api/researches", a, w.ID, map[string]any{"name": "Archived", "lead_type": "agent", "lead_id": own}, nil); code != 400 {
		t.Fatalf("new archived lead: %d", code)
	}
}
