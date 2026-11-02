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
	want := "laptop keeps failing to run Claude Code (provider_network). Waiting studies continue once it recovers."
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
	if got, err := f.T.Complete(ctx, f.Owner, ref, id, Report{}); err != nil || got.Status != "completed" || got.InfraFailures != 0 {
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
	if f.settledCount() != 0 {
		t.Fatal("a breached run settled")
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
