package handler

import (
	"testing"
)

// A retried run keeps its earlier attempt's transcript: by default the
// messages route serves every attempt in order, ?attempt=N still serves one,
// and the study's runs say how each earlier attempt ended and where.
func TestRunMessagesKeepEarlierAttempts(t *testing.T) {
	s := newServer(t)
	a := s.signIn(t, "a@lab.org")
	w := s.workspace(t, a, "lab")
	tok := s.personalToken(t, a)
	var rts []struct{ ID string }
	if code := s.call(t, "POST", "/api/daemon/register", tok, "", map[string]any{"workspace_id": w.ID, "daemon_id": "d1", "device_name": "laptop", "harnesses": []string{"claude"}}, &rts); code != 200 || len(rts) != 1 {
		t.Fatalf("register: %d %+v", code, rts)
	}
	c := s.idleCard(t, a, w.ID, "Why?")
	var run string
	if err := s.Env.Owner.QueryRow(t.Context(), `INSERT INTO agent_run (workspace_id, card_id, responsible_user_id, board_column, round, role, harness, prompt, status, attempt, failure_reason, machine_name, retry_after)
		VALUES ($1, $2, $3, 'grounding', 1, 'scoper', 'claude', 'p', 'queued', 2, 'runtime_offline', 'laptop', now()) RETURNING id::text`, w.ID, c.ID, c.OwnerID).Scan(&run); err != nil {
		t.Fatal(err)
	}
	s.mustUpdate(t, `INSERT INTO agent_run_receipt (workspace_id, agent_run_id, attempt, runtime_id, result)
		VALUES ((SELECT workspace_id FROM agent_run WHERE id = $1), $1, 1, $2, '{"failure_reason":"runtime_offline","machine_name":"laptop"}')`, run, rts[0].ID)
	for _, m := range [][2]int{{1, 1}, {1, 2}, {2, 1}} {
		s.mustUpdate(t, `INSERT INTO agent_run_message (workspace_id, agent_run_id, attempt, seq, type, content)
			SELECT workspace_id, id, $2, $3, 'text', '"x"' FROM agent_run WHERE id = $1`, run, m[0], m[1])
	}

	type msg struct{ Attempt, Seq int }
	read := func(query string) []msg {
		t.Helper()
		var out []msg
		if code := s.call(t, "GET", "/api/agent-runs/"+run+"/messages"+query, a, w.ID, nil, &out); code != 200 {
			t.Fatalf("messages%s: %d", query, code)
		}
		return out
	}
	if got := read(""); len(got) != 3 || got[0] != (msg{1, 1}) || got[1] != (msg{1, 2}) || got[2] != (msg{2, 1}) {
		t.Fatalf("all attempts: %+v", got)
	}
	if got := read("?attempt=1"); len(got) != 2 || got[0] != (msg{1, 1}) || got[1] != (msg{1, 2}) {
		t.Fatalf("attempt 1: %+v", got)
	}
	if got := read("?attempt=2&after=0"); len(got) != 1 || got[0] != (msg{2, 1}) {
		t.Fatalf("attempt 2: %+v", got)
	}
	// The cursor across attempts is the last message's attempt and seq.
	if got := read("?after_attempt=1&after=2"); len(got) != 1 || got[0] != (msg{2, 1}) {
		t.Fatalf("after (1, 2): %+v", got)
	}
	if got := read("?after_attempt=2&after=1"); len(got) != 0 {
		t.Fatalf("after the last message: %+v", got)
	}
	if code := s.call(t, "GET", "/api/agent-runs/"+run+"/messages?after_attempt=-1", a, w.ID, nil, nil); code != 400 {
		t.Fatalf("negative after_attempt: %d", code)
	}

	var runs []struct {
		ID          string
		AttemptEnds []struct {
			Attempt       int
			FailureReason string `json:"failure_reason"`
			Machine       string
		} `json:"attempt_ends"`
	}
	if code := s.call(t, "GET", "/api/cards/"+c.ID+"/runs", a, w.ID, nil, &runs); code != 200 || len(runs) != 1 {
		t.Fatalf("runs: %d %+v", code, runs)
	}
	if ends := runs[0].AttemptEnds; len(ends) != 1 || ends[0].Attempt != 1 || ends[0].FailureReason != "runtime_offline" || ends[0].Machine != "laptop" {
		t.Fatalf("attempt ends: %+v", ends)
	}
}

