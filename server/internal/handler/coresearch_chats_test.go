package handler

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"

	"lykeion/server/internal/coresearch"
	"lykeion/server/internal/service"
	"lykeion/server/internal/sources"
	"lykeion/server/pkg/agent"
	db "lykeion/server/pkg/db/generated"
)

func csChat(t *testing.T, s *server, a, ws, p string) (string, db.CoresearchChat) {
	t.Helper()
	var c db.CoresearchChat
	path := csProjects + "/" + p + "/chats"
	if code := s.call(t, "POST", path, a, ws, map[string]string{"cli": "claude"}, &c); code != 201 {
		t.Fatalf("chat %d", code)
	}
	return path + "/" + c.ID.String(), c
}
func csTurn(t *testing.T, s *server, a, ws, path string) db.AgentRun {
	t.Helper()
	var r db.AgentRun
	if code := s.call(t, "POST", path+"/turns", a, ws, map[string]string{"body": "Research this"}, &r); code != 201 {
		t.Fatalf("turn %d", code)
	}
	return r
}
func csRegister(t *testing.T, s *server, a, ws, daemon, harness string) service.RuntimeRef {
	t.Helper()
	tok := s.personalToken(t, a)
	var rt []db.Runtime
	if code := s.call(t, "POST", "/api/daemon/register", tok, "", map[string]any{"workspace_id": ws, "daemon_id": daemon, "device_name": daemon, "platform": map[string]string{"os": "linux"}, "harnesses": []string{harness}}, &rt); code != 200 || len(rt) != 1 {
		t.Fatalf("register %d %+v", code, rt)
	}
	return service.RuntimeRef{WorkspaceID: uuid.MustParse(ws), RuntimeID: rt[0].ID}
}
func csClaim(t *testing.T, s *server, a string, ref service.RuntimeRef) []service.Claimed {
	t.Helper()
	out, e := s.H.AgentRuns.Claim(t.Context(), uuid.MustParse(s.userID(t, a)), []service.RuntimeRef{ref}, 1, service.ClaimOptions{GuestIsolation: "os_sandbox", CoresearchOnly: true})
	if e != nil {
		t.Fatal(e)
	}
	return out
}
func TestOnlyChatOwnerSendsTurns(t *testing.T) {
	s, a, b, _, ws, p := csFixture(t)
	csAdd(t, s, a, b, ws, p.ID, "member")
	path, _ := csChat(t, s, a, ws, p.ID)
	if code := s.call(t, "POST", path+"/turns", b, ws, map[string]string{"body": "x"}, nil); code != 403 {
		t.Fatal(code)
	}
	if code := s.call(t, "POST", path+"/comments", b, ws, map[string]string{"body": "hello"}, nil); code != 201 {
		t.Fatal(code)
	}
}
func TestTurnIsCoresearchRun(t *testing.T) {
	s, a, _, _, ws, p := csFixture(t)
	path, c := csChat(t, s, a, ws, p.ID)
	r := csTurn(t, s, a, ws, path)
	if r.Kind != "coresearch" || r.ContinuityScope != "coresearch:"+c.ID.String() || r.CardID != nil || r.Harness != "claude" || r.Role != "coresearch" || r.MaxAttempts != 2 || r.RequestedModel != "" || r.Skill != "" {
		t.Fatalf("%+v", r)
	}
}
func TestOneTurnAtATime(t *testing.T) {
	s, a, _, _, ws, p := csFixture(t)
	path, _ := csChat(t, s, a, ws, p.ID)
	csTurn(t, s, a, ws, path)
	if code := s.call(t, "POST", path+"/turns", a, ws, map[string]string{"body": "second"}, nil); code != 409 {
		t.Fatal(code)
	}
}
func TestCoresearchClaimOnlyOwnersDaemon(t *testing.T) {
	s, a, b, _, ws, p := csFixture(t)
	csAdd(t, s, a, b, ws, p.ID, "member")
	path, c := csChat(t, s, a, ws, p.ID)
	run := csTurn(t, s, a, ws, path)
	if _, e := s.Env.Owner.Exec(t.Context(), `UPDATE agent_run SET addon_mcp='["invalid-card-addon"]'::jsonb WHERE id=$1`, run.ID); e != nil {
		t.Fatal(e)
	}
	bref := csRegister(t, s, b, ws, "b", "claude")
	if _, e := s.Env.Owner.Exec(t.Context(), `UPDATE runtime SET visibility='workspace' WHERE id=$1`, bref.RuntimeID); e != nil {
		t.Fatal(e)
	}
	if got := csClaim(t, s, b, bref); len(got) != 0 {
		t.Fatalf("unexpected claimed runs: %d", len(got))
	}
	wrong := csRegister(t, s, a, ws, "a-codex", "codex")
	if got := csClaim(t, s, a, wrong); len(got) != 0 {
		t.Fatalf("unexpected claimed runs: %d", len(got))
	}
	ref := csRegister(t, s, a, ws, "a-claude", "claude")
	got := csClaim(t, s, a, ref)
	if len(got) != 1 {
		t.Fatalf("unexpected claimed runs: %d", len(got))
	}
	var daemon string
	if e := s.Env.Owner.QueryRow(t.Context(), `SELECT daemon_id FROM coresearch_chat WHERE id=$1`, c.ID).Scan(&daemon); e != nil || daemon != "a-claude" {
		t.Fatalf("%s %v", daemon, e)
	}
	if got[0].CallToken != "" || len(got[0].Connectors) != 0 || len(got[0].WorkspaceConnectors) != 0 || len(got[0].Skills) == 0 || len(got[0].MCP) != 0 || len(got[0].Attachments) != 0 || len(got[0].Services) != 0 || got[0].Coresearch == nil {
		t.Fatalf("run %s capability counts: connectors=%d workspace_connectors=%d skills=%d mcp=%d attachments=%d services=%d coresearch=%t door=%t", got[0].AgentRun.ID, len(got[0].Connectors), len(got[0].WorkspaceConnectors), len(got[0].Skills), len(got[0].MCP), len(got[0].Attachments), len(got[0].Services), got[0].Coresearch != nil, got[0].CallToken != "")
	}
	for _, skill := range got[0].Skills {
		if skill.Name == "" || len(skill.Folder) == 0 {
			t.Fatalf("run %s has incomplete enabled skill %q (%d bytes)", got[0].AgentRun.ID, skill.Name, len(skill.Folder))
		}
	}
	if e := s.H.AgentRuns.Cancel(t.Context(), uuid.MustParse(ws), uuid.MustParse(s.userID(t, a)), got[0].AgentRun.ID); e != nil {
		t.Fatal(e)
	}
	csTurn(t, s, a, ws, path)
	second := csRegister(t, s, a, ws, "a-second", "claude")
	if got := csClaim(t, s, a, second); len(got) != 0 {
		t.Fatalf("unexpected claimed runs: %d", len(got))
	}
}

