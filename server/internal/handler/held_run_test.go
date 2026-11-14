package handler

import (
	"testing"

	"github.com/google/uuid"

	"lykeion/server/internal/service"
	"lykeion/server/pkg/agent"
)

func TestDeletingAMachineReleasesTheRunsItHeld(t *testing.T) {
	s := newServer(t)
	ctx := t.Context()
	jwt := s.signIn(t, "a@lab.org")
	w := s.workspace(t, jwt, "lab")
	c := s.idleCard(t, jwt, w.ID, "q")
	owner, ws := uuid.MustParse(s.userID(t, jwt)), uuid.MustParse(w.ID)
	register := func(daemon string) service.RuntimeRef {
		t.Helper()
		rs, err := s.H.AgentRuns.Register(ctx, owner, ws, daemon, daemon, []string{"claude"}, nil, agent.Platform{OS: "linux"})
		if err != nil || len(rs) != 1 {
			t.Fatalf("register %s: %v", daemon, err)
		}
		return service.RuntimeRef{WorkspaceID: ws, RuntimeID: rs[0].ID}
	}
	exec := func(sql string, args ...any) {
		t.Helper()
		if _, err := s.Env.Owner.Exec(ctx, sql, args...); err != nil {
			t.Fatal(err)
		}
	}
	a, b := register("a"), register("b")
	exec("UPDATE runtime SET status = 'offline' WHERE id = $1", b.RuntimeID)
	s.queue(t, w.ID, c.ID, owner.String(), "claude")
	var run uuid.UUID
	breach := service.Report{FailureReason: "fence_breach", Error: "used WebFetch, which is outside Lykeion's sources"}
	for range 2 {
		exec("UPDATE agent_run SET retry_after = NULL WHERE workspace_id = $1", ws)
		got, err := s.H.AgentRuns.Claim(ctx, owner, []service.RuntimeRef{a}, 1)
		if err != nil || len(got) != 1 {
			t.Fatalf("claim on a: %d %v", len(got), err)
		}
		ref := a
		ref.Attempt, run = got[0].AgentRun.Attempt, got[0].AgentRun.ID
		if err := s.H.AgentRuns.Start(ctx, owner, ref, run); err != nil {
			t.Fatal(err)
		}
		if _, err := s.H.AgentRuns.Fail(ctx, owner, ref, run, breach); err != nil {
			t.Fatal(err)
		}
	}
	var view map[string]any
	if code := s.call(t, "GET", "/api/agent-runs/"+run.String(), jwt, w.ID, nil, &view); code != 200 || view["held"] != true || view["retry_after"] != nil {
		t.Fatalf("held run view: %d held=%v retry_after=%v", code, view["held"], view["retry_after"])
	}
	exec("UPDATE runtime SET status = 'online' WHERE id = $1", b.RuntimeID)
	if got, err := s.H.AgentRuns.Claim(ctx, owner, []service.RuntimeRef{b}, 1); err != nil || len(got) != 0 {
		t.Fatalf("a held run was claimed: %d %v", len(got), err)
	}
	if code := s.call(t, "DELETE", "/api/runtimes/"+a.RuntimeID.String(), jwt, w.ID, nil, nil); code != 204 {
		t.Fatalf("delete: %d", code)
	}
	if got, err := s.H.AgentRuns.Claim(ctx, owner, []service.RuntimeRef{b}, 1); err != nil || len(got) != 1 || got[0].AgentRun.ID != run {
		t.Fatalf("the run stayed held after its machine was deleted: %d %v", len(got), err)
	}
}
