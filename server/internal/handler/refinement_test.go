package handler

import (
	"github.com/google/uuid"
	"testing"
)

func TestResearchCRUDAndStudyProperties(t *testing.T) {
	s := newServer(t)
	s.builtIns(t)
	s.H.Engine = nil
	a := s.signIn(t, "a@lab.org")
	w := s.workspace(t, a, "lab")
	var res map[string]any
	if code := s.call(t, "POST", "/api/researches", a, w.ID, map[string]any{"name": "Methods", "description": "Programme"}, nil); code != 201 {
		t.Fatalf("create research: %d, want 201", code)
	}
	var list []map[string]any
	if code := s.call(t, "GET", "/api/researches", a, w.ID, nil, &list); code != 200 || len(list) != 1 {
		t.Fatalf("list: %d %+v", code, list)
	}
	rid := list[0]["id"].(string)
	if code := s.call(t, "PATCH", "/api/researches/"+rid, a, w.ID, map[string]any{"name": "Sharper methods"}, &res); code != 200 || res["description"] != "Programme" {
		t.Fatalf("update: %d %+v", code, res)
	}
	var card map[string]any
	s.call(t, "POST", "/api/cards", a, w.ID, map[string]any{"predicates": testPredicates, "question": "How?"}, &card)
	id, owner := card["id"].(string), card["owner_id"].(string)
	path := "/api/cards/" + id + "/properties"
	if _, err := s.Env.Owner.Exec(t.Context(), `UPDATE card SET board_column='grounding',rounds=2,spent_usd=1 WHERE id=$1`, id); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Env.Owner.Exec(t.Context(), `INSERT INTO agent_run (workspace_id,card_id,responsible_user_id,board_column,round,role,harness,prompt,continuity_scope,policy_digest,session_id) VALUES ($1,$2,$3,'grounding',2,'writer','codex','Existing prompt','same-scope','same-policy','same-session')`, w.ID, id, owner); err != nil {
		t.Fatal(err)
	}
	var scienceBefore, runBefore string
	if err := s.Env.Owner.QueryRow(t.Context(), `SELECT (to_jsonb(c) - ARRAY['research_id','assignee_type','assignee_id','version'])::text FROM card c WHERE id=$1`, id).Scan(&scienceBefore); err != nil {
		t.Fatal(err)
	}
	if err := s.Env.Owner.QueryRow(t.Context(), `SELECT to_jsonb(r)::text FROM agent_run r WHERE card_id=$1`, id).Scan(&runBefore); err != nil {
		t.Fatal(err)
	}
	if code := s.call(t, "PATCH", path, a, w.ID, map[string]any{"research_id": rid, "assignee_type": nil, "assignee_id": nil}, nil); code != 400 {
		t.Fatalf("a study's assignee changed: %d", code)
	}
	if code := s.call(t, "PATCH", path, a, w.ID, map[string]any{"research_id": rid}, &card); code != 200 {
		t.Fatalf("properties: %d %+v", code, card)
	}
	for _, route := range []string{"/api/cards/" + id, "/api/cards"} {
		if route == "/api/cards" {
			var cards []map[string]any
			s.call(t, "GET", route, a, w.ID, nil, &cards)
			card = cards[0]
		} else {
			s.call(t, "GET", route, a, w.ID, nil, &card)
		}
		if card["research_id"] != rid || card["assignee_type"] != "team" || card["assignee_id"] != nil || card["owner_id"] != owner || card["board_column"] != "grounding" || card["rounds"] != float64(2) || card["spent_usd"] != float64(1) {
			t.Fatalf("canonical/science fields: %+v", card)
		}
	}
	var detail struct {
		Name    string
		Studies []map[string]any
	}
	if code := s.call(t, "GET", "/api/researches/"+rid, a, w.ID, nil, &detail); code != 200 || detail.Name != "Sharper methods" || len(detail.Studies) != 1 || detail.Studies[0]["id"] != id {
		t.Fatalf("detail: %d %+v", code, detail)
	}
	for _, bad := range []map[string]any{{"assignee_type": "member"}, {"assignee_id": owner}, {"assignee_type": "member", "assignee_id": nil}, {"research_id": uuid.NewString(), "assignee_type": nil, "assignee_id": nil}, {}} {
		if code := s.call(t, "PATCH", path, a, w.ID, bad, nil); code != 400 {
			t.Fatalf("bad properties %v: %d", bad, code)
		}
	}
	s.call(t, "GET", "/api/cards/"+id, a, w.ID, nil, &card)
	if card["research_id"] != rid || card["assignee_type"] != "team" {
		t.Fatalf("rejected patch leaked: %+v", card)
	}
	if code := s.call(t, "PATCH", path, a, w.ID, map[string]any{"research_id": nil}, &card); code != 200 || card["research_id"] != nil || card["assignee_type"] != "team" {
		t.Fatalf("clear: %d %+v", code, card)
	}
	var runs, events int
	s.Env.Owner.QueryRow(t.Context(), `SELECT count(*) FROM agent_run WHERE card_id=$1`, id).Scan(&runs)
	s.Env.Owner.QueryRow(t.Context(), `SELECT count(*) FROM event WHERE card_id=$1 AND type='card.properties_updated'`, id).Scan(&events)
	var scienceAfter, runAfter string
	if err := s.Env.Owner.QueryRow(t.Context(), `SELECT (to_jsonb(c) - ARRAY['research_id','assignee_type','assignee_id','version'])::text FROM card c WHERE id=$1`, id).Scan(&scienceAfter); err != nil {
		t.Fatal(err)
	}
	if err := s.Env.Owner.QueryRow(t.Context(), `SELECT to_jsonb(r)::text FROM agent_run r WHERE card_id=$1`, id).Scan(&runAfter); err != nil {
		t.Fatal(err)
	}
	if scienceBefore != scienceAfter || runBefore != runAfter {
		t.Fatalf("science or existing role/continuity changed: %s / %s, %s / %s", scienceBefore, scienceAfter, runBefore, runAfter)
	}
	if runs != 1 || events != 2 {
		t.Fatalf("runs=%d events=%d", runs, events)
	}
}

