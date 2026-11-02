package service

import (
	"context"
	"testing"
	"time"

	"github.com/google/uuid"

	"lykeion/server/internal/store"
	db "lykeion/server/pkg/db/generated"
)

type runInfra struct {
	Status        string
	Charged       int32
	InfraFailures int32
	Wait          *time.Duration
}

func (f *fixture) infraState(t *testing.T, run uuid.UUID) runInfra {
	t.Helper()
	var s runInfra
	var wait *float64
	if err := f.Env.Owner.QueryRow(context.Background(),
		"SELECT status, attempt - infra_failures, infra_failures, extract(epoch FROM retry_after - now())::float8 FROM agent_run WHERE id = $1", run).
		Scan(&s.Status, &s.Charged, &s.InfraFailures, &wait); err != nil {
		t.Fatal(err)
	}
	if wait != nil {
		d := time.Duration(*wait * float64(time.Second))
		s.Wait = &d
	}
	return s
}

func (f *fixture) unhealthy(t *testing.T, runtime uuid.UUID) string {
	t.Helper()
	var reason *string
	if err := f.Env.Owner.QueryRow(context.Background(), "SELECT unhealthy_reason FROM runtime WHERE id = $1", runtime).Scan(&reason); err != nil {
		t.Fatal(err)
	}
	if reason == nil {
		return ""
	}
	return *reason
}

type waitItem struct {
	User  uuid.UUID
	Cause string
	Text  string
}

func (f *fixture) openWaits(t *testing.T, runtime uuid.UUID) []waitItem {
	t.Helper()
	rows, err := f.Env.Owner.Query(context.Background(),
		"SELECT user_id, cause, text FROM inbox_item WHERE runtime_id = $1 AND kind = 'waiting_for_machine' AND resolved_at IS NULL", runtime)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	var out []waitItem
	for rows.Next() {
		var w waitItem
		if err := rows.Scan(&w.User, &w.Cause, &w.Text); err != nil {
			t.Fatal(err)
		}
		out = append(out, w)
	}
	return out
}

// clearBackoff lets the next claim take a parked run without waiting.
func (f *fixture) clearBackoff(t *testing.T) {
	t.Helper()
	if _, err := f.Env.Owner.Exec(context.Background(), "UPDATE agent_run SET retry_after = NULL WHERE workspace_id = $1", f.WS); err != nil {
		t.Fatal(err)
	}
}

func TestInfraFailureRequeuesWithoutChargingAnAttempt(t *testing.T) {
	f := setup(t)
	ctx := context.Background()
	f.enqueue(t, 1, "claude")
	ref, id := f.claimStart(t, f.runtime(t, f.Owner, "d1", "claude"))
	got, err := f.T.Fail(ctx, f.Owner, ref, id, Report{Error: "not logged in", FailureReason: "sign_in"})
	if err != nil || got.Status != "queued" {
		t.Fatalf("%+v %v", got, err)
	}
	s := f.infraState(t, id)
	if s.Status != "queued" || s.Charged != 0 || s.InfraFailures != 1 || s.Wait == nil || *s.Wait <= 0 {
		t.Fatalf("run %+v", s)
	}
	if r := f.unhealthy(t, ref.RuntimeID); r != "sign_in" {
		t.Fatalf("unhealthy %q", r)
	}
	want := "Claude Code on laptop is signed out. Sign in again on that machine (run `claude login`); waiting studies continue by themselves."
	if w := f.openWaits(t, ref.RuntimeID); len(w) != 1 || w[0].User != f.Owner || w[0].Cause != "sign_in" || w[0].Text != want {
		t.Fatalf("items %+v", w)
	}
	// The settlement hook is the only way into the flow engine's afterRun.
	if f.settledCount() != 0 {
		t.Fatalf("a parked run settled %d times", f.settledCount())
	}
}

