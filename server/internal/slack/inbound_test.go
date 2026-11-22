package slack

import (
	"context"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync"
	"testing"

	"github.com/google/uuid"

	"lykeion/server/internal/service"
	"lykeion/server/internal/store"
	db "lykeion/server/pkg/db/generated"
)

type call struct {
	Method string
	Form   url.Values
}

type inFix struct {
	*linkFix
	mu    sync.Mutex
	calls []call
	n     int
	fail  map[string]string // method -> Slack error code
}

func (f *inFix) got(method string) []call {
	f.mu.Lock()
	defer f.mu.Unlock()
	var out []call
	for _, c := range f.calls {
		if c.Method == method {
			out = append(out, c)
		}
	}
	return out
}

func (f *inFix) total() int { f.mu.Lock(); defer f.mu.Unlock(); return len(f.calls) }

// inSetup extends linkSetup with a recording Slack, Redis and AgentRuns.
func inSetup(t *testing.T) *inFix {
	t.Helper()
	f := &inFix{linkFix: linkSetup(t)}
	api := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = r.ParseForm()
		f.mu.Lock()
		f.calls = append(f.calls, call{Method: strings.TrimPrefix(r.URL.Path, "/"), Form: r.Form})
		code := f.fail[strings.TrimPrefix(r.URL.Path, "/")]
		f.mu.Unlock()
		if strings.HasSuffix(r.URL.Path, "/conversations.open") && code == "" {
			w.Write([]byte(`{"ok":true,"channel":{"id":"D1"}}`))
			return
		}
		if code == "hangup" { // a transport error: the connection drops unanswered
			conn, _, _ := w.(http.Hijacker).Hijack()
			conn.Close()
			return
		}
		if code != "" {
			w.Write([]byte(`{"ok":false,"error":"` + code + `"}`))
			return
		}
		w.Write([]byte(`{"ok":true,"ts":"1.1"}`))
	}))
	t.Cleanup(api.Close)
	f.S.APIURL = api.URL
	f.S.Redis = f.Env.Redis
	f.S.AgentRuns = &service.AgentRuns{Store: f.S.Store}
	return f
}

// member adds a bound Slack user (with an online claude runtime when online).
func (f *inFix) member(t *testing.T, email, slackUser string, online bool) uuid.UUID {
	t.Helper()
	ctx := context.Background()
	u, err := f.S.Store.Global().UpsertUserByEmail(ctx, email)
	if err != nil {
		t.Fatal(err)
	}
	err = f.S.Store.InWorkspace(ctx, f.WS, u.ID, func(tx *store.Tx) error {
		if err := tx.Q.AddMember(ctx, db.AddMemberParams{WorkspaceID: f.WS, UserID: u.ID, Role: "member"}); err != nil {
			return err
		}
		_, err := tx.Q.CreateSlackBinding(ctx, db.CreateSlackBindingParams{InstallationID: f.Inst.ID, SlackUserID: slackUser, UserID: u.ID})
		return err
	})
	if err != nil {
		t.Fatal(err)
	}
	if online {
		if _, err := f.Env.Owner.Exec(ctx, `INSERT INTO runtime(workspace_id,owner_id,daemon_id,harness) VALUES($1,$2,$3,'claude')`, f.WS, u.ID, "d-"+slackUser); err != nil {
			t.Fatal(err)
		}
	}
	return u.ID
}

func (f *inFix) send(t *testing.T, m Message) {
	t.Helper()
	f.n++
	if m.EventID == "" {
		m.EventID = "Ev" + strings.Repeat("x", f.n)
	}
	if m.AppID == "" {
		m.AppID = f.Inst.AppID
	}
	if m.Channel == "" {
		m.Channel, m.ChannelType = "D1", "im"
	}
	if m.TS == "" {
		m.TS = "100." + strings.Repeat("1", f.n)
	}
	if err := f.S.HandleMessage(context.Background(), f.Inst, m); err != nil {
		t.Fatal(err)
	}
}

