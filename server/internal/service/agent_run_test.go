package service

import (
	"context"
	"errors"
	"math"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	"lykeion/server/internal/store"
	"lykeion/server/internal/testutil"
	"lykeion/server/pkg/agent"
	db "lykeion/server/pkg/db/generated"
)

func usd(v float64) *float64 { return &v }

func reasonOf(t db.AgentRun) string {
	if t.FailureReason == nil {
		return ""
	}
	return *t.FailureReason
}

type fixture struct {
	T     *AgentRuns
	Env   *testutil.Env
	WS    uuid.UUID
	Owner uuid.UUID
	Card  uuid.UUID

	mu      sync.Mutex
	settled []db.AgentRun
}

func setup(t *testing.T) *fixture {
	t.Helper()
	env := testutil.Setup(t)
	st := &store.Store{Pool: env.App}
	f := &fixture{Env: env}
	f.T = &AgentRuns{Store: st, MCPURL: "http://x/mcp/sources", Stale: time.Second,
		OnSettled: func(_ context.Context, tk db.AgentRun) { f.mu.Lock(); f.settled = append(f.settled, tk); f.mu.Unlock() }}
	ctx := context.Background()
	u, err := st.Global().UpsertUserByEmail(ctx, "owner@lab.org")
	if err != nil {
		t.Fatal(err)
	}
	f.Owner, f.WS = u.ID, uuid.New()
	err = st.InWorkspace(ctx, f.WS, u.ID, func(tx *store.Tx) error {
		if _, err := tx.Q.CreateWorkspace(ctx, db.CreateWorkspaceParams{ID: f.WS, Slug: "s" + f.WS.String()[:8], Name: "w"}); err != nil {
			return err
		}
		if err := tx.Q.AddMember(ctx, db.AddMemberParams{WorkspaceID: f.WS, UserID: u.ID, Role: "owner"}); err != nil {
			return err
		}
		c, err := tx.Q.CreateCard(ctx, db.CreateCardParams{WorkspaceID: f.WS, Number: 1, Title: "t", Question: "q", OwnerID: u.ID, FollowsFrom: []uuid.UUID{}})
		f.Card = c.ID
		return err
	})
	if err != nil {
		t.Fatal(err)
	}
	return f
}

