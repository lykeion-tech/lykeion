package service

import (
	"context"
	"testing"

	"lykeion/server/internal/store"
)

// A leader waiting on its member must not hold the runtime's only slot, or
// the member it waits for never starts.
func TestWaitingLeaderFreesItsSlot(t *testing.T) {
	f := setup(t)
	ctx := context.Background()
	ref := f.runtime(t, f.Owner, "d1", "claude")
	if _, err := f.Env.Owner.Exec(ctx, "UPDATE runtime SET max_concurrent = 1 WHERE id = $1", ref.RuntimeID); err != nil {
		t.Fatal(err)
	}
	enqueue := func(role string) {
		t.Helper()
		err := f.T.Store.InWorkspace(ctx, f.WS, f.Owner, func(tx *store.Tx) error {
			_, err := f.T.Enqueue(ctx, tx, f.Owner, NewAgentRun{Card: f.Card, Column: "grounding", Round: 1, Role: role, Harness: "claude", Prompt: "p", MaxAttempts: 2})
			return err
		})
		if err != nil {
			t.Fatal(err)
		}
	}
	enqueue("leader")
	f.claimStart(t, ref)
	enqueue("auditor")
	if got, err := f.T.Claim(ctx, f.Owner, []RuntimeRef{ref}, 1); err != nil || len(got) != 0 {
		t.Fatalf("a busy leader's slot was given to the auditor: %v %+v", err, got)
	}
	if _, err := f.Env.Owner.Exec(ctx, "DELETE FROM agent_run WHERE role = 'auditor'"); err != nil {
		t.Fatal(err)
	}
	enqueue("scoper")
	got, err := f.T.Claim(ctx, f.Owner, []RuntimeRef{ref}, 1)
	if err != nil || len(got) != 1 || got[0].AgentRun.Role != "scoper" {
		t.Fatalf("the member was not claimed: %v %+v", err, got)
	}
	// The running member holds the slot itself.
	enqueue("scoper")
	if got, err := f.T.Claim(ctx, f.Owner, []RuntimeRef{ref}, 1); err != nil || len(got) != 0 {
		t.Fatalf("a second run took the member's slot: %v %+v", err, got)
	}
}