// A turn frozen to a model only goes to a runtime whose CLI lists that model.
func TestCoresearchModelTurnClaimedOnlyWhereModelListed(t *testing.T) {
	s, a, _, _, ws, p := csFixture(t)
	path, _ := csChat(t, s, a, ws, p.ID)
	run := csTurn(t, s, a, ws, path)
	if _, e := s.Env.Owner.Exec(t.Context(), `UPDATE agent_run SET requested_model='opus-frozen' WHERE id=$1`, run.ID); e != nil {
		t.Fatal(e)
	}
	without := csRegister(t, s, a, ws, "a-without", "claude")
	with := csRegister(t, s, a, ws, "a-with", "claude")
	if _, e := s.Env.Owner.Exec(t.Context(), `UPDATE runtime SET models='[{"value":"opus-frozen"}]'::jsonb WHERE id=$1`, with.RuntimeID); e != nil {
		t.Fatal(e)
	}
	if got := csClaim(t, s, a, without); len(got) != 0 {
		t.Fatalf("runtime without the model claimed %d runs", len(got))
	}
	if got := csClaim(t, s, a, with); len(got) != 1 || got[0].AgentRun.ID != run.ID {
		t.Fatalf("runtime listing the model claimed %d runs", len(got))
	}
}
func TestCoresearchUnlistedModelRunsWithCLIDefault(t *testing.T) {
	s, a, _, _, ws, p := csFixture(t)
	path, _ := csChat(t, s, a, ws, p.ID)
	run := csTurn(t, s, a, ws, path)
	if _, e := s.Env.Owner.Exec(t.Context(), `UPDATE agent_run SET requested_model='retired-model' WHERE id=$1`, run.ID); e != nil {
		t.Fatal(e)
	}
	ref := csRegister(t, s, a, ws, "only", "claude")
	got := csClaim(t, s, a, ref)
	if len(got) != 1 || got[0].AgentRun.ID != run.ID {
		t.Fatalf("no runtime lists the model, yet the turn was not claimed: %d", len(got))
	}
	if got[0].AgentRun.RequestedModel != "" {
		t.Fatalf("turn should run on the CLI default, got model %q", got[0].AgentRun.RequestedModel)
	}
}
func TestReleaseUnbinds(t *testing.T) {
	s, a, _, _, ws, p := csFixture(t)
	path, c := csChat(t, s, a, ws, p.ID)
	r := csTurn(t, s, a, ws, path)
	ref := csRegister(t, s, a, ws, "first", "claude")
	csClaim(t, s, a, ref)
	if code := s.call(t, "POST", path+"/release", a, ws, nil, nil); code != 409 {
		t.Fatal(code)
	}
	if code := s.call(t, "POST", path+"/turns/"+r.ID.String()+"/cancel", a, ws, nil, nil); code != 204 {
		t.Fatal(code)
	}
	if code := s.call(t, "POST", path+"/release", a, ws, nil, nil); code != 204 {
		t.Fatal(code)
	}
	var d *string
	s.Env.Owner.QueryRow(t.Context(), `SELECT daemon_id FROM coresearch_chat WHERE id=$1`, c.ID).Scan(&d)
	if d != nil {
		t.Fatal(d)
	}
}
func TestReplyWrittenOnce(t *testing.T) {
	s, a, _, _, ws, p := csFixture(t)
	path, _ := csChat(t, s, a, ws, p.ID)
	r := csTurn(t, s, a, ws, path)
	r.Status = "completed"
	r.Attempt = 2
	if _, e := s.Env.Owner.Exec(t.Context(), `INSERT INTO agent_run_message(workspace_id,agent_run_id,attempt,seq,type,content) VALUES($1,$2,1,1,'text','"old"'),($1,$2,2,1,'text','"first"'),($1,$2,2,2,'text','"final"')`, ws, r.ID); e != nil {
		t.Fatal(e)
	}
	csTerminal(t, s, r)
	hook := (&coresearch.Service{Store: s.H.Store}).OnSettlement(nil)
	for i := 0; i < 2; i++ {
		if e := hook(t.Context(), service.Settlement{ID: r.ID, AgentRun: r}); e != nil {
			t.Fatal(e)
		}
	}
	var msgs []db.CoresearchMessage
	s.call(t, "GET", path+"/messages", a, ws, nil, &msgs)
	if len(msgs) != 2 || msgs[1].Body != "final" || msgs[1].Kind != "reply" {
		t.Fatal(msgs)
	}
}
func TestFailurePosted(t *testing.T) {
	s, a, _, _, ws, p := csFixture(t)
	path, _ := csChat(t, s, a, ws, p.ID)
	r := csTurn(t, s, a, ws, path)
	reason := "agent_error"
	r.Status = "failed"
	r.FailureReason = &reason
	r.Error = "details"
	csTerminal(t, s, r)
	r.Error = "FORGED"
	if e := (&coresearch.Service{Store: s.H.Store}).OnSettlement(nil)(context.Background(), service.Settlement{ID: r.ID, AgentRun: r}); e != nil {
		t.Fatal(e)
	}
	var msgs []db.CoresearchMessage
	s.call(t, "GET", path+"/messages", a, ws, nil, &msgs)
	if len(msgs) != 2 || msgs[1].Kind != "failure" || !strings.Contains(msgs[1].Body, reason) || (!strings.Contains(msgs[1].Body, "details") || strings.Contains(msgs[1].Body, "FORGED")) {
		t.Fatal(msgs)
	}
}
func TestGenericRunRoutesHideCoresearch(t *testing.T) {
	s, a, b, _, ws, p := csFixture(t)
	path, _ := csChat(t, s, a, ws, p.ID)
	r := csTurn(t, s, a, ws, path)
	for _, tok := range []string{a, b} {
		for _, v := range []struct{ method, suffix string }{{"GET", ""}, {"GET", "/messages"}, {"POST", "/cancel"}} {
			if code := s.call(t, v.method, "/api/agent-runs/"+r.ID.String()+v.suffix, tok, ws, nil, nil); code != 404 {
				t.Fatalf("%s %s %d", v.method, v.suffix, code)
			}
		}
	}
}
func TestTokenScopeIsCoresearchOnly(t *testing.T) {
	s, a, _, _, ws, p := csFixture(t)
	path, _ := csChat(t, s, a, ws, p.ID)
	csTurn(t, s, a, ws, path)
	ref := csRegister(t, s, a, ws, "a", "claude")
	got := csClaim(t, s, a, ref)
	if len(got) != 1 {
		t.Fatal(got)
	}
	ref.Attempt = got[0].AgentRun.Attempt
	if e := s.H.AgentRuns.Start(t.Context(), uuid.MustParse(s.userID(t, a)), ref, got[0].AgentRun.ID); e != nil {
		t.Fatal(e)
	}
	if code := s.call(t, "GET", "/api/role/records", got[0].Token, "", nil, nil); code != 403 {
		t.Fatal(code)
	}
	if scopes := service.Scopes("coresearch"); len(scopes) != 1 || scopes[0] != service.ScopeCoresearch {
		t.Fatal(scopes)
	}
}
func TestCoresearchTokenReachesSources(t *testing.T) {
	s, a, b, _, ws, p := csFixture(t)
	path, _ := csChat(t, s, a, ws, p.ID)
	csTurn(t, s, a, ws, path)
	ref := csRegister(t, s, a, ws, "a", "claude")
	got := csClaim(t, s, a, ref)
	if len(got) != 1 {
		t.Fatal(got)
	}
	ref.Attempt = got[0].AgentRun.Attempt
	if e := s.H.AgentRuns.Start(t.Context(), uuid.MustParse(s.userID(t, a)), ref, got[0].AgentRun.ID); e != nil {
		t.Fatal(e)
	}
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`{"results":[]}`))
	}))
	defer upstream.Close()
	s.H.Sources = (&sources.Hosted{Store: s.H.Store, Conn: s.H.Connectors, OA: sources.OpenAlex{Base: upstream.URL}}).Handler()
	srv := httptest.NewServer(NewRouter(s.H))
	defer srv.Close()
	req, e := http.NewRequest("POST", srv.URL+"/mcp/sources", strings.NewReader(`{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":"search_literature","arguments":{"database":"openalex","query":"test"}}}`))
	if e != nil {
		t.Fatal(e)
	}
	req.Header.Set("Authorization", "Bearer "+got[0].Token)
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json, text/event-stream")
	resp, e := http.DefaultClient.Do(req)
	if e != nil {
		t.Fatal(e)
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		t.Fatal(resp.StatusCode)
	}
	var n int
	if e := s.Env.Owner.QueryRow(t.Context(), `SELECT count(*) FROM event WHERE workspace_id=$1 AND type='sources.called' AND payload->>'agent_run_id'=$2`, ws, got[0].AgentRun.ID.String()).Scan(&n); e != nil || n != 1 {
		t.Fatalf("source did not record: %d %v", n, e)
	}

	listReq, e := http.NewRequest("POST", srv.URL+"/mcp/sources", strings.NewReader(`{"jsonrpc":"2.0","id":2,"method":"tools/list","params":{}}`))
	if e != nil {
		t.Fatal(e)
	}
	listReq.Header = req.Header.Clone()
	listed, e := http.DefaultClient.Do(listReq)
	if e != nil {
		t.Fatal(e)
	}
	data, e := io.ReadAll(listed.Body)
	listed.Body.Close()
	if e != nil {
		t.Fatal(e)
	}
	toolNames := map[string]bool{}
	for _, line := range strings.Split(string(data), "\n") {
		if !strings.HasPrefix(line, "data: ") {
			continue
		}
		var message struct {
			Result struct {
				Tools []struct {
					Name string `json:"name"`
				} `json:"tools"`
			} `json:"result"`
		}
		if e := json.Unmarshal([]byte(strings.TrimPrefix(line, "data: ")), &message); e != nil {
			t.Fatal(e)
		}
		for _, tool := range message.Result.Tools {
			toolNames[tool.Name] = true
		}
	}
	if !toolNames["list_connectors"] || !toolNames["fetch"] {
		t.Fatalf("Coresearch built-in connector tools missing: %v", toolNames)
	}
	var payload map[string]any
	var raw []byte
	var eventID int64
	if e = s.Env.Owner.QueryRow(t.Context(), `SELECT id,payload FROM event WHERE workspace_id=$1 AND type='sources.called' AND payload->>'agent_run_id'=$2`, ws, got[0].AgentRun.ID.String()).Scan(&eventID, &raw); e != nil {
		t.Fatal(e)
	}
	if e = json.Unmarshal(raw, &payload); e != nil {
		t.Fatal(e)
	}
	if len(payload) != 3 || payload["project_id"] != p.ID {
		t.Fatalf("source event leaks content: %+v", payload)
	}

	conn := dial(t, s, b, ws, eventID-1)
	conn.SetReadDeadline(time.Now().Add(3 * time.Second))
	var observed struct{ Event db.Event }
	if e = conn.ReadJSON(&observed); e != nil {
		t.Fatal(e)
	}
	if observed.Event.Type != "sources.called" || !json.Valid(observed.Event.Payload) || strings.Contains(string(observed.Event.Payload), `"query"`) || strings.Contains(string(observed.Event.Payload), `"results"`) {
		t.Fatalf("non-project member event leak: %+v", observed)
	}

}
func csTerminal(t *testing.T, s *server, r db.AgentRun) {
	t.Helper()
	if _, e := s.Env.Owner.Exec(t.Context(), `UPDATE agent_run SET status=$2,attempt=$3,error=$4,failure_reason=$5 WHERE id=$1`, r.ID, r.Status, r.Attempt, r.Error, r.FailureReason); e != nil {
		t.Fatal(e)
	}
	if _, e := s.Env.Owner.Exec(t.Context(), `INSERT INTO agent_run_settlement(workspace_id,agent_run_id,result) SELECT workspace_id,id,to_jsonb(agent_run) FROM agent_run WHERE id=$1`, r.ID); e != nil {
		t.Fatal(e)
	}
}
func TestCoresearchRevokedOwnerSettlement(t *testing.T) {
	s, a, b, _, ws, p := csFixture(t)
	csAdd(t, s, a, b, ws, p.ID, "owner")
	path, _ := csChat(t, s, a, ws, p.ID)
	r := csTurn(t, s, a, ws, path)
	r.Status = "cancelled"
	csTerminal(t, s, r)
	if code := s.call(t, "DELETE", csProjects+"/"+p.ID+"/members/"+s.userID(t, a), b, ws, nil, nil); code != 204 {
		t.Fatal(code)
	}
	hook := (&coresearch.Service{Store: s.H.Store}).OnSettlement(nil)
	for i := 0; i < 2; i++ {
		if e := hook(t.Context(), service.Settlement{ID: r.ID, AgentRun: r}); e != nil {
			t.Fatal(e)
		}
	}
	var msgs []db.CoresearchMessage
	if code := s.call(t, "GET", path+"/messages", b, ws, nil, &msgs); code != 200 || len(msgs) != 2 || msgs[1].Kind != "failure" {
		t.Fatalf("%d %+v", code, msgs)
	}
	var n int
	s.Env.Owner.QueryRow(t.Context(), `SELECT count(*) FROM event WHERE type='coresearch.message.created' AND payload->>'message_id'=$1`, msgs[1].ID.String()).Scan(&n)
	if n != 1 {
		t.Fatal(n)
	}
}
func TestCoresearchSettlementHelperAuthority(t *testing.T) {
	s, a, _, _, ws, p := csFixture(t)
	path, _ := csChat(t, s, a, ws, p.ID)
	r := csTurn(t, s, a, ws, path)
	refuse := func(workspace, who uuid.UUID) {
		t.Helper()
		tx, e := s.Env.App.Begin(t.Context())
		if e != nil {
			t.Fatal(e)
		}
		defer tx.Rollback(t.Context())
		if _, e = tx.Exec(t.Context(), `SELECT set_config('app.workspace_id',$1,true),set_config('app.user_id',$2,true)`, workspace.String(), who.String()); e != nil {
			t.Fatal(e)
		}
		if _, e = tx.Exec(t.Context(), `CREATE TEMP TABLE agent_run AS SELECT $1::uuid AS id,'completed'::text AS status`, r.ID); e != nil {
			t.Fatal(e)
		}
		rows, e := tx.Query(t.Context(), `SELECT * FROM public.coresearch_settle($1)`, r.ID)
		if e == nil {
			rows.Next()
			e = rows.Err()
			rows.Close()
		}
		if e == nil {
			t.Fatal("unauthorized settlement accepted")
		}
	}
	refuse(uuid.MustParse(ws), uuid.Nil)
	r.Status = "completed"
	if _, e := s.Env.Owner.Exec(t.Context(), `UPDATE agent_run SET status='completed' WHERE id=$1`, r.ID); e != nil {
		t.Fatal(e)
	}
	refuse(uuid.MustParse(ws), uuid.Nil)
	csTerminal(t, s, r)
	refuse(uuid.New(), uuid.Nil)
	refuse(uuid.MustParse(ws), uuid.MustParse(s.userID(t, a)))
}
func TestCoresearchTranscriptAndSettlement(t *testing.T) {
	s, a, b, _, ws, p := csFixture(t)
	csAdd(t, s, a, b, ws, p.ID, "member")
	path, _ := csChat(t, s, a, ws, p.ID)
	run := csTurn(t, s, a, ws, path)
	ref := csRegister(t, s, a, ws, "owner", "claude")
	got := csClaim(t, s, a, ref)
	if len(got) != 1 {
		t.Fatal(got)
	}
	ref.Attempt = got[0].AgentRun.Attempt
	owner := uuid.MustParse(s.userID(t, a))
	if e := s.H.AgentRuns.Start(t.Context(), owner, ref, run.ID); e != nil {
		t.Fatal(e)
	}
	if e := s.H.AgentRuns.Messages(t.Context(), owner, ref, run.ID, []service.Message{{Seq: 1, Type: "text", Content: json.RawMessage(`"hello"`)}}); e != nil {
		t.Fatal(e)
	}
	var steps []db.AgentRunMessage
	if code := s.call(t, "GET", path+"/turns/"+run.ID.String()+"/steps", b, ws, nil, &steps); code != 200 || len(steps) != 1 {
		t.Fatalf("%d %+v", code, steps)
	}
	if _, e := s.H.AgentRuns.Complete(t.Context(), owner, ref, run.ID, service.Report{Usage: []agent.Usage{}}); e != nil {
		t.Fatal(e)
	}
	var messages []struct{ Kind, Body, Status string }
	if code := s.call(t, "GET", path+"/messages", b, ws, nil, &messages); code != 200 || len(messages) != 2 || messages[1].Body != "hello" || messages[1].Status != "completed" {
		t.Fatalf("%d %+v", code, messages)
	}
}

