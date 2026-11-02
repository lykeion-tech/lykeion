package handler

import (
	"maps"
	"strings"
	"testing"
)

type askOut struct {
	Ticket string
	Busy   bool
	Error  string
}

func (l *teamLab) ask(t *testing.T, tok, member, task string) (int, askOut) {
	t.Helper()
	var out askOut
	c := l.s.call(t, "POST", "/api/role/members", tok, "", map[string]any{"member": member, "task": task}, &out)
	return c, out
}

type resultOut struct {
	Status         string
	ElapsedSeconds *float64       `json:"elapsed_seconds"`
	HandIn         map[string]any `json:"hand_in"`
	Reason         string
	Comments       []any
}

func (l *teamLab) result(t *testing.T, tok, ticket string) (int, resultOut) {
	t.Helper()
	var out resultOut
	c := l.s.call(t, "GET", "/api/role/members/"+ticket, tok, "", nil, &out)
	return c, out
}

func TestAskMemberBusyAndCap(t *testing.T) {
	l, leader := groundingLab(t)
	if !leader.leader {
		t.Fatal("the leader's claim does not offer the member tools")
	}
	c, first := l.ask(t, leader.token, "member-1", "Find papers on telomeres.")
	if c != 201 || first.Ticket == "" || first.Busy {
		t.Fatalf("first ask %d %+v", c, first)
	}
	if n := l.s.count(t, "SELECT count(*) FROM record WHERE card_id = $1 AND kind = 'delegation' AND body->>'agent_run_id' = $2", l.card.ID, first.Ticket); n != 1 {
		t.Fatalf("%d delegation records", n)
	}
	if c, out := l.ask(t, leader.token, "member-1", "And on senescence."); c != 409 || !out.Busy || out.Ticket != first.Ticket {
		t.Fatalf("second ask %d %+v", c, out)
	}
	if c, out := l.ask(t, leader.token, "ghost", "x"); c != 400 {
		t.Fatalf("undeclared %d %+v", c, out)
	}
	if n := l.queued(t); n != 1 {
		t.Fatalf("%d runs queued", n)
	}
	m := l.claim(t)
	if m.id != first.Ticket || m.leader {
		t.Fatalf("claimed %s, want the member %s", m.id, first.Ticket)
	}
	if c, out := l.ask(t, m.token, "member-1", "x"); c != 403 {
		t.Fatalf("a member asking %d %+v", c, out)
	}
	if c, _ := l.result(t, m.token, first.Ticket); c != 403 {
		t.Fatalf("a member polling %d", c)
	}
}

func TestAskMemberStopsAtTheStepCap(t *testing.T) {
	l := newTeamLab(t, 1)
	leader := l.claim(t)
	if c, _ := l.ask(t, leader.token, "member-1", "one"); c != 201 {
		t.Fatalf("first ask %d", c)
	}
	l.complete(t, l.claim(t))
	if c, out := l.ask(t, leader.token, "member-2", "two"); c != 409 || out.Busy || out.Error == "" || strings.Contains(out.Error, "lyk") {
		t.Fatalf("over the cap %d %+v", c, out)
	}
}

