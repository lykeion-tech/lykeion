package handler

import (
	"testing"

	"github.com/google/uuid"
	"lykeion/server/internal/service"
)

func TestDecisionDraftBindsToSelectedMachine(t *testing.T) {
	s, a, b, outsider, ws, _ := csFixture(t)
	first := csRegister(t, s, a, ws, "builder-one", "claude")
	second := csRegister(t, s, a, ws, "builder-two", "claude")
	foreign := csRegister(t, s, b, ws, "builder-other", "claude")
	create := func(runtime string, out any) int {
		return s.call(t, "POST", labChats, a, ws, map[string]any{"purpose": "manual_decision_draft", "runtime_id": runtime}, out)
	}
	if code := create(foreign.RuntimeID.String(), nil); code != 404 {
		t.Fatalf("foreign runtime: %d", code)
	}
	if _, err := s.Env.Owner.Exec(t.Context(), `UPDATE runtime SET unhealthy_reason='sign_in' WHERE id=$1`, first.RuntimeID); err != nil {
		t.Fatal(err)
	}
	if code := create(first.RuntimeID.String(), nil); code != 409 {
		t.Fatalf("unhealthy runtime: %d", code)
	}
	if _, err := s.Env.Owner.Exec(t.Context(), `UPDATE runtime SET unhealthy_reason=NULL WHERE id=$1`, first.RuntimeID); err != nil {
		t.Fatal(err)
	}
	var out struct {
		Chat struct {
			ID string `json:"id"`
		} `json:"chat"`
	}
	if code := create(first.RuntimeID.String(), &out); code != 201 || out.Chat.ID == "" {
		t.Fatalf("create: %d %+v", code, out)
	}
	path := labChats + "/" + out.Chat.ID
	var stored struct {
		RuntimeID string `json:"runtime_id"`
	}
	if code := s.call(t, "GET", path+"/draft", a, ws, nil, &stored); code != 200 || stored.RuntimeID != first.RuntimeID.String() {
		t.Fatalf("read binding: %d %+v", code, stored)
	}
	if code := s.call(t, "GET", path+"/draft", b, ws, nil, nil); code != 404 {
		t.Fatalf("foreign read: %d", code)
	}
	otherWorkspace := s.workspace(t, outsider, "builder-other-workspace").ID
	if code := s.call(t, "GET", path+"/draft", outsider, otherWorkspace, nil, nil); code != 404 {
		t.Fatalf("cross-workspace read: %d", code)
	}
	turn := map[string]any{"user_request": "Draft this", "current_draft": map[string]any{"title": "", "context": "", "deadline": nil, "card_id": nil}, "harness": "claude", "request_id": uuid.NewString()}
	if _, err := s.Env.Owner.Exec(t.Context(), `UPDATE runtime SET status='offline' WHERE id=$1`, first.RuntimeID); err != nil {
		t.Fatal(err)
	}
	if code := s.call(t, "POST", path+"/questions", a, ws, turn, nil); code != 409 {
		t.Fatalf("selected machine offline with another online: %d", code)
	}
	if _, err := s.Env.Owner.Exec(t.Context(), `UPDATE runtime SET status='online' WHERE id=$1`, first.RuntimeID); err != nil {
		t.Fatal(err)
	}
	if code := s.call(t, "POST", path+"/questions", a, ws, turn, nil); code != 202 {
		t.Fatalf("ask: %d", code)
	}
	if _, err := s.Env.Owner.Exec(t.Context(), `UPDATE agent_run SET status='dispatched', runtime_id=$2 WHERE lab_chat_id=$1`, out.Chat.ID, second.RuntimeID); err == nil {
		t.Fatal("database allowed a different machine to dispatch the turn")
	}
	who := uuid.MustParse(s.userID(t, a))
	claimed, err := s.H.AgentRuns.Claim(t.Context(), who, []service.RuntimeRef{second}, 1)
	if err != nil || len(claimed) != 0 {
		t.Fatalf("other machine claimed: %d %v", len(claimed), err)
	}
	claimed, err = s.H.AgentRuns.Claim(t.Context(), who, []service.RuntimeRef{first}, 1)
	if err != nil || len(claimed) != 1 {
		t.Fatalf("selected machine claim: %d %v", len(claimed), err)
	}
}
