package handler

import (
	"encoding/json"
	"fmt"
	"slices"
	"testing"

	"github.com/google/uuid"

	"lykeion/server/internal/service"
)

// blockedCard is a card in column, Blocked for kind, with cap uses spent.
func (s *server) blockedCard(t *testing.T, tok, ws, column, kind string, used int) card {
	t.Helper()
	var c card
	if code := s.call(t, "POST", "/api/cards", tok, ws, map[string]any{"question": "q " + uuid.NewString()}, &c); code != 201 {
		t.Fatalf("card: %d", code)
	}
	if _, err := s.Env.Owner.Exec(t.Context(), `UPDATE card SET board_column = $2, round = 3, flag = 'escalated', block_kind = $3, route_backs = $4 WHERE id = $1`,
		c.ID, column, kind, used); err != nil {
		t.Fatal(err)
	}
	return c
}

// exec runs a statement as the owner, failing the test on an error.
func (s *server) exec(t *testing.T, q string, args ...any) {
	t.Helper()
	if _, err := s.Env.Owner.Exec(t.Context(), q, args...); err != nil {
		t.Fatal(err)
	}
}

type boardOut struct {
	BoardColumn string `json:"board_column"`
	Flag        string `json:"flag"`
	BlockKind   string `json:"block_kind"`
	Ending      string `json:"ending"`
	Cap         int    `json:"cap"`
	Round       int    `json:"round"`
	RouteBacks  int    `json:"route_backs"`
	Reopens     int    `json:"reopens"`
	Question    string `json:"question"`
}

func TestUnblockRoutes(t *testing.T) {
	s := newServer(t)
	a := s.signIn(t, "a@lab.org")
	w := s.workspace(t, a, "lab")
	for _, tc := range []struct {
		column, kind, action string
		cap                  int
		wantColumn           string
	}{
		{"validation", "cap", "raise_cap", 6, "validation"},
		{"experimentation", "no_feasible_test", "add_data", 0, "hypotheses"},
		{"grounding", "infra", "resume", 0, "grounding"},
		{"hypotheses", "drift", "resume", 0, "hypotheses"},
		{"validation", "human_run", "human_run_arrived", 0, "experimentation"},
	} {
		c := s.blockedCard(t, a, w.ID, tc.column, tc.kind, 4)
		var out boardOut
		if code := s.call(t, "POST", "/api/cards/"+c.ID+"/unblock", a, w.ID, map[string]any{"action": tc.action, "cap": tc.cap}, &out); code != 200 {
			t.Fatalf("%s on %s: %d", tc.action, tc.kind, code)
		}
		if out.Flag != "" || out.BlockKind != "" || out.BoardColumn != tc.wantColumn || out.Round != 4 || (tc.cap != 0 && out.Cap != tc.cap) {
			t.Fatalf("%s on %s: %+v", tc.action, tc.kind, out)
		}
	}
	c := s.blockedCard(t, a, w.ID, "validation", "cap", 4)
	for _, bad := range []map[string]any{{"action": "add_data"}, {"action": "resume"}, {"action": "raise_cap", "cap": 4}, {"action": "nonsense"}} {
		var refused struct{ Error string }
		if code := s.call(t, "POST", "/api/cards/"+c.ID+"/unblock", a, w.ID, bad, &refused); code != 409 || refused.Error == "" {
			t.Fatalf("%v: %d %+v", bad, code, refused)
		}
	}
	var flag, kind string
	var round int
	if err := s.Env.Owner.QueryRow(t.Context(), `SELECT flag, block_kind, round FROM card WHERE id = $1`, c.ID).Scan(&flag, &kind, &round); err != nil {
		t.Fatal(err)
	}
	if flag != "escalated" || kind != "cap" || round != 3 {
		t.Fatalf("a refused unblock changed the card: %s %s %d", flag, kind, round)
	}
}

