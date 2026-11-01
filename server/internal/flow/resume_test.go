package flow

import (
	"strings"
	"testing"

	"github.com/google/uuid"

	"lykeion/server/internal/store"
	db "lykeion/server/pkg/db/generated"
)

func TestChangingTheMachineResumesTheCancelledStep(t *testing.T) {
	for _, role := range []string{"leader", "m1", "auditor", "agent"} {
		for _, reason := range []string{"runtime_access_revoked", "runtime_access_denied"} {
			t.Run(role+"/"+reason, func(t *testing.T) {
				l := newLab(t)
				var agent *uuid.UUID
				if role == "agent" {
					id := l.agent(t, "Solo", "claude", "", "Original solo instructions.")
					agent = &id
				}
				c := l.study(t, "hypotheses", role != "agent", agent)
				note := map[string]string{
					"leader":  "The member finished. Read record result-17 and name the next member.",
					"m1":      leaderStep + "Run uname; report Darwin and MAC_RESUME_OK in the result record.",
					"auditor": "Audit the leader summary: Darwin verified. Cited evidence: result-17, call-23.",
					"agent":   "Continue the original single-agent analysis using evidence-11.",
				}[role]
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
					terminal, err = l.e.enqueue(t.Context(), tx, c, actualRole, m, a, note)
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
				if _, err := l.env.Owner.Exec(t.Context(), "UPDATE agent_run SET status=$2,cancel_reason=$3,failure_reason=$4,skill='stale skill',session_id='stale-session',machine_name='old machine',profile_mode='private',policy_digest='old-policy' WHERE id=$1", terminal.ID, status, cancel, failure); err != nil {
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
				// A resumed run takes the owner's current harness/model/instructions,
				// while its research input remains the interrupted run's snapshot.
				if _, err := l.env.Owner.Exec(t.Context(), "UPDATE agent SET harness='codex',model='gpt-5.6-sol',instructions='Current instructions.' WHERE id=$1", terminal.AgentID); err != nil {
					t.Fatal(err)
				}
				c.Question = "The card was edited after the interruption."
				l.in(t, func(tx *store.Tx) error { return l.e.Resume(t.Context(), tx, c) })
				runs := l.runs(t, c.ID)
				if len(runs) != 1 || runs[0].Role != actualRole || runs[0].Column != c.BoardColumn || runs[0].Scope != ContinuityScope(c.ID, actualRole, c.BoardColumn, c.Round) || !strings.Contains(runs[0].Prompt, "Resumed on another machine.") {
					t.Fatalf("resumed %+v", runs)
				}
				r := resumedRun(t, l, c.ID)
				if r.Prompt != terminal.Prompt+"\nResumed on another machine.\n" || r.Assignment != terminal.Assignment {
					t.Errorf("interrupted input lost: prompt=%q assignment=%q; original prompt=%q assignment=%q", r.Prompt, r.Assignment, terminal.Prompt, terminal.Assignment)
				}
				if r.AgentID == nil || *r.AgentID != *terminal.AgentID || r.Harness != "codex" || r.RequestedModel != "gpt-5.6-sol" || !strings.HasPrefix(r.Skill, "Current instructions.\n\n") || !strings.Contains(r.Skill, "lyk record put") || strings.Contains(r.Skill, "stale skill") {
					t.Errorf("did not use current agent and protocol: %+v", r)
				}
				if r.Round != c.Round || r.CardID == nil || *r.CardID != c.ID || r.SessionID != "" || r.RuntimeID != nil || r.MachineName != nil || r.MachineOwnerID != nil || r.ProfileMode != "" || r.PolicyDigest != "" {
					t.Errorf("unexpected run identity or copied execution state: %+v", r)
				}
				l.in(t, func(tx *store.Tx) error { return l.e.Resume(t.Context(), tx, c) })
				if runs := l.runs(t, c.ID); len(runs) != 1 {
					t.Fatalf("duplicate %+v", runs)
				}
			})
		}
	}
}
func resumedRun(t *testing.T, l *lab, card uuid.UUID) db.AgentRun {
	t.Helper()
	var id uuid.UUID
	if err := l.env.Owner.QueryRow(t.Context(), "SELECT id FROM agent_run WHERE card_id=$1 AND status='queued'", card).Scan(&id); err != nil {
		t.Fatal(err)
	}
	var r db.AgentRun
	l.in(t, func(tx *store.Tx) (err error) {
		r, err = tx.Q.GetAgentRun(t.Context(), db.GetAgentRunParams{WorkspaceID: l.ws, ID: id})
		return err
	})
	return r
}

func TestResumeLegacyEmptyInputFallsBackToTheStudy(t *testing.T) {
	for _, tc := range []struct{ name, prompt, assignment string }{
		{"both empty", "", ""},
		{"prompt empty", "", "  Original legacy step.\n"},
		{"assignment empty", "Original prompt snapshot.", ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			l := newLab(t)
			c := l.study(t, "hypotheses", true, nil)
			l.in(t, func(tx *store.Tx) error { return l.e.Start(t.Context(), tx, c) })
			if _, err := l.env.Owner.Exec(t.Context(), "UPDATE agent_run SET status='cancelled',cancel_reason='runtime_access_revoked',prompt=$2,assignment=$3 WHERE card_id=$1", c.ID, tc.prompt, tc.assignment); err != nil {
				t.Fatal(err)
			}
			l.in(t, func(tx *store.Tx) error { return l.e.Resume(t.Context(), tx, c) })
			r := resumedRun(t, l, c.ID)
			wantAssignment := tc.assignment
			if wantAssignment == "" {
				wantAssignment = "t\n\nWhy do cells age?"
			}
			if r.Assignment != wantAssignment || !strings.Contains(r.Prompt, "Resumed on another machine.") {
				t.Fatalf("legacy input lost: prompt=%q assignment=%q", r.Prompt, r.Assignment)
			}
			if tc.prompt != "" {
				if r.Prompt != tc.prompt+"\nResumed on another machine.\n" {
					t.Errorf("legacy prompt changed: %q", r.Prompt)
				}
			} else if !strings.Contains(r.Prompt, "Why do cells age?") || tc.assignment != "" && !strings.Contains(r.Prompt, tc.assignment) {
				t.Errorf("legacy prompt lacks card/assignment context: %q", r.Prompt)
			}
		})
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
			l.legacy = true
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