func (f *fixture) enqueue(t *testing.T, n int, harness string) {
	t.Helper()
	ctx := context.Background()
	err := f.T.Store.InWorkspace(ctx, f.WS, f.Owner, func(tx *store.Tx) error {
		for i := 0; i < n; i++ {
			if _, err := f.T.Enqueue(ctx, tx, f.Owner, NewAgentRun{Card: f.Card, Column: "grounding", Round: 1, Role: "scoper", Harness: harness, Prompt: "p", MaxAttempts: 2}); err != nil {
				return err
			}
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
}

func (f *fixture) runtime(t *testing.T, user uuid.UUID, daemon, harness string) RuntimeRef {
	t.Helper()
	rs, err := f.T.Register(context.Background(), user, f.WS, daemon, "laptop", []string{harness}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(rs) != 1 {
		t.Fatalf("registered runtimes: %d", len(rs))
	}
	return RuntimeRef{WorkspaceID: f.WS, RuntimeID: rs[0].ID, Attempt: 1}
}

func (f *fixture) settledCount() int { f.mu.Lock(); defer f.mu.Unlock(); return len(f.settled) }

func TestClaimRaceDispatchesEachAgentRunOnce(t *testing.T) {
	f := setup(t)
	f.enqueue(t, 10, "claude")
	a, b := f.runtime(t, f.Owner, "d1", "claude"), f.runtime(t, f.Owner, "d2", "claude")
	if _, err := f.Env.Owner.Exec(context.Background(), "UPDATE runtime SET max_concurrent = 10"); err != nil {
		t.Fatal(err)
	}
	var mu sync.Mutex
	seen := map[uuid.UUID]int{}
	var wg sync.WaitGroup
	for _, ref := range []RuntimeRef{a, b, a, b} {
		wg.Add(1)
		go func(ref RuntimeRef) {
			defer wg.Done()
			got, err := f.T.Claim(context.Background(), f.Owner, []RuntimeRef{ref}, 5)
			if err != nil {
				t.Error(err)
			}
			mu.Lock()
			for _, c := range got {
				seen[c.AgentRun.ID]++
			}
			mu.Unlock()
		}(ref)
	}
	wg.Wait()
	if len(seen) != 10 {
		t.Fatalf("dispatched %d distinct agent runs, want 10", len(seen))
	}
	for id, n := range seen {
		if n != 1 {
			t.Fatalf("agent run %s dispatched %d times", id, n)
		}
	}
}

func TestOnlyTheCardOwnersMatchingRuntimeClaims(t *testing.T) {
	f := setup(t)
	f.enqueue(t, 1, "claude")
	other, err := f.T.Store.Global().UpsertUserByEmail(context.Background(), "b@lab.org")
	if err != nil {
		t.Fatal(err)
	}
	if err := f.T.Store.InWorkspace(context.Background(), f.WS, f.Owner, func(tx *store.Tx) error {
		return tx.Q.AddMember(context.Background(), db.AddMemberParams{WorkspaceID: f.WS, UserID: other.ID, Role: "member"})
	}); err != nil {
		t.Fatal(err)
	}
	bRef := f.runtime(t, other.ID, "db", "claude")
	if got, err := f.T.Claim(context.Background(), other.ID, []RuntimeRef{bRef}, 5); err != nil {
		t.Fatal(err)
	} else if len(got) != 0 {
		t.Fatal("another member's runtime claimed the owner's card")
	}
	codex := f.runtime(t, f.Owner, "d1", "codex")
	if got, err := f.T.Claim(context.Background(), f.Owner, []RuntimeRef{codex}, 5); err != nil {
		t.Fatal(err)
	} else if len(got) != 0 {
		t.Fatal("a codex runtime claimed a claude agent run")
	}
	if _, err := f.T.Claim(context.Background(), other.ID, []RuntimeRef{codex}, 5); err == nil {
		t.Fatal("claimed through someone else's runtime")
	}
}

func TestFlaggedOrEndedCardsAreNotClaimed(t *testing.T) {
	f := setup(t)
	f.enqueue(t, 1, "claude")
	ref := f.runtime(t, f.Owner, "d1", "claude")
	ctx := context.Background()
	for _, set := range []string{"flag = 'out_of_budget'", "flag = '', ending = 'stopped'"} {
		if _, err := f.Env.Owner.Exec(ctx, "UPDATE card SET "+set+" WHERE id = $1", f.Card); err != nil {
			t.Fatal(err)
		}
		if got, err := f.T.Claim(ctx, f.Owner, []RuntimeRef{ref}, 5); err != nil {
			t.Fatal(err)
		} else if len(got) != 0 {
			t.Fatalf("claimed an agent run of a card with %s", set)
		}
	}
	if _, err := f.Env.Owner.Exec(ctx, "UPDATE card SET flag = '', ending = '' WHERE id = $1", f.Card); err != nil {
		t.Fatal(err)
	}
	if got, err := f.T.Claim(ctx, f.Owner, []RuntimeRef{ref}, 5); err != nil {
		t.Fatal(err)
	} else if len(got) != 1 {
		t.Fatal("the agent run was not claimed once the card was clear")
	}
}

func TestSettleOnceAndAddSpend(t *testing.T) {
	f := setup(t)
	f.enqueue(t, 1, "claude")
	ref := f.runtime(t, f.Owner, "d1", "claude")
	got, err := f.T.Claim(context.Background(), f.Owner, []RuntimeRef{ref}, 1)
	if err != nil || len(got) != 1 {
		t.Fatalf("claim: %v, got %d runs", err, len(got))
	}
	id := got[0].AgentRun.ID
	ctx := context.Background()
	if err := f.T.Start(ctx, f.Owner, ref, id); err != nil {
		t.Fatal(err)
	}
	if err := f.T.Messages(ctx, f.Owner, ref, id, []Message{{Seq: 1, Type: "text", Content: []byte(`"hi"`)}, {Seq: 1, Type: "text", Content: []byte(`"dup"`)}}); err != nil {
		t.Fatal(err)
	}
	usage := []agent.Usage{
		{Model: "reported", InputTokens: 10, OutputTokens: 2, CostUSD: usd(0.5)},
		{Model: "gpt-5.6-sol", InputTokens: 1_000_000},
		{Model: "nobody-knows", OutputTokens: 5},
	}
	for i := 0; i < 2; i++ {
		if _, err := f.T.Complete(ctx, f.Owner, ref, id, Report{Usage: usage, SessionID: "s"}); err != nil {
			t.Fatalf("complete %d: %v", i, err)
		}
	}
	// One million aggregate input tokens uses the disclosed long-band upper bound.
	want := 8.5 // 0.5 reported cost + 8.0 estimated cost.
	var spent, cost float64
	var msgs, rows int
	if err := f.Env.Owner.QueryRow(ctx, "SELECT spent_usd FROM card WHERE id = $1", f.Card).Scan(&spent); err != nil {
		t.Fatal(err)
	}
	if err := f.Env.Owner.QueryRow(ctx, "SELECT cost_usd FROM agent_run WHERE id = $1", id).Scan(&cost); err != nil {
		t.Fatal(err)
	}
	if err := f.Env.Owner.QueryRow(ctx, "SELECT count(*) FROM agent_run_message WHERE agent_run_id = $1", id).Scan(&msgs); err != nil {
		t.Fatal(err)
	}
	if err := f.Env.Owner.QueryRow(ctx, "SELECT count(*) FROM agent_run_usage WHERE agent_run_id = $1", id).Scan(&rows); err != nil {
		t.Fatal(err)
	}
	if math.Abs(spent-want) > 1e-9 || math.Abs(cost-want) > 1e-9 || rows != 3 || msgs != 1 || f.settledCount() != 1 {
		t.Fatalf("spent %v, cost %v (want %v), usage rows %d, messages %d, settled %d", spent, cost, want, rows, msgs, f.settledCount())
	}
	// Only the CLI's own figure is marked reported; rate-table prices are estimates.
	var reported []string
	rs, err := f.Env.Owner.Query(ctx, "SELECT model FROM agent_run_usage WHERE agent_run_id = $1 AND cost_reported ORDER BY model", id)
	if err != nil {
		t.Fatal(err)
	}
	for rs.Next() {
		var m string
		rs.Scan(&m)
		reported = append(reported, m)
	}
	rs.Close()
	if len(reported) != 1 || reported[0] != "reported" {
		t.Fatalf("cost_reported rows %v", reported)
	}
	if _, err := f.T.Fail(ctx, f.Owner, ref, id, Report{Error: "late", Usage: usage}); err != nil {
		t.Fatalf("a late fail after complete should return the stored result: %v", err)
	}
	if err := f.Env.Owner.QueryRow(ctx, "SELECT count(*) FROM agent_run_usage WHERE agent_run_id = $1", id).Scan(&rows); err != nil {
		t.Fatal(err)
	}
	if rows != 3 {
		t.Fatalf("a late report added usage rows: %d", rows)
	}
}

func TestOnlyMachineAndNetworkFailuresRetry(t *testing.T) {
	f := setup(t)
	ref := f.runtime(t, f.Owner, "d1", "claude")
	ctx := context.Background()
	attempt := func() uuid.UUID {
		t.Helper()
		got, err := f.T.Claim(ctx, f.Owner, []RuntimeRef{ref}, 1)
		if err != nil || len(got) != 1 {
			t.Fatalf("claim: %v, got %d runs", err, len(got))
		}
		if len(got) != 1 {
			t.Fatal("nothing to claim")
		}
		ref.Attempt = got[0].AgentRun.Attempt
		if err := f.T.Start(ctx, f.Owner, ref, got[0].AgentRun.ID); err != nil {
			t.Fatal(err)
		}
		return got[0].AgentRun.ID
	}

	f.enqueue(t, 1, "claude")
	for n, want := range []string{"queued", "failed"} {
		tk, err := f.T.Fail(ctx, f.Owner, ref, attempt(), Report{Error: "connection reset", FailureReason: "provider_network"})
		if err != nil || tk.Status != want || reasonOf(tk) != "provider_network" {
			t.Fatalf("attempt %d: %s %q %v", n+1, tk.Status, reasonOf(tk), err)
		}
	}
	for _, reason := range []string{"context_exhausted", "agent_error", ""} {
		f.enqueue(t, 1, "claude")
		tk, err := f.T.Fail(ctx, f.Owner, ref, attempt(), Report{Error: "x", FailureReason: reason})
		want := reason
		if want == "" {
			want = "agent_error"
		}
		if err != nil || tk.Status != "failed" || tk.Attempt != 1 || reasonOf(tk) != want {
			t.Fatalf("%q: %s attempt %d %q %v", reason, tk.Status, tk.Attempt, reasonOf(tk), err)
		}
	}
	if f.settledCount() != 4 {
		t.Fatalf("settled %d, want 4", f.settledCount())
	}
}

func TestBadReportIsRefused(t *testing.T) {
	f := setup(t)
	f.enqueue(t, 1, "claude")
	ref := f.runtime(t, f.Owner, "d1", "claude")
	ctx := context.Background()
	got, err := f.T.Claim(ctx, f.Owner, []RuntimeRef{ref}, 1)
	if err != nil || len(got) != 1 {
		t.Fatalf("claim: %v, got %d runs", err, len(got))
	}
	id := got[0].AgentRun.ID
	if err := f.T.Start(ctx, f.Owner, ref, id); err != nil {
		t.Fatal(err)
	}
	for name, r := range map[string]Report{
		"negative cost":   {Usage: []agent.Usage{{Model: "m", CostUSD: usd(-5)}}},
		"negative tokens": {Usage: []agent.Usage{{Model: "m", InputTokens: -1}}},
		"unknown reason":  {FailureReason: "solar_flare"},
	} {
		if _, err := f.T.Fail(ctx, f.Owner, ref, id, r); !errors.Is(err, ErrBadReport) {
			t.Fatalf("%s: %v", name, err)
		}
	}
	if s, err := f.T.Status(ctx, f.Owner, ref, id); err != nil {
		t.Fatal(err)
	} else if s != "running" {
		t.Fatalf("a refused report changed the agent run: %s", s)
	}
}

func TestEnqueueAndClaimBookkeeping(t *testing.T) {
	f := setup(t)
	f.enqueue(t, 1, "claude")
	ctx := context.Background()
	if _, err := f.Env.Owner.Exec(ctx, "UPDATE agent_run SET queue_reason = 'runtime_offline' WHERE card_id = $1", f.Card); err != nil {
		t.Fatal(err)
	}
	ref := f.runtime(t, f.Owner, "d1", "claude")
	got, err := f.T.Claim(ctx, f.Owner, []RuntimeRef{ref}, 1)
	if err != nil || len(got) != 1 {
		t.Fatalf("claim: %v, got %d runs", err, len(got))
	}
	if got[0].AgentRun.ResponsibleUserID != f.Owner {
		t.Fatalf("responsible user %s, want the card owner", got[0].AgentRun.ResponsibleUserID)
	}
	if got[0].AgentRun.QueueReason != nil {
		t.Fatalf("a claimed agent run kept its queue reason %q", *got[0].AgentRun.QueueReason)
	}
}

func TestSweepReclaimsLostWork(t *testing.T) {
	f := setup(t)
	f.enqueue(t, 1, "claude")
	ref := f.runtime(t, f.Owner, "d1", "claude")
	ctx := context.Background()
	got, err := f.T.Claim(ctx, f.Owner, []RuntimeRef{ref}, 1)
	if err != nil || len(got) != 1 {
		t.Fatalf("claim: %v, got %d runs", err, len(got))
	}
	if err := f.T.Start(ctx, f.Owner, ref, got[0].AgentRun.ID); err != nil {
		t.Fatal(err)
	}
	time.Sleep(1200 * time.Millisecond) // Stale is 1 s and no heartbeat came
	if err := f.T.Sweep(ctx); err != nil {
		t.Fatal(err)
	}
	var status, reason string
	if err := f.Env.Owner.QueryRow(ctx, "SELECT status, failure_reason FROM agent_run WHERE id = $1", got[0].AgentRun.ID).Scan(&status, &reason); err != nil {
		t.Fatal(err)
	}
	if status != "queued" || reason != "runtime_offline" {
		t.Fatalf("after one lost attempt: %s %s", status, reason)
	}
	if _, err := f.T.Heartbeat(ctx, f.Owner, []RuntimeRef{ref}); err != nil {
		t.Fatal(err)
	}
	got, err = f.T.Claim(ctx, f.Owner, []RuntimeRef{ref}, 1)
	if err != nil || len(got) != 1 {
		t.Fatalf("claim: %v, got %d runs", err, len(got))
	}
	ref.Attempt = got[0].AgentRun.Attempt
	if err := f.T.Start(ctx, f.Owner, ref, got[0].AgentRun.ID); err != nil {
		t.Fatal(err)
	}
	time.Sleep(1200 * time.Millisecond)
	if err := f.T.Sweep(ctx); err != nil {
		t.Fatal(err)
	}
	if err := f.Env.Owner.QueryRow(ctx, "SELECT status FROM agent_run WHERE id = $1", got[0].AgentRun.ID).Scan(&status); err != nil {
		t.Fatal(err)
	}
	if status != "failed" || f.settledCount() != 1 {
		t.Fatalf("after the last attempt: %s, settled %d", status, f.settledCount())
	}
	if _, err := f.T.Complete(ctx, f.Owner, ref, got[0].AgentRun.ID, Report{}); err != nil {
		t.Fatalf("a late report for a swept agent run should return the stored result: %v", err)
	}
	if f.settledCount() != 1 {
		t.Fatal("a late report fired OnSettled again")
	}
}

func TestExpiredLeaseRequeuesAsTimeout(t *testing.T) {
	f := setup(t)
	f.enqueue(t, 1, "claude")
	ref := f.runtime(t, f.Owner, "d1", "claude")
	ctx := context.Background()
	got, err := f.T.Claim(ctx, f.Owner, []RuntimeRef{ref}, 1)
	if err != nil || len(got) != 1 {
		t.Fatalf("claim: %v, got %d runs", err, len(got))
	}
	if _, err := f.Env.Owner.Exec(ctx, "UPDATE agent_run SET prepare_lease_expires_at = now() - interval '1 second' WHERE id = $1", got[0].AgentRun.ID); err != nil {
		t.Fatal(err)
	}
	if err := f.T.Sweep(ctx); err != nil {
		t.Fatal(err)
	}
	var status, reason string
	if err := f.Env.Owner.QueryRow(ctx, "SELECT status, failure_reason FROM agent_run WHERE id = $1", got[0].AgentRun.ID).Scan(&status, &reason); err != nil {
		t.Fatal(err)
	}
	if status != "queued" || reason != "timeout" {
		t.Fatalf("after an expired lease: %s %s", status, reason)
	}
}

func TestCancel(t *testing.T) {
	f := setup(t)
	f.enqueue(t, 1, "claude")
	ref := f.runtime(t, f.Owner, "d1", "claude")
	ctx := context.Background()
	got, err := f.T.Claim(ctx, f.Owner, []RuntimeRef{ref}, 1)
	if err != nil || len(got) != 1 {
		t.Fatalf("claim: %v, got %d runs", err, len(got))
	}
	if err := f.T.Start(ctx, f.Owner, ref, got[0].AgentRun.ID); err != nil {
		t.Fatal(err)
	}
	if err := f.T.Cancel(ctx, f.WS, f.Owner, got[0].AgentRun.ID); err != nil {
		t.Fatal(err)
	}
	if s, err := f.T.Status(ctx, f.Owner, ref, got[0].AgentRun.ID); err != nil {
		t.Fatal(err)
	} else if s != "cancelled" {
		t.Fatalf("status %s", s)
	}
	if f.settledCount() != 1 {
		t.Fatal("cancel did not settle")
	}
}

func TestLateReportOfACancelledRunCountsUsageOnce(t *testing.T) {
	f := setup(t)
	f.enqueue(t, 1, "claude")
	ref := f.runtime(t, f.Owner, "d1", "claude")
	ctx := context.Background()
	got, err := f.T.Claim(ctx, f.Owner, []RuntimeRef{ref}, 1)
	if err != nil || len(got) != 1 {
		t.Fatalf("claim: %v, got %d runs", err, len(got))
	}
	id := got[0].AgentRun.ID
	if err := f.T.Start(ctx, f.Owner, ref, id); err != nil {
		t.Fatal(err)
	}
	if err := f.T.Cancel(ctx, f.WS, f.Owner, id); err != nil {
		t.Fatal(err)
	}
	late := Report{Error: "cancelled", FailureReason: "runtime_offline", Usage: []agent.Usage{{Model: "m", OutputTokens: 7, CostUSD: usd(0.3)}}}
	for i := 0; i < 2; i++ { // the outbox may deliver it twice
		tk, err := f.T.Fail(ctx, f.Owner, ref, id, late)
		if err != nil || tk.Status != "cancelled" {
			t.Fatalf("late report %d: %s %v", i, tk.Status, err)
		}
	}
	if _, err := f.T.Complete(ctx, f.Owner, ref, id, late); err != nil {
		t.Fatal(err)
	}
	var spent, cost float64
	var rows int
	var status string
	if err := f.Env.Owner.QueryRow(ctx, "SELECT spent_usd FROM card WHERE id = $1", f.Card).Scan(&spent); err != nil {
		t.Fatal(err)
	}
	if err := f.Env.Owner.QueryRow(ctx, "SELECT status, cost_usd FROM agent_run WHERE id = $1", id).Scan(&status, &cost); err != nil {
		t.Fatal(err)
	}
	if err := f.Env.Owner.QueryRow(ctx, "SELECT count(*) FROM agent_run_usage WHERE agent_run_id = $1", id).Scan(&rows); err != nil {
		t.Fatal(err)
	}
	if status != "cancelled" || spent != 0.3 || cost != 0.3 || rows != 1 || f.settledCount() != 1 {
		t.Fatalf("status %s, spent %v, cost %v, rows %d, settled %d", status, spent, cost, rows, f.settledCount())
	}
}

func (f *fixture) claimStart(t *testing.T, ref RuntimeRef) (RuntimeRef, uuid.UUID) {
	t.Helper()
	got, err := f.T.Claim(context.Background(), f.Owner, []RuntimeRef{ref}, 1)
	if err != nil || len(got) != 1 {
		t.Fatalf("claim: %v %+v", err, got)
	}
	ref.Attempt = got[0].AgentRun.Attempt
	if err := f.T.Start(context.Background(), f.Owner, ref, got[0].AgentRun.ID); err != nil {
		t.Fatal(err)
	}
	return ref, got[0].AgentRun.ID
}

func TestAttemptReplayCannotChargeOrSettleReclaimedRun(t *testing.T) {
	f := setup(t)
	ctx := context.Background()
	f.enqueue(t, 1, "claude")
	ref := f.runtime(t, f.Owner, "d1", "claude")
	old, id := f.claimStart(t, ref)
	r := Report{FailureReason: "provider_network", Usage: []agent.Usage{{Model: "m", CostUSD: usd(.2)}}}
	first, err := f.T.Fail(ctx, f.Owner, old, id, r)
	if err != nil || first.Status != "queued" {
		t.Fatalf("first: %+v %v", first, err)
	}
	// Replay both before and after the same runtime reclaims the next attempt.
	if again, err := f.T.Fail(ctx, f.Owner, old, id, r); err != nil || again.Status != "queued" {
		t.Fatalf("replay: %+v %v", again, err)
	}
	current, _ := f.claimStart(t, ref)
	if current.Attempt != 2 {
		t.Fatalf("attempt %d", current.Attempt)
	}
	for _, complete := range []bool{false, true} {
		var got db.AgentRun
		if complete {
			got, err = f.T.Complete(ctx, f.Owner, old, id, r)
		} else {
			got, err = f.T.Fail(ctx, f.Owner, old, id, r)
		}
		if err != nil || got.Status != "queued" || got.Attempt != 1 {
			t.Fatalf("old receipt: %+v %v", got, err)
		}
	}
	if err := f.T.Messages(ctx, f.Owner, old, id, []Message{{Seq: 1, Type: "text", Content: []byte(`"stale"`)}}); !errors.Is(err, ErrConflict) {
		t.Fatalf("stale messages: %v", err)
	}
	if err := f.T.PrepareLease(ctx, f.Owner, old, id); !errors.Is(err, ErrConflict) {
		t.Fatalf("stale lease: %v", err)
	}
	if err := f.T.Start(ctx, f.Owner, old, id); !errors.Is(err, ErrConflict) {
		t.Fatalf("stale start: %v", err)
	}
	if err := f.T.Messages(ctx, f.Owner, current, id, []Message{{Seq: 1, Type: "text", Content: []byte(`"new"`)}}); err != nil {
		t.Fatal(err)
	}
	result, err := f.T.Complete(ctx, f.Owner, current, id, Report{Usage: []agent.Usage{{Model: "m", CostUSD: usd(.3)}}})
	if err != nil || result.Status != "completed" || math.Abs(result.CostUsd-.5) > 1e-9 {
		t.Fatalf("result: %+v %v", result, err)
	}
	var spent float64
	var count int
	if err := f.Env.Owner.QueryRow(ctx, "SELECT spent_usd FROM card WHERE id=$1", f.Card).Scan(&spent); err != nil {
		t.Fatal(err)
	}
	if err := f.Env.Owner.QueryRow(ctx, "SELECT count(*) FROM agent_run_usage WHERE agent_run_id=$1", id).Scan(&count); err != nil {
		t.Fatal(err)
	}
	if math.Abs(spent-.5) > 1e-9 || count != 2 || f.settledCount() != 1 {
		t.Fatalf("spent=%v rows=%d settled=%d", spent, count, f.settledCount())
	}
}

func TestCancelBeforeStartNeverAcceptsUsage(t *testing.T) {
	f := setup(t)
	ctx := context.Background()
	f.enqueue(t, 1, "claude")
	ref := f.runtime(t, f.Owner, "d1", "claude")
	got, err := f.T.Claim(ctx, f.Owner, []RuntimeRef{ref}, 1)
	if err != nil || len(got) != 1 {
		t.Fatalf("claim %v", err)
	}
	id := got[0].AgentRun.ID
	if err := f.T.Cancel(ctx, f.WS, f.Owner, id); err != nil {
		t.Fatal(err)
	}
	result, err := f.T.Complete(ctx, f.Owner, ref, id, Report{Usage: []agent.Usage{{Model: "m", CostUSD: usd(2)}}})
	if err != nil || result.CostUsd != 0 || result.Status != "cancelled" {
		t.Fatalf("late %+v %v", result, err)
	}
	var spent float64
	if err := f.Env.Owner.QueryRow(ctx, "SELECT spent_usd FROM card WHERE id=$1", f.Card).Scan(&spent); err != nil {
		t.Fatal(err)
	}
	if spent != 0 {
		t.Fatalf("spent %v", spent)
	}
}

func TestFiniteDistinctModelReports(t *testing.T) {
	f := setup(t)
	ctx := context.Background()
	f.enqueue(t, 1, "claude")
	ref, id := f.claimStart(t, f.runtime(t, f.Owner, "d1", "claude"))
	for name, r := range map[string]Report{
		"nan":                     {Usage: []agent.Usage{{Model: "m", CostUSD: usd(math.NaN())}}},
		"infinity":                {Usage: []agent.Usage{{Model: "m", CostUSD: usd(math.Inf(1))}}},
		"sum overflow":            {Usage: []agent.Usage{{Model: "a", CostUSD: usd(math.MaxFloat64)}, {Model: "b", CostUSD: usd(math.MaxFloat64)}}},
		"duplicates":              {Usage: []agent.Usage{{Model: "m"}, {Model: "m"}}},
		"empty model":             {Usage: []agent.Usage{{}}},
		"too many":                {Usage: make([]agent.Usage, 33)},
		"negative cache":          {Usage: []agent.Usage{{Model: "m", CacheReadTokens: -1}}},
		"complete unknown reason": {FailureReason: "solar_flare"},
	} {
		if _, err := f.T.Complete(ctx, f.Owner, ref, id, r); !errors.Is(err, ErrBadReport) {
			t.Fatalf("%s: %v", name, err)
		}
	}
	if st, err := f.T.Status(ctx, f.Owner, ref, id); err != nil || st != "running" {
		t.Fatalf("status %s %v", st, err)
	}
}

func TestConcurrentClaimsRespectRuntimeLimit(t *testing.T) {
	f := setup(t)
	ctx := context.Background()
	f.enqueue(t, 8, "claude")
	ref := f.runtime(t, f.Owner, "d1", "claude")
	var wg sync.WaitGroup
	var mu sync.Mutex
	total := 0
	for i := 0; i < 6; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			got, err := f.T.Claim(ctx, f.Owner, []RuntimeRef{ref, ref}, 8)
			if err != nil {
				t.Error(err)
			}
			mu.Lock()
			total += len(got)
			mu.Unlock()
		}()
	}
	wg.Wait()
	if total != 2 {
		t.Fatalf("claimed %d; runtime limit is 2", total)
	}
}

func TestMembershipAndAssignedRuntimeRequired(t *testing.T) {
	f := setup(t)
	ctx := context.Background()
	f.enqueue(t, 1, "claude")
	ref, id := f.claimStart(t, f.runtime(t, f.Owner, "d1", "claude"))
	other := f.runtime(t, f.Owner, "d2", "claude")
	if _, err := f.T.Complete(ctx, f.Owner, other, id, Report{}); !errors.Is(err, ErrNotYours) {
		t.Fatalf("wrong runtime report: %v", err)
	}
	if _, err := f.T.Status(ctx, f.Owner, other, id); !errors.Is(err, ErrNotYours) {
		t.Fatalf("wrong runtime status: %v", err)
	}
	outsider, err := f.T.Store.Global().UpsertUserByEmail(ctx, "outsider@example.com")
	if err != nil {
		t.Fatal(err)
	}
	if err := f.T.Cancel(ctx, f.WS, outsider.ID, id); !errors.Is(err, ErrNotYours) {
		t.Fatalf("outsider cancel: %v", err)
	}
	if _, err := f.Env.Owner.Exec(ctx, "DELETE FROM member WHERE workspace_id=$1 AND user_id=$2", f.WS, f.Owner); err != nil {
		t.Fatal(err)
	}
	if _, err := f.T.Heartbeat(ctx, f.Owner, []RuntimeRef{ref}); !errors.Is(err, ErrNotYours) {
		t.Fatalf("removed-member heartbeat: %v", err)
	}
	if _, err := f.T.Claim(ctx, f.Owner, []RuntimeRef{ref}, 1); !errors.Is(err, ErrNotYours) {
		t.Fatalf("removed-member claim: %v", err)
	}
}

func TestPendingSettlementRecoversAndConcurrentDeliveryIsSerialized(t *testing.T) {
	f := setup(t)
	ctx := context.Background()
	f.enqueue(t, 1, "claude")
	ref, id := f.claimStart(t, f.runtime(t, f.Owner, "d1", "claude"))
	f.T.OnSettled = nil
	if _, err := f.T.Complete(ctx, f.Owner, ref, id, Report{}); err != nil {
		t.Fatal(err)
	}
	// Simulate a new service instance after commit but before notification delivery.
	recovered := &AgentRuns{Store: f.T.Store, OnSettlement: func(ctx context.Context, n Settlement) error {
		if n.ID != id || n.AgentRun.ID != id || n.AgentRun.Status != "completed" {
			t.Errorf("notification %+v", n)
		}
		var status string
		if err := f.Env.Owner.QueryRow(ctx, "SELECT status FROM agent_run WHERE id=$1", id).Scan(&status); err != nil || status != "completed" {
			t.Errorf("callback before commit: %s %v", status, err)
		}
		f.mu.Lock()
		f.settled = append(f.settled, n.AgentRun)
		f.mu.Unlock()
		return nil
	}}
	var wg sync.WaitGroup
	for i := 0; i < 4; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if err := recovered.RecoverSettlements(ctx); err != nil {
				t.Error(err)
			}
		}()
	}
	wg.Wait()
	if f.settledCount() != 1 {
		t.Fatalf("delivered %d", f.settledCount())
	}
	if err := recovered.RecoverSettlements(ctx); err != nil {
		t.Fatal(err)
	}
	if f.settledCount() != 1 {
		t.Fatal("delivered acknowledged notification again")
	}
}

