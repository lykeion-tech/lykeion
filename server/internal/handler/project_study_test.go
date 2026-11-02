package handler

import (
	"testing"
	"time"

	"github.com/google/uuid"
)

type projectStudy struct {
	ID         string     `json:"id"`
	ResearchID *string    `json:"research_id"`
	StartDate  *time.Time `json:"start_date"`
}

func TestCreateStudyInProject(t *testing.T) {
	s := newServer(t)
	s.builtIns(t)
	token := s.signIn(t, "project-studies@lab.org")
	workspace := s.workspace(t, token, "project-studies")
	var project struct {
		ID string `json:"id"`
	}
	if code := s.call(t, "POST", "/api/researches", token, workspace.ID, map[string]any{"name": "Linked studies"}, &project); code != 201 {
		t.Fatalf("create project: %d", code)
	}
	var created projectStudy
	if code := s.call(t, "POST", "/api/cards", token, workspace.ID, map[string]any{"question": "What evidence belongs in this project?", "research_id": project.ID, "start_date": "2026-11-03T00:00:00Z"}, &created); code != 201 {
		t.Fatalf("create project study: %d", code)
	}
	if created.ResearchID == nil || *created.ResearchID != project.ID {
		t.Fatalf("create returned an unlinked study: %+v", created)
	}
	if created.StartDate == nil || created.StartDate.Format("2006-01-02") != "2026-11-03" {
		t.Fatalf("create returned wrong start date: %+v", created.StartDate)
	}
	var stored projectStudy
	if code := s.call(t, "GET", "/api/cards/"+created.ID, token, workspace.ID, nil, &stored); code != 200 || stored.ResearchID == nil || *stored.ResearchID != project.ID {
		t.Fatalf("stored link: %d %+v", code, stored)
	}
	if stored.StartDate == nil || stored.StartDate.Format("2006-01-02") != "2026-11-03" {
		t.Fatalf("stored start date: %+v", stored.StartDate)
	}
	var listed []projectStudy
	if code := s.call(t, "GET", "/api/cards", token, workspace.ID, nil, &listed); code != 200 || len(listed) != 1 || listed[0].StartDate == nil || listed[0].StartDate.Format("2006-01-02") != "2026-11-03" {
		t.Fatalf("listed start date: %d %+v", code, listed)
	}
	var detail struct {
		Studies []projectStudy `json:"studies"`
	}
	if code := s.call(t, "GET", "/api/researches/"+project.ID, token, workspace.ID, nil, &detail); code != 200 || len(detail.Studies) != 1 || detail.Studies[0].ID != created.ID {
		t.Fatalf("project studies: %d %+v", code, detail)
	}
	if n := s.count(t, "SELECT count(*) FROM event WHERE card_id=$1 AND type='card.created'", created.ID); n != 1 {
		t.Fatalf("created events: %d", n)
	}
	if n := s.count(t, "SELECT count(*) FROM event WHERE card_id=$1 AND type='card.properties_updated'", created.ID); n != 0 {
		t.Fatalf("project link required a later property update: %d", n)
	}
	for _, input := range []map[string]any{
		{"question": "An independent study without a project"},
		{"question": "An independent study with explicit null", "research_id": nil},
	} {
		var independent projectStudy
		if code := s.call(t, "POST", "/api/cards", token, workspace.ID, input, &independent); code != 201 || independent.ResearchID != nil {
			t.Fatalf("independent create: %d %+v", code, independent)
		}
	}
}

func TestCreateStudyRejectsUnknownAndForeignProjectsAtomically(t *testing.T) {
	s := newServer(t)
	s.builtIns(t)
	token := s.signIn(t, "project-studies@lab.org")
	workspace := s.workspace(t, token, "project-studies")
	other := s.workspace(t, token, "other-project-studies")
	var project struct {
		ID string `json:"id"`
	}
	if code := s.call(t, "POST", "/api/researches", token, other.ID, map[string]any{"name": "Other workspace"}, &project); code != 201 {
		t.Fatalf("create foreign project: %d", code)
	}
	cardsBefore, eventsBefore := s.count(t, "SELECT count(*) FROM card"), s.count(t, "SELECT count(*) FROM event")
	for _, id := range []string{uuid.NewString(), project.ID, "not-a-uuid"} {
		if code := s.call(t, "POST", "/api/cards", token, workspace.ID, map[string]any{"question": "Must not create an unlinked study", "research_id": id}, nil); code != 400 {
			t.Fatalf("accepted invalid project %q: %d", id, code)
		}
		if cards := s.count(t, "SELECT count(*) FROM card"); cards != cardsBefore {
			t.Fatalf("invalid project created a card: before=%d after=%d", cardsBefore, cards)
		}
		if events := s.count(t, "SELECT count(*) FROM event"); events != eventsBefore {
			t.Fatalf("invalid project emitted an event: before=%d after=%d", eventsBefore, events)
		}
	}
}

func TestCreateProjectStudyRollsBackWhenItsEventFails(t *testing.T) {
	s := newServer(t)
	s.builtIns(t)
	token := s.signIn(t, "project-studies@lab.org")
	workspace := s.workspace(t, token, "project-studies")
	var project struct {
		ID string `json:"id"`
	}
	if code := s.call(t, "POST", "/api/researches", token, workspace.ID, map[string]any{"name": "Atomic studies"}, &project); code != 201 {
		t.Fatalf("create project: %d", code)
	}
	if _, err := s.Env.Owner.Exec(t.Context(), "ALTER TABLE event ADD CONSTRAINT refuse_project_study_event CHECK (type <> 'card.created')"); err != nil {
		t.Fatal(err)
	}
	cardsBefore, eventsBefore := s.count(t, "SELECT count(*) FROM card"), s.count(t, "SELECT count(*) FROM event")
	if code := s.call(t, "POST", "/api/cards", token, workspace.ID, map[string]any{"question": "This linked study must roll back", "research_id": project.ID}, nil); code != 500 {
		t.Fatalf("event failure: %d", code)
	}
	if s.count(t, "SELECT count(*) FROM card") != cardsBefore || s.count(t, "SELECT count(*) FROM event") != eventsBefore {
		t.Fatal("failed creation left a card or event behind")
	}
}
