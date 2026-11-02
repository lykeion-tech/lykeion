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

// A fence breach survives the daemon's later passes. Only the pass at start,
// or its owner dismissing the wait, clears it.
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
		// A sign-in wait from earlier, which an ordinary pass does clear.
		s.mustUpdate(t, `INSERT INTO inbox_item (workspace_id, user_id, kind, text, runtime_id, cause)
			SELECT workspace_id, owner_id, 'waiting_for_machine', 'signed out', id, 'sign_in' FROM runtime WHERE id = $1`, rt)
	}
	state := func() (reason *string, fence, signIn int) {
		t.Helper()
		if err := s.Env.Owner.QueryRow(t.Context(), `SELECT unhealthy_reason,
			(SELECT count(*) FROM inbox_item WHERE runtime_id = r.id AND cause = 'fence_breach' AND resolved_at IS NULL),
			(SELECT count(*) FROM inbox_item WHERE runtime_id = r.id AND cause = 'sign_in' AND resolved_at IS NULL)
			FROM runtime r WHERE id = $1`, rt).Scan(&reason, &fence, &signIn); err != nil {
			t.Fatal(err)
		}
		return
	}

	breach()
	if code := s.call(t, "POST", path, tok, "", map[string]any{"workspace_id": w.ID, "ok": true}, nil); code != 204 {
		t.Fatalf("pass: %d", code)
	}
	if reason, fence, signIn := state(); reason == nil || *reason != "fence_breach" || fence != 1 || signIn != 1 {
		t.Fatalf("a later pass touched a fence breach: %v %d %d", reason, fence, signIn)
	}
	// With the flag moved on to another cause, a pass clears that one and
	// leaves the breach's wait.
	s.mustUpdate(t, `UPDATE runtime SET unhealthy_reason = 'sign_in' WHERE id = $1`, rt)
	if code := s.call(t, "POST", path, tok, "", map[string]any{"workspace_id": w.ID, "ok": true}, nil); code != 204 {
		t.Fatalf("pass: %d", code)
	}
	if reason, fence, signIn := state(); reason != nil || fence != 1 || signIn != 0 {
		t.Fatalf("after a pass on sign_in: %v %d %d", reason, fence, signIn)
	}
	s.mustUpdate(t, `UPDATE inbox_item SET resolved_at = now() WHERE runtime_id = $1`, rt)

	breach()
	if code := s.call(t, "POST", path, tok, "", map[string]any{"workspace_id": w.ID, "ok": true, "start": true}, nil); code != 204 {
		t.Fatalf("start pass: %d", code)
	}
	if reason, fence, signIn := state(); reason != nil || fence != 0 || signIn != 0 {
		t.Fatalf("the start pass left: %v %d %d", reason, fence, signIn)
	}

	breach()
	if code := s.call(t, "POST", "/api/inbox/"+item+"/read", a, w.ID, nil, nil); code != 204 {
		t.Fatalf("dismiss: %d", code)
	}
	if reason, fence, signIn := state(); reason != nil || fence != 0 || signIn != 0 {
		t.Fatalf("dismissing the breach left: %v %d %d", reason, fence, signIn)
	}
}