func TestFailedSettlementConsumerRemainsPending(t *testing.T) {
	f := setup(t)
	ctx := context.Background()
	f.enqueue(t, 1, "claude")
	ref, id := f.claimStart(t, f.runtime(t, f.Owner, "d1", "claude"))
	attempts := 0
	f.T.OnSettlement = func(context.Context, Settlement) error { attempts++; return errors.New("temporarily unavailable") }
	if result, err := f.T.Complete(ctx, f.Owner, ref, id, Report{}); err != nil || result.Status != "completed" {
		t.Fatalf("settlement %+v %v", result, err)
	}
	if attempts != 1 {
		t.Fatalf("consumer attempts %d", attempts)
	}
	f.T.OnSettlement = func(context.Context, Settlement) error { attempts++; return nil }
	if err := f.T.RecoverSettlements(ctx); err != nil {
		t.Fatal(err)
	}
	if attempts != 2 {
		t.Fatalf("consumer attempts %d", attempts)
	}
}

func TestSweepRollbackDoesNotLoseStateEventsOrNotification(t *testing.T) {
	f := setup(t)
	ctx := context.Background()
	f.enqueue(t, 1, "claude")
	ref, id := f.claimStart(t, f.runtime(t, f.Owner, "d1", "claude"))
	if _, err := f.Env.Owner.Exec(ctx, "UPDATE agent_run SET max_attempts=1 WHERE id=$1", id); err != nil {
		t.Fatal(err)
	}
	if _, err := f.Env.Owner.Exec(ctx, "UPDATE runtime SET last_seen_at=now()-interval '5 minutes' WHERE id=$1", ref.RuntimeID); err != nil {
		t.Fatal(err)
	}
	// Force the event insert to fail after the sweeper's state mutation.
	if _, err := f.Env.Owner.Exec(ctx, `CREATE FUNCTION reject_failed_event() RETURNS trigger LANGUAGE plpgsql AS $$ BEGIN IF NEW.type = 'agent_run.failed' THEN RAISE EXCEPTION 'injected event failure'; END IF; RETURN NEW; END $$; CREATE TRIGGER reject_failed_event BEFORE INSERT ON event FOR EACH ROW EXECUTE FUNCTION reject_failed_event()`); err != nil {
		t.Fatal(err)
	}
	if err := f.T.Sweep(ctx); err == nil {
		t.Fatal("sweep ignored event failure")
	}
	var status string
	if err := f.Env.Owner.QueryRow(ctx, "SELECT status FROM agent_run WHERE id=$1", id).Scan(&status); err != nil {
		t.Fatal(err)
	}
	if status != "running" || f.settledCount() != 0 {
		t.Fatalf("failed transaction left %s and %d callbacks", status, f.settledCount())
	}
	if _, err := f.Env.Owner.Exec(ctx, "DROP TRIGGER reject_failed_event ON event"); err != nil {
		t.Fatal(err)
	}
	if err := f.T.Sweep(ctx); err != nil {
		t.Fatal(err)
	}
	if status, err := f.T.Status(ctx, f.Owner, ref, id); err != nil || status != "failed" {
		t.Fatalf("swept %s %v", status, err)
	}
	if f.settledCount() != 1 {
		t.Fatalf("settled %d", f.settledCount())
	}
}