// A study whose run waits on a machine says so, to every member who can see
// the study: the wait names the machine, nothing secret. The machine is the
// one the run's last attempt ran on.
func TestWaitingStudyNamesTheMachine(t *testing.T) {
	s := newServer(t)
	a := s.signIn(t, "a@lab.org")
	w := s.workspace(t, a, "lab")
	tok := s.personalToken(t, a)
	b := s.member(t, a, w.ID, "b@lab.org", "member")
	rt := s.laptop(t, tok, w.ID)
	c := s.idleCard(t, a, w.ID, "Why?")
	var run string
	if err := s.Env.Owner.QueryRow(t.Context(), `INSERT INTO agent_run (workspace_id, card_id, responsible_user_id, board_column, round, role, harness, prompt, status, attempt, failure_reason, retry_after)
		VALUES ($1, $2, $3, 'grounding', 1, 'scoper', 'claude', 'p', 'queued', 1, 'sign_in', now() + interval '1 minute') RETURNING id::text`, w.ID, c.ID, c.OwnerID).Scan(&run); err != nil {
		t.Fatal(err)
	}
	s.mustUpdate(t, `INSERT INTO agent_run_receipt (workspace_id, agent_run_id, attempt, runtime_id, result)
		VALUES ((SELECT workspace_id FROM agent_run WHERE id = $1), $1, 1, $2, '{"failure_reason":"sign_in"}')`, run, rt)

	if card, study := s.waiting(t, a, w.ID, c.ID); card != "" || study != "" {
		t.Fatalf("waiting before the machine was flagged: %q %q", card, study)
	}
	s.mustUpdate(t, `UPDATE runtime SET unhealthy_reason = 'sign_in' WHERE id = $1`, rt)
	s.mustUpdate(t, `INSERT INTO inbox_item (workspace_id, user_id, kind, text, runtime_id, cause)
		SELECT workspace_id, owner_id, 'waiting_for_machine', 'Claude Code on laptop is signed out.', id, 'sign_in' FROM runtime WHERE id = $1`, rt)
	want := "Waiting for laptop: Claude Code on laptop is signed out."
	for _, who := range []string{a, b} {
		if card, study := s.waiting(t, who, w.ID, c.ID); card != want || study != want {
			t.Fatalf("waiting: %q %q", card, study)
		}
	}
	s.mustUpdate(t, `UPDATE inbox_item SET resolved_at = now() WHERE runtime_id = $1`, rt)
	s.mustUpdate(t, `UPDATE runtime SET unhealthy_reason = NULL WHERE id = $1`, rt)
	if card, study := s.waiting(t, a, w.ID, c.ID); card != "" || study != "" {
		t.Fatalf("waiting after the wait resolved: %q %q", card, study)
	}
	// A machine that went offline needs no Inbox item to be waited on.
	s.mustUpdate(t, `UPDATE runtime SET status = 'offline' WHERE id = $1`, rt)
	if card, study := s.waiting(t, a, w.ID, c.ID); card != "Waiting for laptop: it is offline." || study != card {
		t.Fatalf("waiting on an offline machine: %q %q", card, study)
	}
}

