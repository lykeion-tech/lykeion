package handler

import (
	"sync"
	"testing"

	"github.com/google/uuid"
)

func TestManualDraftCreationIsIdempotentAndPrivate(t *testing.T) {
	s, a, b, _, ws, _ := csFixture(t)
	var draft struct {
		Chat struct {
			ID string `json:"id"`
		} `json:"chat"`
	}
	if code := s.call(t, "POST", labChats, a, ws, map[string]string{"purpose": "manual_decision_draft"}, &draft); code != 201 {
		t.Fatal(code)
	}
	body := map[string]any{"title": "Review the evidence", "context": "Private", "deadline": nil, "card_id": nil, "draft_chat_id": draft.Chat.ID}
	var first, replay manualRow
	if code := s.call(t, "POST", "/api/decisions/manual", a, ws, body, &first); code != 201 || first.ID == "" {
		t.Fatalf("first %d %+v", code, first)
	}
	if code := s.call(t, "POST", "/api/decisions/manual", a, ws, body, &replay); code != 201 || replay.ID != first.ID {
		t.Fatalf("replay %d %+v", code, replay)
	}
	if code := s.call(t, "POST", "/api/decisions/manual", b, ws, body, nil); code != 404 {
		t.Fatalf("foreign draft: %d", code)
	}
	body["title"] = "A different decision"
	if code := s.call(t, "POST", "/api/decisions/manual", a, ws, body, nil); code != 409 {
		t.Fatalf("changed replay: %d", code)
	}
	var count int
	if err := s.Env.Owner.QueryRow(t.Context(), `SELECT count(*) FROM inbox_item WHERE workspace_id=$1 AND kind='manual_decision'`, ws).Scan(&count); err != nil || count != 1 {
		t.Fatalf("count %d: %v", count, err)
	}
}

func TestManualDraftCannotBeCreatedDuringPendingTurn(t *testing.T) {
	s, a, _, _, ws, _ := csFixture(t)
	csRegister(t, s, a, ws, "draft-runtime", "claude")
	var draft struct {
		Chat struct {
			ID string `json:"id"`
		} `json:"chat"`
	}
	if code := s.call(t, "POST", labChats, a, ws, map[string]string{"purpose": "manual_decision_draft"}, &draft); code != 201 {
		t.Fatal(code)
	}
	turn := map[string]any{"user_request": "Draft", "current_draft": map[string]any{"title": "", "context": "", "deadline": nil, "card_id": nil}, "harness": "claude", "request_id": uuid.NewString()}
	if code := s.call(t, "POST", labChats+"/"+draft.Chat.ID+"/questions", a, ws, turn, nil); code != 202 {
		t.Fatal(code)
	}
	body := map[string]any{"title": "Review", "context": "", "deadline": nil, "card_id": nil, "draft_chat_id": draft.Chat.ID}
	if code := s.call(t, "POST", "/api/decisions/manual", a, ws, body, nil); code != 409 {
		t.Fatalf("create during turn: %d", code)
	}
	if _, err := s.Env.Owner.Exec(t.Context(), `UPDATE agent_run SET status='cancelled' WHERE lab_chat_id=$1`, draft.Chat.ID); err != nil {
		t.Fatal(err)
	}
	if code := s.call(t, "POST", "/api/decisions/manual", a, ws, body, nil); code != 201 {
		t.Fatalf("create after turn: %d", code)
	}
	turn["request_id"] = uuid.NewString()
	if code := s.call(t, "POST", labChats+"/"+draft.Chat.ID+"/questions", a, ws, turn, nil); code != 409 {
		t.Fatalf("new turn after creation: %d", code)
	}
}

func TestManualDraftConcurrentCreationUsesOneReceipt(t *testing.T) {
	s, a, _, _, ws, _ := csFixture(t)
	var draft struct {
		Chat struct {
			ID string `json:"id"`
		} `json:"chat"`
	}
	if code := s.call(t, "POST", labChats, a, ws, map[string]string{"purpose": "manual_decision_draft"}, &draft); code != 201 {
		t.Fatal(code)
	}
	body := map[string]any{"title": "Concurrent", "context": "", "deadline": nil, "card_id": nil, "draft_chat_id": draft.Chat.ID}
	var wg sync.WaitGroup
	results := make([]manualRow, 2)
	codes := make([]int, 2)
	for i := range results {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			codes[i] = s.call(t, "POST", "/api/decisions/manual", a, ws, body, &results[i])
		}(i)
	}
	wg.Wait()
	if codes[0] != 201 || codes[1] != 201 || results[0].ID == "" || results[0].ID != results[1].ID {
		t.Fatalf("codes %v, ids %s %s", codes, results[0].ID, results[1].ID)
	}
}
