package handler

import (
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/google/uuid"

	"lykeion/server/internal/auth"
	"lykeion/server/internal/labmemory"
	"lykeion/server/internal/service"
	"lykeion/server/internal/store"
)

const labChats = "/api/lab/chats"

func newLabChat(t *testing.T, s *server, a, ws string) string {
	t.Helper()
	var out struct{ Chat struct{ ID string } }
	if code := s.call(t, "POST", labChats, a, ws, nil, &out); code != 201 || out.Chat.ID == "" {
		t.Fatalf("create chat: %d %+v", code, out)
	}
	return out.Chat.ID
}

func seedHistoricalLabQuestion(t *testing.T, s *server, chat, body, model string) uuid.UUID {
	t.Helper()
	var id uuid.UUID
	err := s.Env.Owner.QueryRow(t.Context(), `WITH r AS (
 INSERT INTO agent_run(workspace_id,kind,requester_id,responsible_user_id,lab_chat_id,board_column,round,role,harness,prompt,skill,requested_model)
 SELECT workspace_id,'aristotle',owner_id,owner_id,id,'',0,'aristotle','claude',$2,'',$3 FROM lab_chat WHERE id=$1 RETURNING *)
 INSERT INTO lab_message(workspace_id,owner_id,chat_id,kind,body,agent_run_id)
 SELECT workspace_id,requester_id,lab_chat_id,'question',$2,id FROM r RETURNING id`, chat, body, model).Scan(&id)
	if err != nil {
		t.Fatal(err)
	}
	return id
}

func TestPrivateLabQuestionsAreAdmittedAndClaimed(t *testing.T) {
	for _, harness := range []string{"claude", "codex"} {
		t.Run(harness, func(t *testing.T) {
			s, a, _, _, ws, _ := csFixture(t)
			rt := csRegister(t, s, a, ws, "online", harness)
			id := newLabChat(t, s, a, ws)
			var out struct{ Message struct{ ID string } }
			if code := s.call(t, "POST", labChats+"/"+id+"/questions", a, ws, map[string]string{"text": "private", "harness": harness}, &out); code != 202 || out.Message.ID == "" {
				t.Fatal(code, out)
			}
			claims, err := s.H.AgentRuns.Claim(t.Context(), uuid.MustParse(s.userID(t, a)), []service.RuntimeRef{rt}, 1)
			if err != nil || len(claims) != 1 || claims[0].AgentRun.Harness != harness {
				t.Fatal(err, len(claims))
			}
			var n int
			if err := s.Env.Owner.QueryRow(t.Context(), "SELECT count(*) FROM lab_message WHERE chat_id=$1 AND kind='question'", id).Scan(&n); err != nil || n != 1 {
				t.Fatal(err, n)
			}
		})
	}
}

func TestLabRecentChatsExcludeEmptyConversations(t *testing.T) {
	s, a, _, _, ws, _ := csFixture(t)
	empty := newLabChat(t, s, a, ws)
	used := newLabChat(t, s, a, ws)
	seedHistoricalLabQuestion(t, s, used, "A question", "")
	var got struct {
		Chats []struct {
			ID string `json:"id"`
		} `json:"chats"`
	}
	if code := s.call(t, "GET", labChats, a, ws, nil, &got); code != 200 {
		t.Fatal(code)
	}
	if len(got.Chats) != 1 || got.Chats[0].ID != used {
		t.Fatalf("recent chats %+v; empty %s", got.Chats, empty)
	}
}

