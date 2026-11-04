package handler

import (
	"context"
	"encoding/json"
	"github.com/google/uuid"
	"testing"
	"time"
)

func TestManualDraftArchivePreservesReadOnlyHistory(t *testing.T) {
	f := draftForCancel(t, "queued")
	var before string
	if e := f.s.Env.Owner.QueryRow(t.Context(), "SELECT draft::text FROM lab_chat WHERE id=$1", f.chat).Scan(&before); e != nil {
		t.Fatal(e)
	}
	for range 2 {
		if code := f.s.call(t, "POST", labChats+"/"+f.chat+"/archive", f.a, f.ws, nil, nil); code != 204 {
			t.Fatalf("archive: %d", code)
		}
	}
	var draft struct {
		Draft      json.RawMessage
		ArchivedAt *time.Time `json:"archived_at"`
	}
	if code := f.s.call(t, "GET", labChats+"/"+f.chat+"/draft", f.a, f.ws, nil, &draft); code != 200 || draft.ArchivedAt == nil {
		t.Fatal(code, draft)
	}
	var history struct{ Messages []struct{ ID, Body string } }
	if code := f.s.call(t, "GET", labChats+"/"+f.chat+"/messages", f.a, f.ws, nil, &history); code != 200 || len(history.Messages) != 1 || history.Messages[0].ID != f.question {
		t.Fatal(code, history)
	}
	var list struct{ Chats []struct{ ID string } }
	if code := f.s.call(t, "GET", labChats+"?purpose=manual_decision_draft", f.a, f.ws, nil, &list); code != 200 || len(list.Chats) != 0 {
		t.Fatal(code, list)
	}
	if code := f.s.call(t, "GET", labChats+"?purpose=manual_decision_draft&archived=true", f.a, f.ws, nil, &list); code != 200 || len(list.Chats) != 1 || list.Chats[0].ID != f.chat {
		t.Fatal(code, list)
	}
	body := map[string]any{"title": "must not save", "context": "", "deadline": nil, "card_id": nil}
	if code := f.s.call(t, "PUT", labChats+"/"+f.chat+"/draft", f.a, f.ws, body, nil); code != 400 {
		t.Fatal(code)
	}
	ask := map[string]any{"user_request": "must not queue", "current_draft": body, "harness": "claude", "request_id": uuid.NewString()}
	if code := f.s.call(t, "POST", labChats+"/"+f.chat+"/questions", f.a, f.ws, ask, nil); code != 400 {
		t.Fatal(code)
	}
	if code := f.s.call(t, "POST", f.path(), f.a, f.ws, nil, nil); code != 400 {
		t.Fatal(code)
	}
	var after, status string
	var runs, events int
	if e := f.s.Env.Owner.QueryRow(t.Context(), `SELECT c.draft::text,r.status,(SELECT count(*) FROM agent_run WHERE lab_chat_id=c.id),(SELECT count(*) FROM event WHERE payload->>'chat_id'=c.id::text AND recipient_id IS DISTINCT FROM c.owner_id) FROM lab_chat c JOIN agent_run r ON r.lab_chat_id=c.id WHERE r.id=$1`, f.run).Scan(&after, &status, &runs, &events); e != nil || after != before || status != "queued" || runs != 1 || events != 0 {
		t.Fatal(e, after, status, runs, events)
	}
}