func TestDeletedRuntimeIsSweptWithDurableNotification(t *testing.T) {
	f := setup(t)
	ctx := context.Background()
	f.enqueue(t, 1, "claude")
	ref, id := f.claimStart(t, f.runtime(t, f.Owner, "d1", "claude"))
	if _, err := f.Env.Owner.Exec(ctx, "UPDATE agent_run SET max_attempts=1 WHERE id=$1", id); err != nil {
		t.Fatal(err)
	}
	if _, err := f.Env.Owner.Exec(ctx, "DELETE FROM runtime WHERE id=$1", ref.RuntimeID); err != nil {
		t.Fatal(err)
	}
	if err := f.T.Sweep(ctx); err != nil {
		t.Fatal(err)
	}
	var status string
	if err := f.Env.Owner.QueryRow(ctx, "SELECT status FROM agent_run WHERE id=$1", id).Scan(&status); err != nil {
		t.Fatal(err)
	}
	if status != "failed" || f.settledCount() != 1 {
		t.Fatalf("status %s settled %d", status, f.settledCount())
	}
}

func TestCancelledLateReportDoesNotRedeliverPendingSettlement(t *testing.T) {
	f := setup(t)
	ctx := context.Background()
	f.enqueue(t, 1, "claude")
	ref, id := f.claimStart(t, f.runtime(t, f.Owner, "d1", "claude"))
	calls := 0
	f.T.OnSettlement = func(context.Context, Settlement) error { calls++; return errors.New("retry later") }
	if err := f.T.Cancel(ctx, f.WS, f.Owner, id); err != nil {
		t.Fatal(err)
	}
	if _, err := f.T.Complete(ctx, f.Owner, ref, id, Report{Usage: []agent.Usage{{Model: "m", CostUSD: usd(.1)}}}); err != nil {
		t.Fatal(err)
	}
	if calls != 1 {
		t.Fatalf("late usage redelivered settlement: %d calls", calls)
	}
}

