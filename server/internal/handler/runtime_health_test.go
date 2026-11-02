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
