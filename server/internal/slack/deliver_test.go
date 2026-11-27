package slack

import (
	"context"
	"testing"

	"github.com/google/uuid"

	"lykeion/server/internal/store"
	db "lykeion/server/pkg/db/generated"
)

type asked struct {
	user, run, chat uuid.UUID
	post            uuid.UUID
}

// ask sends a Slack question and returns the rows it created.
func ask(t *testing.T, f *inFix, slackUser string) asked {
	t.Helper()
	a := asked{user: f.member(t, slackUser+"@lab.org", slackUser, true)}
	f.send(t, Message{User: slackUser, Text: "what is known?", TS: "5.5"})
	ctx := context.Background()
	if err := f.Env.Owner.QueryRow(ctx, `SELECT p.id, p.agent_run_id, c.lab_chat_id FROM slack_post p JOIN slack_chat c ON c.user_id=p.user_id WHERE p.user_id=$1`, a.user).Scan(&a.post, &a.run, &a.chat); err != nil {
		t.Fatal(err)
	}
	return a
}

func (f *inFix) answer(t *testing.T, a asked, kind, body, statements, evidence string) {
	t.Helper()
	if _, err := f.Env.Owner.Exec(context.Background(), `UPDATE lab_message SET launch_sources='{}' WHERE kind='question' AND agent_run_id=$1`, a.run); err != nil {
		t.Fatal(err)
	}
	if _, err := f.Env.Owner.Exec(context.Background(), `INSERT INTO lab_message(workspace_id,chat_id,owner_id,kind,body,statements,evidence,agent_run_id,history_safe) VALUES($1,$2,$3,$4,$5,$6,$7,$8,true)`,
		f.WS, a.chat, a.user, kind, body, statements, evidence, a.run); err != nil {
		t.Fatal(err)
	}
}

func (f *inFix) deliver(t *testing.T) {
	t.Helper()
	if err := f.S.DeliverOnce(context.Background(), f.Inst); err != nil {
		t.Fatal(err)
	}
}

func (f *inFix) status(t *testing.T, a asked) string {
	t.Helper()
	var s string
	if err := f.Env.Owner.QueryRow(context.Background(), `SELECT status FROM slack_post WHERE id=$1`, a.post).Scan(&s); err != nil {
		t.Fatal(err)
	}
	return s
}

func TestAnswerPostedOnce(t *testing.T) {
	f := inSetup(t)
	a := ask(t, f, "UA")
	f.deliver(t)
	if n := len(f.got("chat.postMessage")); n != 0 {
		t.Fatalf("posted before the answer exists: %d", n)
	}
	f.answer(t, a, "answer", "", `[{"kind":"gap","text":"nothing <yet>","cites":[]}]`, `[]`)
	f.deliver(t)
	f.deliver(t)
	p := f.got("chat.postMessage")
	if len(p) != 1 || p[0].Form.Get("channel") != "D1" || p[0].Form.Get("thread_ts") != "" || p[0].Form.Get("text") != "nothing &lt;yet&gt;" {
		t.Fatalf("%+v", p)
	}
	if r := f.got("reactions.remove"); len(r) != 1 || r[0].Form.Get("timestamp") != "5.5" {
		t.Fatalf("%+v", r)
	}
	if s := f.status(t, a); s != "posted" {
		t.Fatal(s)
	}
}

func TestFailurePosted(t *testing.T) {
	f := inSetup(t)
	a := ask(t, f, "UA")
	f.answer(t, a, "failure", "Aristotle <failed> & stopped", `[]`, `[]`)
	f.deliver(t)
	p := f.got("chat.postMessage")
	if len(p) != 1 || p[0].Form.Get("text") != "Aristotle &lt;failed&gt; &amp; stopped" {
		t.Fatalf("%+v", p)
	}
}

func TestUnsafeEvidence(t *testing.T) {
	f := inSetup(t)
	a := ask(t, f, "UA")
	rec := uuid.New().String() // a record the member cannot read
	f.answer(t, a, "answer", "", `[{"kind":"fact","text":"secret","cites":[{"ref":"record:`+rec+`","version":"v"}]}]`, `[{"ref":"record:`+rec+`","version":"v","via":"read"}]`)
	f.deliver(t)
	p := f.got("chat.postMessage")
	if len(p) != 1 || p[0].Form.Get("text") != unavailableText {
		t.Fatalf("%+v", p)
	}
}