func TestSweepAndCancelDoNotDeadlockAcrossTwoRuns(t *testing.T) {
	f := setup(t)
	ctx := context.Background()
	f.enqueue(t, 2, "claude")
	ref := f.runtime(t, f.Owner, "d1", "claude")
	_, a := f.claimStart(t, ref)
	_, b := f.claimStart(t, ref)
	second := a
	if a.String() < b.String() {
		second = b
	}
	if _, err := f.Env.Owner.Exec(ctx, "UPDATE agent_run SET max_attempts=1 WHERE card_id=$1", f.Card); err != nil {
		t.Fatal(err)
	}
	if _, err := f.Env.Owner.Exec(ctx, "UPDATE runtime SET last_seen_at=now()-interval '5 minutes' WHERE id=$1", ref.RuntimeID); err != nil {
		t.Fatal(err)
	}
	if _, err := f.Env.Owner.Exec(ctx, `CREATE FUNCTION pause_sweep_event() RETURNS trigger LANGUAGE plpgsql AS $$ BEGIN IF NEW.type = 'agent_run.failed' THEN PERFORM pg_sleep(0.3); END IF; RETURN NEW; END $$; CREATE TRIGGER pause_sweep_event BEFORE INSERT ON event FOR EACH ROW EXECUTE FUNCTION pause_sweep_event()`); err != nil {
		t.Fatal(err)
	}
	sweep := make(chan error, 1)
	go func() { sweep <- f.T.Sweep(ctx) }()
	deadline := time.Now().Add(5 * time.Second)
	for {
		var sleeping bool
		if err := f.Env.Owner.QueryRow(ctx, "SELECT EXISTS(SELECT 1 FROM pg_stat_activity WHERE datname=current_database() AND wait_event='PgSleep')").Scan(&sleeping); err != nil {
			t.Fatal(err)
		}
		if sleeping {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("sweep never reached event write")
		}
		time.Sleep(10 * time.Millisecond)
	}
	cancelErr := f.T.Cancel(ctx, f.WS, f.Owner, second)
	if cancelErr != nil && !errors.Is(cancelErr, ErrConflict) {
		t.Fatalf("cancel: %v", cancelErr)
	}
	if err := <-sweep; err != nil {
		t.Fatalf("sweep: %v", err)
	}
	if f.settledCount() != 2 {
		t.Fatalf("settled %d", f.settledCount())
	}
}