func (f *inFix) count(t *testing.T, q string, args ...any) int {
	t.Helper()
	var n int
	if err := f.Env.Owner.QueryRow(context.Background(), q, args...).Scan(&n); err != nil {
		t.Fatal(err)
	}
	return n
}

func TestUnlinkedGetsLink(t *testing.T) {
	f := inSetup(t)
	f.send(t, Message{User: "U9", Text: "hi"})
	e := f.got("chat.postEphemeral")
	if len(e) != 1 || !strings.Contains(e[0].Form.Get("text"), "/app/#/slack/link?") || e[0].Form.Get("user") != "U9" {
		t.Fatalf("%+v", f.calls)
	}
}

func TestQuestionEnqueued(t *testing.T) {
	f := inSetup(t)
	u := f.member(t, "a@lab.org", "UA", true)
	f.send(t, Message{User: "UA", Text: "<@UBOT> what is known?", TS: "5.5"})
	if n := f.count(t, `SELECT count(*) FROM agent_run WHERE kind='aristotle' AND workspace_id=$1 AND requester_id=$2`, f.WS, u); n != 1 {
		t.Fatalf("runs=%d", n)
	}
	if n := f.count(t, `SELECT count(*) FROM slack_post WHERE status='pending' AND kind='answer' AND user_id=$1 AND channel='D1' AND thread_ts='' AND agent_run_id IS NOT NULL`, u); n != 1 {
		t.Fatalf("posts=%d", n)
	}
	if n := f.count(t, `SELECT count(*) FROM lab_chat WHERE owner_id=$1 AND title='what is known?'`, u); n != 1 {
		t.Fatalf("title not set: %d", n)
	}
	if n := f.count(t, `SELECT count(*) FROM lab_message WHERE body='what is known?'`); n != 1 {
		t.Fatalf("question not stripped: %d", n)
	}
	r := f.got("reactions.add")
	if len(r) != 1 || r[0].Form.Get("name") != "hourglass_flowing_sand" || r[0].Form.Get("timestamp") != "5.5" {
		t.Fatalf("%+v", f.calls)
	}
}

func TestThreadIsPerSender(t *testing.T) {
	f := inSetup(t)
	a := f.member(t, "a@lab.org", "UA", true)
	b := f.member(t, "b@lab.org", "UB", true)
	for _, u := range []string{"UA", "UB"} {
		f.send(t, Message{User: u, Text: "q", Channel: "C1", ChannelType: "channel", ThreadTS: "9.9"})
	}
	for _, o := range []uuid.UUID{a, b} {
		if n := f.count(t, `SELECT count(*) FROM slack_chat s JOIN lab_chat c ON c.id=s.lab_chat_id WHERE s.user_id=$1 AND c.owner_id=$1 AND s.thread_ts='9.9'`, o); n != 1 {
			t.Fatalf("owner %s chats=%d", o, n)
		}
	}
	if n := f.count(t, `SELECT count(DISTINCT lab_chat_id) FROM slack_chat WHERE channel='C1'`); n != 2 {
		t.Fatalf("chats=%d", n)
	}
}

func TestDMContinuesChat(t *testing.T) {
	f := inSetup(t)
	f.member(t, "a@lab.org", "UA", true)
	f.send(t, Message{User: "UA", Text: "one"})
	f.send(t, Message{User: "UA", Text: "two"})
	if n := f.count(t, `SELECT count(*) FROM lab_chat WHERE workspace_id=$1`, f.WS); n != 1 {
		t.Fatalf("chats=%d", n)
	}
	if n := f.count(t, `SELECT count(*) FROM agent_run WHERE kind='aristotle'`); n != 2 {
		t.Fatalf("runs=%d", n)
	}
}