func TestAgentRegistryAndStudyIsolation(t *testing.T) {
	s := newServer(t)
	s.H.Engine = nil
	a, b, c := s.signIn(t, "a@lab.org"), s.signIn(t, "b@lab.org"), s.signIn(t, "c@else.org")
	wa, wc := s.workspace(t, a, "lab-a"), s.workspace(t, c, "lab-c")
	inv := s.invite(t, a, wa.ID, "b@lab.org", "member")
	s.call(t, "POST", "/api/invitations/"+inv+"/accept", b, "", nil, nil)
	path := "/api/cards/" + s.idleCard(t, a, wa.ID, "How?").ID + "/properties"
	if code := s.call(t, "POST", "/api/agents", a, wa.ID, map[string]any{"name": "Ada", "description": "Methods", "instructions": "Investigate", "harness": "codex", "model": "gpt-5", "visibility": "private"}, nil); code != 201 {
		t.Fatalf("create agent: %d, want 201", code)
	}
	var agents []map[string]any
	s.call(t, "GET", "/api/agents", a, wa.ID, nil, &agents)
	if len(agents) != 1 {
		t.Fatalf("agents %+v", agents)
	}
	ag := agents[0]
	aid := ag["id"].(string)
	if ag["owner_id"] != s.userID(t, a) || ag["instructions"] != "Investigate" {
		t.Fatalf("agent %+v", ag)
	}
	s.call(t, "GET", "/api/agents", b, wa.ID, nil, &agents)
	if len(agents) != 0 {
		t.Fatalf("private exposed %+v", agents)
	}
	for _, method := range []string{"GET", "PATCH"} {
		if code := s.call(t, method, "/api/agents/"+aid, b, wa.ID, map[string]any{"name": "stolen"}, nil); code != 404 {
			t.Fatalf("private %s: %d", method, code)
		}
	}
	if code := s.call(t, "GET", "/api/agents/"+aid, c, wc.ID, nil, nil); code != 404 {
		t.Fatalf("cross agent: %d", code)
	}
	if code := s.call(t, "PATCH", "/api/agents/"+aid, a, wa.ID, map[string]any{"visibility": "workspace", "name": "Ada Lovelace"}, &ag); code != 200 || ag["name"] != "Ada Lovelace" {
		t.Fatalf("edit: %d %+v", code, ag)
	}
	if code := s.call(t, "PATCH", "/api/agents/"+aid, b, wa.ID, map[string]any{"name": "stolen"}, nil); code != 403 {
		t.Fatalf("nonowner edit: %d", code)
	}
	if code := s.call(t, "POST", "/api/agents/"+aid+"/archive", a, wa.ID, nil, nil); code != 200 {
		t.Fatalf("archive: %d", code)
	}
	if code := s.call(t, "PATCH", "/api/agents/"+aid, a, wa.ID, map[string]any{"name": "revive"}, nil); code != 409 {
		t.Fatalf("archived edit: %d", code)
	}
	if code := s.call(t, "POST", "/api/agents/"+aid+"/archive", a, wa.ID, nil, nil); code != 409 {
		t.Fatalf("archive twice: %d", code)
	}
	var res map[string]any
	s.call(t, "POST", "/api/researches", c, wc.ID, map[string]any{"name": "Private"}, &res)
	for _, method := range []string{"GET", "PATCH"} {
		if code := s.call(t, method, "/api/researches/"+res["id"].(string), a, wa.ID, map[string]any{"name": "stolen"}, nil); code != 404 {
			t.Fatalf("cross research %s: %d", method, code)
		}
	}
	if code := s.call(t, "PATCH", path, a, wa.ID, map[string]any{"research_id": res["id"]}, nil); code != 400 {
		t.Fatalf("cross research: %d", code)
	}
	if code := s.call(t, "PATCH", path, c, wc.ID, map[string]any{"research_id": nil}, nil); code != 404 {
		t.Fatalf("cross card: %d", code)
	}
}