// A run no machine has claimed yet waits on every machine that could claim
// it: it says so when none of them can, and the page hears when one stops or
// starts taking studies.
func TestWaitingUnclaimedRun(t *testing.T) {
	s := newServer(t)
	a := s.signIn(t, "a@lab.org")
	w := s.workspace(t, a, "lab")
	tok := s.personalToken(t, a)
	rt := s.laptop(t, tok, w.ID)
	c := s.idleCard(t, a, w.ID, "Why?")
	s.mustUpdate(t, `INSERT INTO agent_run (workspace_id, card_id, responsible_user_id, board_column, round, role, harness, prompt)
		VALUES ($1, $2, $3, 'grounding', 1, 'scoper', 'claude', 'p')`, w.ID, c.ID, c.OwnerID)
	events := func() (n int) {
		t.Helper()
		if err := s.Env.Owner.QueryRow(t.Context(), `SELECT count(*) FROM event WHERE workspace_id = $1 AND type = 'runtime.updated'`, w.ID).Scan(&n); err != nil {
			t.Fatal(err)
		}
		return n
	}
	if card, study := s.waiting(t, a, w.ID, c.ID); card != "" || study != "" {
		t.Fatalf("waiting with a healthy machine: %q %q", card, study)
	}
	health := "/api/daemon/runtimes/" + rt + "/health"
	before := events()
	if code := s.call(t, "POST", health, tok, "", map[string]any{"workspace_id": w.ID, "ok": false, "reason": "cli_broken"}, nil); code != 204 {
		t.Fatalf("unhealthy report: %d", code)
	}
	if events() != before+1 {
		t.Fatalf("flagging the machine told no page")
	}
	want := "Waiting for laptop: laptop keeps failing to run Claude Code (cli_broken). Waiting studies continue once it recovers."
	if card, study := s.waiting(t, a, w.ID, c.ID); card != want || study != want {
		t.Fatalf("waiting on a flagged machine: %q %q", card, study)
	}
	before = events()
	if code := s.call(t, "POST", health, tok, "", map[string]any{"workspace_id": w.ID, "ok": true}, nil); code != 204 {
		t.Fatalf("healthy report: %d", code)
	}
	if events() != before+1 {
		t.Fatalf("clearing the machine told no page")
	}
	if card, study := s.waiting(t, a, w.ID, c.ID); card != "" || study != "" {
		t.Fatalf("waiting after the machine recovered: %q %q", card, study)
	}
	// A pass that changes nothing tells no one.
	before = events()
	s.call(t, "POST", health, tok, "", map[string]any{"workspace_id": w.ID, "ok": true}, nil)
	if events() != before {
		t.Fatalf("a pass with nothing to clear emitted an event")
	}
	s.mustUpdate(t, `UPDATE runtime SET status = 'offline' WHERE id = $1`, rt)
	if card, study := s.waiting(t, a, w.ID, c.ID); card != "Waiting for laptop: it is offline." || study != card {
		t.Fatalf("waiting on an offline machine: %q %q", card, study)
	}
}

// laptop registers the user's daemon "laptop" with a Claude runtime.
func (s *server) laptop(t *testing.T, tok, wsID string) string {
	t.Helper()
	var rts []struct{ ID string }
	if code := s.call(t, "POST", "/api/daemon/register", tok, "", map[string]any{"workspace_id": wsID, "daemon_id": "d1", "device_name": "laptop", "harnesses": []string{"claude"}}, &rts); code != 200 || len(rts) != 1 {
		t.Fatalf("register: %d %+v", code, rts)
	}
	return rts[0].ID
}

// waiting reads a study's waiting text from the board and from its runs.
func (s *server) waiting(t *testing.T, tok, wsID, cardID string) (card, study string) {
	t.Helper()
	var cards []struct{ ID, Waiting string }
	if code := s.call(t, "GET", "/api/cards", tok, wsID, nil, &cards); code != 200 {
		t.Fatalf("cards: %d", code)
	}
	for _, c := range cards {
		if c.ID == cardID {
			card = c.Waiting
		}
	}
	var runs []struct{ Waiting string }
	if code := s.call(t, "GET", "/api/cards/"+cardID+"/runs", tok, wsID, nil, &runs); code != 200 || len(runs) != 1 {
		t.Fatalf("runs: %d %+v", code, runs)
	}
	return card, runs[0].Waiting
}