func TestDuplicateEventIgnored(t *testing.T) {
	f := inSetup(t)
	f.member(t, "a@lab.org", "UA", true)
	f.send(t, Message{User: "UA", Text: "q", EventID: "EvSame"})
	f.send(t, Message{User: "UA", Text: "q", EventID: "EvSame"})
	if n := f.count(t, `SELECT count(*) FROM agent_run WHERE kind='aristotle'`); n != 1 {
		t.Fatalf("runs=%d", n)
	}
	if f.Env.Redis.Exists(context.Background(), "lyk:slack:event:"+f.Inst.ID.String()+":EvSame").Val() != 1 {
		t.Fatal("dedupe key missing")
	}
}

func TestIgnoresBotAndEdits(t *testing.T) {
	f := inSetup(t)
	f.member(t, "a@lab.org", "UA", true)
	f.send(t, Message{User: "UA", Text: "q", BotID: "B1"})
	f.send(t, Message{User: "UA", Text: "q", Subtype: "message_changed"})
	f.send(t, Message{User: "UBOT", Text: "q"})
	f.send(t, Message{User: "U9", Text: "q", Subtype: "channel_join"})
	f.send(t, Message{User: "UA", Text: "q", Subtype: "message_deleted"})
	if f.total() != 0 || f.count(t, `SELECT count(*) FROM agent_run`) != 0 {
		t.Fatalf("calls=%+v", f.calls)
	}
}

func TestWrongAppIgnored(t *testing.T) {
	f := inSetup(t)
	f.member(t, "a@lab.org", "UA", true)
	f.send(t, Message{User: "UA", Text: "q", AppID: "OTHER"})
	if f.total() != 0 || f.count(t, `SELECT count(*) FROM agent_run`) != 0 {
		t.Fatalf("calls=%+v", f.calls)
	}
}

func TestOfflineMachine(t *testing.T) {
	f := inSetup(t)
	f.member(t, "a@lab.org", "UA", false)
	f.send(t, Message{User: "UA", Text: "q", TS: "7.7"})
	p := f.got("chat.postMessage")
	if len(p) != 1 || !strings.Contains(p[0].Form.Get("text"), "Your machine is offline") || p[0].Form.Get("thread_ts") != "" {
		t.Fatalf("%+v", f.calls)
	}
	if f.count(t, `SELECT count(*) FROM agent_run`) != 0 || f.count(t, `SELECT count(*) FROM slack_post`) != 0 {
		t.Fatal("run or post created")
	}
}

func TestFilesNote(t *testing.T) {
	f := inSetup(t)
	u := f.member(t, "a@lab.org", "UA", true)
	// Slack sends a DM with a file as subtype file_share.
	f.send(t, Message{User: "UA", Text: "see file", Subtype: "file_share", HasFiles: true})
	p := f.got("chat.postMessage")
	if len(p) != 1 || !strings.Contains(p[0].Form.Get("text"), "Aristotle can't read Slack files yet; I answered from your text only.") || p[0].Form.Get("thread_ts") != "" {
		t.Fatalf("%+v", f.calls)
	}
	if n := f.count(t, `SELECT count(*) FROM slack_post WHERE status='pending' AND user_id=$1`, u); n != 1 {
		t.Fatalf("posts=%d", n)
	}
}

func TestChannelMentionThreaded(t *testing.T) {
	f := inSetup(t)
	u := f.member(t, "a@lab.org", "UA", false)
	f.send(t, Message{User: "UA", Text: "q", Channel: "C1", ChannelType: "channel", TS: "3.3"})
	if p := f.got("chat.postMessage"); len(p) != 1 || p[0].Form.Get("thread_ts") != "3.3" {
		t.Fatalf("offline reply not threaded: %+v", f.calls)
	}
	if _, err := f.Env.Owner.Exec(context.Background(), `INSERT INTO runtime(workspace_id,owner_id,daemon_id,harness) VALUES($1,$2,'d-UA','claude')`, f.WS, u); err != nil {
		t.Fatal(err)
	}
	f.send(t, Message{User: "UA", Text: "q", Channel: "C1", ChannelType: "channel", TS: "4.4"})
	if n := f.count(t, `SELECT count(*) FROM slack_post WHERE user_id=$1 AND channel='C1' AND thread_ts='4.4'`, u); n != 1 {
		t.Fatalf("posts=%d", n)
	}
}