func TestCoresearchChatListMachineNames(t *testing.T) {
	for _, state := range []string{"new", "bound", "released", "missing-runtime"} {
		t.Run(state, func(t *testing.T) {
			s, a, _, _, ws, p := csFixture(t)
			path, chat := csChat(t, s, a, ws, p.ID)
			wantName := ""
			if state != "new" {
				run := csTurn(t, s, a, ws, path)
				ref := csRegister(t, s, a, ws, "chat-machine", "claude")
				if claimed := csClaim(t, s, a, ref); len(claimed) != 1 {
					t.Fatalf("claim: %+v", claimed)
				}
				wantName = "chat-machine"
				if state != "bound" {
					if code := s.call(t, "POST", path+"/turns/"+run.ID.String()+"/cancel", a, ws, nil, nil); code != 204 {
						t.Fatalf("cancel: %d", code)
					}
					if state == "released" {
						if code := s.call(t, "POST", path+"/release", a, ws, nil, nil); code != 204 {
							t.Fatalf("release: %d", code)
						}
					} else if code := s.call(t, "DELETE", "/api/runtimes/"+ref.RuntimeID.String(), a, ws, nil, nil); code != 204 {
						t.Fatalf("delete runtime: %d", code)
					}
					wantName = ""
				}
			}
			var raw json.RawMessage
			if code := s.call(t, "GET", csProjects+"/"+p.ID+"/chats", a, ws, nil, &raw); code != 200 {
				t.Fatalf("list %s: HTTP %d: %s", state, code, raw)
			}
			var chats []db.ListCoresearchChatsRow
			if err := json.Unmarshal(raw, &chats); err != nil {
				t.Fatal(err)
			}
			if len(chats) != 1 || chats[0].ID != chat.ID || chats[0].MachineName != wantName {
				t.Fatalf("list %s: want machine %q, got %+v", state, wantName, chats)
			}
		})
	}
}

