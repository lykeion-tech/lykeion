package handler

import (
	"context"
	"encoding/json"
	"github.com/google/uuid"
	db "lykeion/server/pkg/db/generated"
	"testing"
	"time"
)

func TestManualDraftOfflineAndReplay(t *testing.T) {
	s, a, b, _, ws, _ := csFixture(t)
	var out struct {
		Chat struct {
			ID      string
			Purpose string
		}
	}
	if code := s.call(t, "POST", labChats, a, ws, map[string]string{"purpose": "manual_decision_draft"}, &out); code != 201 || out.Chat.Purpose != "manual_decision_draft" {
		t.Fatalf("create draft: %d %+v", code, out)
	}
	path := labChats + "/" + out.Chat.ID + "/draft"
	draft := map[string]any{"title": "", "context": "unfinished private text", "deadline": nil, "card_id": nil}
	if code := s.call(t, "PUT", path, a, ws, draft, nil); code != 200 {
		t.Fatalf("offline save: %d", code)
	}
	var got map[string]any
	if code := s.call(t, "GET", path, a, ws, nil, &got); code != 200 {
		t.Fatal(code)
	}
	if got["draft"].(map[string]any)["context"] != "unfinished private text" {
		t.Fatal(got)
	}
	if code := s.call(t, "GET", path, b, ws, nil, nil); code != 404 {
		t.Fatalf("foreign draft: %d", code)
	}
	input := map[string]any{"user_request": "Refine this", "current_draft": draft, "harness": "claude", "request_id": uuid.NewString()}
	if code := s.call(t, "POST", labChats+"/"+out.Chat.ID+"/questions", a, ws, input, nil); code != 409 {
		t.Fatalf("offline ask: %d", code)
	}
	var count int
	if err := s.Env.Owner.QueryRow(t.Context(), "SELECT count(*) FROM agent_run WHERE lab_chat_id=$1", out.Chat.ID).Scan(&count); err != nil || count != 0 {
		t.Fatal(err, count)
	}
	csRegister(t, s, a, ws, "own-draft", "claude")
	for range 2 {
		if code := s.call(t, "POST", labChats+"/"+out.Chat.ID+"/questions", a, ws, input, nil); code != 202 {
			t.Fatalf("online/replay: %d", code)
		}
	}
	if err := s.Env.Owner.QueryRow(t.Context(), "SELECT count(*) FROM agent_run WHERE lab_chat_id=$1", out.Chat.ID).Scan(&count); err != nil || count != 1 {
		t.Fatal(err, count)
	}
}

func TestManualDraftDeletionRace(t *testing.T) {
	s, a, _, _, ws, _ := csFixture(t)
	var out struct{ Chat struct{ ID string } }
	if code := s.call(t, "POST", labChats, a, ws, map[string]string{"purpose": "manual_decision_draft"}, &out); code != 201 {
		t.Fatal(code)
	}
	path := labChats + "/" + out.Chat.ID
	draft := map[string]any{"title": "private", "context": "", "deadline": nil, "card_id": nil}
	if code := s.call(t, "PUT", path+"/draft", a, ws, draft, nil); code != 200 {
		t.Fatal(code)
	}
	if code := s.call(t, "DELETE", path, a, ws, nil, nil); code != 204 {
		t.Fatal(code)
	}
	if code := s.call(t, "PUT", path+"/draft", a, ws, draft, nil); code != 404 {
		t.Fatal(code)
	}
	var retained bool
	if err := s.Env.Owner.QueryRow(t.Context(), "SELECT draft IS NOT NULL FROM lab_chat WHERE id=$1", out.Chat.ID).Scan(&retained); err != nil || retained {
		t.Fatal(err, retained)
	}
}

func TestManualDraftListRechecksStudyAccess(t *testing.T) {
	s, a, _, _, ws, _ := csFixture(t)
	var out struct{ Chat struct{ ID string } }
	if code := s.call(t, "POST", labChats, a, ws, map[string]string{"purpose": "manual_decision_draft"}, &out); code != 201 {
		t.Fatal(code)
	}
	// A lost or physically deleted link must not publish saved form text.
	if _, err := s.Env.Owner.Exec(t.Context(), `UPDATE lab_chat SET draft=jsonb_build_object('title','private linked text','context','','deadline',NULL,'card_id',$2::text) WHERE id=$1`, out.Chat.ID, uuid.NewString()); err != nil {
		t.Fatal(err)
	}
	var list struct{ Chats []map[string]any }
	if code := s.call(t, "GET", labChats+"?purpose=manual_decision_draft", a, ws, nil, &list); code != 200 || len(list.Chats) != 0 {
		t.Fatalf("hidden draft leaked: %d %+v", code, list)
	}
}

