package handler

import (
	"testing"

	"github.com/google/uuid"
)

func TestDeleteResearchDetachesStudiesAndRequiresAdmin(t *testing.T) {
	s := newServer(t)
	s.builtIns(t)
	owner := s.signIn(t, "project-delete-owner@lab.org")
	member := s.signIn(t, "project-delete-member@lab.org")
	admin := s.signIn(t, "project-delete-admin@lab.org")
	outsider := s.signIn(t, "project-delete-foreign@lab.org")
	ws := s.workspace(t, owner, "project-delete")
	other := s.workspace(t, outsider, "project-delete-other")
	for _, m := range []struct{ token, role string }{{member, "member"}, {admin, "admin"}} {
		if _, err := s.Env.Owner.Exec(t.Context(), `INSERT INTO member(workspace_id,user_id,role) VALUES($1,$2,$3)`, ws.ID, s.userID(t, m.token), m.role); err != nil {
			t.Fatal(err)
		}
	}
	var project struct {
		ID string `json:"id"`
	}
	if code := s.call(t, "POST", "/api/researches", owner, ws.ID, map[string]any{"name": "Delete test"}, &project); code != 201 {
		t.Fatalf("create project: %d", code)
	}
	var study projectStudy
	if code := s.call(t, "POST", "/api/cards", owner, ws.ID, map[string]any{"question": "Preserve this study", "research_id": project.ID}, &study); code != 201 {
		t.Fatalf("create study: %d", code)
	}
	var beforeVersion int
	if err := s.Env.Owner.QueryRow(t.Context(), `SELECT version FROM card WHERE id=$1`, study.ID).Scan(&beforeVersion); err != nil {
		t.Fatal(err)
	}
	path := "/api/researches/" + project.ID
	if code := s.call(t, "DELETE", path, member, ws.ID, nil, nil); code != 403 {
		t.Fatalf("member delete: %d", code)
	}
	if code := s.call(t, "DELETE", path, outsider, other.ID, nil, nil); code != 404 {
		t.Fatalf("foreign delete: %d", code)
	}
	if code := s.call(t, "DELETE", path, admin, ws.ID, nil, nil); code != 204 {
		t.Fatalf("admin delete: %d", code)
	}
	if code := s.call(t, "GET", path, owner, ws.ID, nil, nil); code != 404 {
		t.Fatalf("deleted project still visible: %d", code)
	}
	var retained projectStudy
	if code := s.call(t, "GET", "/api/cards/"+study.ID, owner, ws.ID, nil, &retained); code != 200 || retained.ResearchID != nil {
		t.Fatalf("study lost or still linked: %d %+v", code, retained)
	}
	var afterVersion int
	if err := s.Env.Owner.QueryRow(t.Context(), `SELECT version FROM card WHERE id=$1`, study.ID).Scan(&afterVersion); err != nil {
		t.Fatal(err)
	}
	if afterVersion != beforeVersion+1 {
		t.Fatalf("detached study version: got %d want %d", afterVersion, beforeVersion+1)
	}
	if n := s.count(t, `SELECT count(*) FROM event WHERE workspace_id=$1 AND card_id=$2 AND type='card.properties_updated' AND payload->>'previous_research_id'=$3 AND payload->'research_id'='null'::jsonb AND payload->'fields'='["research_id"]'::jsonb`, ws.ID, study.ID, project.ID); n != 1 {
		t.Fatalf("detached study property event count %d", n)
	}
	if n := s.count(t, `SELECT count(*) FROM event WHERE workspace_id=$1 AND type='research.deleted' AND payload->>'research_id'=$2`, ws.ID, project.ID); n != 1 {
		t.Fatalf("deleted event count %d", n)
	}
	if code := s.call(t, "DELETE", path, owner, ws.ID, nil, nil); code != 404 {
		t.Fatalf("delete missing project: %d", code)
	}
	if code := s.call(t, "DELETE", "/api/researches/"+uuid.NewString(), owner, ws.ID, nil, nil); code != 404 {
		t.Fatalf("delete unknown project: %d", code)
	}
}