func TestCoresearchTurnClaimRequiresExplicitMode(t *testing.T) {
	s, a, _, _, ws, p := csFixture(t)
	path, _ := csChat(t, s, a, ws, p.ID)
	csTurn(t, s, a, ws, path)
	ref := csRegister(t, s, a, ws, "mode", "claude")
	var got []service.Claimed
	body := map[string]any{"runtimes": []service.RuntimeRef{ref}, "slots": 1}
	tok := s.personalToken(t, a)
	if code := s.call(t, "POST", "/api/daemon/agent-runs/claim", tok, "", body, &got); code != 200 || len(got) != 0 {
		t.Fatalf("default claim consumed Coresearch: %d %+v", code, got)
	}
	body["coresearch_only"] = true
	if code := s.call(t, "POST", "/api/daemon/agent-runs/claim", tok, "", body, &got); code != 200 || len(got) != 1 {
		t.Fatalf("explicit claim: %d %+v", code, got)
	}
	path, _ = csChat(t, s, a, ws, p.ID)
	csTurn(t, s, a, ws, path)
	if _, err := s.Env.Owner.Exec(t.Context(), "UPDATE runtime SET max_concurrent=1 WHERE id=$1", ref.RuntimeID); err != nil {
		t.Fatal(err)
	}
	if code := s.call(t, "POST", "/api/daemon/agent-runs/claim", tok, "", body, &got); code != 200 || len(got) != 1 {
		t.Fatalf("second chat blocked by runtime slots: %d %+v", code, got)
	}
}

