package handler

import (
	"github.com/google/uuid"
	"lykeion/server/internal/service"
	"lykeion/server/pkg/agent"
	"testing"
)

func TestDeletePrivateLabChatOwnership(t *testing.T) {
	s, a, b, c, ws, _ := csFixture(t)
	// Workspace owners have no authority to delete someone else's private chat.
	id := newLabChat(t, s, b, ws)
	path := labChats + "/" + id
	for _, tok := range []string{a, c} {
		if code := s.call(t, "DELETE", path, tok, ws, nil, nil); code != 404 {
			t.Fatalf("nonowner delete: %d", code)
		}
	}
	if code := s.call(t, "DELETE", path, b, s.workspace(t, a, "other").ID, nil, nil); code != 404 {
		t.Fatal(code)
	}
	if code := s.call(t, "DELETE", path, b, ws, nil, nil); code != 204 {
		t.Fatalf("owner delete: %d", code)
	}
	for _, suffix := range []string{"", "/messages"} {
		method := "GET"
		if suffix == "" {
			method = "DELETE"
		}
		if code := s.call(t, method, path+suffix, b, ws, nil, nil); code != 404 {
			t.Fatalf("deleted %s: %d", suffix, code)
		}
	}
	if code := s.call(t, "POST", path+"/questions", b, ws, map[string]string{"text": "again", "harness": "claude"}, nil); code != 404 {
		t.Fatal(code)
	}
	var out struct{ Chats []struct{ ID string } }
	if code := s.call(t, "GET", labChats, b, ws, nil, &out); code != 200 || len(out.Chats) != 0 {
		t.Fatalf("list: %d %+v", code, out)
	}
	id = newLabChat(t, s, b, ws)
	if _, err := s.Env.Owner.Exec(t.Context(), `DELETE FROM member WHERE workspace_id=$1 AND user_id=$2`, ws, s.userID(t, b)); err != nil {
		t.Fatal(err)
	}
	if code := s.call(t, "DELETE", labChats+"/"+id, b, ws, nil, nil); code != 404 {
		t.Fatalf("removed member: %d", code)
	}
}

