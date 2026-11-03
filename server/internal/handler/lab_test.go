package handler

import (
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"strings"
	"testing"

	"github.com/google/uuid"

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

func TestAskEnqueuesAndReturns202(t *testing.T) {
	s, a, _, _, ws, _ := csFixture(t)
	csRegister(t, s, a, ws, "lab-ask", "claude")
	id := newLabChat(t, s, a, ws)
	question := strings.Repeat("界", 8000)
	var out struct {
		Message struct{ ID, Kind, Body string }
	}
	if code := s.call(t, "POST", labChats+"/"+id+"/questions", a, ws, map[string]string{"text": question, "harness": "claude"}, &out); code != 202 || out.Message.ID == "" || out.Message.Body != question || out.Message.Kind != "question" {
		t.Fatalf("accepted %d", code)
	}
	if code := s.call(t, "POST", labChats+"/"+id+"/questions", a, ws, map[string]string{"text": "followup", "harness": "claude"}, nil); code != 202 {
		t.Fatal(code)
	}
	var page struct {
		Messages []struct{ ID, Body string }
		Next     string `json:"next_cursor"`
	}
	path := labChats + "/" + id + "/messages?limit=1"
	if code := s.call(t, "GET", path, a, ws, nil, &page); code != 200 || len(page.Messages) != 1 || page.Messages[0].Body != "followup" || page.Next == "" {
		t.Fatalf("page %d %+v", code, page)
	}
	if code := s.call(t, "GET", path+"&cursor="+page.Next, a, ws, nil, &page); code != 200 || len(page.Messages) != 1 || page.Messages[0].ID != out.Message.ID {
		t.Fatalf("page2 %d", code)
	}
	var chats struct{ Chats []struct{ Title string } }
	if code := s.call(t, "GET", labChats, a, ws, nil, &chats); code != 200 || len(chats.Chats) != 1 || chats.Chats[0].Title != strings.Repeat("界", 40)+"…" {
		t.Fatalf("title %d %+v", code, chats)
	}
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

func TestAskWithNoOwnMachineIs409NoMachine(t *testing.T) {
	s, a, b, _, ws, _ := csFixture(t)
	csRegister(t, s, b, ws, "other", "claude")
	path := labChats + "/" + newLabChat(t, s, a, ws) + "/questions"
	for _, tt := range []struct {
		cli, code string
		status    int
	}{{"claude", "no_machine", 409}, {"codex", "unsupported_harness", 422}} {
		var out struct{ Code, Error string }
		if code := s.call(t, "POST", path, a, ws, map[string]string{"text": "why", "harness": tt.cli}, &out); code != tt.status || out.Code != tt.code || (tt.cli == "claude" && out.Error != "Your machine is offline") {
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
		var msg struct{ Message struct{ ID string } }
		if code := s.call(t, "POST", path, a, ws, map[string]string{"text": "Read context", "context_ref": ref, "harness": "claude"}, &msg); code != 202 {
			t.Fatal(code)
		}
		var prompt string
		if err := s.Env.Owner.QueryRow(t.Context(), `SELECT r.prompt FROM lab_message m JOIN agent_run r ON r.id=m.agent_run_id WHERE m.id=$1`, msg.Message.ID).Scan(&prompt); err != nil || !strings.Contains(prompt, ref) {
			t.Fatal(err, prompt)
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

func TestAskStaleModelAdvertiserAllowsFreshFallback(t *testing.T) {
	s, a, _, _, ws, _ := csFixture(t)
	stale := csRegister(t, s, a, ws, "stale-advertiser", "claude")
	fresh := csRegister(t, s, a, ws, "fresh-fallback", "claude")
	if _, err := s.Env.Owner.Exec(t.Context(), `UPDATE runtime SET models='[{"value":"chosen"}]',last_seen_at=now()-interval '2 minutes' WHERE id=$1`, stale.RuntimeID); err != nil {
		t.Fatal(err)
	}
	chat := newLabChat(t, s, a, ws)
	if code := s.call(t, "POST", labChats+"/"+chat+"/questions", a, ws, map[string]string{"text": "your lab", "harness": "claude", "model": "chosen"}, nil); code != 202 {
		t.Fatalf("admission %d", code)
	}
	owner := uuid.MustParse(s.userID(t, a))
	if claims, err := s.H.AgentRuns.Claim(t.Context(), owner, []service.RuntimeRef{stale}, 1); err != nil || len(claims) != 0 {
		t.Fatalf("stale advertiser claimed: %v %+v", err, claims)
	}
	claims, err := s.H.AgentRuns.Claim(t.Context(), owner, []service.RuntimeRef{fresh}, 1)
	if err != nil || len(claims) != 1 || claims[0].AgentRun.LabChatID == nil || claims[0].AgentRun.LabChatID.String() != chat || claims[0].AgentRun.RequestedModel != "" {
		t.Fatalf("fresh CLI default fallback: %v %+v", err, claims)
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
		var q struct{ Message labMessage }
		if code := s.call(t, "POST", labChats+"/"+chat+"/questions", a, ws, map[string]string{"text": body, "harness": "claude"}, &q); code != 202 {
			t.Fatal(code)
		}
		var run uuid.UUID
		if err := s.Env.Owner.QueryRow(t.Context(), `UPDATE lab_message SET launch_sources=$2 WHERE id=$1 RETURNING agent_run_id`, q.Message.ID, sources).Scan(&run); err != nil {
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
