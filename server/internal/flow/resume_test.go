package flow

import (
	"strings"
	"testing"

	"lykeion/server/internal/store"
	db "lykeion/server/pkg/db/generated"
)

func TestChangingTheMachineResumesTheCancelledStep(t *testing.T) {
	for _, role := range []string{"leader", "m1", "auditor"} {
		for _, reason := range []string{"runtime_access_revoked", "runtime_access_denied"} {
			t.Run(role+"/"+reason, func(t *testing.T) {
				l := newLab(t)
				c := l.study(t, "hypotheses", true, nil)
				// The fixture's actual member name is read from the manifest.
				actualRole := role
				if role == "m1" {
					actualRole = l.man.Members[0].Name
				}
				var terminal db.AgentRun
				l.in(t, func(tx *store.Tx) error {
					_, m, a, err := l.e.worker(t.Context(), tx, c)
					if err != nil {
						return err
					}
					if role == "m1" || role == "auditor" {
						member := actualRole
						if role == "auditor" {
							member = l.man.Auditor.Name
						}
						a, err = teamAgent(t.Context(), tx, l.pack.ID, l.owner, member)
						if err != nil {
							return err
						}
					}
					terminal, err = l.e.enqueue(t.Context(), tx, c, actualRole, m, a, "Original step")
					return err
				})
				status := "cancelled"
				var cancel any = reason
				var failure any
				if reason == "runtime_access_denied" {
					status = "failed"
					cancel = nil
					failure = reason
				}
				if _, err := l.env.Owner.Exec(t.Context(), "UPDATE agent_run SET status=$2,cancel_reason=$3,failure_reason=$4 WHERE id=$1", terminal.ID, status, cancel, failure); err != nil {
					t.Fatal(err)
				}
				terminal.Status = status
				if reason == "runtime_access_denied" {
					terminal.FailureReason = &reason
				} else {
					terminal.CancelReason = &reason
				}
				if err := l.e.afterRun(t.Context(), terminal); err != nil {
					t.Fatal(err)
				}
				if runs := l.runs(t, c.ID); len(runs) != 0 {
					t.Fatalf("access failure auto-woke %+v", runs)
				}
				l.in(t, func(tx *store.Tx) error { return l.e.Resume(t.Context(), tx, c) })
				runs := l.runs(t, c.ID)
				if len(runs) != 1 || runs[0].Role != actualRole || runs[0].Column != c.BoardColumn || runs[0].Scope != ContinuityScope(c.ID, actualRole, c.BoardColumn, c.Round) || !strings.Contains(runs[0].Prompt, "Resumed on another machine.") {
					t.Fatalf("resumed %+v", runs)
				}
				l.in(t, func(tx *store.Tx) error { return l.e.Resume(t.Context(), tx, c) })
				if runs := l.runs(t, c.ID); len(runs) != 1 {
					t.Fatalf("duplicate %+v", runs)
				}
			})
		}
	}
}
func TestChangingTheMachineLeavesARunningRunAlone(t *testing.T) {
	l := newLab(t)
	c := l.study(t, "hypotheses", true, nil)
	l.in(t, func(tx *store.Tx) error { return l.e.Start(t.Context(), tx, c) })
	if _, err := l.env.Owner.Exec(t.Context(), "UPDATE agent_run SET status='running' WHERE card_id=$1", c.ID); err != nil {
		t.Fatal(err)
	}
	l.in(t, func(tx *store.Tx) error { return l.e.Resume(t.Context(), tx, c) })
	if runs := l.runs(t, c.ID); len(runs) != 0 {
		t.Fatalf("extra run %+v", runs)
	}
}

