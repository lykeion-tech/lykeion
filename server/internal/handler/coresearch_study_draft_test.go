package handler

import (
	"testing"

	"github.com/google/uuid"

	db "lykeion/server/pkg/db/generated"
)

func TestCoresearchStudyDraftReviewAndRetry(t *testing.T) {
	s, owner, colleague, _, ws, _ := csFixture(t)
	csRegister(t, s, owner, ws, "aristotle-study", "claude")
	agent := s.stagedAgent(t, owner, ws, "grounding")
	var accepted struct {
		Conversation db.CoresearchConversation
		Run          db.AgentRun
	}
	send := "/api/coresearch/conversations"
	type turn struct{ trigger, message string }
	turns := []turn{
		{"offer", "I am curious about street heat."},
		{"request", "Can you draft a study on matched streets?"},
		{"offer", "Compare daytime measurements in one city."},
	}
	var replies []uuid.UUID
	for _, tt := range turns {
		if code := s.call(t, "POST", send, owner, ws, map[string]string{"body": tt.message, "request_id": uuid.NewString()}, &accepted); code != 201 {
			t.Fatalf("accept %d", code)
		}
		ctx := t.Context()
		if _, err := s.Env.Owner.Exec(ctx, "UPDATE agent_run SET status='completed' WHERE id=$1", accepted.Run.ID); err != nil {
			t.Fatal(err)
		}
		body := "The discussion has a practical first step.\n\n```study-draft\n" +
			`{"trigger":"` + tt.trigger + `","title":"Street heat","description":"Compare matched streets","stage":"grounding"}` + "\n```"
		var id uuid.UUID
		if err := s.Env.Owner.QueryRow(ctx, "INSERT INTO coresearch_message (workspace_id,chat_id,kind,agent_run_id,body) VALUES ($1,$2,'reply',$3,$4) RETURNING id",
			ws, accepted.Conversation.ChatID, accepted.Run.ID, body).Scan(&id); err != nil {
			t.Fatal(err)
		}
		replies = append(replies, id)
		send = "/api/coresearch/conversations/" + accepted.Conversation.ID.String() + "/turns"
	}
	chat := accepted.Conversation
	messages := csProjects + "/" + chat.ProjectID.String() + "/chats/" + chat.ChatID.String() + "/messages"
	var rows []coresearchMessageView
	if code := s.call(t, "GET", messages, owner, ws, nil, &rows); code != 200 {
		t.Fatal(code)
	}
	shown := make(map[uuid.UUID]bool)
	for _, row := range rows {
		shown[row.ID] = row.StudyDraft != nil
	}
	if shown[replies[0]] || !shown[replies[1]] || !shown[replies[2]] {
		t.Fatalf("wrong draft visibility: %v", shown)
	}
	var mobile []db.ListCoresearchMessagesRow
	if code := s.call(t, "GET", "/api/mobile/coresearch/"+chat.ID.String()+"/messages", owner, ws, nil, &mobile); code != 200 {
		t.Fatal(code)
	}
	var mobileReply string
	for _, row := range mobile {
		if row.ID == replies[1] {
			mobileReply = row.Body
		}
	}
	if mobileReply != "The discussion has a practical first step.\n\nStudy draft: Compare matched streets" {
		t.Fatalf("wrong mobile draft projection: %q", mobileReply)
	}
	var conversations []db.ListCoresearchConversationsRow
	if code := s.call(t, "GET", "/api/coresearch/conversations", owner, ws, nil, &conversations); code != 200 ||
		len(conversations) == 0 || conversations[0].Preview != "The discussion has a practical first step." {
		t.Fatalf("wrong conversation preview (%d): %+v", code, conversations)
	}
	id := replies[1]
	input := map[string]any{
		"revision": 0, "operation_id": id, "title": "Reviewed street heat",
		"description": "Compare matched streets during the day.", "stage": "grounding", "priority": "high",
		"assignee":   map[string]string{"type": "agent", "agent_id": agent},
		"coresearch": coresearchDraftSource{ProjectID: chat.ProjectID, ChatID: chat.ChatID},
	}
	path := "/api/study-proposals/" + id.String() + "/start"
	if code := s.call(t, "POST", path, colleague, ws, input, nil); code == 200 {
		t.Fatal("another workspace member started this private draft")
	}
	var first, retry proposalReceipt
	if code := s.call(t, "POST", path, owner, ws, input, &first); code != 200 || first.CardID == uuid.Nil {
		t.Fatalf("start %d: %+v", code, first)
	}
	rows = nil
	if code := s.call(t, "GET", messages, owner, ws, nil, &rows); code != 200 {
		t.Fatal(code)
	}
	var visible bool
	for _, row := range rows {
		if row.ID == id && row.StudyID != nil && *row.StudyID == first.CardID {
			visible = true
		}
	}
	if !visible {
		t.Fatal("created study did not appear on the chat draft")
	}
	if code := s.call(t, "DELETE", "/api/cards/"+first.CardID.String(), owner, ws, nil, nil); code != 204 {
		t.Fatalf("delete study: %d", code)
	}
	rows = nil
	if code := s.call(t, "GET", messages, owner, ws, nil, &rows); code != 200 {
		t.Fatal(code)
	}
	visible = false
	for _, row := range rows {
		if row.ID == id && row.StudyDeleted && row.StudyID == nil {
			visible = true
		}
	}
	if !visible {
		t.Fatal("chat draft did not show that its study was deleted")
	}
	if _, err := s.Env.Owner.Exec(t.Context(), "UPDATE coresearch_project SET archived_at=now() WHERE id=$1", chat.ProjectID); err != nil {
		t.Fatal(err)
	}
	input["title"] = "An old copy of the draft"
	if code := s.call(t, "POST", path, owner, ws, input, &retry); code != 200 || retry != first {
		t.Fatalf("retry after archive %d: %+v", code, retry)
	}
	input["operation_id"] = replies[2]
	path = "/api/study-proposals/" + replies[2].String() + "/start"
	if code := s.call(t, "POST", path, owner, ws, input, nil); code != 409 {
		t.Fatalf("started a new study from an archived project: %d", code)
	}
}