func TestCloseWritesStop(t *testing.T) {
	s := newServer(t)
	a := s.signIn(t, "a@lab.org")
	w := s.workspace(t, a, "lab")
	c := s.blockedCard(t, a, w.ID, "validation", "cap", 4)
	for i, g := range []string{
		`{"column":"validation","round":1,"verdict":"fail","reason":"n too small"}`,
		`{"column":"validation","round":2,"verdict":"fail","reason":"confound left"}`,
	} {
		if _, err := s.Env.Owner.Exec(t.Context(), `INSERT INTO record (workspace_id, card_id, board_column, round, kind, role, key, body)
			VALUES ($1, $2, 'validation', $3, 'gate', 'server', $4, $5)`, w.ID, c.ID, i+1, fmt.Sprint(i+1), g); err != nil {
			t.Fatal(err)
		}
	}
	var out boardOut
	if code := s.call(t, "POST", "/api/cards/"+c.ID+"/close", a, w.ID, map[string]string{"reason": "not worth more"}, &out); code != 200 {
		t.Fatalf("close: %d", code)
	}
	if out.Ending != "stopped" || out.BoardColumn != "done" || out.Flag != "" {
		t.Fatalf("closed: %+v", out)
	}
	var body []byte
	if err := s.Env.Owner.QueryRow(t.Context(), `SELECT body FROM record WHERE card_id = $1 AND kind = 'stop' AND board_column = 'validation' AND role = 'server'`, c.ID).Scan(&body); err != nil {
		t.Fatalf("stop record: %v", err)
	}
	var stop struct {
		Reason    string   `json:"reason"`
		BlockKind string   `json:"block_kind"`
		Failures  []string `json:"failures"`
		LastGate  struct {
			Round int `json:"round"`
		} `json:"last_gate"`
	}
	if err := json.Unmarshal(body, &stop); err != nil {
		t.Fatal(err)
	}
	if stop.Reason != "not worth more" || stop.BlockKind != "cap" || !slices.Equal(stop.Failures, []string{"n too small", "confound left"}) || stop.LastGate.Round != 2 {
		t.Fatalf("stop: %s", body)
	}
	if code := s.call(t, "POST", "/api/cards/"+c.ID+"/close", a, w.ID, map[string]string{"reason": "again"}, nil); code != 409 {
		t.Fatalf("closed an ended card: %d", code)
	}
	if code := s.call(t, "POST", "/api/cards/"+c.ID+"/close", a, w.ID, map[string]string{"reason": " "}, nil); code != 400 {
		t.Fatalf("close without a reason: %d", code)
	}
}

// A reopened study's stop record lists only the failures since its last reopen.
func TestCloseListsFailuresSinceTheLastReopen(t *testing.T) {
	s := newServer(t)
	a := s.signIn(t, "a@lab.org")
	w := s.workspace(t, a, "lab")
	c := s.blockedCard(t, a, w.ID, "validation", "cap", 4)
	s.exec(t, `UPDATE card SET reopens = 1 WHERE id = $1`, c.ID)
	for i, r := range []string{"before the release", "after the reopen"} {
		s.exec(t, `INSERT INTO record (workspace_id, card_id, board_column, round, kind, role, key, body)
			VALUES ($1, $2, 'validation', $3, 'gate', 'server', $4, jsonb_build_object('verdict', 'fail', 'reason', $5::text))`, w.ID, c.ID, i*2+1, fmt.Sprint(i), r)
	}
	s.exec(t, `INSERT INTO record (workspace_id, card_id, board_column, round, kind, role, key, body)
		VALUES ($1, $2, 'grounding', 2, 'reopen_evidence', 'server', '1', '{"reopen":1}')`, w.ID, c.ID)
	if code := s.call(t, "POST", "/api/cards/"+c.ID+"/close", a, w.ID, map[string]string{"reason": "enough"}, nil); code != 200 {
		t.Fatalf("close: %d", code)
	}
	var stop struct{ Failures []string }
	var body []byte
	if err := s.Env.Owner.QueryRow(t.Context(), `SELECT body FROM record WHERE card_id = $1 AND kind = 'stop'`, c.ID).Scan(&body); err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(body, &stop); err != nil || !slices.Equal(stop.Failures, []string{"after the reopen"}) {
		t.Fatalf("stop: %s", body)
	}
}

func (s *server) release(t *testing.T, id string) {
	t.Helper()
	if _, err := s.Env.Owner.Exec(t.Context(), `UPDATE card SET board_column = 'done', ending = 'released', state = 'ready', route_backs = 2, flag = '', block_kind = '' WHERE id = $1`, id); err != nil {
		t.Fatal(err)
	}
}