func TestMemberResultStates(t *testing.T) {
	l, leader := groundingLab(t)
	_, ask := l.ask(t, leader.token, "member-1", "Find papers.")
	working := func(when string) {
		t.Helper()
		c, out := l.result(t, leader.token, ask.Ticket)
		if c != 200 || out.Status != "working" || out.ElapsedSeconds == nil || out.HandIn != nil || out.Comments == nil || len(out.Comments) != 0 {
			t.Fatalf("%s: %d %+v", when, c, out)
		}
	}
	working("queued")
	if _, err := l.s.Env.Owner.Exec(t.Context(), "UPDATE agent_run SET retry_after = now() + interval '1 hour', infra_failures = 1 WHERE id = $1", ask.Ticket); err != nil {
		t.Fatal(err)
	}
	working("parked")
	if _, err := l.s.Env.Owner.Exec(t.Context(), "UPDATE agent_run SET retry_after = NULL WHERE id = $1", ask.Ticket); err != nil {
		t.Fatal(err)
	}
	m := l.claim(t)
	working("running")
	h := map[string]any{"status": "done", "answer": "Nothing qualifies.", "reason": nil, "replies": []any{},
		"papers": []any{}, "datasets": []any{}, "no_qualifying": map[string]any{"reason": "no study measures it"}}
	if out := l.handIn(t, m, h); !out.Accepted || len(out.Problems) > 0 {
		t.Fatalf("hand-in %+v", out)
	}
	working("handed in, not yet completed")
	l.complete(t, m)
	c, out := l.result(t, leader.token, ask.Ticket)
	if c != 200 || out.Status != "done" || out.HandIn["agent_run_id"] != ask.Ticket || out.HandIn["hand_in"].(map[string]any)["answer"] != "Nothing qualifies." {
		t.Fatalf("done: %d %+v", c, out)
	}

	// A second member's run that fails for an agent reason reports its error.
	_, ask2 := l.ask(t, leader.token, "member-1", "Try again.")
	m2 := l.claim(t)
	for {
		ref := maps.Clone(m2.ref)
		ref["failure_reason"], ref["error"] = "agent_error", "the model refused the task"
		if c := l.s.call(t, "POST", "/api/daemon/agent-runs/"+m2.id+"/fail", l.personal, "", ref, nil); c != 200 {
			t.Fatalf("fail %d", c)
		}
		if l.s.count(t, "SELECT count(*) FROM agent_run WHERE id = $1 AND status = 'failed'", m2.id) == 1 {
			break
		}
		working("retrying an agent failure")
		m2 = l.claim(t)
	}
	c, out = l.result(t, leader.token, ask2.Ticket)
	if c != 200 || out.Status != "failed" || out.Reason != "the model refused the task" || out.HandIn != nil {
		t.Fatalf("failed: %d %+v", c, out)
	}
}

func TestMemberResultHidesOtherRuns(t *testing.T) {
	l, leader := groundingLab(t)
	for _, ticket := range []string{leader.id, "00000000-0000-0000-0000-000000000001", "not-a-uuid"} {
		var out struct{ Error string }
		if c := l.s.call(t, "GET", "/api/role/members/"+ticket, leader.token, "", nil, &out); c != 404 || out.Error != "no such member run" {
			t.Fatalf("%s: %d %+v", ticket, c, out)
		}
	}
	// A ticket of another study's member reads as unknown too.
	var second card
	if c := l.s.call(t, "POST", "/api/cards", l.jwt, l.ws, map[string]any{"question": "Why do mice age?",
		"assignee": teamAssignee(map[string]string{"grounding": l.team})}, &second); c != 201 {
		t.Fatalf("card: %d", c)
	}
	other := l.claim(t)
	if other.role != "leader" || other.id == leader.id {
		t.Fatalf("second study's run %+v", other)
	}
	_, ask := l.ask(t, other.token, "member-1", "x")
	var out struct{ Error string }
	if c := l.s.call(t, "GET", "/api/role/members/"+ask.Ticket, leader.token, "", nil, &out); c != 404 || out.Error != "no such member run" {
		t.Fatalf("other study's ticket: %d %+v", c, out)
	}
}

func TestLeaderReclaimListsMemberHandIns(t *testing.T) {
	l, leader := groundingLab(t)
	_, ask := l.ask(t, leader.token, "member-1", "Find papers.")
	m := l.claim(t)
	h := map[string]any{"status": "done", "answer": "Nothing qualifies on telomeres.", "reason": nil, "replies": []any{},
		"papers": []any{}, "datasets": []any{}, "no_qualifying": map[string]any{"reason": "no study measures it"}}
	if out := l.handIn(t, m, h); !out.Accepted {
		t.Fatalf("hand-in %+v", out)
	}
	l.complete(t, m)
	_, ask2 := l.ask(t, leader.token, "member-1", "Now datasets.")
	l.fail(t, leader, "runtime_offline")
	if _, err := l.s.Env.Owner.Exec(t.Context(), "UPDATE agent_run SET retry_after = NULL WHERE id = $1", leader.id); err != nil {
		t.Fatal(err)
	}
	var again teamRun
	for again.id != leader.id {
		again = l.claim(t)
	}
	for _, want := range []string{ask.Ticket, "Nothing qualifies on telomeres.", ask2.Ticket, "member-1", "Use their work; don't ask them for it again."} {
		if !strings.Contains(again.prompt, want) {
			t.Fatalf("re-claimed prompt lacks %q:\n%s", want, again.prompt)
		}
	}
	section := strings.TrimPrefix(again.prompt, leader.prompt)
	never := []string{"lyk ", "hand_in"}
	rows, err := l.s.Env.Owner.Query(t.Context(), "SELECT key FROM record WHERE card_id = $1", l.card.ID)
	if err != nil {
		t.Fatal(err)
	}
	for rows.Next() {
		var k string
		rows.Scan(&k)
		never = append(never, " "+k+" ", "("+k+")")
	}
	rows.Close()
	for _, n := range never {
		if strings.Contains(section, n) {
			t.Fatalf("re-claim section names %q:\n%s", n, section)
		}
	}
	if strings.Contains(leader.prompt, ask.Ticket) {
		t.Fatal("the first attempt's prompt already listed a member")
	}
}