func TestSettlementConsumersCanUseTheSameSingleConnectionPool(t *testing.T) {
	for _, legacy := range []bool{false, true} {
		name := "OnSettlement"
		if legacy {
			name = "OnSettled"
		}
		t.Run(name, func(t *testing.T) {
			f := setup(t)
			ctx := context.Background()
			f.enqueue(t, 1, "claude")
			ref, id := f.claimStart(t, f.runtime(t, f.Owner, "d1", "claude"))
			config, err := pgxpool.ParseConfig(f.Env.AppURL)
			if err != nil {
				t.Fatal(err)
			}
			config.MaxConns = 1
			pool, err := pgxpool.NewWithConfig(ctx, config)
			if err != nil {
				t.Fatal(err)
			}
			defer pool.Close()
			f.T.Store = &store.Store{Pool: pool}
			var consumerErr error
			calls := 0
			consume := func(ctx context.Context, run db.AgentRun) error {
				calls++
				bounded, cancel := context.WithTimeout(ctx, 500*time.Millisecond)
				defer cancel()
				consumerErr = f.T.Store.InWorkspace(bounded, f.WS, f.Owner, func(tx *store.Tx) error {
					got, err := tx.Q.GetAgentRun(bounded, db.GetAgentRunParams{WorkspaceID: f.WS, ID: run.ID})
					if err != nil {
						return err
					}
					if got.Status != "completed" {
						return errors.New("consumer did not observe committed completion")
					}
					return nil
				})
				return consumerErr
			}
			if legacy {
				f.T.OnSettled = func(ctx context.Context, run db.AgentRun) { _ = consume(ctx, run) }
			} else {
				f.T.OnSettlement = func(ctx context.Context, n Settlement) error { return consume(ctx, n.AgentRun) }
			}
			if _, err := f.T.Complete(ctx, f.Owner, ref, id, Report{}); err != nil {
				t.Fatal(err)
			}
			if consumerErr != nil {
				t.Fatalf("consumer blocked on its own app pool: %v", consumerErr)
			}
			if calls != 1 {
				t.Fatalf("calls %d", calls)
			}
			if err := f.T.RecoverSettlements(ctx); err != nil {
				t.Fatal(err)
			}
			if calls != 1 {
				t.Fatalf("acknowledged consumer called %d times", calls)
			}
		})
	}
}

