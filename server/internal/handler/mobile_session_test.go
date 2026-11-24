package handler

import (
	"encoding/json"
	"net/http"
	"strings"
	"testing"

	"github.com/google/uuid"

	"lykeion/server/internal/store"
)

func TestMobileSession(t *testing.T) {
	s := newServer(t)
	email := "mobile@example.org"
	sent := map[string]string{"email": email}
	if got := s.call(t, "POST", "/auth/send-code", "", "", sent, nil); got != 204 {
		t.Fatal(got)
	}
	webCode := s.Mail.code(email)
	if got := s.call(t, "POST", "/auth/mobile/send-code", "", "", sent, nil); got != 204 {
		t.Fatal(got)
	}
	input := map[string]string{"email": email, "code": s.Mail.code(email)}
	if got := s.call(t, "POST", "/auth/verify-code", "", "", input, nil); got != 401 {
		t.Fatalf("desktop accepted mobile code: %d", got)
	}
	if got := s.call(t, "POST", "/auth/verify-code", "", "", map[string]string{"email": email, "code": webCode}, nil); got != 200 {
		t.Fatal("mobile invalidated web code", got)
	}
	res, err := http.Post(s.URL+"/auth/mobile/verify-code", "application/json", jsonBody(input))
	if err != nil {
		t.Fatal(err)
	}
	defer res.Body.Close()
	if res.StatusCode != 200 || len(res.Cookies()) != 0 {
		t.Fatalf("verify %d cookies %d", res.StatusCode, len(res.Cookies()))
	}
	var out struct{ Token string }
	if err := json.NewDecoder(res.Body).Decode(&out); err != nil {
		t.Fatal(err)
	}
	if got := s.call(t, "POST", "/auth/mobile/verify-code", "", "", input, nil); got != 401 {
		t.Fatal(got)
	}
	if got := s.call(t, "GET", "/api/workspaces", out.Token, "", nil, nil); got != 200 {
		t.Fatal(got)
	}
	if got := s.call(t, "POST", "/api/workspaces", out.Token, "", map[string]string{"name": "My Lab", "slug": "my-lab"}, nil); got != 201 {
		t.Fatal("create lab", got)
	}
	if got := s.call(t, "PUT", "/api/me/research-profile", out.Token, "", map[string]any{"identity": "Research background", "links": []string{}}, nil); got != 200 {
		t.Fatal("save profile", got)
	}
	var profile struct{ Draft struct{ Identity string } }
	if got := s.call(t, "GET", "/api/me/research-profile", out.Token, "", nil, &profile); got != 200 || profile.Draft.Identity != "Research background" {
		t.Fatal("read profile", got)
	}
	for _, path := range []string{"/api/cli-token", "/api/tokens", "/api/cards", "/api/agent-runs/00000000-0000-0000-0000-000000000001/cancel"} {
		if got := s.call(t, "POST", path, out.Token, "", map[string]any{}, nil); got != 403 {
			t.Fatalf("%s: %d", path, got)
		}
	}
	if _, _, err := s.H.JWT.Parse(out.Token); err == nil {
		t.Fatal("mobile bearer accepted by general issuer")
	}
	if got := s.call(t, "POST", "/auth/mobile/logout", out.Token, "", nil, nil); got != 204 {
		t.Fatal(got)
	}
	if got := s.call(t, "GET", "/api/workspaces", out.Token, "", nil, nil); got != 401 {
		t.Fatal("signed-out token still works", got)
	}

}