func TestRemovedMemberAnswerDropped(t *testing.T) {
	f := inSetup(t)
	a := ask(t, f, "UA")
	f.answer(t, a, "answer", "", `[{"kind":"gap","text":"hi","cites":[]}]`, `[]`)
	if _, err := f.Env.Owner.Exec(context.Background(), `DELETE FROM member WHERE workspace_id=$1 AND user_id=$2`, f.WS, a.user); err != nil {
		t.Fatal(err)
	}
	f.deliver(t)
	if n := len(f.got("chat.postMessage")); n != 0 || f.status(t, a) != "dropped" {
		t.Fatalf("posted=%d status=%s", n, f.status(t, a))
	}
}

func TestSendingNotRetried(t *testing.T) {
	f := inSetup(t)
	a := ask(t, f, "UA")
	f.answer(t, a, "answer", "", `[{"kind":"gap","text":"hi","cites":[]}]`, `[]`)
	if _, err := f.Env.Owner.Exec(context.Background(), `UPDATE slack_post SET status='sending' WHERE id=$1`, a.post); err != nil {
		t.Fatal(err)
	}
	f.deliver(t)
	if n := len(f.got("chat.postMessage")); n != 0 || f.status(t, a) != "sending" {
		t.Fatalf("posted=%d status=%s", n, f.status(t, a))
	}
}

func TestSlackErrorRetriesThenDrops(t *testing.T) {
	f := inSetup(t)
	a := ask(t, f, "UA")
	f.answer(t, a, "answer", "", `[{"kind":"gap","text":"hi","cites":[]}]`, `[]`)
	f.fail = map[string]string{"chat.postMessage": "ratelimited"}
	pass := func() {
		if err := f.S.DeliverOnce(context.Background(), f.Inst); err == nil {
			t.Fatal("expected the Slack error")
		}
	}
	for i := 1; i <= 4; i++ {
		pass()
		if s := f.status(t, a); s != "pending" {
			t.Fatalf("attempt %d: %s", i, s)
		}
	}
	pass()
	if s := f.status(t, a); s != "dropped" {
		t.Fatal(s)
	}
	f.deliver(t)
	if n := len(f.got("chat.postMessage")); n != 5 {
		t.Fatalf("posts=%d", n)
	}
	if r := f.got("reactions.remove"); len(r) != 1 || r[0].Form.Get("timestamp") != "5.5" {
		t.Fatalf("%+v", r)
	}
}

// draft seeds a draft post, its request and (optionally) a failed run or a card.
func draft(t *testing.T, f *inFix, user uuid.UUID, fail, complete bool) (post, card uuid.UUID) {
	t.Helper()
	ctx, ar := context.Background(), uuid.New()
	post, card = uuid.New(), uuid.New()
	for _, q := range []struct {
		sql  string
		args []any
	}{
		{`INSERT INTO autoresearch(id,workspace_id,owner_id,title,execution_mode) VALUES($1,$2,$3,'d','create_card')`, []any{ar, f.WS, user}},
		{`INSERT INTO study_draft_request(workspace_id,autoresearch_id) VALUES($1,$2)`, []any{f.WS, ar}},
		{`INSERT INTO slack_post(id,workspace_id,installation_id,user_id,channel,kind,autoresearch_id) VALUES($1,$2,$3,$4,'D1','draft',$5)`, []any{post, f.WS, f.Inst.ID, user, ar}},
	} {
		if _, err := f.Env.Owner.Exec(ctx, q.sql, q.args...); err != nil {
			t.Fatal(err)
		}
	}
	if fail {
		if _, err := f.Env.Owner.Exec(ctx, `INSERT INTO autoresearch_run(workspace_id,autoresearch_id,source,acting_user_id,planned_at,status,reason,idempotency_key) VALUES($1,$2,'manual',$3,now(),'failed','dispatch_failed',$4)`, f.WS, ar, user, uuid.NewString()); err != nil {
			t.Fatal(err)
		}
	}
	if complete {
		if _, err := f.Env.Owner.Exec(ctx, `INSERT INTO card(id,workspace_id,number,title,question,owner_id,board_column) VALUES($1,$2,1,'t','q',$3,'grounding')`, card, f.WS, user); err != nil {
			t.Fatal(err)
		}
		if _, err := f.Env.Owner.Exec(ctx, `UPDATE study_draft_request SET completed=true, card_id=$2 WHERE autoresearch_id=$1`, ar, card); err != nil {
			t.Fatal(err)
		}
	}
	return
}