func TestDeleteResearchRollsBackIfEventFails(t *testing.T) {
	s := newServer(t)
	s.builtIns(t)
	owner := s.signIn(t, "project-delete-atomic@lab.org")
	ws := s.workspace(t, owner, "project-delete-atomic")
	var project struct {
		ID string `json:"id"`
	}
	if code := s.call(t, "POST", "/api/researches", owner, ws.ID, map[string]any{"name": "Atomic"}, &project); code != 201 {
		t.Fatalf("create project: %d", code)
	}
	var study projectStudy
	if code := s.call(t, "POST", "/api/cards", owner, ws.ID, map[string]any{"question": "Keep project link", "research_id": project.ID}, &study); code != 201 {
		t.Fatalf("create study: %d", code)
	}
	if _, err := s.Env.Owner.Exec(t.Context(), `ALTER TABLE event ADD CONSTRAINT refuse_research_delete CHECK (type <> 'research.deleted')`); err != nil {
		t.Fatal(err)
	}
	if code := s.call(t, "DELETE", "/api/researches/"+project.ID, owner, ws.ID, nil, nil); code != 500 {
		t.Fatalf("event failure: %d", code)
	}
	var retained projectStudy
	if code := s.call(t, "GET", "/api/cards/"+study.ID, owner, ws.ID, nil, &retained); code != 200 || retained.ResearchID == nil || *retained.ResearchID != project.ID {
		t.Fatalf("link not rolled back: %d %+v", code, retained)
	}
	if code := s.call(t, "GET", "/api/researches/"+project.ID, owner, ws.ID, nil, nil); code != 200 {
		t.Fatalf("project not rolled back: %d", code)
	}
}

func TestDeleteResearchRollsBackIfStudyEventFails(t *testing.T) {
	s := newServer(t)
	s.builtIns(t)
	owner := s.signIn(t, "project-delete-card-event@lab.org")
	ws := s.workspace(t, owner, "project-delete-card-event")
	var project struct {
		ID string `json:"id"`
	}
	if code := s.call(t, "POST", "/api/researches", owner, ws.ID, map[string]any{"name": "Atomic"}, &project); code != 201 {
		t.Fatalf("create project: %d", code)
	}
	var study projectStudy
	if code := s.call(t, "POST", "/api/cards", owner, ws.ID, map[string]any{"question": "Keep project link", "research_id": project.ID}, &study); code != 201 {
		t.Fatalf("create study: %d", code)
	}
	var beforeVersion int
	if err := s.Env.Owner.QueryRow(t.Context(), `SELECT version FROM card WHERE id=$1`, study.ID).Scan(&beforeVersion); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Env.Owner.Exec(t.Context(), `ALTER TABLE event ADD CONSTRAINT refuse_detach_event CHECK (type <> 'card.properties_updated')`); err != nil {
		t.Fatal(err)
	}
	if code := s.call(t, "DELETE", "/api/researches/"+project.ID, owner, ws.ID, nil, nil); code != 500 {
		t.Fatalf("study event failure: %d", code)
	}
	var retained projectStudy
	if code := s.call(t, "GET", "/api/cards/"+study.ID, owner, ws.ID, nil, &retained); code != 200 || retained.ResearchID == nil || *retained.ResearchID != project.ID {
		t.Fatalf("link not rolled back: %d %+v", code, retained)
	}
	var afterVersion int
	if err := s.Env.Owner.QueryRow(t.Context(), `SELECT version FROM card WHERE id=$1`, study.ID).Scan(&afterVersion); err != nil {
		t.Fatal(err)
	}
	if afterVersion != beforeVersion {
		t.Fatalf("study version not rolled back: got %d want %d", afterVersion, beforeVersion)
	}
	if code := s.call(t, "GET", "/api/researches/"+project.ID, owner, ws.ID, nil, nil); code != 200 {
		t.Fatalf("project not rolled back: %d", code)
	}
}
