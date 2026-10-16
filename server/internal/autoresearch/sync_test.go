package autoresearch

import (
	"context"
	db "lykeion/server/pkg/db/generated"
	"sync"
	"testing"
)

func TestARunFollowsItsCard(t *testing.T) {
	f := setup(t)
	ctx := context.Background()
	ap := f.autoresearch(t, "create_card", "Digest", "", "")
	r := f.fire(t, ap, Admission{})
	for _, step := range []struct{ sql, want string }{
		{"flag = 'escalated'", "blocked/escalated"},
		{"flag = 'out_of_budget'", "blocked/out_of_budget"},
		{"flag = ''", "running/"},
		{"board_column = 'done', ending = 'released'", "completed/"},
		{"flag = 'out_of_budget'", "completed/"},
	} {
		f.exec(t, "UPDATE card SET "+step.sql+" WHERE id = $1", *r.CardID)
		if err := f.s.Sync(ctx); err != nil {
			t.Fatal(err)
		}
		if got := reason(f.run(t, r.ID)); got != step.want {
			t.Fatalf("%s: %s", step.sql, got)
		}
	}
	if got := f.count(t, `SELECT count(*) FROM event WHERE type IN ('autoresearch_run.blocked', 'autoresearch_run.completed')`); got != 3 {
		t.Fatalf("events: %d", got)
	}
	other := f.autoresearch(t, "create_card", "Other", "", "")
	stopped := f.fire(t, other, Admission{})
	f.exec(t, `UPDATE card SET board_column = 'done', ending = 'stopped', end_reason = 'duplicate' WHERE id = $1`, *stopped.CardID)
	tick(t, f.s, ctx)
	if got := reason(f.run(t, stopped.ID)); got != "failed/card_stopped" {
		t.Fatal(got)
	}
}

func TestARunFollowsItsAgentRunAndSettlementSyncsIt(t *testing.T) {
	f := setup(t)
	ctx := context.Background()
	f.online(t, f.owner, "claude")
	ap := f.autoresearch(t, "run_only", "Digest", "", "")
	var passed []db.AgentRun
	hook := f.s.OnSettled(func(_ context.Context, r db.AgentRun) { passed = append(passed, r) })
	for _, step := range []struct{ status, failure, want string }{
		{"completed", "", "completed/"}, {"failed", "timeout", "failed/timeout"}, {"cancelled", "", "failed/cancelled"},
	} {
		r := f.fire(t, ap, Admission{})
		f.exec(t, `UPDATE agent_run SET status = $2, failure_reason = NULLIF($3, '') WHERE id = $1`, *r.AgentRunID, step.status, step.failure)
		settled := f.agentRun(t, *r.AgentRunID)
		hook(ctx, settled)
		hook(ctx, settled)
		if got := reason(f.run(t, r.ID)); got != step.want {
			t.Fatalf("%s: %s", step.status, got)
		}
	}
	if len(passed) != 0 {
		t.Fatal("autoresearch settlement reached next hook")
	}
	if n := f.count(t, `SELECT count(*) FROM event WHERE type IN ('autoresearch_run.completed', 'autoresearch_run.failed')`); n != 3 {
		t.Fatalf("repeated settlement emitted %d events", n)
	}
	hook(ctx, db.AgentRun{Kind: "role"})
	hook(ctx, db.AgentRun{Kind: "role"})
	if len(passed) != 2 {
		t.Fatalf("next hook deliveries: %d", len(passed))
	}
}

func TestConcurrentSyncEmitsOneStatusChange(t *testing.T) {
	f := setup(t)
	ctx := context.Background()
	ap := f.autoresearch(t, "create_card", "Digest", "", "")
	r := f.fire(t, ap, Admission{})
	f.exec(t, `UPDATE card SET flag = 'escalated' WHERE id = $1`, *r.CardID)
	var wg sync.WaitGroup
	errs := make(chan error, 8)
	for range 8 {
		wg.Add(1)
		go func() { defer wg.Done(); errs <- f.s.Sync(ctx) }()
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		if err != nil {
			t.Fatal(err)
		}
	}
	if n := f.count(t, `SELECT count(*) FROM event WHERE type = 'autoresearch_run.blocked'`); n != 1 {
		t.Fatalf("blocked events: %d", n)
	}
}
