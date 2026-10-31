package handler

import (
	"github.com/google/uuid"
	db "lykeion/server/pkg/db/generated"
	"strings"
	"testing"
)

func TestCoresearchSelectedAgent(t *testing.T) {
	s, a, b, _, ws, _ := csFixture(t)
	runtime := csRegister(t, s, a, ws, "selected-agent", "claude")
	var selected db.Agent
	if code := s.call(t, "POST", "/api/agents", a, ws, map[string]string{"name": "Selected", "harness": "claude", "model": "selected-model", "instructions": "Selected instructions", "visibility": "private"}, &selected); code != 201 {
		t.Fatalf("agent %d", code)
	}
	in := map[string]string{"body": "Hello", "request_id": uuid.NewString(), "agent_id": selected.ID.String()}
	var out struct {
		Conversation db.CoresearchConversation
		Run          db.AgentRun
	}
	if code := s.call(t, "POST", "/api/coresearch/conversations", a, ws, in, &out); code != 201 {
		t.Fatalf("start %d", code)
	}
	if out.Run.AgentID == nil || *out.Run.AgentID != selected.ID {
		t.Fatal("run missing selected agent attribution")
	}
	if code := s.call(t, "DELETE", "/api/agents/"+selected.ID.String(), a, ws, nil, nil); code != 409 {
		t.Fatalf("delete running agent: %d", code)
	}
	if out.Conversation.AgentID != selected.ID || out.Conversation.SnapshotModel == nil || *out.Conversation.SnapshotModel != "selected-model" || !strings.Contains(out.Run.Prompt, "Selected instructions") {
		t.Fatalf("wrong agent: %+v %+v", out.Conversation, out.Run)
	}
	if _, err := s.Env.Owner.Exec(t.Context(), `UPDATE runtime SET models='[{"value":"selected-model"}]'::jsonb WHERE id=$1`, runtime.RuntimeID); err != nil {
		t.Fatal(err)
	}
	claims := csClaim(t, s, a, runtime)
	if len(claims) != 1 || claims[0].AgentRun.RequestedModel != "selected-model" || !strings.Contains(claims[0].AgentRun.Prompt, "Selected instructions") {
		t.Fatalf("claim did not use selected agent: %+v", claims)
	}
	in["request_id"] = uuid.NewString()
	if code := s.call(t, "POST", "/api/coresearch/conversations", b, ws, in, nil); code != 404 {
		t.Fatalf("private agent accessible: %d", code)
	}
	if code := s.call(t, "POST", "/api/coresearch/conversations/"+out.Conversation.ID.String()+"/turns", a, ws, in, nil); code != 422 {
		t.Fatalf("changing established agent: %d", code)
	}
}

func TestCoresearchSelectedAgentSharingAndReceipt(t *testing.T) {
	s, a, b, _, ws, _ := csFixture(t)
	csRegister(t, s, b, ws, "shared-recipient", "claude")
	var shared db.Agent
	if code := s.call(t, "POST", "/api/agents", a, ws, map[string]string{"name": "Shared agent", "harness": "claude", "instructions": "shared instructions", "visibility": "workspace"}, &shared); code != 201 {
		t.Fatal(code)
	}
	input := map[string]string{"body": "Research", "request_id": uuid.NewString(), "agent_id": shared.ID.String()}
	var first, replay struct {
		Conversation db.CoresearchConversation
		Run          db.AgentRun
	}
	if code := s.call(t, "POST", "/api/coresearch/conversations", b, ws, input, &first); code != 201 {
		t.Fatalf("shared agent: %d", code)
	}
	if code := s.call(t, "POST", "/api/coresearch/conversations", b, ws, input, &replay); code != 201 || replay.Run.ID != first.Run.ID {
		t.Fatalf("retry: %d", code)
	}
	changed := map[string]string{"body": "Research", "request_id": input["request_id"], "agent_id": uuid.NewString()}
	if code := s.call(t, "POST", "/api/coresearch/conversations", b, ws, changed, nil); code != 409 {
		t.Fatalf("changed agent in retry: %d", code)
	}
	var archived db.Agent
	if code := s.call(t, "POST", "/api/agents", b, ws, map[string]string{"name": "Archived agent", "harness": "claude"}, &archived); code != 201 {
		t.Fatal(code)
	}
	if code := s.call(t, "POST", "/api/agents/"+archived.ID.String()+"/archive", b, ws, nil, nil); code != 200 {
		t.Fatal(code)
	}
	if code := s.call(t, "POST", "/api/coresearch/conversations", b, ws, map[string]string{"body": "Research", "request_id": uuid.NewString(), "agent_id": archived.ID.String()}, nil); code != 404 {
		t.Fatalf("archived agent: %d", code)
	}
}
