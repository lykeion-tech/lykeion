package handler

import (
	"testing"

	"github.com/google/uuid"
	db "lykeion/server/pkg/db/generated"
)

func TestResearchCoresearchViewerAccess(t *testing.T) {
	s := newServer(t)
	s.builtIns(t)
	owner := s.signIn(t, "linked-chat-owner@lab.org")
	viewer := s.signIn(t, "linked-chat-viewer@lab.org")
	outsider := s.signIn(t, "linked-chat-outsider@lab.org")
	ws := s.workspace(t, owner, "linked-chats")
	other := s.workspace(t, outsider, "unrelated-chats")
	if _, err := s.Env.Owner.Exec(t.Context(), "INSERT INTO member(workspace_id,user_id,role) VALUES($1,$2,'member')", ws.ID, s.userID(t, viewer)); err != nil {
		t.Fatal(err)
	}
	var project struct{ ID string }
	if code := s.call(t, "POST", "/api/researches", owner, ws.ID, map[string]string{"name": "Research"}, &project); code != 201 {
		t.Fatalf("create project: %d", code)
	}
	var boot struct{ Conversation db.CoresearchConversation }
	if code := s.call(t, "POST", "/api/coresearch/bootstrap", owner, ws.ID, nil, &boot); code != 200 {
		t.Fatalf("bootstrap: %d", code)
	}
	c := boot.Conversation
	if _, err := s.Env.Owner.Exec(t.Context(), "INSERT INTO coresearch_message(workspace_id,chat_id,kind,body) VALUES($1,$2,'turn','Shared question')", ws.ID, c.ChatID); err != nil {
		t.Fatal(err)
	}
	list := "/api/researches/" + project.ID + "/coresearch"
	detail := list + "/" + c.ID.String()
	edit := "/api/coresearch/conversations/" + c.ID.String()
	if code := s.call(t, "GET", detail, viewer, ws.ID, nil, nil); code != 404 {
		t.Fatalf("unlinked viewer access: %d", code)
	}
	if code := s.call(t, "PATCH", edit, owner, ws.ID, map[string]any{"research_id": project.ID}, nil); code != 200 {
		t.Fatalf("link: %d", code)
	}
	var chats []struct {
		ID      uuid.UUID
		IsOwner bool `json:"is_owner"`
	}
	if code := s.call(t, "GET", list, viewer, ws.ID, nil, &chats); code != 200 || len(chats) != 1 || chats[0].ID != c.ID || chats[0].IsOwner {
		t.Fatalf("viewer list: %d %+v", code, chats)
	}
	if code, _ := csUpload(t, s, owner, ws.ID, c.ProjectID.String(), "results.txt", 0, false, []byte("saved result")); code != 201 {
		t.Fatalf("save artifact: %d", code)
	}
	var chat struct {
		Messages  []struct{ Body string }
		Artifacts []struct{ ID, Path string }
	}
	if code := s.call(t, "GET", detail, viewer, ws.ID, nil, &chat); code != 200 || len(chat.Messages) != 1 || chat.Messages[0].Body != "Shared question" {
		t.Fatalf("viewer transcript: %d %+v", code, chat)
	}
	if code := s.call(t, "GET", detail, outsider, other.ID, nil, nil); code != 404 {
		t.Fatalf("cross-workspace transcript: %d", code)
	}
	if code := s.call(t, "PATCH", edit, viewer, ws.ID, map[string]string{"title": "Forbidden"}, nil); code != 404 {
		t.Fatalf("viewer rename: %d", code)
	}
	if len(chat.Artifacts) != 1 || chat.Artifacts[0].Path != "results.txt" {
		t.Fatalf("shared artifacts: %+v", chat.Artifacts)
	}
	download := detail + "/artifacts/" + chat.Artifacts[0].ID
	if code := s.call(t, "GET", download, viewer, ws.ID, nil, nil); code != 200 {
		t.Fatalf("viewer download: %d", code)
	}
	if code := s.call(t, "GET", download, outsider, other.ID, nil, nil); code != 404 {
		t.Fatalf("cross-workspace download: %d", code)
	}
	original := "/api/coresearch/projects/" + c.ProjectID.String() + "/chats/" + c.ChatID.String() + "/messages"
	if code := s.call(t, "GET", original, viewer, ws.ID, nil, nil); code != 404 {
		t.Fatalf("unexpected access through backing project: %d", code)
	}
	if code := s.call(t, "PATCH", edit, owner, ws.ID, map[string]any{"research_id": nil}, nil); code != 200 {
		t.Fatalf("unlink: %d", code)
	}
	if code := s.call(t, "GET", download, viewer, ws.ID, nil, nil); code != 404 {
		t.Fatalf("viewer download after unlink: %d", code)
	}
	if code := s.call(t, "PATCH", edit, owner, ws.ID, map[string]any{"research_id": project.ID}, nil); code != 200 {
		t.Fatalf("relink: %d", code)
	}
	if code := s.call(t, "DELETE", "/api/researches/"+project.ID, owner, ws.ID, nil, nil); code != 204 {
		t.Fatalf("delete project: %d", code)
	}
	if code := s.call(t, "GET", detail, viewer, ws.ID, nil, nil); code != 404 {
		t.Fatalf("viewer access after project deletion: %d", code)
	}
	if code := s.call(t, "GET", original, owner, ws.ID, nil, nil); code != 200 {
		t.Fatalf("owner lost original chat: %d", code)
	}
}

