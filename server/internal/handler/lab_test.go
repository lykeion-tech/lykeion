package handler

import (
	"strings"
	"testing"

	"github.com/google/uuid"
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