func TestInThreadDMStaysThreaded(t *testing.T) {
	f := inSetup(t)
	u := f.member(t, "a@lab.org", "UA", true)
	f.send(t, Message{User: "UA", Text: "q", TS: "6.7", ThreadTS: "6.6"})
	if n := f.count(t, `SELECT count(*) FROM slack_post WHERE user_id=$1 AND thread_ts='6.6'`, u); n != 1 {
		t.Fatalf("posts=%d", n)
	}
}

func TestRemovedMemberIsUnbound(t *testing.T) {
	f := inSetup(t)
	u := f.member(t, "a@lab.org", "UA", true)
	if _, err := f.Env.Owner.Exec(context.Background(), `DELETE FROM member WHERE workspace_id=$1 AND user_id=$2`, f.WS, u); err != nil {
		t.Fatal(err)
	}
	f.send(t, Message{User: "UA", Text: "q"})
	e := f.got("chat.postEphemeral")
	if len(e) != 1 || !strings.Contains(e[0].Form.Get("text"), "/app/#/slack/link?") || f.count(t, `SELECT count(*) FROM agent_run`) != 0 {
		t.Fatalf("%+v", f.calls)
	}
}

func TestDeletedChatStartsNewOne(t *testing.T) {
	f := inSetup(t)
	u := f.member(t, "a@lab.org", "UA", true)
	f.send(t, Message{User: "UA", Text: "one"})
	err := f.S.Store.InWorkspace(context.Background(), f.WS, u, func(tx *store.Tx) error {
		var id uuid.UUID
		if err := f.Env.Owner.QueryRow(context.Background(), `SELECT id FROM lab_chat WHERE owner_id=$1`, u).Scan(&id); err != nil {
			return err
		}
		_, err := tx.Q.DeletePrivateLabChat(context.Background(), id)
		return err
	})
	if err != nil {
		t.Fatal(err)
	}
	f.send(t, Message{User: "UA", Text: "two"})
	if n := f.count(t, `SELECT count(*) FROM lab_chat WHERE owner_id=$1 AND deleted_at IS NULL`, u); n != 1 {
		t.Fatalf("live chats=%d", n)
	}
	if n := f.count(t, `SELECT count(*) FROM agent_run WHERE kind='aristotle'`); n != 2 {
		t.Fatalf("runs=%d", n)
	}
}

func TestSlackErrorsAreScrubbed(t *testing.T) {
	f := inSetup(t)
	f.member(t, "a@lab.org", "UA", true)
	f.fail = map[string]string{"reactions.add": "boom xoxb-1-bot xapp-1-A1-2-app"}
	f.n++
	m := Message{EventID: "EvFail", AppID: f.Inst.AppID, User: "UA", Channel: "D1", ChannelType: "im", TS: "1.2", Text: "q"}
	err := f.S.HandleMessage(context.Background(), f.Inst, m)
	if err == nil || strings.Contains(err.Error(), "xoxb-1-bot") || strings.Contains(err.Error(), "xapp-1-A1-2-app") {
		t.Fatalf("err=%v", err)
	}
}

func TestFailedBeforeCommitCanRetry(t *testing.T) {
	f := inSetup(t)
	f.member(t, "a@lab.org", "UA", true)
	f.S.Store.Pool.Close() // the database fails before the question commits
	m := Message{EventID: "EvRetry", AppID: f.Inst.AppID, User: "UA", Channel: "D1", ChannelType: "im", TS: "1.2", Text: "q"}
	if err := f.S.HandleMessage(context.Background(), f.Inst, m); err == nil {
		t.Fatal("want error")
	}
	if f.Env.Redis.Exists(context.Background(), "lyk:slack:event:"+f.Inst.ID.String()+":EvRetry").Val() != 0 {
		t.Fatal("dedupe key kept after failure")
	}
}
