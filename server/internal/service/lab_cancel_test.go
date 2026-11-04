package service

import (
	"testing"

	db "lykeion/server/pkg/db/generated"
)

// Startup reconciliation, status polling and hand-in all share this guard.
// Available CLIs must retain their run and bearer throughout the lifecycle.
func TestAvailableLabCancellationGuardPreservesLifecycle(t *testing.T) {
	for _, harness := range []string{"claude", "codex"} {
		for _, status := range []string{"queued", "dispatched", "running", "completed", "failed", "cancelled"} {
			t.Run(harness+"/"+status, func(t *testing.T) {
				runs := &AgentRuns{}
				// A nil transaction deliberately fails if the guard attempts a mutation.
				cancelled, err := runs.CancelUnavailableLabInTx(t.Context(), nil, db.AgentRun{Kind: "aristotle", Harness: harness, Status: status})
				if err != nil || cancelled {
					t.Fatalf("available run was rejected: cancelled=%v err=%v", cancelled, err)
				}
			})
		}
	}
}
