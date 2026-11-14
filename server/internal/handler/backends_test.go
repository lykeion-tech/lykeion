package handler

import (
	"fmt"
	"testing"
)

func TestNewHarnessesRegisterAndUnknownOnesAreRefused(t *testing.T) {
	s := newServer(t)
	a := s.signIn(t, "a@lab.org")
	w := s.workspace(t, a, "lab")
	tok := s.personalToken(t, a) // /api/daemon refuses a session JWT
	var rts []struct{ ID, Harness string }
	if code := s.call(t, "POST", "/api/daemon/register", tok, "", map[string]any{"workspace_id": w.ID, "daemon_id": "d1", "device_name": "laptop",
		"harnesses": []string{"claude", "codex"}}, &rts); code != 200 || len(rts) != 2 {
		t.Fatalf("register: %d %+v", code, rts)
	}
	var rejected struct{ Error string }
	if code := s.call(t, "POST", "/api/daemon/register", tok, "", map[string]any{"workspace_id": w.ID, "daemon_id": "d2",
		"harnesses": []string{"claude", "gemini"}}, &rejected); code != 400 || rejected.Error != `unknown harness "gemini"` {
		t.Fatalf("an unknown harness registered: %d", code)
	}
}

func TestSettingsRefuseAHarnessNoDaemonRuns(t *testing.T) {
	s := newServer(t)
	a := s.signIn(t, "a@lab.org")
	w := s.workspace(t, a, "lab")
	ok := map[string]any{"settings": map[string]any{"harness": map[string]any{"default": "claude", "roles": map[string]string{"grounding-reviewer": "codex"}}}}
	if code := s.call(t, "PATCH", "/api/workspaces/"+w.ID+"/settings", a, "", ok, nil); code != 204 {
		t.Fatalf("valid harnesses refused: %d", code)
	}
	for _, tc := range []struct {
		bad     map[string]any
		harness string
	}{{map[string]any{"default": "nope"}, "nope"}, {map[string]any{"roles": map[string]string{"scoper": "gemini"}}, "gemini"}, {map[string]any{"default": ""}, ""}} {
		bad := tc.bad
		var rejected struct{ Error string }
		body := map[string]any{"settings": map[string]any{"harness": bad}}
		if code := s.call(t, "PATCH", "/api/workspaces/"+w.ID+"/settings", a, "", body, &rejected); code != 400 || rejected.Error != fmt.Sprintf("unknown harness %q", tc.harness) {
			t.Fatalf("%v accepted: %d", bad, code)
		}
	}
}

func TestNewHarnessesAgentRegistryCreateUpdate(t *testing.T) {
	s := newServer(t)
	a := s.signIn(t, "registry@lab.org")
	w := s.workspace(t, a, "registry")
	for _, h := range []string{"claude", "codex"} {
		t.Run(h, func(t *testing.T) {
			var created struct{ ID, Harness string }
			t.Run("create", func(t *testing.T) {
				if code := s.call(t, "POST", "/api/agents", a, w.ID, map[string]string{"stage": "experimentation", "name": h, "harness": h}, &created); code != 201 {
					t.Fatalf("create %s: %d", h, code)
				}
				var got struct{ Harness string }
				if code := s.call(t, "GET", "/api/agents/"+created.ID, a, w.ID, nil, &got); code != 200 || got.Harness != h {
					t.Fatalf("persisted create: %d %+v", code, got)
				}
			})
			t.Run("update", func(t *testing.T) {
				var got struct{ Harness string }
				var baseline struct{ ID string }
				if code := s.call(t, "POST", "/api/agents", a, w.ID, map[string]string{"stage": "experimentation", "name": h + "-updated", "harness": "claude"}, &baseline); code != 201 {
					t.Fatal(code)
				}
				if code := s.call(t, "PATCH", "/api/agents/"+baseline.ID, a, w.ID, map[string]string{"harness": h}, nil); code != 200 {
					t.Fatalf("update %s: %d", h, code)
				}
				if code := s.call(t, "GET", "/api/agents/"+baseline.ID, a, w.ID, nil, &got); code != 200 || got.Harness != h {
					t.Fatalf("persisted update: %d %+v", code, got)
				}
				if code := s.call(t, "PATCH", "/api/agents/"+baseline.ID, a, w.ID, map[string]string{"harness": "gemini"}, nil); code != 400 {
					t.Fatalf("unknown update: %d", code)
				}
				if code := s.call(t, "GET", "/api/agents/"+baseline.ID, a, w.ID, nil, &got); code != 200 || got.Harness != h {
					t.Fatalf("rejected update changed harness: %d %+v", code, got)
				}
			})
		})
	}
	if code := s.call(t, "POST", "/api/agents", a, w.ID, map[string]string{"stage": "experimentation", "name": "unknown", "harness": "gemini"}, nil); code != 400 {
		t.Fatalf("unknown create: %d", code)
	}
}

func TestRemovedHarnessRejected(t *testing.T) {
	s := newServer(t)
	a := s.signIn(t, "removed@lab.org")
	w := s.workspace(t, a, "removed")
	tok := s.personalToken(t, a)
	var baseline struct{ ID string }
	if code := s.call(t, "POST", "/api/agents", a, w.ID, map[string]string{"stage": "experimentation", "name": "supported", "harness": "claude"}, &baseline); code != 201 {
		t.Fatal(code)
	}
	for _, harness := range []string{"cursor", "antigravity"} {
		if code := s.call(t, "POST", "/api/agents", a, w.ID, map[string]string{"stage": "experimentation", "name": harness, "harness": harness}, nil); code != 400 {
			t.Fatalf("create %s: %d", harness, code)
		}
		if code := s.call(t, "PATCH", "/api/agents/"+baseline.ID, a, w.ID, map[string]string{"harness": harness}, nil); code != 400 {
			t.Fatalf("update %s: %d", harness, code)
		}
		if code := s.call(t, "POST", "/api/daemon/register", tok, "", map[string]any{"workspace_id": w.ID, "daemon_id": "removed", "harnesses": []string{harness}}, nil); code != 400 {
			t.Fatalf("register %s: %d", harness, code)
		}
	}
}
