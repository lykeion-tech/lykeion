package handler

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"testing"

	"github.com/google/uuid"
	"lykeion/server/internal/board"
)

func TestQuestionAnswersEndpoint(t *testing.T) {
	s := newServer(t)
	owner := s.signIn(t, "owner@lab.org")
	w := s.workspace(t, owner, "lab")
	member := s.member(t, owner, w.ID, "member@lab.org", "member")
	admin := s.member(t, owner, w.ID, "admin@lab.org", "admin")
	var agent struct {
		ID string `json:"id"`
	}
	if code := s.call(t, "POST", "/api/agents", owner, w.ID, map[string]any{"name": "Ada", "harness": "codex"}, &agent); code != 201 {
		t.Fatalf("agent %d", code)
	}
	agentID := uuid.MustParse(agent.ID)
	c := s.seedCard(t, owner, w.ID, "Which population?", &agentID, board.Grounding, board.Validation)
	if _, err := s.Env.Owner.Exec(t.Context(), `UPDATE card SET state='blocked',block_kind='decision' WHERE id=$1`, c.ID); err != nil {
		t.Fatal(err)
	}
	var id string
	set := `{"questions":[{"id":"scope","prompt":"Which population?","help_text":null,"required":true,"mode":"single","options":[{"id":"children","label":"Children","consequence":"children"},{"id":"adults","label":"Adults","consequence":"adults"}],"allow_other":true},{"id":"notes","prompt":"Notes?","help_text":null,"required":false,"mode":"text","options":[],"allow_other":false}]}`
	if err := s.Env.Owner.QueryRow(t.Context(), `INSERT INTO inbox_item(workspace_id,card_id,user_id,kind,text,options,question_set,raised_by,decision_kind,expires_at) VALUES($1,$2,$3,'agent_decision','Scope','[]',$4::jsonb,'agent','scope',now()+interval '1 day') RETURNING id`, w.ID, c.ID, s.userID(t, owner), set).Scan(&id); err != nil {
		t.Fatal(err)
	}
	path := "/api/decisions/" + id + "/answers"
	good := map[string]any{"answers": []any{map[string]any{"question_id": "scope", "option_ids": []string{"children"}, "text": nil}}}
	outsider := s.signIn(t, "outsider@lab.org")
	other := s.workspace(t, outsider, "other")
	if code := s.call(t, "POST", path, outsider, other.ID, good, nil); code != 404 {
		t.Fatalf("cross-workspace status %d", code)
	}
	if code := s.call(t, "POST", path, member, w.ID, good, nil); code != 403 {
		t.Fatalf("member status %d", code)
	}
	if code := s.call(t, "POST", path, owner, w.ID, map[string]any{"answers": []any{}}, nil); code != 400 {
		t.Fatalf("missing answer status %d", code)
	}
	if code := s.call(t, "POST", path, owner, w.ID, map[string]any{"answers": []any{map[string]any{"question_id": "scope", "option_ids": []string{"children"}, "text": "other"}}}, nil); code != 400 {
		t.Fatalf("mixed answer status %d", code)
	}
	req, _ := http.NewRequest("POST", s.URL+path, strings.NewReader(`{"answers":[]}`+strings.Repeat(" ", 10<<20)))
	req.Header.Set("Authorization", "Bearer "+owner)
	req.Header.Set("X-Workspace-ID", w.ID)
	req.Header.Set("Content-Type", "application/json")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	io.Copy(io.Discard, resp.Body)
	resp.Body.Close()
	if resp.StatusCode != 413 {
		t.Fatalf("oversize status %d", resp.StatusCode)
	}
	var status string
	var saved []byte
	if err := s.Env.Owner.QueryRow(t.Context(), `SELECT status,answers FROM inbox_item WHERE id=$1`, id).Scan(&status, &saved); err != nil || status != "open" || saved != nil {
		t.Fatalf("invalid requests wrote: %s %s %v", status, saved, err)
	}
	var out struct {
		Status  string          `json:"status"`
		Answers json.RawMessage `json:"answers"`
	}
	body, err := json.Marshal(good)
	if err != nil {
		t.Fatal(err)
	}
	body = append(body, bytes.Repeat([]byte(" "), (10<<20)-len(body))...)
	boundary, _ := http.NewRequest("POST", s.URL+path, bytes.NewReader(body))
	boundary.Header.Set("Authorization", "Bearer "+owner)
	boundary.Header.Set("X-Workspace-ID", w.ID)
	boundary.Header.Set("Content-Type", "application/json")
	response, err := http.DefaultClient.Do(boundary)
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	if err := json.NewDecoder(response.Body).Decode(&out); err != nil {
		t.Fatal(err)
	}
	if response.StatusCode != 200 || out.Status != "decided" || !bytes.Contains(out.Answers, []byte("children")) {
		t.Fatalf("owner at boundary %d %+v", response.StatusCode, out)
	}
	if code := s.call(t, "POST", path, owner, w.ID, good, nil); code != 409 {
		t.Fatalf("second answer status %d", code)
	}
	var cancelled string
	if err := s.Env.Owner.QueryRow(t.Context(), `INSERT INTO inbox_item(workspace_id,card_id,user_id,kind,text,options,question_set,raised_by,decision_kind,status,resolved_at) VALUES($1,$2,$3,'agent_decision','Cancelled','[]',$4::jsonb,'agent','scope','cancelled',now()) RETURNING id`, w.ID, c.ID, s.userID(t, owner), set).Scan(&cancelled); err != nil {
		t.Fatal(err)
	}
	if code := s.call(t, "POST", "/api/decisions/"+cancelled+"/answers", owner, w.ID, good, nil); code != 409 {
		t.Fatalf("cancelled answer status %d", code)
	}
	history := s.decisions(t, owner, w.ID, "?view=history")
	if len(history) != 2 || (history[0].ID != id && history[1].ID != id) {
		t.Fatalf("history %+v", history)
	}
	second := s.seedCard(t, owner, w.ID, "Second question", &agentID, board.Grounding, board.Validation)
	if _, err := s.Env.Owner.Exec(t.Context(), `UPDATE card SET state='blocked',block_kind='decision' WHERE id=$1`, second.ID); err != nil {
		t.Fatal(err)
	}
	var adminItem string
	if err := s.Env.Owner.QueryRow(t.Context(), `INSERT INTO inbox_item(workspace_id,card_id,user_id,kind,text,options,question_set,raised_by,decision_kind) VALUES($1,$2,$3,'agent_decision','Scope','[]',$4::jsonb,'agent','scope') RETURNING id`, w.ID, second.ID, s.userID(t, owner), set).Scan(&adminItem); err != nil {
		t.Fatal(err)
	}
	if code := s.call(t, "POST", "/api/decisions/"+adminItem+"/answers", admin, w.ID, good, nil); code != 200 {
		t.Fatalf("admin answer status %d", code)
	}
}
