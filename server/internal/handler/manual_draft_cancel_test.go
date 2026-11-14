package handler

import (
	"context"
	"github.com/google/uuid"
	"lykeion/server/internal/labmemory"
	"lykeion/server/internal/service"
	"sync"
	"testing"
	"time"
)

type draftCancelFixture struct {
	s                        *server
	a, b, ws, chat, question string
	run                      uuid.UUID
	ref                      service.RuntimeRef
}

func draftForCancel(t *testing.T, state string) draftCancelFixture {
	t.Helper()
	s, a, b, _, ws, _ := csFixture(t)
	ref := csRegister(t, s, a, ws, "cancel-own", "claude")
	var created struct{ Chat struct{ ID string } }
	if code := s.call(t, "POST", labChats, a, ws, map[string]string{"purpose": "manual_decision_draft"}, &created); code != 201 {
		t.Fatal(code)
	}
	var asked struct{ Message struct{ ID string } }
	body := map[string]any{"user_request": "Refine my private note", "current_draft": map[string]any{"title": "Keep my edits", "context": "private context", "deadline": nil, "card_id": nil}, "harness": "claude", "request_id": uuid.NewString()}
	if code := s.call(t, "POST", labChats+"/"+created.Chat.ID+"/questions", a, ws, body, &asked); code != 202 {
		t.Fatal(code)
	}
	var run uuid.UUID
	if e := s.Env.Owner.QueryRow(t.Context(), "SELECT agent_run_id FROM lab_message WHERE id=$1", asked.Message.ID).Scan(&run); e != nil {
		t.Fatal(e)
	}
	if state == "dispatched" || state == "running" {
		who := uuid.MustParse(s.userID(t, a))
		claims, e := s.H.AgentRuns.Claim(t.Context(), who, []service.RuntimeRef{ref}, 1)
		if e != nil || len(claims) != 1 {
			t.Fatal(e, claims)
		}
		ref.Attempt = claims[0].AgentRun.Attempt
		if state == "running" {
			if _, e = s.H.AgentRuns.StartLab(t.Context(), who, ref, run, labmemory.LaunchPrompt); e != nil {
				t.Fatal(e)
			}
		}
	} else if state != "queued" {
		if _, e := s.Env.Owner.Exec(t.Context(), "UPDATE agent_run SET status=$1 WHERE id=$2", state, run); e != nil {
			t.Fatal(e)
		}
	}
	return draftCancelFixture{s, a, b, ws, created.Chat.ID, asked.Message.ID, run, ref}
}
func (f draftCancelFixture) path() string {
	return labChats + "/" + f.chat + "/questions/" + f.question + "/cancel"
}

func TestManualDraftCancelPreservesDraftAndHistory(t *testing.T) {
	for _, state := range []string{"queued", "dispatched", "running", "completed", "failed", "cancelled"} {
		t.Run(state, func(t *testing.T) {
			f := draftForCancel(t, state)
			if _, e := f.s.Env.Owner.Exec(t.Context(), `INSERT INTO agent_run_usage(workspace_id,agent_run_id,attempt,model,input_tokens,output_tokens,cache_read_tokens,cache_write_tokens,cost_usd) VALUES($1,$2,1,'m',7,3,0,0,0.1)`, f.ws, f.run); e != nil {
				t.Fatal(e)
			}
			var before string
			if e := f.s.Env.Owner.QueryRow(t.Context(), "SELECT draft::text FROM lab_chat WHERE id=$1", f.chat).Scan(&before); e != nil {
				t.Fatal(e)
			}
			for range 2 {
				if code := f.s.call(t, "POST", f.path(), f.a, f.ws, nil, nil); code != 204 {
					t.Fatalf("cancel %s: %d", state, code)
				}
			}
			want := state
			if state == "queued" || state == "dispatched" || state == "running" {
				want = "cancelled"
			}
			var after, status string
			var usage, notes, tokens, shared int
			if e := f.s.Env.Owner.QueryRow(t.Context(), `SELECT c.draft::text,r.status,(SELECT output_tokens FROM agent_run_usage WHERE agent_run_id=r.id),(SELECT count(*) FROM inbox_item WHERE workspace_id=r.workspace_id AND kind='manual_decision'),(SELECT count(*) FROM agent_run_token WHERE agent_run_id=r.id),(SELECT count(*) FROM event WHERE workspace_id=r.workspace_id AND payload->>'agent_run_id'=r.id::text AND recipient_id IS DISTINCT FROM r.requester_id) FROM lab_chat c JOIN agent_run r ON r.lab_chat_id=c.id WHERE r.id=$1`, f.run).Scan(&after, &status, &usage, &notes, &tokens, &shared); e != nil {
				t.Fatal(e)
			}
			if after != before || status != want || usage != 3 || notes != 0 || tokens != 0 || shared != 0 {
				t.Fatalf("draft/accounting/privacy changed: %s %s %d %d %d %d", after, status, usage, notes, tokens, shared)
			}
			var history struct{ Messages []struct{ ID, Body string } }
			if code := f.s.call(t, "GET", labChats+"/"+f.chat+"/messages", f.a, f.ws, nil, &history); code != 200 {
				t.Fatal(code)
			}
			found := false
			for _, m := range history.Messages {
				if m.ID == f.question && m.Body == "Refine my private note" {
					found = true
				}
			}
			if !found {
				t.Fatal("user question lost")
			}
		})
	}
}