func TestManualDraftSchemasForBothCLIs(t *testing.T) {
	for _, cli := range []string{"claude", "codex"} {
		t.Run(cli, func(t *testing.T) {
			s, a, _, _, ws, _ := csFixture(t)
			rt := csRegister(t, s, a, ws, "draft-own", cli)
			var out struct{ Chat struct{ ID string } }
			if code := s.call(t, "POST", labChats, a, ws, map[string]string{"purpose": "manual_decision_draft"}, &out); code != 201 {
				t.Fatal(code)
			}
			input := map[string]any{"user_request": "Draft", "current_draft": map[string]any{"title": "", "context": "", "deadline": nil, "card_id": nil}, "harness": cli, "request_id": uuid.NewString()}
			if code := s.call(t, "POST", labChats+"/"+out.Chat.ID+"/questions", a, ws, input, nil); code != 202 {
				t.Fatal(code)
			}
			var claims []struct {
				OutputSchema string `json:"output_schema"`
			}
			if code := s.call(t, "POST", "/api/daemon/agent-runs/claim", s.personalToken(t, a), "", map[string]any{"runtimes": []any{rt}, "slots": 1}, &claims); code != 200 || len(claims) != 1 {
				t.Fatal(code, claims)
			}
			var schema struct{ Properties map[string]any }
			if err := json.Unmarshal([]byte(claims[0].OutputSchema), &schema); err != nil {
				t.Fatal(err)
			}
			if len(schema.Properties) != 3 || schema.Properties["draft"] == nil || schema.Properties["message"] == nil {
				t.Fatal(schema)
			}
		})
	}
}

func TestManualDraftConcurrentAutosaveDelete(t *testing.T) {
	s, a, _, _, ws, _ := csFixture(t)
	var out struct{ Chat struct{ ID string } }
	if code := s.call(t, "POST", labChats, a, ws, map[string]string{"purpose": "manual_decision_draft"}, &out); code != 201 {
		t.Fatal(code)
	}
	tx, err := s.Env.Owner.Begin(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback(t.Context())
	if _, err = tx.Exec(t.Context(), "UPDATE lab_chat SET deleted_at=now() WHERE id=$1", out.Chat.ID); err != nil {
		t.Fatal(err)
	}
	done := make(chan int, 1)
	go func() {
		done <- s.call(t, "PUT", labChats+"/"+out.Chat.ID+"/draft", a, ws, map[string]any{"title": "late private", "context": "", "deadline": nil, "card_id": nil}, nil)
	}()
	ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
	defer cancel()
	for {
		var blocked bool
		err = s.Env.Owner.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM pg_stat_activity WHERE datname=current_database() AND $1::int=ANY(pg_blocking_pids(pid)))`, tx.Conn().PgConn().PID()).Scan(&blocked)
		if err != nil {
			t.Fatal(err)
		}
		if blocked {
			break
		}
		select {
		case code := <-done:
			t.Fatalf("save did not wait for delete: %d", code)
		case <-ctx.Done():
			t.Fatal(ctx.Err())
		case <-time.After(time.Millisecond):
		}
	}
	if err = tx.Commit(t.Context()); err != nil {
		t.Fatal(err)
	}
	if code := <-done; code != 404 {
		t.Fatal(code)
	}
	var retained bool
	if err = s.Env.Owner.QueryRow(t.Context(), "SELECT draft IS NOT NULL FROM lab_chat WHERE id=$1", out.Chat.ID).Scan(&retained); err != nil || retained {
		t.Fatal(err, retained)
	}
}

func TestManualDraftQuestionDisplayIsolation(t *testing.T) {
	raw := `{"user_request":"literal JSON question","current_draft":{"title":"x","context":"","deadline":null,"card_id":null}}`
	msg := labPlainMessage(db.LabMessage{Kind: "question", Body: raw})
	if msg.Body != raw {
		t.Fatal("ordinary Aristotle question was interpreted as a draft envelope")
	}
}

func TestManualDraftMessageRunLink(t *testing.T) {
	run := uuid.New()
	for _, kind := range []string{"question", "answer"} {
		row := db.LabMessage{ID: uuid.New(), Kind: kind, AgentRunID: &run}
		raw, err := json.Marshal(labDraftMessage(row))
		if err != nil {
			t.Fatal(err)
		}
		var got map[string]any
		if err = json.Unmarshal(raw, &got); err != nil {
			t.Fatal(err)
		}
		if got["agent_run_id"] != run.String() {
			t.Fatalf("missing authoritative run link: %s", raw)
		}
		raw, _ = json.Marshal(labPlainMessage(row))
		if err = json.Unmarshal(raw, &got); err != nil {
			t.Fatal(err)
		}
		var plain map[string]any
		json.Unmarshal(raw, &plain)
		if _, exists := plain["agent_run_id"]; exists {
			t.Fatal("ordinary response changed")
		}
	}
}
