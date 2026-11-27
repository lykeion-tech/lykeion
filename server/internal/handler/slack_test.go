package handler

import (
	"bytes"
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/google/uuid"
	"lykeion/server/internal/slack"
	"lykeion/server/internal/store"
	db "lykeion/server/pkg/db/generated"
)

const (
	testBot = "xoxb-111-secret-bot"
	testApp = "xapp-1-A0APP1-222-secret-app"
)

// useFakeSlack points the server's Slack service at a stand-in for slack.com.
func useFakeSlack(t *testing.T, s *server) {
	t.Helper()
	api := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = r.ParseForm()
		switch {
		case strings.HasSuffix(r.URL.Path, "/auth.test"):
			w.Write([]byte(`{"ok":true,"team_id":"T1","team":"Lab","user_id":"UBOT","bot_id":"B1"}`))
		case strings.HasSuffix(r.URL.Path, "/apps.connections.open"):
			w.Write([]byte(`{"ok":true,"url":"wss://x"}`))
		case strings.HasSuffix(r.URL.Path, "/users.info"):
			// U-<email> is a confirmed Slack profile with that email.
			if u := r.Form.Get("user"); strings.HasPrefix(u, "U-") {
				w.Write([]byte(`{"ok":true,"user":{"id":"` + u + `","is_email_confirmed":true,"profile":{"email":"` + strings.ToUpper(u[2:]) + `"}}}`))
				return
			}
			w.Write([]byte(`{"ok":false,"error":"user_not_found"}`))
		case strings.HasSuffix(r.URL.Path, "/conversations.open"):
			w.Write([]byte(`{"ok":true,"channel":{"id":"D1"}}`))
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(api.Close)
	s.H.Slack = &slack.Service{Store: s.H.Store, Key: bytes.Repeat([]byte{9}, 32), APIURL: api.URL, PublicURL: "https://lyk.test"}
}

func slackBody(agent string) map[string]string {
	return map[string]string{"bot_token": testBot, "app_token": testApp, "drafting_agent_id": agent}
}

func TestOwnerConnectsSlack(t *testing.T) {
	s := newServer(t)
	useFakeSlack(t, s)
	owner := s.signIn(t, "owner@lab.org")
	ws := s.workspace(t, owner, "lab")
	member := s.member(t, owner, ws.ID, "m@lab.org", "member")
	agent := s.stagedAgent(t, owner, ws.ID, "hypotheses")

	if code := s.call(t, "PUT", "/api/slack", member, ws.ID, slackBody(agent), nil); code != 403 {
		t.Fatalf("member put: %d", code)
	}
	if code := s.call(t, "PUT", "/api/slack", owner, ws.ID, map[string]string{"bot_token": testBot, "app_token": testApp}, nil); code != 200 {
		t.Fatalf("owner put: %d", code)
	}
	req, _ := http.NewRequest("GET", s.URL+"/api/slack", nil)
	req.Header.Set("Authorization", "Bearer "+member)
	req.Header.Set("X-Workspace-ID", ws.ID)
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	raw, _ := io.ReadAll(res.Body)
	res.Body.Close()
	if res.StatusCode != 200 || !strings.Contains(string(raw), `"connected":true`) || !strings.Contains(string(raw), `"team_name":"Lab"`) {
		t.Fatalf("get: %d %s", res.StatusCode, raw)
	}
	if strings.Contains(string(raw), "xoxb-") || strings.Contains(string(raw), "xapp-") {
		t.Fatalf("token in response: %s", raw)
	}
	var cipher []byte
	if err := s.Env.Owner.QueryRow(t.Context(), `SELECT bot_token_ciphertext FROM slack_installation WHERE workspace_id=$1`, ws.ID).Scan(&cipher); err != nil || bytes.Contains(cipher, []byte(testBot)) {
		t.Fatalf("bot token stored in the clear: %v", err)
	}
	if code := s.call(t, "PUT", "/api/slack", owner, ws.ID, slackBody(agent), nil); code != 409 {
		t.Fatalf("second put: %d", code)
	}
	if n := s.count(t, `SELECT count(*) FROM event WHERE workspace_id=$1 AND type='slack.changed'`, ws.ID); n != 1 {
		t.Fatalf("slack.changed events: %d", n)
	}
	if code := s.call(t, "PATCH", "/api/slack", owner, ws.ID, map[string]string{"drafting_agent_id": uuid.NewString()}, nil); code != 400 {
		t.Fatalf("patch unknown agent: %d", code)
	}
}

func TestSlackWithoutSecretKey(t *testing.T) {
	s := newServer(t)
	useFakeSlack(t, s)
	s.H.Slack.Key = nil
	owner := s.signIn(t, "owner@lab.org")
	ws := s.workspace(t, owner, "lab")
	agent := s.stagedAgent(t, owner, ws.ID, "hypotheses")
	if code := s.call(t, "PUT", "/api/slack", owner, ws.ID, slackBody(agent), nil); code != 503 {
		t.Fatalf("put without key: %d", code)
	}
	if code := s.call(t, "POST", "/api/slack/link", owner, ws.ID, map[string]string{"token": "t"}, nil); code != 503 {
		t.Fatalf("link without key: %d", code)
	}
	if code := s.call(t, "GET", "/api/slack", owner, ws.ID, nil, nil); code != 200 {
		t.Fatalf("get without key: %d", code)
	}
}

func TestSlackRefusesAPrivateAgentAndBadTokens(t *testing.T) {
	s := newServer(t)
	useFakeSlack(t, s)
	owner := s.signIn(t, "owner@lab.org")
	ws := s.workspace(t, owner, "lab")
	agent := s.stagedAgent(t, owner, ws.ID, "hypotheses")
	s.mustUpdate(t, `UPDATE agent SET visibility='private' WHERE id=$1`, agent)
	if code := s.call(t, "PUT", "/api/slack", owner, ws.ID, slackBody(agent), nil); code != 400 {
		t.Fatalf("private agent: %d", code)
	}
	public := s.stagedAgent(t, owner, ws.ID, "grounding")
	s.mustUpdate(t, `UPDATE agent SET visibility='workspace' WHERE id=$1`, public)
	bad := slackBody(public)
	bad["bot_token"] = "not-a-token"
	if code := s.call(t, "PUT", "/api/slack", owner, ws.ID, bad, nil); code != 400 {
		t.Fatalf("bad token: %d", code)
	}
	if n := s.count(t, `SELECT count(*) FROM slack_installation WHERE workspace_id=$1`, ws.ID); n != 0 {
		t.Fatalf("rows: %d", n)
	}
	s.H.Slack = nil
	if code := s.call(t, "GET", "/api/slack", owner, ws.ID, nil, nil); code != 503 {
		t.Fatalf("unconfigured: %d", code)
	}
}

func TestDisconnectCancelsSlackRuns(t *testing.T) {
	s := newServer(t)
	useFakeSlack(t, s)
	owner := s.signIn(t, "owner@lab.org")
	ws := s.workspace(t, owner, "lab")
	agent := s.stagedAgent(t, owner, ws.ID, "hypotheses")
	if code := s.call(t, "PUT", "/api/slack", owner, ws.ID, slackBody(agent), nil); code != 200 {
		t.Fatalf("put: %d", code)
	}
	var chatID, runID, instID string
	q := func(dst *string, sql string, args ...any) {
		t.Helper()
		if err := s.Env.Owner.QueryRow(t.Context(), sql, args...).Scan(dst); err != nil {
			t.Fatal(err)
		}
	}
	q(&chatID, `INSERT INTO lab_chat(workspace_id,owner_id) SELECT $1,id FROM "user" WHERE email='owner@lab.org' RETURNING id::text`, ws.ID)
	q(&runID, `INSERT INTO agent_run(workspace_id,lab_chat_id,responsible_user_id,requester_id,board_column,round,role,kind,harness,prompt)
 SELECT workspace_id,id,owner_id,owner_id,'',0,'aristotle','aristotle','claude','q' FROM lab_chat WHERE id=$1 RETURNING id::text`, chatID)
	q(&instID, `SELECT id::text FROM slack_installation WHERE workspace_id=$1`, ws.ID)
	s.mustUpdate(t, `INSERT INTO slack_post(workspace_id,installation_id,user_id,channel,kind,agent_run_id)
 SELECT workspace_id,$2,owner_id,'D1','answer',$3 FROM lab_chat WHERE id=$1`, chatID, instID, runID)
	s.mustUpdate(t, `INSERT INTO slack_chat(workspace_id,installation_id,user_id,channel,lab_chat_id)
 SELECT workspace_id,$2,owner_id,'D1',id FROM lab_chat WHERE id=$1`, chatID, instID)

	if code := s.call(t, "DELETE", "/api/slack", owner, ws.ID, nil, nil); code != 204 {
		t.Fatalf("delete: %d", code)
	}
	var status string
	q(&status, `SELECT status FROM agent_run WHERE id=$1`, runID)
	if status != "cancelled" {
		t.Fatalf("run is %s", status)
	}
	for table, want := range map[string]int{"slack_installation": 0, "slack_post": 0, "slack_chat": 0, "lab_chat": 1} {
		if n := s.count(t, `SELECT count(*) FROM `+table+` WHERE workspace_id=$1`, ws.ID); n != want {
			t.Fatalf("%s: %d rows, want %d", table, n, want)
		}
	}
	if n := s.count(t, `SELECT count(*) FROM event WHERE workspace_id=$1 AND type='slack.changed'`, ws.ID); n != 2 {
		t.Fatalf("slack.changed events: %d", n)
	}
}

func TestSlackBindingIsTheCallersOwn(t *testing.T) {
	s := newServer(t)
	useFakeSlack(t, s)
	owner := s.signIn(t, "owner@lab.org")
	ws := s.workspace(t, owner, "lab")
	member := s.member(t, owner, ws.ID, "m@lab.org", "member")
	agent := s.stagedAgent(t, owner, ws.ID, "hypotheses")
	s.call(t, "PUT", "/api/slack", owner, ws.ID, slackBody(agent), nil)
	for _, email := range []string{"owner@lab.org", "m@lab.org"} {
		s.mustUpdate(t, `INSERT INTO slack_binding(workspace_id,installation_id,slack_user_id,user_id)
 SELECT i.workspace_id,i.id,'U-'||$1::text,u.id FROM slack_installation i, "user" u WHERE u.email=$1 AND i.workspace_id=$2`, email, ws.ID)
	}
	var view struct {
		MyBinding *struct {
			SlackUserID string `json:"slack_user_id"`
		} `json:"my_binding"`
	}
	s.call(t, "GET", "/api/slack", member, ws.ID, nil, &view)
	if view.MyBinding == nil || view.MyBinding.SlackUserID != "U-m@lab.org" {
		t.Fatalf("binding: %+v", view.MyBinding)
	}
	if code := s.call(t, "DELETE", "/api/slack/binding", member, ws.ID, nil, nil); code != 204 {
		t.Fatalf("unlink: %d", code)
	}
	if n := s.count(t, `SELECT count(*) FROM slack_binding WHERE workspace_id=$1`, ws.ID); n != 1 {
		t.Fatalf("bindings left: %d", n)
	}
}

func TestSlackIsolation(t *testing.T) {
	s := newServer(t)
	useFakeSlack(t, s)
	a := s.signIn(t, "a@lab.org")
	wa := s.workspace(t, a, "lab-a")
	b := s.signIn(t, "b@lab.org")
	wb := s.workspace(t, b, "lab-b")
	agent := s.stagedAgent(t, a, wa.ID, "hypotheses")
	if code := s.call(t, "PUT", "/api/slack", a, wa.ID, slackBody(agent), nil); code != 200 {
		t.Fatalf("put: %d", code)
	}
	var view struct {
		Connected bool `json:"connected"`
	}
	if code := s.call(t, "GET", "/api/slack", b, wb.ID, nil, &view); code != 200 || view.Connected {
		t.Fatalf("B sees A's Slack: %d %+v", code, view)
	}
	if code := s.call(t, "DELETE", "/api/slack", b, wb.ID, nil, nil); code != 404 {
		t.Fatalf("B delete: %d", code)
	}
	if code := s.call(t, "DELETE", "/api/slack", b, wa.ID, nil, nil); code != 403 && code != 404 {
		t.Fatalf("B deletes A's: %d", code)
	}
	if n := s.count(t, `SELECT count(*) FROM slack_installation WHERE workspace_id=$1`, wa.ID); n != 1 {
		t.Fatal("A's installation was removed")
	}
	// The same Slack app cannot join a second workspace.
	agentB := s.stagedAgent(t, b, wb.ID, "hypotheses")
	if code := s.call(t, "PUT", "/api/slack", b, wb.ID, slackBody(agentB), nil); code != 409 {
		t.Fatalf("same app in B: %d", code)
	}
}

func TestSlackLinkRoute(t *testing.T) {
	s := newServer(t)
	useFakeSlack(t, s)
	owner := s.signIn(t, "owner@lab.org")
	ws := s.workspace(t, owner, "lab")
	member := s.member(t, owner, ws.ID, "m@lab.org", "member")
	agent := s.stagedAgent(t, owner, ws.ID, "hypotheses")
	s.call(t, "PUT", "/api/slack", owner, ws.ID, slackBody(agent), nil)
	mint := func(slackUser string) string {
		var raw string
		err := s.H.Store.InWorkspace(t.Context(), uuid.MustParse(ws.ID), uuid.Nil, func(tx *store.Tx) error {
			inst, err := tx.Q.GetSlackInstallation(t.Context())
			if err != nil {
				return err
			}
			u, err := s.H.Slack.MintLink(t.Context(), tx, inst, slackUser)
			raw = u[strings.Index(u, "token=")+6:]
			return err
		})
		if err != nil {
			t.Fatal(err)
		}
		return raw
	}
	var out struct {
		SlackUserID string `json:"slack_user_id"`
		TeamName    string `json:"team_name"`
		Error       string `json:"error"`
	}
	// someone else's email: 400 with the message, and the token is spent
	raw := mint("U-owner@lab.org")
	if code := s.call(t, "POST", "/api/slack/link", member, ws.ID, map[string]string{"token": raw}, &out); code != 400 ||
		out.Error != "Your Slack email doesn't match your Lykeion email." {
		t.Fatalf("mismatch: %d %+v", code, out)
	}
	out.Error = ""
	if code := s.call(t, "POST", "/api/slack/link", owner, ws.ID, map[string]string{"token": raw}, &out); code != 400 ||
		out.Error != "This link has expired. Message Aristotle in Slack for a new one." {
		t.Fatalf("spent: %d %+v", code, out)
	}
	out.Error = ""
	if code := s.call(t, "POST", "/api/slack/link", member, ws.ID, map[string]string{"token": mint("U-m@lab.org")}, &out); code != 200 ||
		out.SlackUserID != "U-m@lab.org" || out.TeamName != "Lab" {
		t.Fatalf("link: %d %+v", code, out)
	}
	if n := s.count(t, `SELECT count(*) FROM slack_binding WHERE workspace_id=$1`, ws.ID); n != 1 {
		t.Fatalf("bindings: %d", n)
	}
	// connect + link: other tabs refresh the binding
	if n := s.count(t, `SELECT count(*) FROM event WHERE workspace_id=$1 AND type='slack.changed'`, ws.ID); n != 2 {
		t.Fatalf("slack.changed events: %d", n)
	}
}

// slackCmdFixture connects Slack and binds UOWNER to the owner.
func slackCmdFixture(t *testing.T) (s *server, svc *slack.Service, inst db.SlackInstallation, owner, ws, agent string) {
	t.Helper()
	s = newServer(t)
	useFakeSlack(t, s)
	owner = s.signIn(t, "owner@lab.org")
	wsr := s.workspace(t, owner, "lab")
	ws = wsr.ID
	agent = s.stagedAgent(t, owner, ws, "hypotheses")
	if code := s.call(t, "PUT", "/api/slack", owner, ws, slackBody(agent), nil); code != 200 {
		t.Fatalf("connect: %d", code)
	}
	w, u := uuid.MustParse(ws), uuid.MustParse(s.userID(t, owner))
	err := s.H.Store.InWorkspace(t.Context(), w, u, func(tx *store.Tx) (err error) {
		if inst, err = tx.Q.GetSlackInstallation(t.Context()); err != nil {
			return err
		}
		_, err = tx.Q.CreateSlackBinding(t.Context(), db.CreateSlackBindingParams{InstallationID: inst.ID, SlackUserID: "UOWNER", UserID: u})
		return err
	})
	if err != nil {
		t.Fatal(err)
	}
	svc = s.H.Slack
	svc.Drafter = s.H
	svc.AgentRuns = s.H.AgentRuns
	svc.DeleteChat = func(ctx context.Context, tx *store.Tx, user uuid.UUID, chat db.LabChat) error {
		return DeleteLabChatInTx(ctx, tx, s.H.AgentRuns, user, chat)
	}
	return
}

func TestSlackClearDeletesChatAndCancelsRun(t *testing.T) {
	s, svc, inst, owner, ws, _ := slackCmdFixture(t)
	chat := newLabChat(t, s, owner, ws)
	seedHistoricalLabQuestion(t, s, chat, "private question", "")
	if _, err := s.Env.Owner.Exec(t.Context(), `UPDATE agent_run SET status='queued' WHERE lab_chat_id=$1`, chat); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Env.Owner.Exec(t.Context(), `INSERT INTO slack_chat(workspace_id,installation_id,user_id,channel,thread_ts,lab_chat_id) VALUES($1,$2,$3,'D1','',$4)`, ws, inst.ID, s.userID(t, owner), chat); err != nil {
		t.Fatal(err)
	}
	got, err := svc.HandleAction(t.Context(), inst, slack.Action{ID: slack.ClearConfirmAction, User: "UOWNER"})
	if err != nil || got != "Conversation deleted." {
		t.Fatalf("%q %v", got, err)
	}
	var deleted bool
	var status string
	var mapped, messages int
	if err := s.Env.Owner.QueryRow(t.Context(), `SELECT (SELECT deleted_at IS NOT NULL FROM lab_chat WHERE id=$1),
 (SELECT status FROM agent_run WHERE lab_chat_id=$1),
 (SELECT count(*) FROM slack_chat WHERE lab_chat_id=$1),
 (SELECT count(*) FROM lab_message WHERE chat_id=$1)`, chat).Scan(&deleted, &status, &mapped, &messages); err != nil {
		t.Fatal(err)
	}
	if !deleted || status != "cancelled" || mapped != 0 || messages != 0 {
		t.Fatalf("deleted=%v status=%s mapped=%d messages=%d", deleted, status, mapped, messages)
	}
}

func TestSlackStudyRequiresTheResearchersRuntime(t *testing.T) {
	s, svc, inst, _, _, _ := slackCmdFixture(t)
	text, _, err := svc.HandleCommand(t.Context(), inst, slack.Command{Name: "/to-do", Text: "x", User: "UOWNER", Channel: "C1"})
	if err != nil || text != "The agent's CLI is offline. Connect a machine and try again." {
		t.Fatalf("%q %v", text, err)
	}
	var n int
	if err := s.Env.Owner.QueryRow(t.Context(), `SELECT count(*) FROM slack_post`).Scan(&n); err != nil || n != 0 {
		t.Fatalf("posts=%d %v", n, err)
	}
}

func TestSlackStudyUsesAristotleAndSavesToTodo(t *testing.T) {
	s, svc, inst, owner, ws, oldAgent := slackCmdFixture(t)
	s.mustUpdate(t, `UPDATE agent SET archived_at=now() WHERE id=$1`, oldAgent)
	personal := s.personalToken(t, owner)
	if code := s.call(t, "POST", "/api/daemon/register", personal, "", map[string]any{"workspace_id": ws, "daemon_id": "slack-study", "harnesses": []string{"claude", "codex"}}, nil); code != 200 {
		t.Fatalf("register: %d", code)
	}
	s.mustUpdate(t, `UPDATE runtime SET unhealthy_reason='cli_broken' WHERE workspace_id=$1 AND harness='claude'`, ws)
	text, _, err := svc.HandleCommand(t.Context(), inst, slack.Command{Name: "/to-do", Text: "Study shade and heat", User: "UOWNER", Channel: "C1"})
	if err != nil || text != "Drafting your study. I'll DM you the link." {
		t.Fatalf("command: %q %v", text, err)
	}
	ctx := t.Context()
	user := uuid.MustParse(s.userID(t, owner))
	var request, run, agentOwner uuid.UUID
	var agentKey, harness string
	err = s.Env.Owner.QueryRow(ctx, `SELECT p.autoresearch_id, r.agent_run_id, a.owner_id, a.built_in_key, execution.harness
		FROM slack_post p JOIN autoresearch_run r USING (workspace_id, autoresearch_id)
		JOIN autoresearch ar ON (ar.workspace_id, ar.id) = (p.workspace_id, p.autoresearch_id)
		JOIN agent a ON a.id=ar.assignee_id JOIN agent_run execution ON execution.id=r.agent_run_id
		WHERE p.workspace_id=$1 AND p.kind='draft'`, ws).Scan(&request, &run, &agentOwner, &agentKey, &harness)
	if err != nil || agentKey != "aristotle" || agentOwner != user || harness != "codex" {
		t.Fatalf("draft run: %s %s %s %v", agentKey, agentOwner, harness, err)
	}
	const question = "How does shade affect neighborhood heat?"
	err = s.H.Store.InWorkspace(ctx, uuid.MustParse(ws), user, func(tx *store.Tx) error {
		_, err := s.H.Autoresearch.OpenCardForAgentRun(ctx, tx, run, "Shade and heat", question)
		return err
	})
	if err != nil {
		t.Fatal(err)
	}
	var proposals struct{ Items []store.StudyProposal }
	if code := s.call(t, "GET", "/api/study-proposals", owner, ws, nil, &proposals); code != 200 || len(proposals.Items) != 1 {
		t.Fatalf("proposals: %d %+v", code, proposals)
	}
	p := proposals.Items[0]
	if p.ID != request || p.OwnerID != user || p.Description != question || p.Stage != "grounding" || p.Assignee.Type != "team" {
		t.Fatalf("proposal: %+v", p)
	}
	if n := s.count(t, `SELECT count(*) FROM card WHERE workspace_id=$1`, ws); n != 0 {
		t.Fatalf("unexpected stage cards: %d", n)
	}
}

func TestReconnectRevokedSlack(t *testing.T) {
	s := newServer(t)
	useFakeSlack(t, s)
	owner := s.signIn(t, "owner@lab.org")
	ws := s.workspace(t, owner, "lab")
	agent := s.stagedAgent(t, owner, ws.ID, "hypotheses")
	if code := s.call(t, "PUT", "/api/slack", owner, ws.ID, slackBody(agent), nil); code != 200 {
		t.Fatalf("put: %d", code)
	}
	s.mustUpdate(t, `INSERT INTO slack_binding(workspace_id,installation_id,slack_user_id,user_id)
 SELECT i.workspace_id,i.id,'UOWNER',u.id FROM slack_installation i, "user" u WHERE u.email='owner@lab.org' AND i.workspace_id=$1`, ws.ID)
	s.mustUpdate(t, `UPDATE slack_installation SET status='revoked' WHERE workspace_id=$1`, ws.ID)
	var view struct {
		Status string `json:"status"`
	}
	// The same app: replaced in place, the binding survives.
	if code := s.call(t, "PUT", "/api/slack", owner, ws.ID, slackBody(agent), &view); code != 200 || view.Status != "active" {
		t.Fatalf("reconnect: %d %+v", code, view)
	}
	if n := s.count(t, `SELECT count(*) FROM slack_binding WHERE workspace_id=$1`, ws.ID); n != 1 {
		t.Fatalf("bindings: %d", n)
	}
	if code := s.call(t, "PUT", "/api/slack", owner, ws.ID, slackBody(agent), nil); code != 409 {
		t.Fatalf("active put: %d", code)
	}
	// Another app: the revoked installation is disconnected first.
	s.mustUpdate(t, `UPDATE slack_installation SET status='revoked' WHERE workspace_id=$1`, ws.ID)
	other := slackBody(agent)
	other["app_token"] = "xapp-1-A0APP2-333-secret-app"
	if code := s.call(t, "PUT", "/api/slack", owner, ws.ID, other, &view); code != 200 || view.Status != "active" {
		t.Fatalf("other app: %d %+v", code, view)
	}
	if n := s.count(t, `SELECT count(*) FROM slack_installation WHERE workspace_id=$1 AND app_id='A0APP2' AND status='active'`, ws.ID); n != 1 {
		t.Fatalf("installations: %d", n)
	}
	if n := s.count(t, `SELECT count(*) FROM slack_binding WHERE workspace_id=$1`, ws.ID); n != 0 {
		t.Fatalf("bindings kept across apps: %d", n)
	}
}