func TestLabPendingAcrossOverlappingQuestions(t *testing.T) {
	s, a, _, _, ws, _ := csFixture(t)
	csRegister(t, s, a, ws, "parallel", "claude")
	id := newLabChat(t, s, a, ws)
	for _, q := range []string{"First", "Second"} {
		seedHistoricalLabQuestion(t, s, id, q, "")
	}
	var run string
	if err := s.Env.Owner.QueryRow(t.Context(), "SELECT agent_run_id FROM lab_message WHERE chat_id=$1 AND body='First'", id).Scan(&run); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Env.Owner.Exec(t.Context(), "UPDATE agent_run SET status='failed' WHERE id=$1", run); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Env.Owner.Exec(t.Context(), `INSERT INTO lab_message(workspace_id,owner_id,chat_id,kind,body,agent_run_id,created_at)
		SELECT workspace_id,owner_id,chat_id,'failure','First failed',agent_run_id,clock_timestamp() FROM lab_message
		WHERE agent_run_id=$1 AND kind='question' ON CONFLICT DO NOTHING`, run); err != nil {
		t.Fatal(err)
	}
	page := func(want bool) {
		t.Helper()
		var got struct {
			Pending  *bool
			Messages []struct{ Kind string }
		}
		if code := s.call(t, "GET", labChats+"/"+id+"/messages?limit=1", a, ws, nil, &got); code != 200 || got.Pending == nil || *got.Pending != want || len(got.Messages) != 1 || got.Messages[0].Kind != "failure" {
			t.Fatalf("pending %t: %d %+v", want, code, got)
		}
	}
	page(true)
	if _, err := s.Env.Owner.Exec(t.Context(), "UPDATE agent_run SET status='failed' WHERE lab_chat_id=$1 AND status='queued'", id); err != nil {
		t.Fatal(err)
	}
	page(false)
}

func TestAskRefusesBlankAndOversizedQuestions(t *testing.T) {
	s, a, _, _, ws, _ := csFixture(t)
	path := labChats + "/" + newLabChat(t, s, a, ws) + "/questions"
	for _, in := range []map[string]string{{"text": ""}, {"text": "   "}, {"text": strings.Repeat("界", 8001)}, {"text": "why", "context_card_id": uuid.NewString()}, {"text": "why", "context_ref": "file:" + uuid.NewString()}} {
		in["harness"] = "claude"
		if code := s.call(t, "POST", path, a, ws, in, nil); code != 400 {
			t.Fatalf("invalid request: %d", code)
		}
	}
	var n int
	if err := s.Env.Owner.QueryRow(t.Context(), "SELECT count(*) FROM agent_run WHERE kind='aristotle' AND workspace_id=$1", ws).Scan(&n); err != nil || n != 0 {
		t.Fatalf("partial runs %d %v", n, err)
	}
}

func TestPrivateLabUnavailableWithoutOwnMachine(t *testing.T) {
	s, a, b, _, ws, _ := csFixture(t)
	csRegister(t, s, b, ws, "other", "claude")
	path := labChats + "/" + newLabChat(t, s, a, ws) + "/questions"
	for _, tt := range []struct {
		cli, code string
		status    int
	}{{"claude", "no_machine", 409}, {"codex", "no_machine", 409}} {
		var out struct{ Code, Error string }
		if code := s.call(t, "POST", path, a, ws, map[string]string{"text": "why", "harness": tt.cli}, &out); code != tt.status || out.Code != tt.code || out.Error != "Your machine is offline" {
			t.Fatalf("%s: %d %+v", tt.cli, code, out)
		}
	}
	own := csRegister(t, s, a, ws, "unhealthy", "claude")
	if _, err := s.Env.Owner.Exec(t.Context(), "UPDATE runtime SET unhealthy_reason='sign_in' WHERE id=$1", own.RuntimeID); err != nil {
		t.Fatal(err)
	}
	if code := s.call(t, "POST", path, a, ws, map[string]string{"text": "why", "harness": "claude"}, nil); code != 409 {
		t.Fatal("unhealthy own runtime", code)
	}
	var count int
	if err := s.Env.Owner.QueryRow(t.Context(), "SELECT count(*) FROM lab_message WHERE workspace_id=$1", ws).Scan(&count); err != nil || count != 0 {
		t.Fatal(err, count)
	}
}