func TestReopenRoute(t *testing.T) {
	s := newServer(t)
	a := s.signIn(t, "a@lab.org")
	w := s.workspace(t, a, "lab")
	var c card
	s.call(t, "POST", "/api/cards", a, w.ID, map[string]any{"question": "q"}, &c)
	s.exec(t, `UPDATE card SET board_column = 'validation', round = 3 WHERE id = $1`, c.ID)
	in := map[string]string{"reason": "a retraction", "evidence": "doi:10.1/x"}
	for _, prep := range []string{
		``, // active
		`UPDATE card SET flag = 'escalated', block_kind = 'cap' WHERE id = $1`,
		`UPDATE card SET board_column = 'done', ending = 'stopped', flag = '', block_kind = '' WHERE id = $1`,
	} {
		if prep != "" {
			s.exec(t, prep, c.ID)
		}
		if code := s.call(t, "POST", "/api/cards/"+c.ID+"/reopen", a, w.ID, in, nil); code != 409 {
			t.Fatalf("reopened an unreleased card (%s): %d", prep, code)
		}
	}
	if n := s.count(t, `SELECT count(*) FROM card WHERE id = $1 AND reopens = 0 AND ending = 'stopped' AND round = 3`, c.ID); n != 1 {
		t.Fatal("a refused reopen changed the card")
	}
	if code := s.call(t, "POST", "/api/cards/"+c.ID+"/reopen", a, w.ID, map[string]string{"evidence": "x"}, nil); code != 400 {
		t.Fatalf("reopen without a reason: %d", code)
	}

	s.release(t, c.ID)
	var out boardOut
	if code := s.call(t, "POST", "/api/cards/"+c.ID+"/reopen", a, w.ID, in, &out); code != 200 {
		t.Fatalf("reopen: %d", code)
	}
	if out.BoardColumn != "grounding" || out.Ending != "" || out.Reopens != 1 || out.RouteBacks != 0 {
		t.Fatalf("reopened: %+v", out)
	}
	if s.count(t, `SELECT count(*) FROM inbox_item WHERE card_id = $1 AND kind = 'reopened' AND text = 'a retraction'`, c.ID) != 1 ||
		s.count(t, `SELECT count(*) FROM record WHERE card_id = $1 AND kind = 'report_status' AND board_column = 'reporting' AND key = '0'
			AND body = '{"state":"under_review","release":0}'::jsonb`, c.ID) != 1 ||
		s.count(t, `SELECT count(*) FROM record WHERE card_id = $1 AND kind = 'reopen_evidence' AND board_column = 'grounding' AND key = '1'
			AND body = '{"reopen":1,"reason":"a retraction","evidence":"doi:10.1/x"}'::jsonb`, c.ID) != 1 ||
		s.count(t, `SELECT count(*) FROM event WHERE card_id = $1 AND type = 'card.reopened'`, c.ID) != 1 {
		t.Fatal("reopen did not write its inbox item, records and event")
	}

	s.release(t, c.ID)
	if code := s.call(t, "POST", "/api/cards/"+c.ID+"/reopen", a, w.ID, in, &out); code != 200 || out.Reopens != 2 || out.RouteBacks != 0 {
		t.Fatalf("second reopen: %d %+v", code, out)
	}
	if s.count(t, `SELECT count(*) FROM record WHERE card_id = $1 AND kind = 'report_status' AND key IN ('0', '1') AND superseded_at IS NULL`, c.ID) != 2 {
		t.Fatal("the earlier release's status was lost")
	}
}

func TestAutoresearchScopesIncludeReopen(t *testing.T) {
	if !slices.Contains(service.Scopes("autoresearch"), service.ScopeCardReopen) || service.ScopeCardReopen != "card:reopen" {
		t.Fatalf("autoresearch scopes: %v", service.Scopes("autoresearch"))
	}
	if slices.Contains(service.Scopes("role"), service.ScopeCardReopen) {
		t.Fatal("a role run may reopen cards")
	}
	s := newServer(t)
	_, ws, c, run := s.roleFixture(t)
	s.release(t, c.ID)
	in := map[string]string{"reason": "contradicted", "evidence": "pmid:1"}
	if code := s.call(t, "POST", "/api/cards/"+c.ID+"/reopen", run.token, ws, in, nil); code != 403 {
		t.Fatalf("a role run's token reopened a card: %d", code)
	}

	jwt := s.signIn(t, "role@lab.org")
	_, _, ltk, _ := s.runOnly(t, jwt, ws)
	var out boardOut
	if code := s.call(t, "POST", "/api/cards/"+c.ID+"/reopen", ltk, "", in, &out); code != 200 || out.BoardColumn != "grounding" || out.Reopens != 1 {
		t.Fatalf("an autoresearch run's reopen: %d %+v", code, out)
	}
	if code := s.call(t, "POST", "/api/cards/"+c.ID+"/reopen", ltk, "", in, nil); code != 409 {
		t.Fatalf("an autoresearch run reopened an active card: %d", code)
	}
}