func TestExpiredSettlementDeliveryLeaseIsRecovered(t *testing.T) {
	f := setup(t)
	ctx := context.Background()
	f.enqueue(t, 1, "claude")
	ref, id := f.claimStart(t, f.runtime(t, f.Owner, "d1", "claude"))
	f.T.OnSettled = nil
	if _, err := f.T.Complete(ctx, f.Owner, ref, id, Report{}); err != nil {
		t.Fatal(err)
	}
	abandoned := uuid.New()
	if _, err := f.Env.Owner.Exec(ctx, "UPDATE agent_run_settlement SET delivery_id=$2, lease_expires_at=now()+interval '1 minute' WHERE agent_run_id=$1", id, abandoned); err != nil {
		t.Fatal(err)
	}
	calls := 0
	f.T.OnSettlement = func(context.Context, Settlement) error { calls++; return nil }
	if err := f.T.RecoverSettlements(ctx); err != nil {
		t.Fatal(err)
	}
	if calls != 0 {
		t.Fatal("an unexpired lease was stolen")
	}
	if _, err := f.Env.Owner.Exec(ctx, "UPDATE agent_run_settlement SET lease_expires_at=now()-interval '1 second' WHERE agent_run_id=$1", id); err != nil {
		t.Fatal(err)
	}
	if err := f.T.RecoverSettlements(ctx); err != nil {
		t.Fatal(err)
	}
	if calls != 1 {
		t.Fatalf("expired lease calls %d", calls)
	}
	var delivered bool
	if err := f.Env.Owner.QueryRow(ctx, "SELECT delivered_at IS NOT NULL AND delivery_id IS NULL AND lease_expires_at IS NULL FROM agent_run_settlement WHERE agent_run_id=$1", id).Scan(&delivered); err != nil {
		t.Fatal(err)
	}
	if !delivered {
		t.Fatal("recovered delivery not acknowledged")
	}
}

func TestStaleSettlementConsumerCannotAcknowledgeOrReleaseNewLease(t *testing.T) {
	for _, failOld := range []bool{false, true} {
		name := "acknowledge"
		if failOld {
			name = "release"
		}
		t.Run(name, func(t *testing.T) {
			f := setup(t)
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			f.enqueue(t, 1, "claude")
			ref, id := f.claimStart(t, f.runtime(t, f.Owner, "d1", "claude"))
			f.T.OnSettled = nil
			if _, err := f.T.Complete(ctx, f.Owner, ref, id, Report{}); err != nil {
				t.Fatal(err)
			}
			enteredOld, finishOld := make(chan struct{}), make(chan struct{})
			enteredNew, finishNew := make(chan struct{}), make(chan struct{})
			old := &AgentRuns{Store: f.T.Store, OnSettlement: func(ctx context.Context, _ Settlement) error {
				close(enteredOld)
				select {
				case <-finishOld:
				case <-ctx.Done():
					return ctx.Err()
				}
				if failOld {
					return errors.New("old consumer failed")
				}
				return nil
			}}
			current := &AgentRuns{Store: f.T.Store, OnSettlement: func(ctx context.Context, _ Settlement) error {
				close(enteredNew)
				select {
				case <-finishNew:
					return nil
				case <-ctx.Done():
					return ctx.Err()
				}
			}}
			oldDone, newDone := make(chan error, 1), make(chan error, 1)
			go func() { oldDone <- old.RecoverSettlements(ctx) }()
			select {
			case <-enteredOld:
			case <-ctx.Done():
				t.Fatal(ctx.Err())
			}
			if _, err := f.Env.Owner.Exec(ctx, "UPDATE agent_run_settlement SET lease_expires_at=now()-interval '1 second' WHERE agent_run_id=$1", id); err != nil {
				t.Fatal(err)
			}
			go func() { newDone <- current.RecoverSettlements(ctx) }()
			select {
			case <-enteredNew:
			case <-ctx.Done():
				t.Fatal(ctx.Err())
			}
			var lease uuid.UUID
			if err := f.Env.Owner.QueryRow(ctx, "SELECT delivery_id FROM agent_run_settlement WHERE agent_run_id=$1", id).Scan(&lease); err != nil {
				t.Fatal(err)
			}
			close(finishOld)
			if err := <-oldDone; err == nil {
				t.Fatal("stale consumer did not report lost lease")
			}
			var intact bool
			if err := f.Env.Owner.QueryRow(ctx, "SELECT delivered_at IS NULL AND delivery_id=$2 AND lease_expires_at>now() FROM agent_run_settlement WHERE agent_run_id=$1", id, lease).Scan(&intact); err != nil {
				t.Fatal(err)
			}
			if !intact {
				t.Fatal("stale consumer modified the new delivery lease")
			}
			close(finishNew)
			if err := <-newDone; err != nil {
				t.Fatal(err)
			}
		})
	}
}