func TestDraftReadyDM(t *testing.T) {
	f := inSetup(t)
	u := f.member(t, "ua@lab.org", "UA", true)
	_, card := draft(t, f, u, false, true)
	f.deliver(t)
	p := f.got("chat.postMessage")
	want := "Your study is ready: https://lyk.test/app/#/studies/" + card.String()
	if len(p) != 1 || p[0].Form.Get("text") != want || p[0].Form.Get("channel") != "D1" || p[0].Form.Get("thread_ts") != "" {
		t.Fatalf("%+v", p)
	}
}

func TestTodoDraftReadyDM(t *testing.T) {
	f := inSetup(t)
	u := f.member(t, "ua@lab.org", "UA", true)
	post, _ := draft(t, f, u, false, false)
	if _, err := f.Env.Owner.Exec(t.Context(), `UPDATE study_draft_request SET save_to_todo=true, completed=true
		WHERE workspace_id=$1 AND autoresearch_id=(SELECT autoresearch_id FROM slack_post WHERE workspace_id=$1 AND id=$2)`, f.WS, post); err != nil {
		t.Fatal(err)
	}
	f.deliver(t)
	p := f.got("chat.postMessage")
	if len(p) != 1 || p[0].Form.Get("text") != "Your study draft is ready in To-do: https://lyk.test/app/#/studies" || p[0].Form.Get("channel") != "D1" {
		t.Fatalf("%+v", p)
	}
}

func TestDraftFailedAndWaiting(t *testing.T) {
	f := inSetup(t)
	u := f.member(t, "ua@lab.org", "UA", true)
	draft(t, f, u, false, false) // still running: nothing to say yet
	f.deliver(t)
	if n := len(f.got("chat.postMessage")); n != 0 {
		t.Fatalf("posted while drafting: %d", n)
	}
	draft(t, f, u, true, false)
	f.deliver(t)
	p := f.got("chat.postMessage")
	if len(p) != 1 || p[0].Form.Get("text") != "I couldn't draft that study: the agent could not start." {
		t.Fatalf("%+v", p)
	}
}

func TestStalePostPostedOnce(t *testing.T) {
	f := inSetup(t)
	a := ask(t, f, "UA")
	f.answer(t, a, "answer", "", `[{"kind":"gap","text":"hi","cites":[]}]`, `[]`)
	var p db.SlackPost
	if err := f.S.Store.InWorkspace(context.Background(), f.WS, f.User, func(tx *store.Tx) (err error) {
		ps, err := tx.Q.ListPendingSlackPosts(context.Background(), f.Inst.ID)
		if len(ps) == 1 {
			p = ps[0]
		}
		return err
	}); err != nil || p.ID == uuid.Nil {
		t.Fatal(err)
	}
	c, err := f.S.session(f.Inst)
	if err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 2; i++ {
		if err := f.S.deliver(context.Background(), f.Inst, c, p); err != nil {
			t.Fatal(err)
		}
	}
	if n := len(f.got("chat.postMessage")); n != 1 {
		t.Fatalf("posts=%d", n)
	}
}

func TestCompletedDraftWithoutCardDropped(t *testing.T) {
	f := inSetup(t)
	u := f.member(t, "ua@lab.org", "UA", true)
	post, card := draft(t, f, u, false, true)
	if _, err := f.Env.Owner.Exec(context.Background(), `DELETE FROM card WHERE id=$1`, card); err != nil {
		t.Fatal(err)
	}
	f.deliver(t)
	var s string
	if err := f.Env.Owner.QueryRow(context.Background(), `SELECT status FROM slack_post WHERE id=$1`, post).Scan(&s); err != nil || s != "dropped" || len(f.got("chat.postMessage")) != 0 {
		t.Fatal(err, s)
	}
}

func (f *inFix) exec(t *testing.T, sql string, args ...any) {
	t.Helper()
	if _, err := f.Env.Owner.Exec(context.Background(), sql, args...); err != nil {
		t.Fatal(err)
	}
}

func TestChannelAnswerThreaded(t *testing.T) {
	f := inSetup(t)
	u := f.member(t, "a@lab.org", "UA", true)
	f.send(t, Message{User: "UA", Text: "q", Channel: "C1", ChannelType: "channel", TS: "3.3"})
	a := asked{user: u}
	if err := f.Env.Owner.QueryRow(context.Background(), `SELECT p.id, p.agent_run_id, c.lab_chat_id FROM slack_post p JOIN slack_chat c ON c.user_id=p.user_id WHERE p.user_id=$1`, u).Scan(&a.post, &a.run, &a.chat); err != nil {
		t.Fatal(err)
	}
	f.answer(t, a, "answer", "", `[{"kind":"gap","text":"hi","cites":[]}]`, `[]`)
	f.deliver(t)
	if p := f.got("chat.postMessage"); len(p) != 1 || p[0].Form.Get("channel") != "C1" || p[0].Form.Get("thread_ts") != "3.3" {
		t.Fatalf("%+v", p)
	}
}