func TestMobileStudyAcrossClients(t *testing.T) {
	s := newServer(t)
	email := "reader@example.org"
	dashboard := s.signIn(t, email)
	ws := s.workspace(t, dashboard, "studies").ID
	team := s.addedTeam(t, dashboard, ws, "validation", "validation")
	if code := s.call(t, "POST", "/auth/mobile/send-code", "", "", map[string]string{"email": email}, nil); code != 204 {
		t.Fatal(code)
	}
	var credentials struct{ Token string }
	if code := s.call(t, "POST", "/auth/mobile/verify-code", "", "", map[string]string{"email": email, "code": s.Mail.code(email)}, &credentials); code != 200 {
		t.Fatal(code)
	}
	native := credentials.Token
	var availability struct {
		Harnesses    []string
		Capabilities []string
	}
	if code := s.call(t, "GET", "/api/mobile/availability", native, ws, nil, &availability); code != 200 || len(availability.Harnesses) != 0 || len(availability.Capabilities) == 0 {
		t.Fatal(code, availability)
	}
	input := map[string]any{"revision": 0, "title": "Calibration", "priority": "high", "description": "Check calibration across cohorts", "stage": "validation", "assignee": map[string]string{"type": "team", "pack_id": team}}
	id := uuid.NewString()
	base := "/api/study-proposals"
	if code := s.call(t, "PUT", base+"/"+id, native, ws, input, nil); code != 200 {
		t.Fatal(code)
	}
	var saved struct{ Items []store.StudyProposal }
	if code := s.call(t, "GET", base, dashboard, ws, nil, &saved); code != 200 || len(saved.Items) != 1 || saved.Items[0].ID.String() != id || saved.Items[0].Title != "Calibration" || saved.Items[0].Priority != "high" {
		t.Fatal(code, saved)
	}
	op := map[string]any{"revision": 1, "operation_id": uuid.NewString()}
	var result, replay proposalReceipt
	s.H.Engine.Inline = true
	if code := s.call(t, "POST", base+"/"+id+"/start", dashboard, ws, op, &result); code != 200 {
		t.Fatal(code)
	}
	if code := s.call(t, "POST", base+"/"+id+"/start", native, ws, op, &replay); code != 200 || result != replay {
		t.Fatal(code, replay)
	}
	var runs struct{ Items []store.StudyRunRow }
	if code := s.call(t, "GET", "/api/study-runs", native, ws, nil, &runs); code != 200 || len(runs.Items) != 1 || runs.Items[0].CardID != result.CardID {
		t.Fatal(code, runs)
	}
	// A web-board card is visible even before anyone has enqueued a run.
	empty := uuid.New()
	if _, err := s.Env.Owner.Exec(t.Context(), `INSERT INTO card(id,workspace_id,number,title,question,owner_id,board_column)
		SELECT $1,workspace_id,number+1,'Pending study','Pending study',owner_id,board_column FROM card WHERE id=$2`, empty, result.CardID); err != nil {
		t.Fatal(err)
	}
	var board []cardListView
	var mobile []mobileStudy
	if code := s.call(t, "GET", "/api/cards", dashboard, ws, nil, &board); code != 200 {
		t.Fatal(code)
	}
	if code := s.call(t, "GET", "/api/mobile/studies", native, ws, nil, &mobile); code != 200 || len(mobile) != 2 || len(board) != len(mobile) {
		t.Fatal(code, mobile)
	}
	for i, study := range mobile {
		if study.ID != board[i].ID || study.Title != board[i].Title || study.State != board[i].State {
			t.Fatal(study, board[i])
		}
		if study.ID == empty && study.Latest != nil {
			t.Fatal(study)
		}
		if study.ID == result.CardID && (study.Latest == nil || study.Latest.ID != runs.Items[0].ID || study.Latest.Status != runs.Items[0].Status) {
			t.Fatal(study)
		}
	}
	if code := s.call(t, "GET", "/api/mobile/study-suggestions", native, ws, nil, nil); code != 200 {
		t.Fatal(code)
	}
	if code := s.call(t, "GET", "/health", native, ws, nil, nil); code != 403 {
		t.Fatalf("bypassed shared admission: %d", code)
	}
}

func TestMobileQuestionRetry(t *testing.T) {
	s, owner, _, _, ws, _ := csFixture(t)
	csRegister(t, s, owner, ws, "online", "codex")
	chat := newLabChat(t, s, owner, ws)
	path := labChats + "/" + chat + "/questions"
	request := map[string]string{"text": "How did validation go?", "harness": "codex", "operation_id": uuid.NewString()}
	var first, second struct{ Message labMessage }
	if code := s.call(t, "POST", path, owner, ws, request, &first); code != 202 {
		t.Fatal(code)
	}
	if code := s.call(t, "POST", path, owner, ws, request, &second); code != 202 || first.Message.ID != second.Message.ID {
		t.Fatal(code, second)
	}
	request["text"] = "Different question"
	if code := s.call(t, "POST", path, owner, ws, request, nil); code != 409 {
		t.Fatal(code)
	}
}