func TestManualDraftArchiveCreatorOnlyAndReauthorizes(t *testing.T) {
	f := draftForCancel(t, "queued")
	admin := f.s.member(t, f.a, f.ws, "archive-admin@lab.org", "admin")
	other := f.s.workspace(t, f.a, "archive-other")
	for _, x := range []struct{ token, ws string }{{f.b, f.ws}, {admin, f.ws}, {f.a, other.ID}} {
		if code := f.s.call(t, "POST", labChats+"/"+f.chat+"/archive", x.token, x.ws, nil, nil); code != 404 {
			t.Fatal(code)
		}
	}
	normal := newLabChat(t, f.s, f.a, f.ws)
	if code := f.s.call(t, "POST", labChats+"/"+normal+"/archive", f.a, f.ws, nil, nil); code != 404 {
		t.Fatal(code)
	}
	if code := f.s.call(t, "POST", labChats+"/"+f.chat+"/archive", f.a, f.ws, nil, nil); code != 204 {
		t.Fatal(code)
	}
	if _, e := f.s.Env.Owner.Exec(t.Context(), `UPDATE lab_chat SET draft=jsonb_set(draft,'{card_id}',to_jsonb($2::text)) WHERE id=$1`, f.chat, uuid.NewString()); e != nil {
		t.Fatal(e)
	}
	for _, suffix := range []string{"/draft", "/messages"} {
		if code := f.s.call(t, "GET", labChats+"/"+f.chat+suffix, f.a, f.ws, nil, nil); code != 404 {
			t.Fatal(code)
		}
	}
	var list struct{ Chats []any }
	if code := f.s.call(t, "GET", labChats+"?purpose=manual_decision_draft&archived=true", f.a, f.ws, nil, &list); code != 200 || len(list.Chats) != 0 {
		t.Fatal(code, list)
	}
}

func TestManualDraftArchiveRacesAutosave(t *testing.T) {
	f := draftForCancel(t, "queued")
	tx, e := f.s.Env.Owner.Begin(t.Context())
	if e != nil {
		t.Fatal(e)
	}
	defer tx.Rollback(t.Context())
	if _, e = tx.Exec(t.Context(), "SELECT id FROM workspace WHERE id=$1 FOR NO KEY UPDATE", f.ws); e != nil {
		t.Fatal(e)
	}
	if _, e = tx.Exec(t.Context(), "UPDATE lab_chat SET archived_at=now() WHERE id=$1", f.chat); e != nil {
		t.Fatal(e)
	}
	done := make(chan int, 1)
	go func() {
		done <- f.s.call(t, "PUT", labChats+"/"+f.chat+"/draft", f.a, f.ws, map[string]any{"title": "late edit", "context": "", "deadline": nil, "card_id": nil}, nil)
	}()
	ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
	defer cancel()
	for {
		var blocked bool
		if e = f.s.Env.Owner.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM pg_stat_activity WHERE datname=current_database() AND $1::int=ANY(pg_blocking_pids(pid)))`, tx.Conn().PgConn().PID()).Scan(&blocked); e != nil {
			t.Fatal(e)
		}
		if blocked {
			break
		}
		select {
		case code := <-done:
			t.Fatalf("save did not wait: %d", code)
		case <-ctx.Done():
			t.Fatal(ctx.Err())
		case <-time.After(time.Millisecond):
		}
	}
	if e = tx.Commit(t.Context()); e != nil {
		t.Fatal(e)
	}
	if code := <-done; code != 400 {
		t.Fatal(code)
	}
	var title string
	if e = f.s.Env.Owner.QueryRow(t.Context(), "SELECT draft->>'title' FROM lab_chat WHERE id=$1", f.chat).Scan(&title); e != nil || title != "Keep my edits" {
		t.Fatal(e, title)
	}
}

func TestManualDraftArchiveFailurePreservesCreatedNote(t *testing.T) {
	f := draftForCancel(t, "queued")
	note := f.s.createManual(t, f.a, f.ws, "")
	if _, e := f.s.Env.Owner.Exec(t.Context(), `CREATE FUNCTION reject_archive() RETURNS trigger LANGUAGE plpgsql AS $$ BEGIN RAISE EXCEPTION 'archive unavailable'; END $$; CREATE TRIGGER reject_archive BEFORE UPDATE OF archived_at ON lab_chat FOR EACH ROW EXECUTE FUNCTION reject_archive()`); e != nil {
		t.Fatal(e)
	}
	if code := f.s.call(t, "POST", labChats+"/"+f.chat+"/archive", f.a, f.ws, nil, nil); code != 500 {
		t.Fatal(code)
	}
	if n := f.s.count(t, `SELECT count(*) FROM inbox_item WHERE id=$1 AND title=$2 AND status='open' AND resolved_at IS NULL`, note.ID, note.Title); n != 1 {
		t.Fatal("created note lost", n)
	}
	var archived bool
	if e := f.s.Env.Owner.QueryRow(t.Context(), "SELECT archived_at IS NOT NULL FROM lab_chat WHERE id=$1", f.chat).Scan(&archived); e != nil || archived {
		t.Fatal(e, archived)
	}
}