func TestCoresearchLifecycleReplayOutsideProjectHasOnlyIdentifiers(t *testing.T) {
	s, a, b, _, ws, p := csFixture(t)
	path, _ := csChat(t, s, a, ws, p.ID)
	run := csTurn(t, s, a, ws, path)
	ref := csRegister(t, s, a, ws, "work", "claude")
	owner := uuid.MustParse(s.userID(t, a))
	first := csClaim(t, s, a, ref)
	if len(first) != 1 {
		t.Fatal(first)
	}
	ref.Attempt = first[0].AgentRun.Attempt
	if e := s.H.AgentRuns.Start(t.Context(), owner, ref, run.ID); e != nil {
		t.Fatal(e)
	}
	if _, e := s.H.AgentRuns.Fail(t.Context(), owner, ref, run.ID, service.Report{Error: "temporary", FailureReason: "provider_network"}); e != nil {
		t.Fatal(e)
	}
	second := csClaim(t, s, a, ref)
	if len(second) != 1 {
		t.Fatal(second)
	}
	ref.Attempt = second[0].AgentRun.Attempt
	if e := s.H.AgentRuns.Start(t.Context(), owner, ref, run.ID); e != nil {
		t.Fatal(e)
	}
	if code := s.call(t, "POST", path+"/turns/"+run.ID.String()+"/cancel", a, ws, nil, nil); code != 204 {
		t.Fatal(code)
	}
	conn := dial(t, s, b, ws, 0)
	defer conn.Close()
	conn.SetReadDeadline(time.Now().Add(3 * time.Second))
	seen := map[string]int{}
	for {
		var msg struct {
			Event struct {
				Type    string
				Payload map[string]any
			}
		}
		if e := conn.ReadJSON(&msg); e != nil {
			t.Fatal(e)
		}
		if !strings.HasPrefix(msg.Event.Type, "agent_run.") {
			continue
		}
		for key := range msg.Event.Payload {
			if !strings.HasSuffix(key, "_id") {
				t.Fatalf("lifecycle replay leaked %s: %+v", key, msg.Event)
			}
		}
		seen[msg.Event.Type]++
		if msg.Event.Type == "agent_run.cancelled" {
			break
		}
	}
	if seen["agent_run.dispatched"] != 2 || seen["agent_run.started"] != 2 {
		t.Fatalf("missing retry replay: %+v", seen)
	}
}