func TestChatsAreOwnerOnly(t *testing.T) {
	s, a, b, c, ws, _ := csFixture(t)
	id := newLabChat(t, s, a, ws)
	for _, tok := range []string{b, c} {
		for _, suffix := range []string{"/messages", "/questions"} {
			method := "GET"
			if suffix == "/questions" {
				method = "POST"
			}
			if code := s.call(t, method, labChats+"/"+id+suffix, tok, ws, map[string]string{"text": "why", "harness": "claude"}, nil); code != 404 {
				t.Fatalf("%s %d", suffix, code)
			}
		}
	}
	var list struct{ Chats []map[string]any }
	if code := s.call(t, "GET", labChats, b, ws, nil, &list); code != 200 || len(list.Chats) != 0 {
		t.Fatalf("list %d %+v", code, list)
	}
}

func TestFlagSetsLessonAside(t *testing.T) {
	s, _, b, _, ws, _ := csFixture(t)
	{
		var id string
		if err := s.Env.Owner.QueryRow(t.Context(), "INSERT INTO lesson(workspace_id,kind,text,model,checked_at) VALUES($1,'lab','lesson','model',now()) RETURNING id", ws).Scan(&id); err != nil {
			t.Fatal(err)
		}
		path := "/api/lessons/" + id + "/flag"
		if code := s.call(t, "POST", path, b, ws, map[string]string{"reason": strings.Repeat("界", 501)}, nil); code != 400 {
			t.Fatal(code)
		}
		if code := s.call(t, "POST", path, b, ws, map[string]string{"reason": strings.Repeat("界", 500)}, nil); code != 204 {
			t.Fatal(code)
		}
		var aside, job, flag bool
		if err := s.Env.Owner.QueryRow(t.Context(), `SELECT status='set_aside' AND set_aside_reason='flagged',EXISTS(SELECT 1 FROM lesson_job WHERE lesson_id=$1 AND triggers=ARRAY['flag'] AND due_at<=now()),EXISTS(SELECT 1 FROM lesson_flag WHERE lesson_id=$1 AND length(reason)=500) FROM lesson WHERE id=$1`, id).Scan(&aside, &job, &flag); err != nil || !aside || !flag || job {
			t.Fatalf("flag %v %v %v %v", aside, job, flag, err)
		}
	}
}

func TestAskContextOnlyUsesAccessibleStudyAndRecordRefs(t *testing.T) {
	s, a, _, _, ws, _ := csFixture(t)
	csRegister(t, s, a, ws, "mine", "claude")
	path := labChats + "/" + newLabChat(t, s, a, ws) + "/questions"
	card := s.idleCard(t, a, ws, "lab")
	record := uuid.NewString()
	if _, err := s.Env.Owner.Exec(t.Context(), `INSERT INTO record(id,workspace_id,card_id,board_column,round,kind,role,body) VALUES($1,$2,$3,'hypotheses',1,'hypothesis','hypotheses','{}')`, record, ws, card.ID); err != nil {
		t.Fatal(err)
	}
	for _, ref := range []string{"study:" + card.ID, "record:" + record} {
		if code := s.call(t, "POST", path, a, ws, map[string]string{"text": "Read context", "context_ref": ref, "harness": "claude"}, nil); code != 202 {
			t.Fatal(code)
		}

	}
	other := s.workspace(t, a, "another").ID
	foreign := s.idleCard(t, a, other, "other")
	for _, ref := range []string{"study:" + foreign.ID, "record:" + uuid.NewString(), "workspace:context", "coresearch:" + record} {
		if code := s.call(t, "POST", path, a, ws, map[string]string{"text": "Read context", "context_ref": ref, "harness": "claude"}, nil); code != 400 {
			t.Fatal(ref, code)
		}
	}
}

