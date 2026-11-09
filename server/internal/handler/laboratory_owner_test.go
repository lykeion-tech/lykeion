package handler

import (
	"testing"

	"github.com/google/uuid"

	"lykeion/server/internal/pack"
	"lykeion/server/pkg/agent"
)

// labOwnerFixture is a workspace with the built-in laboratory, owned by a
// (the laboratory's owner) and b, whose agents are bound to a's private
// machine.
func labOwnerFixture(t *testing.T) (s *server, a, b, ws string) {
	t.Helper()
	s = newServer(t)
	s.H.Shipped = pack.Shipped()
	a, b = s.signIn(t, "a@lab.org"), s.signIn(t, "b@lab.org")
	ws = s.workspace(t, a, "lab-owner").ID
	if _, err := s.Env.Owner.Exec(t.Context(), `INSERT INTO member(workspace_id,user_id,role) VALUES ($1,$2,'owner')`, ws, s.userID(t, b)); err != nil {
		t.Fatal(err)
	}
	rs, err := s.H.AgentRuns.Register(t.Context(), uuid.MustParse(s.userID(t, a)), uuid.MustParse(ws), "a-mac", "a-mac", []string{"claude"}, nil, agent.Platform{OS: "darwin"})
	if err != nil || len(rs) != 1 {
		t.Fatalf("register: %v", err)
	}
	if n := s.count(t, `SELECT count(*) FROM agent WHERE workspace_id = $1 AND laboratory <> '' AND owner_id = $2 AND runtime_id = $3`, ws, s.userID(t, a), rs[0].ID); n != 18 {
		t.Fatalf("%d laboratory agents owned by a and bound to a's machine, want 18", n)
	}
	return s, a, b, ws
}

// labHandedTo checks every laboratory agent now belongs to user and is
// unbound, and that the agents' open machine notices went with them.
func labHandedTo(t *testing.T, s *server, ws, user string) {
	t.Helper()
	if n := s.count(t, `SELECT count(*) FROM agent WHERE workspace_id = $1 AND laboratory <> '' AND owner_id = $2 AND runtime_id IS NULL`, ws, user); n != 18 {
		t.Fatalf("%d laboratory agents handed over and unbound, want 18", n)
	}
	if n := s.count(t, `SELECT count(*) FROM inbox_item WHERE workspace_id = $1 AND cause LIKE 'unbound:%' AND resolved_at IS NULL AND user_id <> $2`, ws, user); n != 0 {
		t.Fatalf("%d machine notices left with the former owner", n)
	}
}

func TestRemovedOwnersLaboratoryGoesToTheOldestOwner(t *testing.T) {
	s, a, b, ws := labOwnerFixture(t)
	if _, err := s.Env.Owner.Exec(t.Context(), `INSERT INTO inbox_item (workspace_id, user_id, kind, text, cause)
		SELECT $1, $2, 'waiting_for_machine', 'wait', 'unbound:' || id FROM agent WHERE workspace_id = $1 AND laboratory <> '' LIMIT 1`, ws, s.userID(t, a)); err != nil {
		t.Fatal(err)
	}
	if code := s.call(t, "DELETE", "/api/workspaces/"+ws+"/members/"+s.userID(t, a), b, "", nil, nil); code != 204 {
		t.Fatalf("remove: %d", code)
	}
	labHandedTo(t, s, ws, s.userID(t, b))
}

func TestDemotedOwnersLaboratoryGoesToTheOldestOwner(t *testing.T) {
	s, a, b, ws := labOwnerFixture(t)
	if code := s.call(t, "PATCH", "/api/workspaces/"+ws+"/members/"+s.userID(t, a), b, "", map[string]string{"role": "admin"}, nil); code != 204 {
		t.Fatalf("demote: %d", code)
	}
	labHandedTo(t, s, ws, s.userID(t, b))
	// Making a an owner again leaves the laboratory where it is.
	if code := s.call(t, "PATCH", "/api/workspaces/"+ws+"/members/"+s.userID(t, a), b, "", map[string]string{"role": "owner"}, nil); code != 204 {
		t.Fatalf("promote: %d", code)
	}
	labHandedTo(t, s, ws, s.userID(t, b))
}

// A binding the new owner may still use is kept.
func TestDemotedOwnersSharedMachineStaysBound(t *testing.T) {
	s, a, b, ws := labOwnerFixture(t)
	if _, err := s.Env.Owner.Exec(t.Context(), `UPDATE runtime SET os = 'linux', visibility = 'workspace' WHERE workspace_id = $1`, ws); err != nil {
		t.Fatal(err)
	}
	if code := s.call(t, "PATCH", "/api/workspaces/"+ws+"/members/"+s.userID(t, a), b, "", map[string]string{"role": "member"}, nil); code != 204 {
		t.Fatalf("demote: %d", code)
	}
	if n := s.count(t, `SELECT count(*) FROM agent WHERE workspace_id = $1 AND laboratory <> '' AND owner_id = $2 AND runtime_id IS NOT NULL`, ws, s.userID(t, b)); n != 18 {
		t.Fatalf("%d laboratory agents handed over and still bound, want 18", n)
	}
}