func TestRetiredStudyDraftChats(t *testing.T) {
	s := newServer(t)
	email := "drafter@example.org"
	dashboard := s.signIn(t, email)
	ws := s.workspace(t, dashboard, "drafts").ID
	csRegister(t, s, dashboard, ws, "online", "codex")
	if code := s.call(t, "POST", "/auth/mobile/send-code", "", "", map[string]string{"email": email}, nil); code != 204 {
		t.Fatal(code)
	}
	var credentials struct{ Token string }
	if code := s.call(t, "POST", "/auth/mobile/verify-code", "", "", map[string]string{"email": email, "code": s.Mail.code(email)}, &credentials); code != 200 {
		t.Fatal(code)
	}
	native := credentials.Token
	window := newLabChat(t, s, dashboard, ws)
	seedHistoricalLabQuestion(t, s, window, "An existing dashboard question", "")
	draft := uuid.NewString()
	if _, err := s.Env.Owner.Exec(t.Context(), `INSERT INTO lab_chat(id,workspace_id,owner_id,purpose)
		SELECT $1,workspace_id,owner_id,'study_suggestion' FROM lab_chat WHERE id=$2`, draft, window); err != nil {
		t.Fatal(err)
	}
	seedHistoricalLabQuestion(t, s, draft, "Suggest a study", "")
	for _, req := range []struct{ method, path string }{
		{"GET", labChats}, {"POST", labChats}, {"GET", labChats + "/" + draft + "/messages"},
		{"POST", labChats + "/" + draft + "/questions"}, {"DELETE", labChats + "/" + draft},
	} {
		if code := s.call(t, req.method, req.path, native, ws, nil, nil); code != 403 {
			t.Fatal(req, code)
		}
	}
	var out struct{ Chats []struct{ ID string } }
	if code := s.call(t, "GET", labChats, dashboard, ws, nil, &out); code != 200 || len(out.Chats) != 1 || out.Chats[0].ID != window {
		t.Fatal(code, out)
	}
	if code := s.call(t, "GET", labChats+"/"+draft+"/messages", dashboard, ws, nil, nil); code != 404 {
		t.Fatal("dashboard read a retired draft chat", code)
	}
	if code := s.call(t, "GET", labChats+"?purpose=study_suggestion", dashboard, ws, nil, nil); code != 400 {
		t.Fatal("dashboard listed study suggestion chats", code)
	}
	if code := s.call(t, "POST", labChats, dashboard, ws, map[string]string{"purpose": "study_suggestion"}, nil); code != 400 {
		t.Fatal("dashboard created a study suggestion chat", code)
	}
	if code := s.call(t, "POST", labChats, native, ws, map[string]string{"purpose": "manual_decision_draft"}, nil); code != 403 {
		t.Fatal("phone created a decision draft", code)
	}
	const reasoning = "I found no findings in the lab context."
	if _, err := s.Env.Owner.Exec(t.Context(), `UPDATE lab_message SET launch_sources='{}' WHERE chat_id=$1 AND kind='question'`, draft); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Env.Owner.Exec(t.Context(), `INSERT INTO lab_message(workspace_id,chat_id,owner_id,agent_run_id,kind,body,statements,evidence,history_safe,proposal)
		SELECT workspace_id,chat_id,owner_id,agent_run_id,'answer','',jsonb_build_array(jsonb_build_object('kind','gap','text',$2::text,'cites','[]'::jsonb)),'[]',true,
		'{"new_study":{"description":"Does a nap improve recall?","stage":"experimentation","assignee_name":null,"assignee":null}}'
		FROM lab_message WHERE chat_id=$1 AND kind='question'`, draft, reasoning); err != nil {
		t.Fatal(err)
	}
	var suggestions json.RawMessage
	if code := s.call(t, "GET", "/api/mobile/study-suggestions", native, ws, nil, &suggestions); code != 200 || !strings.Contains(string(suggestions), "Does a nap improve recall?") || strings.Contains(string(suggestions), reasoning) {
		t.Fatal(code, string(suggestions))
	}

}

func TestMobileMachineActions(t *testing.T) {
	s := newServer(t)
	email := "machine-owner@example.org"
	owner := s.signIn(t, email)
	ws := s.workspace(t, owner, "machines").ID
	var creds struct{ Token string }
	if c := s.call(t, "POST", "/auth/mobile/send-code", "", "", map[string]string{"email": email}, nil); c != 204 {
		t.Fatal(c)
	}
	if c := s.call(t, "POST", "/auth/mobile/verify-code", "", "", map[string]string{"email": email, "code": s.Mail.code(email)}, &creds); c != 200 {
		t.Fatal(c)
	}
	ref := csRegister(t, s, owner, ws, "lab-machine", "claude")
	csRegister(t, s, owner, ws, "lab-machine", "codex")
	path := "/api/mobile/machines/" + ref.RuntimeID.String() + "/name"
	if c := s.call(t, "PATCH", path, creds.Token, ws, map[string]string{"name": "Microscope"}, nil); c != 204 {
		t.Fatal(c)
	}
	var machines []mobileMachine
	if c := s.call(t, "GET", "/api/mobile/machines", creds.Token, ws, nil, &machines); c != 200 || len(machines) != 1 || len(machines[0].Runtimes) != 2 || !machines[0].Owned || machines[0].Name != "Microscope" {
		t.Fatal(c, machines)
	}
	if n := s.count(t, "SELECT count(*) FROM runtime WHERE workspace_id=$1 AND custom_name='Microscope'", ws); n != 2 {
		t.Fatal(n)
	}
	other := s.workspace(t, owner, "other").ID
	if c := s.call(t, "PATCH", path, creds.Token, other, map[string]string{"name": "Wrong workspace"}, nil); c != 404 {
		t.Fatal(c)
	}
}