func TestDeletePrivateLabChatCancelsAndPurges(t *testing.T) {
	for _, state := range []string{"queued", "dispatched", "running", "completed"} {
		t.Run(state, func(t *testing.T) {
			s, a, _, _, ws, _ := csFixture(t)
			rt := csRegister(t, s, a, ws, "delete", "claude")
			id := newLabChat(t, s, a, ws)
			seedHistoricalLabQuestion(t, s, id, "sensitive question", "")
			var run uuid.UUID
			if err := s.Env.Owner.QueryRow(t.Context(), `SELECT id FROM agent_run WHERE lab_chat_id=$1`, id).Scan(&run); err != nil {
				t.Fatal(err)
			}
			if state != "queued" {
				claims, err := s.H.AgentRuns.Claim(t.Context(), uuid.MustParse(s.userID(t, a)), []service.RuntimeRef{rt}, 1)
				if err != nil || len(claims) != 1 {
					t.Fatal(err, len(claims))
				}
				rt.Attempt = claims[0].AgentRun.Attempt
				if _, err = s.Env.Owner.Exec(t.Context(), `UPDATE agent_run SET status=$2,error='sensitive error',started_at=CASE WHEN $2='running' THEN now() ELSE NULL END WHERE id=$1`, run, state); err != nil {
					t.Fatal(err)
				}
			}
			if _, err := s.Env.Owner.Exec(t.Context(), `INSERT INTO lab_read(workspace_id,agent_run_id,ref,via,read_version) SELECT workspace_id,id,'sensitive existing ref','private traversal','private version' FROM agent_run WHERE id=$1`, run); err != nil {
				t.Fatal(err)
			}
			if _, err := s.Env.Owner.Exec(t.Context(), `INSERT INTO agent_run_message(workspace_id,agent_run_id,attempt,seq,type,content) SELECT workspace_id,id,1,1,'text','{"text":"existing private transcript"}' FROM agent_run WHERE id=$1`, run); err != nil {
				t.Fatal(err)
			}
			// The new DELETE policy does not permit erasing live chat trails.
			probe, err := s.Env.App.Begin(t.Context())
			if err != nil {
				t.Fatal(err)
			}
			if _, err = probe.Exec(t.Context(), `SELECT set_config('app.workspace_id',$1,true),set_config('app.user_id',$2,true)`, ws, s.userID(t, a)); err != nil {
				t.Fatal(err)
			}
			deleted, err := probe.Exec(t.Context(), `DELETE FROM lab_read WHERE agent_run_id=$1`, run)
			if err != nil || deleted.RowsAffected() != 0 {
				t.Fatalf("live trail deletion: %v %d", err, deleted.RowsAffected())
			}
			if err = probe.Rollback(t.Context()); err != nil {
				t.Fatal(err)
			}
			keep := newLabChat(t, s, a, ws)
			seedHistoricalLabQuestion(t, s, keep, "keep this", "")
			if _, err := s.Env.Owner.Exec(t.Context(), `INSERT INTO lab_read(workspace_id,agent_run_id,ref) SELECT workspace_id,id,'keep traversal' FROM agent_run WHERE lab_chat_id=$1`, keep); err != nil {
				t.Fatal(err)
			}

			if code := s.call(t, "DELETE", labChats+"/"+id, a, ws, nil, nil); code != 204 {
				t.Fatalf("delete: %d", code)
			}
			var status, prompt, runError string
			var messages, tokens, transcript int
			if err := s.Env.Owner.QueryRow(t.Context(), `SELECT status,prompt,error,
 (SELECT count(*) FROM lab_message WHERE chat_id=$2),
 (SELECT count(*) FROM agent_run_token WHERE agent_run_id=$1),
 (SELECT count(*) FROM agent_run_message WHERE agent_run_id=$1)
 FROM agent_run WHERE id=$1`, run, id).Scan(&status, &prompt, &runError, &messages, &tokens, &transcript); err != nil {
				t.Fatal(err)
			}
			want := "cancelled"
			if state == "completed" {
				want = "completed"
			}
			if status != want || prompt != "" || runError != "" || messages != 0 || tokens != 0 || transcript != 0 {
				t.Fatalf("retained: %s %q %q %d %d %d", status, prompt, runError, messages, tokens, transcript)
			}
			if state == "running" {
				who := uuid.MustParse(s.userID(t, a))
				if status, err := s.H.AgentRuns.Status(t.Context(), who, rt, run); err != nil || status != "cancelled" {
					t.Fatalf("daemon cancellation: %s %v", status, err)
				}
				late := service.Report{Error: "sensitive late report", FailureReason: "runtime_offline", Usage: []agent.Usage{{Model: "m", OutputTokens: 7}}}
				for range 2 {
					if got, err := s.H.AgentRuns.Fail(t.Context(), who, rt, run, late); err != nil || got.Status != "cancelled" {
						t.Fatalf("late settlement: %s %v", got.Status, err)
					}
				}
				var n int
				if err := s.Env.Owner.QueryRow(t.Context(), `SELECT count(*) FROM agent_run_usage WHERE agent_run_id=$1`, run).Scan(&n); err != nil || n != 1 {
					t.Fatal(err, n)
				}
			}
			// Late reports and settlement delivery cannot restore private content.
			if _, err := s.Env.Owner.Exec(t.Context(), `UPDATE agent_run SET prompt='late private',error='late error' WHERE id=$1`, run); err != nil {
				t.Fatal(err)
			}
			if _, err := s.Env.Owner.Exec(t.Context(), `INSERT INTO lab_message(workspace_id,chat_id,owner_id,kind,body,agent_run_id) SELECT workspace_id,lab_chat_id,requester_id,'failure','late private',id FROM agent_run WHERE id=$1`, run); err != nil {
				t.Fatal(err)
			}
			if _, err := s.Env.Owner.Exec(t.Context(), `INSERT INTO agent_run_message(workspace_id,agent_run_id,attempt,seq,type,content) SELECT workspace_id,id,1,999,'text','{"text":"late private"}' FROM agent_run WHERE id=$1`, run); err != nil {
				t.Fatal(err)
			}
			if _, err := s.Env.Owner.Exec(t.Context(), `INSERT INTO lab_read(workspace_id,agent_run_id,ref) SELECT workspace_id,id,'late private' FROM agent_run WHERE id=$1`, run); err != nil {
				t.Fatal(err)
			}
			if _, err := s.Env.Owner.Exec(t.Context(), `UPDATE agent_run_receipt SET result=result||'{"prompt":"late private","error":"late error"}' WHERE agent_run_id=$1`, run); err != nil {
				t.Fatal(err)
			}
			var retained int
			if err := s.Env.Owner.QueryRow(t.Context(), `SELECT (SELECT count(*) FROM agent_run_message WHERE agent_run_id=$1)+(SELECT count(*) FROM lab_read WHERE agent_run_id=$1)+(SELECT count(*) FROM agent_run_receipt WHERE agent_run_id=$1 AND (result ? 'prompt' OR result ? 'error'))`, run).Scan(&retained); err != nil || retained != 0 {
				t.Fatal(err, retained)
			}
			if err := s.Env.Owner.QueryRow(t.Context(), `SELECT prompt,(SELECT count(*) FROM lab_message WHERE chat_id=$2) FROM agent_run WHERE id=$1`, run, id).Scan(&prompt, &messages); err != nil || prompt != "" || messages != 0 {
				t.Fatal(err, prompt, messages)
			}
			var n int
			if err := s.Env.Owner.QueryRow(t.Context(), `SELECT count(*) FROM lab_message WHERE chat_id=$1 AND body='keep this'`, keep).Scan(&n); err != nil || n != 1 {
				t.Fatal(err, n)
			}
			if err := s.Env.Owner.QueryRow(t.Context(), `SELECT count(*) FROM lab_read m JOIN agent_run r ON r.id=m.agent_run_id WHERE r.lab_chat_id=$1 AND m.ref='keep traversal'`, keep).Scan(&n); err != nil || n != 1 {
				t.Fatal(err, n)
			}

		})
	}
}