func TestEndedRunWithoutResultDropped(t *testing.T) {
	f := inSetup(t)
	a := ask(t, f, "UA")
	// Just cancelled: its failure message may still be on its way.
	f.exec(t, `UPDATE agent_run SET status='cancelled', completed_at=now() WHERE id=$1`, a.run)
	f.deliver(t)
	if s := f.status(t, a); s != "pending" {
		t.Fatal(s)
	}
	// Cancelled long ago with no result (its chat was deleted): it never gets one.
	f.exec(t, `UPDATE agent_run SET completed_at=now()-interval '1 hour' WHERE id=$1`, a.run)
	f.deliver(t)
	if s := f.status(t, a); s != "dropped" || len(f.got("chat.postMessage")) != 0 {
		t.Fatalf("status=%s posts=%d", s, len(f.got("chat.postMessage")))
	}
	if r := f.got("reactions.remove"); len(r) != 1 || r[0].Form.Get("timestamp") != "5.5" {
		t.Fatalf("%+v", r)
	}
}

func TestOldPendingPostDropped(t *testing.T) {
	f := inSetup(t)
	a := ask(t, f, "UA")
	f.exec(t, `UPDATE slack_post SET created_at=now()-interval '25 hours' WHERE id=$1`, a.post)
	f.deliver(t)
	if s := f.status(t, a); s != "dropped" || len(f.got("reactions.remove")) != 1 {
		t.Fatalf("status=%s", s)
	}
}

func TestSkippedDraftFails(t *testing.T) {
	f := inSetup(t)
	u := f.member(t, "ua@lab.org", "UA", true)
	draft(t, f, u, false, false)
	f.exec(t, `INSERT INTO autoresearch_run(workspace_id,autoresearch_id,source,acting_user_id,planned_at,status,reason,idempotency_key)
 SELECT workspace_id,autoresearch_id,'manual',$2,now(),'skipped','runtime_offline',gen_random_uuid()::text FROM slack_post WHERE workspace_id=$1 AND kind='draft'`, f.WS, u)
	f.deliver(t)
	p := f.got("chat.postMessage")
	if len(p) != 1 || p[0].Form.Get("text") != "I couldn't draft that study: your machine went offline." {
		t.Fatalf("%+v", p)
	}
}

func TestTransportErrorDropsWithoutRetry(t *testing.T) {
	f := inSetup(t)
	a := ask(t, f, "UA")
	f.answer(t, a, "answer", "", `[{"kind":"gap","text":"hi","cites":[]}]`, `[]`)
	f.fail = map[string]string{"chat.postMessage": "hangup"}
	if err := f.S.DeliverOnce(context.Background(), f.Inst); err == nil {
		t.Fatal("expected the transport error")
	}
	if s := f.status(t, a); s != "dropped" {
		t.Fatal(s)
	}
	f.deliver(t)
	if n := len(f.got("chat.postMessage")); n != 1 {
		t.Fatalf("posts=%d", n)
	}
}

func (f *inFix) instStatus(t *testing.T) (status string, events int) {
	t.Helper()
	if err := f.Env.Owner.QueryRow(context.Background(), `SELECT status, (SELECT count(*) FROM event WHERE workspace_id=$1 AND type='slack.changed') FROM slack_installation WHERE id=$2`, f.WS, f.Inst.ID).Scan(&status, &events); err != nil {
		t.Fatal(err)
	}
	return
}

func TestInvalidAuthOnDeliveryRevokes(t *testing.T) {
	f := inSetup(t)
	a := ask(t, f, "UA")
	f.answer(t, a, "answer", "", `[{"kind":"gap","text":"hi","cites":[]}]`, `[]`)
	f.fail = map[string]string{"chat.postMessage": "invalid_auth"}
	if err := f.S.DeliverOnce(context.Background(), f.Inst); err == nil {
		t.Fatal("expected the Slack error")
	}
	if s, n := f.instStatus(t); s != "revoked" || n != 1 {
		t.Fatalf("status=%s events=%d", s, n)
	}
}