func TestFiveStudiesOneSignedOutMachineOneInboxItem(t *testing.T) {
	f := setup(t)
	ctx := context.Background()
	err := f.T.Store.InWorkspace(ctx, f.WS, f.Owner, func(tx *store.Tx) error {
		cards := []uuid.UUID{f.Card}
		for n := int32(2); n <= 5; n++ {
			c, err := tx.Q.CreateCard(ctx, db.CreateCardParams{BoardColumn: "grounding", WorkspaceID: f.WS, Number: n, Title: "t", Question: "q", OwnerID: f.Owner})
			if err != nil {
				return err
			}
			cards = append(cards, c.ID)
		}
		for _, c := range cards {
			if _, err := f.T.Enqueue(ctx, tx, f.Owner, NewAgentRun{Card: c, Column: "grounding", Round: 1, Role: "scoper", Harness: "codex", Prompt: "p", MaxAttempts: 2}); err != nil {
				return err
			}
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	ref := f.runtime(t, f.Owner, "d1", "codex")
	if _, err := f.Env.Owner.Exec(ctx, "UPDATE runtime SET max_concurrent = 10, custom_name = 'Lab Mac' WHERE id = $1", ref.RuntimeID); err != nil {
		t.Fatal(err)
	}
	claimed, err := f.T.Claim(ctx, f.Owner, []RuntimeRef{ref}, 5)
	if err != nil || len(claimed) != 5 {
		t.Fatalf("claim %d %v", len(claimed), err)
	}
	for _, c := range claimed {
		r := ref
		r.Attempt = c.AgentRun.Attempt
		if err := f.T.Start(ctx, f.Owner, r, c.AgentRun.ID); err != nil {
			t.Fatal(err)
		}
		if got, err := f.T.Fail(ctx, f.Owner, r, c.AgentRun.ID, Report{FailureReason: "sign_in"}); err != nil || got.Status != "queued" {
			t.Fatalf("%+v %v", got, err)
		}
	}
	w := f.openWaits(t, ref.RuntimeID)
	want := "Codex on Lab Mac is signed out. Sign in again on that machine (run `codex login`); waiting studies continue by themselves."
	if len(w) != 1 || w[0].Text != want {
		t.Fatalf("items %+v", w)
	}
	if err := f.T.MarkHealthy(ctx, f.Owner, ref); err != nil {
		t.Fatal(err)
	}
	if r := f.unhealthy(t, ref.RuntimeID); r != "" || len(f.openWaits(t, ref.RuntimeID)) != 0 {
		t.Fatalf("after MarkHealthy: %q %+v", r, f.openWaits(t, ref.RuntimeID))
	}
}

func TestBackoffSchedule(t *testing.T) {
	f := setup(t)
	ctx := context.Background()
	f.enqueue(t, 1, "claude")
	runtime := f.runtime(t, f.Owner, "d1", "claude")
	for n, want := range []time.Duration{30 * time.Second, 2 * time.Minute, 10 * time.Minute} {
		ref, id := f.claimStart(t, runtime)
		if _, err := f.T.Fail(ctx, f.Owner, ref, id, Report{FailureReason: "provider_network"}); err != nil {
			t.Fatal(err)
		}
		s := f.infraState(t, id)
		// The run is never failed, however many attempts its limit allows.
		if s.Status != "queued" || s.Charged != 0 || s.Wait == nil || (*s.Wait-want).Abs() > 5*time.Second {
			t.Fatalf("failure %d: %+v, want wait %s", n+1, s, want)
		}
		items := len(f.openWaits(t, runtime.RuntimeID))
		if n < 2 && (items != 0 || f.unhealthy(t, runtime.RuntimeID) != "") {
			t.Fatalf("failure %d raised the machine early", n+1)
		}
		f.clearBackoff(t)
	}
	if r := f.unhealthy(t, runtime.RuntimeID); r != "provider_network" {
		t.Fatalf("unhealthy %q", r)
	}
	want := "Claude Code on laptop cannot reach its provider. Waiting studies continue once the connection is back."
	if w := f.openWaits(t, runtime.RuntimeID); len(w) != 1 || w[0].Text != want {
		t.Fatalf("items %+v", w)
	}
	// A success ends the streak; the machine stays flagged until MarkHealthy.
	if got, err := f.T.Claim(ctx, f.Owner, []RuntimeRef{runtime}, 1); err != nil || len(got) != 0 {
		t.Fatalf("an unhealthy machine claimed: %d %v", len(got), err)
	}
	if err := f.T.MarkHealthy(ctx, f.Owner, runtime); err != nil {
		t.Fatal(err)
	}
	ref, id := f.claimStart(t, runtime)
	if got, err := f.T.Complete(ctx, f.Owner, ref, id, Report{}); err != nil || got.Status != "completed" {
		t.Fatalf("%+v %v", got, err)
	}
}

func TestSecondFenceBreachRaisesTheItem(t *testing.T) {
	f := setup(t)
	ctx := context.Background()
	f.enqueue(t, 1, "claude")
	runtime := f.runtime(t, f.Owner, "d1", "claude")
	breach := Report{FailureReason: "fence_breach", Error: "used mcp__github__search, which is outside Lykeion's sources"}
	ref, id := f.claimStart(t, runtime)
	if got, err := f.T.Fail(ctx, f.Owner, ref, id, breach); err != nil || got.Status != "queued" {
		t.Fatalf("%+v %v", got, err)
	}
	if len(f.openWaits(t, runtime.RuntimeID)) != 0 || f.unhealthy(t, runtime.RuntimeID) != "" {
		t.Fatal("the first breach raised the machine")
	}
	f.clearBackoff(t)
	ref, id = f.claimStart(t, runtime)
	if got, err := f.T.Fail(ctx, f.Owner, ref, id, breach); err != nil || got.Status != "queued" {
		t.Fatalf("%+v %v", got, err)
	}
	want := "A run on laptop used mcp__github__search, which is outside Lykeion's sources, so its work was discarded. Check Claude Code's apps and plugins on that machine."
	if w := f.openWaits(t, runtime.RuntimeID); len(w) != 1 || w[0].Cause != "fence_breach" || w[0].Text != want {
		t.Fatalf("items %+v", w)
	}
	if r := f.unhealthy(t, runtime.RuntimeID); r != "fence_breach" {
		t.Fatalf("unhealthy %q", r)
	}
	if f.settledCount() != 0 {
		t.Fatal("a breached run settled")
	}
	// The run is held: not even another healthy machine takes it.
	other := f.runtime(t, f.Owner, "d2", "claude")
	if got, err := f.T.Claim(ctx, f.Owner, []RuntimeRef{other}, 1); err != nil || len(got) != 0 {
		t.Fatalf("a held run was claimed: %d %v", len(got), err)
	}
	if err := f.T.MarkHealthy(ctx, f.Owner, runtime); err != nil {
		t.Fatal(err)
	}
	if again, _ := f.claimStart(t, runtime); again.Attempt != 3 {
		t.Fatalf("released run claimed as attempt %d", again.Attempt)
	}
}

func TestInterveningFailureResetsBreachDetection(t *testing.T) {
	f := setup(t)
	ctx := context.Background()
	f.enqueue(t, 1, "claude")
	runtime := f.runtime(t, f.Owner, "d1", "claude")
	breach := Report{FailureReason: "fence_breach", Error: "used WebFetch, which is outside Lykeion's sources"}
	for _, r := range []Report{breach, {FailureReason: "provider_network"}, breach} {
		ref, id := f.claimStart(t, runtime)
		if _, err := f.T.Fail(ctx, f.Owner, ref, id, r); err != nil {
			t.Fatal(err)
		}
		f.clearBackoff(t)
	}
	if len(f.openWaits(t, runtime.RuntimeID)) != 0 || f.unhealthy(t, runtime.RuntimeID) != "" {
		t.Fatal("breaches with a different failure between them raised the machine")
	}
}

// failOnce claims the oldest claimable run on ref and fails it with reason.
func (f *fixture) failOnce(t *testing.T, ref RuntimeRef, reason string) {
	t.Helper()
	ref, id := f.claimStart(t, ref)
	if got, err := f.T.Fail(context.Background(), f.Owner, ref, id, Report{FailureReason: reason}); err != nil || got.Status != "queued" {
		t.Fatalf("%+v %v", got, err)
	}
}

func TestThreeRunsFailingOnceFlagTheRuntime(t *testing.T) {
	f := setup(t)
	f.enqueue(t, 3, "claude")
	runtime := f.runtime(t, f.Owner, "d1", "claude")
	for n := range 3 {
		if f.unhealthy(t, runtime.RuntimeID) != "" {
			t.Fatalf("flagged after %d failures", n)
		}
		f.failOnce(t, runtime, "timeout")
	}
	want := "Runs of Claude Code on laptop keep running out of time. Waiting studies continue once it recovers."
	if w := f.openWaits(t, runtime.RuntimeID); f.unhealthy(t, runtime.RuntimeID) != "timeout" || len(w) != 1 || w[0].Text != want {
		t.Fatalf("items %+v", w)
	}
}

func TestTheStreakBelongsToTheRuntime(t *testing.T) {
	f := setup(t)
	f.enqueue(t, 1, "claude")
	a, b := f.runtime(t, f.Owner, "a", "claude"), f.runtime(t, f.Owner, "b", "claude")
	// Only one runtime is online at a time, so each claim lands where intended.
	for _, ref := range []RuntimeRef{a, a, b} {
		other := b
		if ref == b {
			other = a
		}
		if _, err := f.Env.Owner.Exec(context.Background(), "UPDATE runtime SET status = CASE WHEN id = $1 THEN 'online' ELSE 'offline' END WHERE id IN ($1, $2)", ref.RuntimeID, other.RuntimeID); err != nil {
			t.Fatal(err)
		}
		f.failOnce(t, ref, "provider_network")
		f.clearBackoff(t)
	}
	if f.unhealthy(t, a.RuntimeID) != "" || f.unhealthy(t, b.RuntimeID) != "" || len(f.openWaits(t, b.RuntimeID)) != 0 {
		t.Fatal("a run's failures on another machine flagged this one")
	}
}

func TestSuccessResetsTheRuntimeStreak(t *testing.T) {
	f := setup(t)
	ctx := context.Background()
	f.enqueue(t, 1, "claude")
	runtime := f.runtime(t, f.Owner, "d1", "claude")
	f.failOnce(t, runtime, "provider_network")
	f.clearBackoff(t)
	f.failOnce(t, runtime, "provider_network")
	f.clearBackoff(t)
	ref, id := f.claimStart(t, runtime)
	if _, err := f.T.Complete(ctx, f.Owner, ref, id, Report{}); err != nil {
		t.Fatal(err)
	}
	f.enqueue(t, 1, "claude")
	f.failOnce(t, runtime, "provider_network")
	var streak int
	if err := f.Env.Owner.QueryRow(ctx, "SELECT infra_streak FROM runtime WHERE id = $1", runtime.RuntimeID).Scan(&streak); err != nil {
		t.Fatal(err)
	}
	if streak != 1 || f.unhealthy(t, runtime.RuntimeID) != "" {
		t.Fatalf("streak %d after a success and one failure", streak)
	}
}

// A machine that went offline already shows as offline: its lost attempts
// raise no item and never flag it.
func TestLostAttemptsDoNotFlagAnOfflineMachine(t *testing.T) {
	f := setup(t)
	ctx := context.Background()
	f.enqueue(t, 1, "claude")
	runtime := f.runtime(t, f.Owner, "d1", "claude")
	for range 3 {
		if _, err := f.T.Heartbeat(ctx, f.Owner, []RuntimeRef{runtime}, nil); err != nil {
			t.Fatal(err)
		}
		f.clearBackoff(t)
		f.claimStart(t, runtime)
		if _, err := f.Env.Owner.Exec(ctx, "UPDATE runtime SET last_seen_at = now() - interval '5 minutes' WHERE id = $1", runtime.RuntimeID); err != nil {
			t.Fatal(err)
		}
		if err := f.T.Sweep(ctx); err != nil {
			t.Fatal(err)
		}
	}
	if w := f.openWaits(t, runtime.RuntimeID); len(w) != 0 || f.unhealthy(t, runtime.RuntimeID) != "" {
		t.Fatalf("items %+v, unhealthy %q", w, f.unhealthy(t, runtime.RuntimeID))
	}
}

func TestARunWithNoStudyRetriesThenFails(t *testing.T) {
	f := setup(t)
	ctx := context.Background()
	run := f.enqueueAutoresearch(t, f.Owner, "claude")
	runtime := f.runtime(t, f.Owner, "d1", "claude")
	for n, want := range []string{"queued", "failed"} {
		// No backoff stands between the retries.
		ref, id := f.claimStart(t, runtime)
		got, err := f.T.Fail(ctx, f.Owner, ref, id, Report{FailureReason: "sign_in"})
		if err != nil || id != run.ID || got.Status != want || got.RetryAfter != nil || got.InfraFailures != 0 {
			t.Fatalf("attempt %d: %+v %v", n+1, got, err)
		}
	}
	if f.settledCount() != 1 || f.unhealthy(t, runtime.RuntimeID) != "" || len(f.openWaits(t, runtime.RuntimeID)) != 0 {
		t.Fatal("a run with no study touched the machine or did not settle")
	}
}

func TestAgentErrorStillFailsAsToday(t *testing.T) {
	f := setup(t)
	ctx := context.Background()
	f.enqueue(t, 1, "claude")
	ref, id := f.claimStart(t, f.runtime(t, f.Owner, "d1", "claude"))
	got, err := f.T.Fail(ctx, f.Owner, ref, id, Report{Error: "boom", FailureReason: "agent_error"})
	if err != nil || got.Status != "failed" || reasonOf(got) != "agent_error" {
		t.Fatalf("%+v %v", got, err)
	}
	if s := f.infraState(t, id); s.InfraFailures != 0 || s.Wait != nil {
		t.Fatalf("run %+v", s)
	}
	if f.settledCount() != 1 || len(f.openWaits(t, ref.RuntimeID)) != 0 || f.unhealthy(t, ref.RuntimeID) != "" {
		t.Fatal("an agent error touched the machine or skipped settlement")
	}
}

type decisionItem struct {
	User uuid.UUID
	Card uuid.UUID
	Text string
}

func (f *fixture) openDecisions(t *testing.T) []decisionItem {
	t.Helper()
	rows, err := f.Env.Owner.Query(context.Background(),
		"SELECT user_id, card_id, text FROM inbox_item WHERE workspace_id = $1 AND kind = 'needs_decision' AND resolved_at IS NULL", f.WS)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	var out []decisionItem
	for rows.Next() {
		var d decisionItem
		if err := rows.Scan(&d.User, &d.Card, &d.Text); err != nil {
			t.Fatal(err)
		}
		out = append(out, d)
	}
	return out
}

// A study's run that its machines keep failing is not retried forever: the
// fifth park holds it, whatever passes its machine made in between, and its
// owner is asked once, in plain words, what to do. Resolving that item lets
// the run try again.
func TestTheFifthParkHoldsTheRunAndAsksItsOwner(t *testing.T) {
	f := setup(t)
	ctx := context.Background()
	f.enqueue(t, 1, "claude")
	runtime := f.runtime(t, f.Owner, "d1", "claude")
	var id uuid.UUID
	for n := 1; n <= 5; n++ {
		var ref RuntimeRef
		ref, id = f.claimStart(t, runtime)
		if got, err := f.T.Fail(ctx, f.Owner, ref, id, Report{FailureReason: "provider_network", Error: "connect ECONNRESET"}); err != nil || got.Status != "queued" {
			t.Fatalf("park %d: %+v %v", n, got, err)
		}
		if n < 5 {
			if d := f.openDecisions(t); len(d) != 0 {
				t.Fatalf("park %d asked the owner: %+v", n, d)
			}
			// A machine pass in between doesn't reset the run's count.
			if err := f.T.MarkHealthy(ctx, f.Owner, runtime); err != nil {
				t.Fatal(err)
			}
			f.clearBackoff(t)
		}
	}
	if s := f.infraState(t, id); s.Status != "queued" || s.Wait == nil || *s.Wait < 24*time.Hour {
		t.Fatalf("the fifth park did not hold the run: %+v", s)
	}
	want := "Study #1 “t” has stopped trying: its run failed 5 times for reasons outside the agent's work, last because the agent's provider could not be reached (connect ECONNRESET). Resolve this item to try again."
	d := f.openDecisions(t)
	if len(d) != 1 || d[0].User != f.Owner || d[0].Card != f.Card || d[0].Text != want {
		t.Fatalf("items %+v", d)
	}
	// Neither a machine pass nor another healthy machine takes the held run.
	if err := f.T.MarkHealthy(ctx, f.Owner, runtime); err != nil {
		t.Fatal(err)
	}
	other := f.runtime(t, f.Owner, "d2", "claude")
	if got, err := f.T.Claim(ctx, f.Owner, []RuntimeRef{runtime, other}, 1); err != nil || len(got) != 0 {
		t.Fatalf("a held run was claimed: %d %v", len(got), err)
	}
	err := f.T.Store.InWorkspace(ctx, f.WS, f.Owner, func(tx *store.Tx) error { return ReleaseParkedRuns(ctx, tx, f.Card) })
	if err != nil {
		t.Fatal(err)
	}
	if s := f.infraState(t, id); s.InfraFailures != 0 || s.Wait != nil {
		t.Fatalf("released run %+v", s)
	}
	if again, _ := f.claimStart(t, runtime); again.Attempt != 6 {
		t.Fatalf("released run claimed as attempt %d", again.Attempt)
	}
}

// A preflight report takes the workspace's locks in the order every other
// writer does: a writer holding them and then locking the runtime row, as a
// skill claim does, must not deadlock with it.
func TestHealthReportsTakeLocksInWorkspaceOrder(t *testing.T) {
	for name, report := range map[string]func(*fixture, RuntimeRef) error{
		"unhealthy": func(f *fixture, ref RuntimeRef) error {
			return f.T.MarkUnhealthy(context.Background(), f.Owner, ref, "cli_too_old")
		},
		"healthy": func(f *fixture, ref RuntimeRef) error { return f.T.MarkHealthy(context.Background(), f.Owner, ref) },
		"passed":  func(f *fixture, ref RuntimeRef) error { return f.T.MarkPassed(context.Background(), f.Owner, ref) },
	} {
		t.Run(name, func(t *testing.T) {
			f := setup(t)
			ctx := context.Background()
			ref := f.runtime(t, f.Owner, "d1", "claude")
			// A flagged runtime, so that every report changes it and emits.
			if err := f.T.MarkUnhealthy(ctx, f.Owner, ref, "sandbox"); err != nil {
				t.Fatal(err)
			}
			reported := make(chan error, 1)
			err := f.T.Store.InWorkspace(ctx, f.WS, f.Owner, func(tx *store.Tx) error {
				if _, err := tx.Q.LockWorkspaceExecution(ctx, f.WS); err != nil {
					return err
				}
				if err := tx.Q.LockWorkspaceEvents(ctx, f.WS); err != nil {
					return err
				}
				go func() { reported <- report(f, ref) }()
				// Wait until the report is blocked on a lock this
				// transaction holds, then reach for the runtime row.
				deadline := time.Now().Add(10 * time.Second)
				for {
					var waiting int
					if err := f.Env.Owner.QueryRow(ctx, "SELECT count(*) FROM pg_stat_activity WHERE datname = current_database() AND wait_event_type = 'Lock'").Scan(&waiting); err != nil {
						return err
					}
					if waiting > 0 {
						break
					}
					if time.Now().After(deadline) {
						t.Fatal("the report never waited for the workspace's locks")
					}
					time.Sleep(20 * time.Millisecond)
				}
				_, err := tx.Q.LockRuntime(ctx, db.LockRuntimeParams{WorkspaceID: f.WS, ID: ref.RuntimeID})
				return err
			})
			if err != nil {
				t.Fatalf("lock holder: %v", err)
			}
			if err := <-reported; err != nil {
				t.Fatalf("report: %v", err)
			}
		})
	}
}