func TestCancelledTailBeforeUsageAndAttemptFence(t *testing.T) {
	f := setup(t)
	ctx := context.Background()
	f.enqueue(t, 1, "claude")
	ref := f.runtime(t, f.Owner, "tail", "claude")
	got, err := f.T.Claim(ctx, f.Owner, []RuntimeRef{ref}, 1)
	if err != nil || len(got) != 1 {
		t.Fatalf("claim %v %v", got, err)
	}
	id := got[0].AgentRun.ID
	if err = f.T.Start(ctx, f.Owner, ref, id); err != nil {
		t.Fatal(err)
	}
	if err = f.T.Cancel(ctx, f.WS, f.Owner, id); err != nil {
		t.Fatal(err)
	}
	ms := []Message{{Seq: 1, Type: "text", Content: []byte(`"final tail"`)}}
	for i := 0; i < 2; i++ {
		if err = f.T.Messages(ctx, f.Owner, ref, id, ms); err != nil {
			t.Fatalf("cancelled tail: %v", err)
		}
	}
	var count int
	if err = f.Env.Owner.QueryRow(ctx, "SELECT count(*) FROM agent_run_message WHERE agent_run_id=$1", id).Scan(&count); err != nil || count != 1 {
		t.Fatalf("count %d %v", count, err)
	}
	stale := ref
	stale.Attempt++
	if !errors.Is(f.T.Messages(ctx, f.Owner, stale, id, ms), ErrConflict) {
		t.Fatal("accepted wrong attempt")
	}
	if _, err = f.T.Complete(ctx, f.Owner, ref, id, Report{}); err != nil {
		t.Fatal(err)
	}
	if !errors.Is(f.T.Messages(ctx, f.Owner, ref, id, ms), ErrConflict) {
		t.Fatal("accepted tail after final usage")
	}
}
func TestDispatchedPreparationFailure(t *testing.T) {
	for _, reason := range []string{"agent_error", "runtime_offline"} {
		t.Run(reason, func(t *testing.T) {
			f := setup(t)
			ctx := context.Background()
			f.enqueue(t, 1, "claude")
			ref := f.runtime(t, f.Owner, "prepare", "claude")
			got, err := f.T.Claim(ctx, f.Owner, []RuntimeRef{ref}, 1)
			if err != nil || len(got) != 1 {
				t.Fatalf("claim %v %v", got, err)
			}
			id := got[0].AgentRun.ID
			if _, err = f.T.Complete(ctx, f.Owner, ref, id, Report{}); !errors.Is(err, ErrConflict) {
				t.Fatalf("prestart complete %v", err)
			}
			for _, r := range []Report{{FailureReason: reason, SessionID: "fabricated"}, {FailureReason: reason, Usage: []agent.Usage{{Model: "m", CostUSD: usd(1)}}}, {FailureReason: "provider_network"}} {
				if _, err = f.T.Fail(ctx, f.Owner, ref, id, r); err == nil {
					t.Fatalf("accepted invalid prestart report %+v", r)
				}
			}
			out, err := f.T.Fail(ctx, f.Owner, ref, id, Report{FailureReason: reason, Error: "setup failed"})
			if err != nil {
				t.Fatal(err)
			}
			want := "failed"
			if reason == "runtime_offline" {
				want = "queued"
			}
			if out.Status != want || out.CostUsd != 0 {
				t.Fatalf("result %+v", out)
			}
			if _, err = f.T.Fail(ctx, f.Owner, ref, id, Report{FailureReason: reason}); err != nil {
				t.Fatal("replay", err)
			}
		})
	}
}

// A workspace mutation owns the workspace row before emitting an event. Claim
// must wait for that row before taking the event lock, including implicit FK locks.
func TestRuntimeOperationsOrderWorkspaceBeforeEventLock(t *testing.T) {
	for _, operation := range []string{"claim", "register"} {
		t.Run(operation, func(t *testing.T) {
			f := setup(t)
			f.enqueue(t, 1, "claude")
			ref := f.runtime(t, f.Owner, "lock-order", "claude")
			ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
			defer cancel()
			mutation, err := f.Env.Owner.Begin(ctx)
			if err != nil {
				t.Fatal(err)
			}
			defer mutation.Rollback(context.Background())
			if _, err := mutation.Exec(ctx, "SELECT id FROM workspace WHERE id=$1 FOR UPDATE", f.WS); err != nil {
				t.Fatal(err)
			}
			done := make(chan error, 1)
			go func() {
				var err error
				if operation == "claim" {
					got, claimErr := f.T.Claim(ctx, f.Owner, []RuntimeRef{ref}, 1)
					err = claimErr
					if err == nil && len(got) != 1 {
						err = errors.New("claim did not return its dispatch")
					}
				} else {
					_, err = f.T.Register(ctx, f.Owner, f.WS, "lock-order", "updated", []string{"claude"}, nil)
				}
				done <- err
			}()
			ticker := time.NewTicker(10 * time.Millisecond)
			defer ticker.Stop()
			for {
				var blocked bool
				err = f.Env.Owner.QueryRow(ctx, "SELECT EXISTS (SELECT 1 FROM pg_stat_activity WHERE datname=current_database() AND $1::int=ANY(pg_blocking_pids(pid)))", mutation.Conn().PgConn().PID()).Scan(&blocked)
				if err != nil {
					t.Fatal(err)
				}
				if blocked {
					break
				}
				select {
				case err := <-done:
					t.Fatalf("operation completed before workspace mutation: %v", err)
				case <-ctx.Done():
					t.Fatal(ctx.Err())
				case <-ticker.C:
				}
			}
			var available bool
			if err := mutation.QueryRow(ctx, "SELECT pg_try_advisory_xact_lock(hashtextextended($1::uuid::text,0))", f.WS).Scan(&available); err != nil {
				t.Fatal(err)
			}
			if !available {
				t.Fatal("runtime operation holds event lock while waiting for workspace mutation")
			}
			if err := mutation.Commit(ctx); err != nil {
				t.Fatal(err)
			}
			if err := <-done; err != nil {
				t.Fatal(err)
			}
		})
	}
}

// Existing event writers take the advisory lock before their INSERT needs a
// workspace FK KEY SHARE lock. Runtime operations must not invert that order.
func TestRuntimeOperationsCompatibleWithReportWorkspaceLocks(t *testing.T) {
	for _, operation := range []string{"claim", "register"} {
		t.Run(operation, func(t *testing.T) {
			f := setup(t)
			f.enqueue(t, 1, "claude")
			ref := f.runtime(t, f.Owner, "report-lock", "claude")
			ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
			defer cancel()
			report, err := f.Env.Owner.Begin(ctx)
			if err != nil {
				t.Fatal(err)
			}
			defer report.Rollback(context.Background())
			if _, err := report.Exec(ctx, "SELECT pg_advisory_xact_lock(hashtextextended($1::uuid::text,0))", f.WS); err != nil {
				t.Fatal(err)
			}
			done := make(chan error, 1)
			go func() {
				var err error
				if operation == "claim" {
					got, claimErr := f.T.Claim(ctx, f.Owner, []RuntimeRef{ref}, 1)
					err = claimErr
					if err == nil && len(got) != 1 {
						err = errors.New("claim did not return its dispatch")
					}
				} else {
					_, err = f.T.Register(ctx, f.Owner, f.WS, "report-lock", "updated", []string{"claude"}, nil)
				}
				done <- err
			}()
			ticker := time.NewTicker(10 * time.Millisecond)
			defer ticker.Stop()
			for {
				var blocked bool
				err = f.Env.Owner.QueryRow(ctx, "SELECT EXISTS (SELECT 1 FROM pg_stat_activity WHERE datname=current_database() AND $1::int=ANY(pg_blocking_pids(pid)))", report.Conn().PgConn().PID()).Scan(&blocked)
				if err != nil {
					t.Fatal(err)
				}
				if blocked {
					break
				}
				select {
				case err := <-done:
					t.Fatalf("operation completed before report event lock released: %v", err)
				case <-ctx.Done():
					t.Fatal(ctx.Err())
				case <-ticker.C:
				}
			}
			// Claim/Register demonstrably waits on our event lock. Its workspace lock
			// must permit the FK check needed to finish and release this transaction.
			if _, err := report.Exec(ctx, "SELECT id FROM workspace WHERE id=$1 FOR KEY SHARE NOWAIT", f.WS); err != nil {
				t.Fatalf("event writer cannot acquire workspace FK lock: %v", err)
			}
			if err := report.Commit(ctx); err != nil {
				t.Fatal(err)
			}
			if err := <-done; err != nil {
				t.Fatal(err)
			}
		})
	}
}