// An Autoresearch run reopens only studies its requester owns.
func TestAutoresearchReopensOnlyItsOwnersStudies(t *testing.T) {
	s := newServer(t)
	owner := s.signIn(t, "owner@lab.org")
	w := s.workspace(t, owner, "lab")
	b := s.member(t, owner, w.ID, "b@lab.org", "member")
	var mine, theirs card
	if code := s.call(t, "POST", "/api/cards", owner, w.ID, map[string]any{"question": "mine"}, &mine); code != 201 {
		t.Fatalf("card: %d", code)
	}
	if code := s.call(t, "POST", "/api/cards", b, w.ID, map[string]any{"question": "theirs"}, &theirs); code != 201 {
		t.Fatalf("card: %d", code)
	}
	s.release(t, mine.ID)
	s.release(t, theirs.ID)
	_, _, ltk, _ := s.runOnly(t, owner, w.ID)
	in := map[string]string{"reason": "contradicted", "evidence": "pmid:1"}
	var refused struct{ Error string }
	if code := s.call(t, "POST", "/api/cards/"+theirs.ID+"/reopen", ltk, "", in, &refused); code != 403 || refused.Error != "this Autoresearch can only reopen its owner's studies" {
		t.Fatalf("reopened another member's study: %d %+v", code, refused)
	}
	if s.count(t, `SELECT count(*) FROM card WHERE id = $1 AND ending = 'released' AND reopens = 0`, theirs.ID) != 1 ||
		s.count(t, `SELECT count(*) FROM record WHERE card_id = $1`, theirs.ID) != 0 {
		t.Fatal("a refused reopen changed the study")
	}
	var out boardOut
	if code := s.call(t, "POST", "/api/cards/"+mine.ID+"/reopen", ltk, "", in, &out); code != 200 || out.BoardColumn != "grounding" || out.Reopens != 1 {
		t.Fatalf("the requester's own study: %d %+v", code, out)
	}
}

func TestRevertRevisionRoute(t *testing.T) {
	s := newServer(t)
	a := s.signIn(t, "a@lab.org")
	w := s.workspace(t, a, "lab")
	var c card
	s.call(t, "POST", "/api/cards", a, w.ID, map[string]any{"question": "Does X raise Y now?"}, &c)
	s.exec(t, `UPDATE card SET board_column = 'hypotheses', round = 3 WHERE id = $1`, c.ID)
	if code := s.call(t, "POST", "/api/cards/"+c.ID+"/revert-revision", a, w.ID, nil, nil); code != 409 {
		t.Fatalf("reverted a card never revised: %d", code)
	}
	s.exec(t, `UPDATE card SET question = 'revised', route_backs = 1 WHERE id = $1`, c.ID)
	for key, text := range map[string]string{"0": "Does X raise Y now?", "1": "revised"} {
		s.exec(t, `INSERT INTO record (workspace_id, card_id, board_column, round, kind, role, key, body)
			VALUES ($1, $2, 'hypotheses', 3, 'starting_point', 'server', $3, jsonb_build_object('text', $4::text))`, w.ID, c.ID, key, text)
	}
	s.exec(t, `UPDATE card SET flag = 'escalated', block_kind = 'drift' WHERE id = $1`, c.ID)
	var refused struct{ Error string }
	if code := s.call(t, "POST", "/api/cards/"+c.ID+"/revert-revision", a, w.ID, nil, &refused); code != 409 || refused.Error != "unblock or close this study first" {
		t.Fatalf("reverted a Blocked study: %d %+v", code, refused)
	}
	if s.count(t, `SELECT count(*) FROM card WHERE id = $1 AND question = 'revised' AND board_column = 'hypotheses' AND block_kind = 'drift'`, c.ID) != 1 {
		t.Fatal("a refused revert changed the study")
	}
	s.exec(t, `UPDATE card SET flag = '', block_kind = '' WHERE id = $1`, c.ID)
	var out boardOut
	if code := s.call(t, "POST", "/api/cards/"+c.ID+"/revert-revision", a, w.ID, nil, &out); code != 200 {
		t.Fatalf("revert: %d", code)
	}
	if out.Question != "Does X raise Y now?" || out.BoardColumn != "grounding" || out.RouteBacks != 1 {
		t.Fatalf("reverted: %+v", out)
	}
}