func TestRegistryValidationAndOwnerDeparture(t *testing.T) {
	s := newServer(t)
	s.H.Engine = nil
	owner, member := s.signIn(t, "owner@lab.org"), s.signIn(t, "member@lab.org")
	w := s.workspace(t, owner, "registry")
	inv := s.invite(t, owner, w.ID, "member@lab.org", "member")
	s.call(t, "POST", "/api/invitations/"+inv+"/accept", member, "", nil, nil)
	for _, bad := range []map[string]any{{"name": "", "harness": "codex"}, {"name": "A", "harness": "other"}, {"name": "A", "harness": "codex", "model": "--execute"}, {"name": "A", "harness": "codex", "instructions": "x\x00y"}, {"name": "A", "harness": "codex", "visibility": "public"}, {"name": "A", "harness": "codex", "env": map[string]string{"KEY": "value"}}} {
		if code := s.call(t, "POST", "/api/agents", owner, w.ID, bad, nil); code != 400 {
			t.Fatalf("invalid agent %v: %d", bad, code)
		}
	}
	var a map[string]any
	if code := s.call(t, "POST", "/api/agents", member, w.ID, map[string]any{"name": "Ada", "harness": "codex", "visibility": "private"}, &a); code != 201 {
		t.Fatalf("member create: %d", code)
	}
	id := a["id"].(string)
	if code := s.call(t, "POST", "/api/agents", member, w.ID, map[string]any{"name": "ada", "harness": "codex"}, nil); code != 409 {
		t.Fatalf("live name duplicate: %d", code)
	}
	if code := s.call(t, "GET", "/api/agents/"+id, owner, w.ID, nil, &a); code != 200 {
		t.Fatalf("admin private visibility: %d", code)
	}
	if code := s.call(t, "PATCH", "/api/agents/"+id, owner, w.ID, map[string]any{"description": "admin edit"}, nil); code != 200 {
		t.Fatalf("admin private edit: %d", code)
	}
	studyID := s.idleCard(t, owner, w.ID, "Preserve on departure").ID
	if _, err := s.Env.Owner.Exec(t.Context(), `UPDATE card SET board_column='grounding',rounds=3,spent_usd=2 WHERE id=$1`, studyID); err != nil {
		t.Fatal(err)
	}
	if code := s.call(t, "POST", "/api/workspaces/"+w.ID+"/leave", member, "", nil, nil); code != 204 {
		t.Fatalf("leave: %d", code)
	}
	var study map[string]any
	if code := s.call(t, "GET", "/api/cards/"+studyID, owner, w.ID, nil, &study); code != 200 || study["board_column"] != "grounding" || study["rounds"] != float64(3) || study["spent_usd"] != float64(2) {
		t.Fatalf("departure must preserve the study: %d %+v", code, study)
	}
	if code := s.call(t, "GET", "/api/agents/"+id, owner, w.ID, nil, &a); code != 200 || a["archived_at"] == nil {
		t.Fatalf("departure archives owned agent: %d %+v", code, a)
	}
	if code := s.call(t, "POST", "/api/agents", owner, w.ID, map[string]any{"name": "Ada", "harness": "codex"}, nil); code != 201 {
		t.Fatalf("archive frees name: %d", code)
	}
}