func TestAskContextAcceptsAReportOnlyWhenTheStudyHasOne(t *testing.T) {
	s, a, _, _, ws, _ := csFixture(t)
	csRegister(t, s, a, ws, "mine", "claude")
	path := labChats + "/" + newLabChat(t, s, a, ws) + "/questions"
	card := s.idleCard(t, a, ws, "lab")
	ask := func(ref string) int {
		return s.call(t, "POST", path, a, ws, map[string]string{"text": "Read context", "context_ref": ref, "harness": "claude"}, nil)
	}
	if code := ask("report:" + card.ID); code != 400 {
		t.Fatal("a study with no report", code)
	}
	s.mustUpdate(t, `UPDATE card SET state = 'done' WHERE id = $1`, card.ID)
	s.mustUpdate(t, `INSERT INTO inbox_item (workspace_id, card_id, user_id, kind, text) SELECT workspace_id, id, owner_id, 'study_report', 'ready' FROM card WHERE id = $1`, card.ID)
	if code := ask("report:" + card.ID + "#question"); code != 400 {
		t.Fatal("a report node", code)
	}
	if code := ask("report:" + card.ID); code != 202 {
		t.Fatal(code)
	}
	var ref string
	if err := s.Env.Owner.QueryRow(t.Context(), `SELECT context_ref FROM lab_message WHERE kind = 'question' AND context_ref IS NOT NULL`).Scan(&ref); err != nil || ref != "report:"+card.ID {
		t.Fatal(ref, err)
	}
}

func TestLabRunCanBeClaimedByFreshFallback(t *testing.T) {
	s, a, _, _, ws, _ := csFixture(t)
	stale := csRegister(t, s, a, ws, "stale-advertiser", "claude")
	fresh := csRegister(t, s, a, ws, "fresh-fallback", "claude")
	if _, err := s.Env.Owner.Exec(t.Context(), `UPDATE runtime SET models='[{"value":"chosen"}]',last_seen_at=now()-interval '2 minutes' WHERE id=$1`, stale.RuntimeID); err != nil {
		t.Fatal(err)
	}
	chat := newLabChat(t, s, a, ws)
	seedHistoricalLabQuestion(t, s, chat, "your lab", "chosen")
	owner := uuid.MustParse(s.userID(t, a))
	if claims, err := s.H.AgentRuns.Claim(t.Context(), owner, []service.RuntimeRef{stale}, 1); err != nil || len(claims) != 0 {
		t.Fatal("stale runtime claimed work", err, len(claims))
	}
	if claims, err := s.H.AgentRuns.Claim(t.Context(), owner, []service.RuntimeRef{fresh}, 1); err != nil || len(claims) != 1 || claims[0].AgentRun.RequestedModel != "" {
		t.Fatal("fresh fallback did not claim with its default model", err, len(claims))
	}

}