func TestResearchCoresearchCowriteRights(t *testing.T) {
	s, owner, collaborator, viewer, ws, _ := csFixture(t)
	if _, err := s.Env.Owner.Exec(t.Context(), "INSERT INTO member(workspace_id,user_id,role) VALUES($1,$2,'member')", ws, s.userID(t, viewer)); err != nil {
		t.Fatal(err)
	}
	csRegister(t, s, owner, ws, "linked-cowrite", "claude")
	var created struct{ Conversation db.CoresearchConversation }
	in := map[string]string{"body": "Review the manuscript", "request_id": uuid.NewString(), "mode": "cowrite", "template": "blank"}
	if code := s.call(t, "POST", "/api/coresearch/conversations", owner, ws, in, &created); code != 201 {
		t.Fatalf("start Cowrite: %d", code)
	}
	c := created.Conversation
	if code := csAdd(t, s, owner, collaborator, ws, c.ProjectID.String(), "member"); code != 201 {
		t.Fatalf("add collaborator: %d", code)
	}
	edit := "/api/coresearch/conversations/" + c.ID.String()
	var result db.CoresearchConversation
	if code := s.call(t, "PATCH", edit, owner, ws, map[string]string{"new_project_name": "Manuscript review"}, &result); code != 200 || result.ResearchID == nil {
		t.Fatalf("share Cowrite: %d %+v", code, result.ResearchID)
	}
	shared := "/api/researches/" + result.ResearchID.String() + "/coresearch/" + c.ID.String()
	var chat struct {
		Mode      string
		Artifacts []struct{ ID string }
	}
	if code := s.call(t, "GET", shared, viewer, ws, nil, &chat); code != 200 || chat.Mode != "cowrite" || len(chat.Artifacts) == 0 {
		t.Fatalf("viewer access to Cowrite files: %d %+v", code, chat)
	}
	original := csProjects + "/" + c.ProjectID.String() + "/chats/" + c.ChatID.String() + "/messages"
	if code := s.call(t, "GET", original, viewer, ws, nil, nil); code != 404 {
		t.Fatalf("project viewer became a Cowrite collaborator: %d", code)
	}
	if code := s.call(t, "PATCH", edit, owner, ws, map[string]any{"research_id": nil}, nil); code != 200 {
		t.Fatalf("unshare Cowrite: %d", code)
	}
	if code := s.call(t, "GET", shared, viewer, ws, nil, nil); code != 404 {
		t.Fatalf("viewer retained revoked access: %d", code)
	}
	if code := s.call(t, "GET", original, collaborator, ws, nil, nil); code != 200 {
		t.Fatalf("existing Cowrite collaborator lost access: %d", code)
	}
}
