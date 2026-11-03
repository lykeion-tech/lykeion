package handler

import (
	"testing"
)

// A failed preflight flags the machine and tells its owner once; a passing
// one lets it claim again and resolves the wait.
func TestHealthRouteClearsTheWait(t *testing.T) {
	s := newServer(t)
	a := s.signIn(t, "a@lab.org")
	w := s.workspace(t, a, "lab")
	tok := s.personalToken(t, a)
	var rts []struct{ ID string }
	if code := s.call(t, "POST", "/api/daemon/register", tok, "", map[string]any{"workspace_id": w.ID, "daemon_id": "d1", "device_name": "laptop", "harnesses": []string{"claude"}}, &rts); code != 200 || len(rts) != 1 {
		t.Fatalf("register: %d %+v", code, rts)
	}
	path := "/api/daemon/runtimes/" + rts[0].ID + "/health"
	state := func() (reason *string, open int) {
		t.Helper()
		if err := s.Env.Owner.QueryRow(t.Context(), `SELECT unhealthy_reason, (SELECT count(*) FROM inbox_item WHERE runtime_id = r.id AND kind = 'waiting_for_machine' AND resolved_at IS NULL) FROM runtime r WHERE id = $1`, rts[0].ID).Scan(&reason, &open); err != nil {
			t.Fatal(err)
		}
		return reason, open
	}

	for range 2 {
		if code := s.call(t, "POST", path, tok, "", map[string]any{"workspace_id": w.ID, "ok": false, "reason": "cli_broken", "detail": "no answer"}, nil); code != 204 {
			t.Fatalf("unhealthy report: %d", code)
		}
	}
	if reason, open := state(); reason == nil || *reason != "cli_broken" || open != 1 {
		t.Fatalf("after failed preflight: %v %d", reason, open)
	}

	// Only the preflight's own reasons, and only from the machine's owner.
	if code := s.call(t, "POST", path, tok, "", map[string]any{"workspace_id": w.ID, "ok": false, "reason": "agent_error"}, nil); code != 400 {
		t.Fatalf("agent_error accepted: %d", code)
	}
	b := s.member(t, a, w.ID, "b@lab.org", "member")
	if code := s.call(t, "POST", path, s.personalToken(t, b), "", map[string]any{"workspace_id": w.ID, "ok": true}, nil); code != 404 {
		t.Fatalf("a colleague cleared the machine: %d", code)
	}
	if code := s.call(t, "POST", path, a, "", map[string]any{"workspace_id": w.ID, "ok": true}, nil); code != 401 {
		t.Fatalf("a session token reached a daemon route: %d", code)
	}

	if code := s.call(t, "POST", path, tok, "", map[string]any{"workspace_id": w.ID, "ok": true}, nil); code != 204 {
		t.Fatalf("healthy report: %d", code)
	}
	if reason, open := state(); reason != nil || open != 0 {
		t.Fatalf("after passing preflight: %v %d", reason, open)
	}
}

