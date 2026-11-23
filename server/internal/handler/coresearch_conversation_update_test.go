package handler

import (
	"testing"

	"github.com/google/uuid"
)

func TestCoresearchConversationOwnerProjectLifecycle(t *testing.T) {
	s, owner, member, _, ws, _ := csFixture(t)
	var boot struct{ Conversation struct{ ID string } }
	if code := s.call(t, "POST", "/api/coresearch/bootstrap", owner, ws, nil, &boot); code != 200 {
		t.Fatalf("create welcome conversation: %d", code)
	}
	path := "/api/coresearch/conversations/" + boot.Conversation.ID
	var updated struct {
		ID         string  `json:"id"`
		Title      string  `json:"title"`
		ResearchID *string `json:"research_id"`
		PinnedAt   *string `json:"pinned_at"`
		ArchivedAt *string `json:"archived_at"`
	}
	change := map[string]any{"title": "My analysis", "new_project_name": "Linked experiments", "pinned": true, "archived": true}
	if code := s.call(t, "PATCH", path, owner, ws, change, &updated); code != 200 || updated.ID != boot.Conversation.ID || updated.Title != "My analysis" || updated.ResearchID == nil || updated.PinnedAt == nil || updated.ArchivedAt == nil {
		t.Fatalf("create and link: %d %+v", code, updated)
	}
	research := *updated.ResearchID
	if code := s.call(t, "PATCH", path, member, ws, map[string]any{"research_id": nil}, nil); code != 404 {
		t.Fatalf("nonowner unlinked conversation: %d", code)
	}
	otherWS := s.workspace(t, owner, "other-coresearch-links").ID
	var other struct{ ID string }
	if code := s.call(t, "POST", "/api/researches", owner, otherWS, map[string]string{"name": "Other project"}, &other); code != 201 {
		t.Fatalf("create cross-workspace project: %d", code)
	}
	if code := s.call(t, "PATCH", path, owner, ws, map[string]string{"research_id": other.ID}, nil); code != 404 {
		t.Fatalf("linked cross-workspace project: %d", code)
	}
	if code := s.call(t, "DELETE", "/api/researches/"+research, owner, ws, nil, nil); code != 204 {
		t.Fatalf("delete workspace project: %d", code)
	}
	if code := s.call(t, "PATCH", path, owner, ws, map[string]bool{"pinned": false, "archived": false}, &updated); code != 200 || updated.ID != boot.Conversation.ID || updated.ResearchID != nil || updated.PinnedAt != nil || updated.ArchivedAt != nil {
		t.Fatalf("conversation survives project deletion: %d %+v", code, updated)
	}
	if code := s.call(t, "PATCH", path, owner, ws, map[string]string{"new_project_name": "Manuscript"}, &updated); code != 200 || updated.ResearchID == nil {
		t.Fatalf("link another project: %d %+v", code, updated)
	}
	research = *updated.ResearchID
	csRegister(t, s, owner, ws, "linked-welcome", "claude")
	started := struct {
		Conversation struct {
			ID         string  `json:"id"`
			Title      string  `json:"title"`
			ResearchID *string `json:"research_id"`
		}
	}{}
	turn := map[string]string{"body": "First drafting turn", "request_id": uuid.NewString(), "mode": "cowrite", "template": "blank"}
	if code := s.call(t, "POST", path+"/turns", owner, ws, turn, &started); code != 201 || started.Conversation.ID != boot.Conversation.ID || started.Conversation.Title != "My analysis" || started.Conversation.ResearchID == nil || *started.Conversation.ResearchID != research {
		t.Fatalf("welcome conversion lost managed metadata: %d %+v", code, started)
	}
}

func TestCoresearchConversationConcurrentProjectDelete(t *testing.T) {
	s, owner, _, _, ws, _ := csFixture(t)
	var boot struct{ Conversation struct{ ID string } }
	if code := s.call(t, "POST", "/api/coresearch/bootstrap", owner, ws, nil, &boot); code != 200 {
		t.Fatalf("create welcome conversation: %d", code)
	}
	var research struct{ ID string }
	if code := s.call(t, "POST", "/api/researches", owner, ws, map[string]string{"name": "Temporary project"}, &research); code != 201 {
		t.Fatalf("create research project: %d", code)
	}
	path := "/api/coresearch/conversations/" + boot.Conversation.ID
	linked, deleted := make(chan int, 1), make(chan int, 1)
	go func() {
		defer close(linked)
		linked <- s.call(t, "PATCH", path, owner, ws, map[string]string{"research_id": research.ID}, nil)
	}()
	go func() {
		defer close(deleted)
		deleted <- s.call(t, "DELETE", "/api/researches/"+research.ID, owner, ws, nil, nil)
	}()
	if code := <-linked; code != 200 && code != 404 {
		t.Fatalf("link concurrent with project deletion: %d", code)
	}
	if code := <-deleted; code != 204 {
		t.Fatalf("delete project concurrent with link: %d", code)
	}
	var rows []struct {
		ID         string  `json:"id"`
		ResearchID *string `json:"research_id"`
	}
	if code := s.call(t, "GET", "/api/coresearch/conversations", owner, ws, nil, &rows); code != 200 || len(rows) != 1 || rows[0].ID != boot.Conversation.ID || rows[0].ResearchID != nil {
		t.Fatalf("project deletion left dangling or lost conversation: %d %+v", code, rows)
	}
}