func TestInterruptedAuditorWaitsForMachineAndItsReplacement(t *testing.T) {
	for _, reason := range []string{"runtime_access_revoked", "runtime_access_denied"} {
		t.Run(reason, func(t *testing.T) {
			l := newLab(t)
			j := l.jev(t, 200, func(string) float64 { return 4 })
			c := l.study(t, "hypotheses", true, nil)
			l.done(t, c.ID)
			status := "cancelled"
			var cancel any = reason
			var failure any
			if reason == "runtime_access_denied" {
				status = "failed"
				cancel = nil
				failure = reason
			}
			if _, err := l.env.Owner.Exec(t.Context(), "UPDATE agent_run SET status=$2,cancel_reason=$3,failure_reason=$4 WHERE card_id=$1 AND role='auditor'", c.ID, status, cancel, failure); err != nil {
				t.Fatal(err)
			}
			assertWaiting := func() {
				t.Helper()
				l.in(t, func(tx *store.Tx) error {
					runs, err := tx.Q.RoundAgentRuns(t.Context(), db.RoundAgentRunsParams{WorkspaceID: l.ws, CardID: c.ID, BoardColumn: c.BoardColumn, Round: c.Round})
					if err != nil {
						return err
					}
					ready, err := l.e.afterDone(t.Context(), tx, c, runs)
					if ready {
						t.Error("interrupted or queued auditor is ready to score")
					}
					return err
				})
				if err := l.e.Score(t.Context(), l.ws, c.ID); err != nil {
					t.Fatal(err)
				}
				if n := len(j.calls()); n != 0 {
					t.Fatalf("scored %d calls", n)
				}
			}
			assertWaiting()
			l.in(t, func(tx *store.Tx) error { return l.e.Resume(t.Context(), tx, c) })
			assertWaiting()
			if _, err := l.env.Owner.Exec(t.Context(), "UPDATE agent_run SET status='completed' WHERE card_id=$1 AND role='auditor' AND status='queued'", c.ID); err != nil {
				t.Fatal(err)
			}
			if err := l.e.Score(t.Context(), l.ws, c.ID); err != nil {
				t.Fatal(err)
			}
			if n := len(j.calls()); n == 0 {
				t.Fatal("completed replacement did not score")
			}
		})
	}
}

func TestResumeOnlyRestoresLatestAccessInterruptedWorkInCurrentRound(t *testing.T) {
	for _, tc := range []struct {
		name, status, reason string
		later                bool
		column               string
		round                int32
	}{
		{"ordinary cancellation", "cancelled", "", false, "hypotheses", 1},
		{"ordinary failure", "failed", "agent_error", false, "hypotheses", 1},
		{"completed successor", "cancelled", "runtime_access_revoked", true, "hypotheses", 1},
		{"previous round", "cancelled", "runtime_access_revoked", false, "hypotheses", 2},
		{"previous column", "cancelled", "runtime_access_revoked", false, "grounding", 1},
	} {
		t.Run(tc.name, func(t *testing.T) {
			l := newLab(t)
			c := l.study(t, "hypotheses", true, nil)
			var first db.AgentRun
			l.in(t, func(tx *store.Tx) error {
				_, m, a, err := l.e.worker(t.Context(), tx, c)
				if err != nil {
					return err
				}
				first, err = l.e.enqueue(t.Context(), tx, c, "leader", m, a, "")
				return err
			})
			var cancel, failure any
			if tc.reason != "" {
				if tc.status == "cancelled" {
					cancel = tc.reason
				} else {
					failure = tc.reason
				}
			}
			if _, err := l.env.Owner.Exec(t.Context(), "UPDATE agent_run SET status=$2,cancel_reason=$3,failure_reason=$4,board_column=$5,round=$6 WHERE id=$1", first.ID, tc.status, cancel, failure, tc.column, tc.round); err != nil {
				t.Fatal(err)
			}
			if tc.later {
				l.in(t, func(tx *store.Tx) error { return l.e.Start(t.Context(), tx, c) })
				if _, err := l.env.Owner.Exec(t.Context(), "UPDATE agent_run SET status='completed' WHERE card_id=$1 AND status='queued'", c.ID); err != nil {
					t.Fatal(err)
				}
			}
			l.in(t, func(tx *store.Tx) error { return l.e.Resume(t.Context(), tx, c) })
			if runs := l.runs(t, c.ID); len(runs) != 0 {
				t.Fatalf("unexpected resume %+v", runs)
			}
		})
	}
}