// A fence breach survives the daemon's later passes, and a later failure
// of another cause does not replace it. Only the pass at start, or its owner
// resolving the wait, clears it; reading the item does not.
func TestHealthRouteKeepsAFenceBreach(t *testing.T) {
	s := newServer(t)
	a := s.signIn(t, "a@lab.org")
	w := s.workspace(t, a, "lab")
	tok := s.personalToken(t, a)
	var rts []struct{ ID string }
	if code := s.call(t, "POST", "/api/daemon/register", tok, "", map[string]any{"workspace_id": w.ID, "daemon_id": "d1", "device_name": "laptop", "harnesses": []string{"claude"}}, &rts); code != 200 || len(rts) != 1 {
		t.Fatalf("register: %d %+v", code, rts)
	}
	rt := rts[0].ID
	path := "/api/daemon/runtimes/" + rt + "/health"
	var item string
	breach := func() {
		t.Helper()
		s.mustUpdate(t, `UPDATE runtime SET unhealthy_reason = 'fence_breach' WHERE id = $1`, rt)
		if err := s.Env.Owner.QueryRow(t.Context(), `INSERT INTO inbox_item (workspace_id, user_id, kind, text, runtime_id, cause)
			SELECT workspace_id, owner_id, 'waiting_for_machine', 'breach', id, 'fence_breach' FROM runtime WHERE id = $1 RETURNING id::text`, rt).Scan(&item); err != nil {
			t.Fatal(err)
		}
	}
	state := func() (reason *string, fence, other int) {
		t.Helper()
		if err := s.Env.Owner.QueryRow(t.Context(), `SELECT unhealthy_reason,
			(SELECT count(*) FROM inbox_item WHERE runtime_id = r.id AND cause = 'fence_breach' AND resolved_at IS NULL),
			(SELECT count(*) FROM inbox_item WHERE runtime_id = r.id AND cause <> 'fence_breach' AND resolved_at IS NULL)
			FROM runtime r WHERE id = $1`, rt).Scan(&reason, &fence, &other); err != nil {
			t.Fatal(err)
		}
		return
	}
	pass := func(start bool) {
		t.Helper()
		if code := s.call(t, "POST", path, tok, "", map[string]any{"workspace_id": w.ID, "ok": true, "start": start}, nil); code != 204 {
			t.Fatalf("pass: %d", code)
		}
	}
	stillBreached := func(when string) {
		t.Helper()
		if reason, fence, _ := state(); reason == nil || *reason != "fence_breach" || fence != 1 {
			t.Fatalf("%s: %v %d", when, reason, fence)
		}
	}

	breach()
	pass(false)
	stillBreached("after a pass")
	// A later failure of another cause, then a pass: the breach stays.
	for _, cause := range []string{"cli_broken", "sign_in"} {
		if code := s.call(t, "POST", path, tok, "", map[string]any{"workspace_id": w.ID, "ok": false, "reason": cause}, nil); code != 204 {
			t.Fatalf("%s report: %d", cause, code)
		}
		stillBreached("after " + cause)
		pass(false)
		stillBreached("after " + cause + " and a pass")
	}
	// Reading the item, which the Inbox does on select, changes nothing.
	if code := s.call(t, "POST", "/api/decisions/"+item+"/read", a, w.ID, nil, nil); code != 204 {
		t.Fatalf("read: %d", code)
	}
	stillBreached("after reading the item")

	pass(true)
	if reason, fence, other := state(); reason != nil || fence != 0 || other != 0 {
		t.Fatalf("the start pass left: %v %d %d", reason, fence, other)
	}

	breach()
	old := item
	if code := s.call(t, "POST", "/api/decisions/"+item+"/resolve", a, w.ID, nil, nil); code != 204 {
		t.Fatalf("resolve: %d", code)
	}
	if reason, fence, _ := state(); reason != nil || fence != 0 {
		t.Fatalf("resolving the breach left: %v %d", reason, fence)
	}
	// A resolved item can't clear a newer breach, nor can another member.
	breach()
	if code := s.call(t, "POST", "/api/decisions/"+old+"/resolve", a, w.ID, nil, nil); code != 404 {
		t.Fatalf("resolving a resolved item: %d", code)
	}
	b := s.member(t, a, w.ID, "b@lab.org", "member")
	if code := s.call(t, "POST", "/api/decisions/"+item+"/resolve", b, w.ID, nil, nil); code != 404 {
		t.Fatalf("a colleague resolved the owner's wait: %d", code)
	}
	stillBreached("after stale and foreign resolves")
}

// Resolving the item that asks about a run its machines kept failing lets
// the run try again from a fresh count; only its owner can, and only for
// that sort of item.
func TestResolvingAParkedRunsItemReleasesIt(t *testing.T) {
	s := newServer(t)
	a := s.signIn(t, "a@lab.org")
	w := s.workspace(t, a, "lab")
	c := s.idleCard(t, a, w.ID, "Why?")
	var run, item, other string
	if err := s.Env.Owner.QueryRow(t.Context(), `INSERT INTO agent_run (workspace_id, card_id, responsible_user_id, board_column, round, role, harness, prompt, status, attempt, infra_failures, failure_reason, retry_after)
		VALUES ($1, $2, $3, 'grounding', 1, 'scoper', 'claude', 'p', 'queued', 5, 5, 'provider_network', '9999-12-31') RETURNING id::text`, w.ID, c.ID, c.OwnerID).Scan(&run); err != nil {
		t.Fatal(err)
	}
	insert := `INSERT INTO inbox_item (workspace_id, card_id, user_id, kind, text, cause) VALUES ($1, $2, $3, 'needs_decision', 't', $4) RETURNING id::text`
	if err := s.Env.Owner.QueryRow(t.Context(), insert, w.ID, c.ID, c.OwnerID, "machine_failures").Scan(&item); err != nil {
		t.Fatal(err)
	}
	if err := s.Env.Owner.QueryRow(t.Context(), insert, w.ID, c.ID, c.OwnerID, nil).Scan(&other); err != nil {
		t.Fatal(err)
	}
	held := func() (failures int, heldRun bool) {
		t.Helper()
		if err := s.Env.Owner.QueryRow(t.Context(), `SELECT infra_failures, retry_after IS NOT NULL FROM agent_run WHERE id = $1`, run).Scan(&failures, &heldRun); err != nil {
			t.Fatal(err)
		}
		return
	}
	b := s.member(t, a, w.ID, "b@lab.org", "member")
	if code := s.call(t, "POST", "/api/decisions/"+item+"/resolve", b, w.ID, nil, nil); code != 404 {
		t.Fatalf("a colleague resolved the owner's item: %d", code)
	}
	if code := s.call(t, "POST", "/api/decisions/"+other+"/resolve", a, w.ID, nil, nil); code != 404 {
		t.Fatalf("another decision resolved: %d", code)
	}
	if n, h := held(); n != 5 || !h {
		t.Fatalf("released early: %d %v", n, h)
	}
	if code := s.call(t, "POST", "/api/decisions/"+item+"/resolve", a, w.ID, nil, nil); code != 204 {
		t.Fatalf("resolve: %d", code)
	}
	if n, h := held(); n != 0 || h {
		t.Fatalf("still held: %d %v", n, h)
	}
}