func TestManualDraftCancelCreatorAndPurposeOnly(t *testing.T) {
	f := draftForCancel(t, "queued")
	admin := f.s.member(t, f.a, f.ws, "draft-cancel-admin@lab.org", "admin")
	otherWS := f.s.workspace(t, f.a, "cancel-other")
	for _, tc := range []struct{ token, ws string }{{f.b, f.ws}, {admin, f.ws}, {f.a, otherWS.ID}} {
		if code := f.s.call(t, "POST", f.path(), tc.token, tc.ws, nil, nil); code != 404 {
			t.Fatal(code)
		}
	}
	var other struct{ Chat struct{ ID string } }
	f.s.call(t, "POST", labChats, f.a, f.ws, map[string]string{"purpose": "manual_decision_draft"}, &other)
	if code := f.s.call(t, "POST", labChats+"/"+other.Chat.ID+"/questions/"+f.question+"/cancel", f.a, f.ws, nil, nil); code != 404 {
		t.Fatal(code)
	}
	normal := newLabChat(t, f.s, f.a, f.ws)
	var asked struct{ Message struct{ ID string } }
	if code := f.s.call(t, "POST", labChats+"/"+normal+"/questions", f.a, f.ws, map[string]string{"text": "normal question", "harness": "claude"}, &asked); code != 202 {
		t.Fatal(code)
	}
	if code := f.s.call(t, "POST", labChats+"/"+normal+"/questions/"+asked.Message.ID+"/cancel", f.a, f.ws, nil, nil); code != 404 {
		t.Fatal(code)
	}
	var n int
	if e := f.s.Env.Owner.QueryRow(t.Context(), "SELECT count(*) FROM agent_run WHERE workspace_id=$1 AND status='queued'", f.ws).Scan(&n); e != nil || n != 2 {
		t.Fatal(e, n)
	}
}

func TestManualDraftCancelConcurrentReplay(t *testing.T) {
	f := draftForCancel(t, "running")
	start := make(chan struct{})
	codes := make(chan int, 2)
	var wg sync.WaitGroup
	for range 2 {
		wg.Add(1)
		go func() { defer wg.Done(); <-start; codes <- f.s.call(t, "POST", f.path(), f.a, f.ws, nil, nil) }()
	}
	close(start)
	wg.Wait()
	close(codes)
	for code := range codes {
		if code != 204 {
			t.Fatal(code)
		}
	}
	var events, settlements int
	if e := f.s.Env.Owner.QueryRow(t.Context(), `SELECT (SELECT count(*) FROM event WHERE type='agent_run.cancelled' AND payload->>'agent_run_id'=$1),(SELECT count(*) FROM agent_run_settlement WHERE agent_run_id=$2)`, f.run.String(), f.run).Scan(&events, &settlements); e != nil || events != 1 || settlements != 1 {
		t.Fatal(e, events, settlements)
	}
}

func TestManualDraftCancelRacingCompletionIsNoOp(t *testing.T) {
	f := draftForCancel(t, "running")
	tx, e := f.s.Env.Owner.Begin(t.Context())
	if e != nil {
		t.Fatal(e)
	}
	defer tx.Rollback(t.Context())
	if _, e = tx.Exec(t.Context(), "UPDATE agent_run SET status='completed' WHERE id=$1", f.run); e != nil {
		t.Fatal(e)
	}
	done := make(chan int, 1)
	go func() { done <- f.s.call(t, "POST", f.path(), f.a, f.ws, nil, nil) }()
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
			t.Fatalf("cancellation did not wait for completion: %d", code)
		case <-ctx.Done():
			t.Fatal(ctx.Err())
		case <-time.After(time.Millisecond):
		}
	}
	if e = tx.Commit(t.Context()); e != nil {
		t.Fatal(e)
	}
	if code := <-done; code != 204 {
		t.Fatal(code)
	}
	var status string
	var events int
	if e = f.s.Env.Owner.QueryRow(t.Context(), `SELECT status,(SELECT count(*) FROM event WHERE type='agent_run.cancelled' AND payload->>'agent_run_id'=r.id::text) FROM agent_run r WHERE id=$1`, f.run).Scan(&status, &events); e != nil || status != "completed" || events != 0 {
		t.Fatal(e, status, events)
	}
}