func TestDeletePrivateLabChatRacesQuestionAdmission(t *testing.T) {
	s, a, _, _, ws, _ := csFixture(t)
	csRegister(t, s, a, ws, "race", "claude")
	for range 8 {
		id := newLabChat(t, s, a, ws)
		start := make(chan struct{})
		asked := make(chan int, 1)
		go func() {
			<-start
			asked <- s.call(t, "POST", labChats+"/"+id+"/questions", a, ws, map[string]string{"text": "race private", "harness": "claude"}, nil)
		}()
		close(start)
		if code := s.call(t, "DELETE", labChats+"/"+id, a, ws, nil, nil); code != 204 {
			t.Fatalf("delete race: %d", code)
		}
		if code := <-asked; code != 202 && code != 404 {
			t.Fatalf("ask race: %d", code)
		}
		var n int
		if err := s.Env.Owner.QueryRow(t.Context(), `SELECT (SELECT count(*) FROM lab_message WHERE chat_id=$1)+(SELECT count(*) FROM agent_run WHERE lab_chat_id=$1 AND (status IN ('queued','dispatched','running') OR prompt<>''))`, id).Scan(&n); err != nil || n != 0 {
			t.Fatal(err, n)
		}
	}
}

func TestDeletePrivateLabChatRollsBackCancellationFailure(t *testing.T) {
	s, a, _, _, ws, _ := csFixture(t)
	id := newLabChat(t, s, a, ws)
	seedHistoricalLabQuestion(t, s, id, "preserve on failure", "")
	if _, err := s.Env.Owner.Exec(t.Context(), `CREATE FUNCTION reject_lab_cancellation() RETURNS trigger LANGUAGE plpgsql AS $$ BEGIN IF NEW.status='cancelled' THEN RAISE EXCEPTION 'test cancellation failure'; END IF; RETURN NEW; END $$; CREATE TRIGGER reject_lab_cancellation BEFORE UPDATE ON agent_run FOR EACH ROW EXECUTE FUNCTION reject_lab_cancellation()`); err != nil {
		t.Fatal(err)
	}
	if code := s.call(t, "DELETE", labChats+"/"+id, a, ws, nil, nil); code != 500 {
		t.Fatal(code)
	}
	var n int
	if err := s.Env.Owner.QueryRow(t.Context(), `SELECT count(*) FROM lab_message m JOIN lab_chat c ON c.id=m.chat_id JOIN agent_run r ON r.id=m.agent_run_id WHERE c.id=$1 AND c.deleted_at IS NULL AND m.body='preserve on failure' AND r.status='queued'`, id).Scan(&n); err != nil || n != 1 {
		t.Fatal(err, n)
	}
}