// A member's run parked for a machine fault keeps the fault's text in its
// error until it ends; when it then fails for an agent reason, the leader
// reads that reason, never the fault.
func TestMemberResultNeverShowsAParkedFault(t *testing.T) {
	for _, agentText := range []string{"the model refused the task", ""} {
		l, leader := groundingLab(t)
		_, ask := l.ask(t, leader.token, "member-1", "Find papers.")
		m := l.claim(t)
		ref := maps.Clone(m.ref)
		ref["failure_reason"], ref["error"] = "provider_network", "connection reset by peer at api.example"
		if c := l.s.call(t, "POST", "/api/daemon/agent-runs/"+m.id+"/fail", l.personal, "", ref, nil); c != 200 {
			t.Fatalf("park %d", c)
		}
		if c, out := l.result(t, leader.token, ask.Ticket); c != 200 || out.Status != "working" || strings.Contains(out.Reason, "connection reset") {
			t.Fatalf("parked: %d %+v", c, out)
		}
		if _, err := l.s.Env.Owner.Exec(t.Context(), "UPDATE agent_run SET retry_after = NULL, max_attempts = attempt + 1 WHERE id = $1", ask.Ticket); err != nil {
			t.Fatal(err)
		}
		for l.s.count(t, "SELECT count(*) FROM agent_run WHERE id = $1 AND status = 'failed'", ask.Ticket) == 0 {
			m = l.claim(t)
			ref = maps.Clone(m.ref)
			ref["failure_reason"], ref["error"] = "agent_error", agentText
			if c := l.s.call(t, "POST", "/api/daemon/agent-runs/"+m.id+"/fail", l.personal, "", ref, nil); c != 200 {
				t.Fatalf("fail %d", c)
			}
		}
		c, out := l.result(t, leader.token, ask.Ticket)
		if c != 200 || out.Status != "failed" || out.Reason == "" || strings.Contains(out.Reason, "connection reset") || agentText != "" && out.Reason != agentText {
			t.Fatalf("failed (%q): %d %+v", agentText, c, out)
		}
	}
}

// A ticket names a member's run of the leader's own column and round.
func TestMemberResultKeepsToItsColumn(t *testing.T) {
	l, leader := groundingLab(t)
	_, ask := l.ask(t, leader.token, "member-1", "Find papers.")
	if _, err := l.s.Env.Owner.Exec(t.Context(), "UPDATE agent_run SET round = round + 1 WHERE id = $1", ask.Ticket); err != nil {
		t.Fatal(err)
	}
	if c, _ := l.result(t, leader.token, ask.Ticket); c != 404 {
		t.Fatalf("another round's run: %d", c)
	}
	if _, err := l.s.Env.Owner.Exec(t.Context(), "UPDATE agent_run SET round = round - 1, board_column = 'hypotheses' WHERE id = $1", ask.Ticket); err != nil {
		t.Fatal(err)
	}
	if c, _ := l.result(t, leader.token, ask.Ticket); c != 404 {
		t.Fatalf("another column's run: %d", c)
	}
}

// A member's hand-in from an earlier attempt of its run is not its answer:
// an attempt that ends without one reports that.
func TestMemberResultIgnoresAnEarlierAttemptsHandIn(t *testing.T) {
	l, leader := groundingLab(t)
	_, ask := l.ask(t, leader.token, "member-1", "Find papers.")
	m := l.claim(t)
	l.record(t, m, 0, "hand_in", "", map[string]any{"agent_run_id": m.id, "attempt": 0, "hand_in": map[string]any{"status": "done", "answer": "stale"}}, &m.id)
	l.complete(t, m)
	c, out := l.result(t, leader.token, ask.Ticket)
	if c != 200 || out.Status != "failed" || out.Reason != "the member's run ended without a hand-in" {
		t.Fatalf("result %d %+v", c, out)
	}
}
