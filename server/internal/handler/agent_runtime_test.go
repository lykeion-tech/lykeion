package handler

import (
	"testing"

	"github.com/google/uuid"

	"lykeion/server/internal/pack"
	"lykeion/server/internal/store"
	"lykeion/server/internal/team"
	"lykeion/server/pkg/agent"
)

func TestBindRuntimeRules(t *testing.T) {
	s, a, b, _, ws, _ := csFixture(t)
	ctx := t.Context()
	owner, member, wsID := uuid.MustParse(s.userID(t, a)), uuid.MustParse(s.userID(t, b)), uuid.MustParse(ws)
	if err := s.H.Store.InWorkspace(ctx, wsID, owner, func(tx *store.Tx) error {
		return pack.InstallShipped(ctx, tx, s.H.Blobs, pack.Shipped())
	}); err != nil {
		t.Fatal(err)
	}
	if err := s.H.Store.InWorkspace(ctx, wsID, uuid.Nil, func(tx *store.Tx) error { return team.InstallLaboratory(ctx, tx) }); err != nil {
		t.Fatal(err)
	}
	var leader, packID string
	if err := s.Env.Owner.QueryRow(ctx, `SELECT t.agent_id::text, p.id::text FROM team_agent t JOIN field_pack p ON p.id = t.pack_id
		WHERE t.workspace_id = $1 AND t.user_id IS NULL AND t.member = 'leader' AND p.source = 'shipped' AND p.stage = 'grounding'`, ws).Scan(&leader, &packID); err != nil {
		t.Fatal(err)
	}
	machine := func(user uuid.UUID, daemon, harness string) string {
		t.Helper()
		rs, err := s.H.AgentRuns.Register(ctx, user, wsID, daemon, daemon, []string{harness}, nil, agent.Platform{OS: "linux"})
		if err != nil || len(rs) != 1 {
			t.Fatalf("register %s: %v", daemon, err)
		}
		return rs[0].ID.String()
	}
	bind := func(tok, agentID, runtime string, want int, text string) {
		t.Helper()
		var out map[string]any
		if got := s.call(t, "PUT", "/api/agents/"+agentID+"/runtime", tok, ws, map[string]string{"runtime_id": runtime}, &out); got != want {
			t.Fatalf("PUT %s to %s: %d %v, want %d", agentID, runtime, got, out, want)
		}
		if text != "" && out["error"] != text {
			t.Fatalf("error %q, want %q", out["error"], text)
		}
		if want == 200 && out["runtime_id"] != runtime {
			t.Fatalf("bound to %v, want %s", out["runtime_id"], runtime)
		}
	}
	shared := machine(member, "member-shared", "claude")
	if _, err := s.Env.Owner.Exec(ctx, `UPDATE runtime SET visibility = 'workspace' WHERE id = $1`, shared); err != nil {
		t.Fatal(err)
	}
	private := machine(member, "member-private", "claude")

	bind(a, leader, shared, 200, "")
	bind(a, leader, private, 400, "this machine isn't shared with the workspace owner")
	bind(b, leader, shared, 403, "only the workspace owner can choose a laboratory agent's machine")

	var own map[string]any
	if got := s.call(t, "POST", "/api/agents", a, ws, map[string]any{"name": "mine", "harness": "claude", "visibility": "private"}, &own); got != 201 {
		t.Fatalf("create agent: %d %v", got, own)
	}
	bind(a, own["id"].(string), shared, 400, "only laboratory agents are bound to a machine")

	if _, err := s.Env.Owner.Exec(ctx, `UPDATE field_pack SET manifest = jsonb_set(manifest, '{leader,agent,models}', '{"claude": "sonnet"}') WHERE id = $1`, packID); err != nil {
		t.Fatal(err)
	}
	codex := machine(owner, "owner-codex", "codex")
	bind(a, leader, codex, 400, "this agent can't run on that CLI")
	var bound string
	if err := s.Env.Owner.QueryRow(ctx, `SELECT runtime_id::text FROM agent WHERE id = $1`, leader).Scan(&bound); err != nil {
		t.Fatal(err)
	}
	if bound != shared {
		t.Fatalf("refused binds changed the machine: %s", bound)
	}
}