func TestChatsHiddenAnswerInvalidatesDependentGapAcrossPages(t *testing.T) {
	s, a, _, _, ws, _ := csFixture(t)
	csRegister(t, s, a, ws, "dependent-gaps", "claude")
	chat := newLabChat(t, s, a, ws)
	card := s.idleCard(t, a, ws, "source")
	who := uuid.MustParse(s.userID(t, a))
	id := uuid.New()
	var v string
	if err := s.H.Store.InWorkspace(t.Context(), uuid.MustParse(ws), who, func(tx *store.Tx) error {
		var err error
		v, err = labmemory.Version(t.Context(), tx, labmemory.Ref{Kind: "study", ID: uuid.MustParse(card.ID)})
		return err
	}); err != nil {
		t.Fatal(err)
	}
	cites, _ := json.Marshal([]labmemory.Citation{{Ref: "study:" + card.ID, Version: v}})
	if _, err := s.Env.Owner.Exec(t.Context(), `INSERT INTO lesson(id,workspace_id,kind,text,cites,checked_at,model) VALUES($1,$2,'lab','active lesson',$3,now(),'test')`, id, ws, cites); err != nil {
		t.Fatal(err)
	}
	ref := labmemory.Ref{Kind: "lesson", ID: id}
	var version string
	if err := s.H.Store.InWorkspace(t.Context(), uuid.MustParse(ws), who, func(tx *store.Tx) error {
		f, err := labmemory.Read(t.Context(), tx, who, ref)
		if err != nil {
			return err
		}
		data, err := json.Marshal(f)
		if err != nil {
			return err
		}
		version = fmt.Sprintf("%x", sha256.Sum256(data))
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	seed := func(body string, sources []uuid.UUID, evidence, statements any) uuid.UUID {
		t.Helper()
		q := seedHistoricalLabQuestion(t, s, chat, body, "")
		var run uuid.UUID
		if err := s.Env.Owner.QueryRow(t.Context(), `UPDATE lab_message SET launch_sources=$2 WHERE id=$1 RETURNING agent_run_id`, q, sources).Scan(&run); err != nil {
			t.Fatal(err)
		}
		var id uuid.UUID
		if err := s.Env.Owner.QueryRow(t.Context(), `INSERT INTO lab_message(workspace_id,chat_id,owner_id,agent_run_id,kind,body,statements,evidence,history_safe,created_at) VALUES($1,$2,$3,$4,'answer','',$5,$6,true,clock_timestamp()) RETURNING id`, ws, chat, who, run, statements, evidence).Scan(&id); err != nil {
			t.Fatal(err)
		}
		return id
	}
	cite := map[string]string{"ref": ref.String(), "title": "source", "version": version, "via": "read"}
	aID := seed("A", []uuid.UUID{}, []any{cite}, []any{map[string]any{"kind": "lesson", "text": "earlier secret", "cites": []any{cite}}})
	gap := func(q, text string, src []uuid.UUID) uuid.UUID {
		return seed(q, src, []any{}, []any{map[string]any{"kind": "gap", "text": text, "cites": []any{}}})
	}
	bID := gap("B", "dependent secret", []uuid.UUID{aID})
	cID := gap("C", "independent", []uuid.UUID{})
	read := func(wantB string) {
		t.Helper()
		var page struct {
			Messages []labMessage
			Next     string `json:"next_cursor"`
		}
		if code := s.call(t, "GET", labChats+"/"+chat+"/messages?limit=3", a, ws, nil, &page); code != 200 || len(page.Messages) != 3 || page.Next == "" {
			t.Fatalf("page %d %+v", code, page)
		}
		b, c := page.Messages[0], page.Messages[2]
		if b.ID != bID || b.Kind != wantB || c.ID != cID || c.Kind != "answer" || len(c.Statements) != 1 || c.Statements[0].Text != "independent" {
			t.Fatalf("cross-page history: %+v", page)
		}
		if wantB == "failure" && (len(b.Evidence) != 0 || len(b.Statements) != 0 || b.Body != "This answer is unavailable because its evidence has changed.") {
			t.Fatalf("private gap leaked: %+v", b)
		}
	}
	read("answer")
	s.H.Store.LessonsEnabled = false
	read("failure")
	s.H.Store.LessonsEnabled = true
	read("answer")
	if _, err := s.Env.Owner.Exec(t.Context(), "UPDATE card SET title='changed' WHERE id=$1", card.ID); err != nil {
		t.Fatal(err)
	}
	read("failure")
}

// A valid run-scoped bearer reaches the lab evidence handler after admission.
func TestLabDaemonCanFetchEvidenceAndStart(t *testing.T) {
	s, a, _, _, ws, _ := csFixture(t)
	reached := false
	s.H.Lab = http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { reached = true; w.WriteHeader(204) })
	server := httptest.NewServer(NewRouter(s.H))
	defer server.Close()
	s.Server = server
	rt := csRegister(t, s, a, ws, "online", "claude")
	chat := newLabChat(t, s, a, ws)
	if code := s.call(t, "POST", labChats+"/"+chat+"/questions", a, ws, map[string]string{"text": "synthetic question", "harness": "claude"}, nil); code != 202 {
		t.Fatal(code)
	}
	claims, err := s.H.AgentRuns.Claim(t.Context(), uuid.MustParse(s.userID(t, a)), []service.RuntimeRef{rt}, 1)
	if err != nil || len(claims) != 1 {
		t.Fatal(err, len(claims))
	}
	run := claims[0].AgentRun
	rt.Attempt = run.Attempt
	if code := s.call(t, "POST", "/api/daemon/agent-runs/"+run.ID.String()+"/start?lab_history=1", s.personalToken(t, a), "", rt, nil); code != 200 {
		t.Fatal("start", code)
	}
	if status := s.call(t, "POST", "/mcp/lab", claims[0].Token, "", nil, nil); status != 204 || !reached {
		t.Fatal("lab MCP", status, reached)
	}
}

func TestUnsupportedLabStatusAndHandInStopTheAttempt(t *testing.T) {
	for _, first := range []string{"status", "hand-in"} {
		t.Run(first, func(t *testing.T) {
			s, a, _, _, ws, _ := csFixture(t)
			rt := csRegister(t, s, a, ws, "old-daemon", "claude")
			rt.Attempt = 1
			actor := s.personalToken(t, a)
			chat := newLabChat(t, s, a, ws)
			q := seedHistoricalLabQuestion(t, s, chat, "synthetic secret", "")
			var run string
			err := s.Env.Owner.QueryRow(t.Context(), `UPDATE agent_run SET harness='cursor',status='running',attempt=1,runtime_id=$2,started_at=now()
 WHERE id=(SELECT agent_run_id FROM lab_message WHERE id=$1) RETURNING id::text`, q, rt.RuntimeID).Scan(&run)
			if err != nil {
				t.Fatal(err)
			}
			token := "ltk_" + uuid.NewString()
			if _, err = s.Env.Owner.Exec(t.Context(), `INSERT INTO agent_run_token(token_hash,workspace_id,agent_run_id,role,attempt,scopes,expires_at) VALUES($1,$2,$3,'aristotle',1,$4,now()+interval '1 hour')`, auth.Hash(token), ws, run, service.Scopes("aristotle")); err != nil {
				t.Fatal(err)
			}
			base := "/api/daemon/agent-runs/" + run
			status := func() {
				t.Helper()
				var out struct{ Status string }
				code := s.call(t, "GET", base+"/status?workspace_id="+ws+"&runtime_id="+rt.RuntimeID.String()+"&attempt=1", actor, "", nil, &out)
				if code != 200 || out.Status != "cancelled" {
					t.Fatal(code, out)
				}
			}
			handIn := func() {
				t.Helper()
				var out struct {
					Problems     []string
					FreshAttempt bool `json:"fresh_attempt_required"`
				}
				code := s.call(t, "POST", base+"/hand-in", actor, "", map[string]any{"workspace_id": ws, "runtime_id": rt.RuntimeID, "attempt": 1, "hand_in": map[string]any{}}, &out)
				if code != 409 || len(out.Problems) > 0 || out.FreshAttempt {
					t.Fatal(code, out)
				}
			}
			if first == "hand-in" {
				handIn()
			} else {
				status()
			}
			for range 2 {
				status()
				handIn()
			}
			var receipt map[string]any
			if code := s.call(t, "POST", base+"/complete", actor, "", rt, &receipt); code != 200 || len(receipt) != 3 || receipt["status"] != "cancelled" || receipt["id"] != run {
				t.Fatal("late settlement exposed run data", code, receipt)
			}
			var state string
			var tokens, settlements, answers int
			err = s.Env.Owner.QueryRow(t.Context(), `SELECT r.status,(SELECT count(*) FROM agent_run_token WHERE agent_run_id=r.id),
 (SELECT count(*) FROM agent_run_settlement WHERE agent_run_id=r.id),
 (SELECT count(*) FROM lab_message WHERE agent_run_id=r.id AND kind='answer') FROM agent_run r WHERE id=$1`, run).Scan(&state, &tokens, &settlements, &answers)
			if err != nil || state != "cancelled" || tokens != 0 || settlements != 1 || answers != 0 {
				t.Fatal(err, state, tokens, settlements, answers)
			}
		})
	}
}
