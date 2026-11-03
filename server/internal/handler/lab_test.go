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
	"lykeion/server/pkg/agent"
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

func TestPrivateLabQuestionsAreRefusedBeforeStorage(t *testing.T) {
	s, a, _, _, ws, _ := csFixture(t)
	csRegister(t, s, a, ws, "online", "claude")
	id := newLabChat(t, s, a, ws)
	for _, h := range []string{"claude", "codex"} {
		var out struct{ Code, Error string }
		if code := s.call(t, "POST", labChats+"/"+id+"/questions", a, ws, map[string]string{"text": "private", "harness": h}, &out); code != 503 || out.Code != "lab_unavailable" || out.Error != agent.LabAvailability(h).Error() {
			t.Fatal(code, out)
		}
	}
	var n int
	if err := s.Env.Owner.QueryRow(t.Context(), `SELECT (SELECT count(*) FROM lab_message WHERE chat_id=$1)+(SELECT count(*) FROM agent_run WHERE lab_chat_id=$1)`, id).Scan(&n); err != nil || n != 0 {
		t.Fatal(err, n)
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
	}{{"claude", "lab_unavailable", 503}, {"codex", "lab_unavailable", 503}} {
		var out struct{ Code, Error string }
		if code := s.call(t, "POST", path, a, ws, map[string]string{"text": "why", "harness": tt.cli}, &out); code != tt.status || out.Code != tt.code || out.Error != agent.LabAvailability(tt.cli).Error() {
			t.Fatalf("%s: %d %+v", tt.cli, code, out)
		}
	}
	own := csRegister(t, s, a, ws, "unhealthy", "claude")
	if _, err := s.Env.Owner.Exec(t.Context(), "UPDATE runtime SET unhealthy_reason='sign_in' WHERE id=$1", own.RuntimeID); err != nil {
		t.Fatal(err)
	}
	if code := s.call(t, "POST", path, a, ws, map[string]string{"text": "why", "harness": "claude"}, nil); code != 503 {
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

func TestFlagSetsLessonAsideAndQueuesRewrite(t *testing.T) {
	s, _, b, _, ws, _ := csFixture(t)
	for _, enabled := range []bool{false, true} {
		s.H.LessonWritesEnabled = enabled
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
		if err := s.Env.Owner.QueryRow(t.Context(), `SELECT status='set_aside' AND set_aside_reason='flagged',EXISTS(SELECT 1 FROM lesson_job WHERE lesson_id=$1 AND triggers=ARRAY['flag'] AND due_at<=now()),EXISTS(SELECT 1 FROM lesson_flag WHERE lesson_id=$1 AND length(reason)=500) FROM lesson WHERE id=$1`, id).Scan(&aside, &job, &flag); err != nil || !aside || !flag || job != enabled {
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
		if code := s.call(t, "POST", path, a, ws, map[string]string{"text": "Read context", "context_ref": ref, "harness": "claude"}, nil); code != 503 {
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

func TestLegacyLabRunCannotBeClaimedByFreshFallback(t *testing.T) {
	s, a, _, _, ws, _ := csFixture(t)
	stale := csRegister(t, s, a, ws, "stale-advertiser", "claude")
	fresh := csRegister(t, s, a, ws, "fresh-fallback", "claude")
	if _, err := s.Env.Owner.Exec(t.Context(), `UPDATE runtime SET models='[{"value":"chosen"}]',last_seen_at=now()-interval '2 minutes' WHERE id=$1`, stale.RuntimeID); err != nil {
		t.Fatal(err)
	}
	chat := newLabChat(t, s, a, ws)
	seedHistoricalLabQuestion(t, s, chat, "your lab", "chosen")
	owner := uuid.MustParse(s.userID(t, a))
	for _, ref := range []service.RuntimeRef{stale, fresh} {
		if claims, err := s.H.AgentRuns.Claim(t.Context(), owner, []service.RuntimeRef{ref}, 1); err != nil || len(claims) != 0 {
			t.Fatal("legacy private claim", err, claims)
		}
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

// A daemon from a previous release can retain a dispatched run or bearer.
// Neither the historical prompt nor new lab evidence may reach that daemon.
func TestLegacyLabDaemonCannotFetchPromptOrEvidence(t *testing.T) {
	s, a, _, _, ws, _ := csFixture(t)
	s.H.Lab = http.HandlerFunc(func(http.ResponseWriter, *http.Request) { t.Error("reached the lab evidence handler") })
	server := httptest.NewServer(NewRouter(s.H))
	defer server.Close()
	s.Server = server
	chat := newLabChat(t, s, a, ws)
	rt := csRegister(t, s, a, ws, "old-daemon", "claude")
	q := seedHistoricalLabQuestion(t, s, chat, "private", "")
	var run uuid.UUID
	if err := s.Env.Owner.QueryRow(t.Context(), `UPDATE agent_run SET status='running',attempt=1,runtime_id=$2 WHERE id=(SELECT agent_run_id FROM lab_message WHERE id=$1) RETURNING id`, q, rt.RuntimeID).Scan(&run); err != nil {
		t.Fatal(err)
	}
	token := "ltk_" + uuid.NewString()
	if _, err := s.Env.Owner.Exec(t.Context(), `INSERT INTO agent_run_token(token_hash,workspace_id,agent_run_id,role,attempt,scopes,expires_at) VALUES($1,$2,$3,'aristotle',1,$4,now()+interval '1 hour')`, auth.Hash(token), ws, run, service.Scopes("aristotle")); err != nil {
		t.Fatal(err)
	}
	if status := s.call(t, "POST", "/mcp/lab", token, "", nil, nil); status != 503 {
		t.Fatal("lab MCP", status)
	}
	rt.Attempt = 1
	for _, suffix := range []string{"/start", "/start?lab_history=1"} {
		if status := s.call(t, "POST", "/api/daemon/agent-runs/"+run.String()+suffix, s.personalToken(t, a), "", rt, nil); status != 503 {
			t.Fatal("lab launch", suffix, status)
		}
	}
}
