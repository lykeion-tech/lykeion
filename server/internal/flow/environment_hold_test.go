package flow

import (
	"context"
	"encoding/json"
	"testing"

	"lykeion/server/internal/records"
	"lykeion/server/internal/service"
	"lykeion/server/internal/store"
	db "lykeion/server/pkg/db/generated"
)

func TestDelegationRecordsCurrentLeaderAttempt(t *testing.T) {
	l := newLab(t)
	c := l.study(t, "hypotheses", true, nil)
	l.e.Kick(l.ws, c.ID)
	lead := l.latestRun(t, c.ID, "leader")
	if _, err := l.env.Owner.Exec(t.Context(), "UPDATE agent_run SET attempt=2 WHERE id=$1", lead.ID); err != nil {
		t.Fatal(err)
	}
	l.in(t, func(tx *store.Tx) error {
		rec, _, err := l.e.Delegate(t.Context(), tx, service.Writer{Card: c.ID, AgentRun: lead.ID, Column: c.BoardColumn, Role: "leader", Round: c.Round}, "member-1", "Find evidence")
		if err != nil {
			return err
		}
		var body struct {
			LeaderAttempt int32 `json:"leader_attempt"`
		}
		if err := json.Unmarshal(rec.Body, &body); err != nil {
			return err
		}
		if body.LeaderAttempt != 2 {
			t.Errorf("delegation leader_attempt=%d, want 2", body.LeaderAttempt)
		}
		return nil
	})
}

func TestAttestedEnvironmentFailureHoldsWithoutDecision(t *testing.T) {
	l := newLab(t)
	c := l.study(t, "experimentation", true, nil)
	l.e.Kick(l.ws, c.ID)
	lead := l.latestRun(t, c.ID, "leader")
	if _, err := l.env.Owner.Exec(t.Context(), "UPDATE agent_run SET attempt=1 WHERE id=$1", lead.ID); err != nil {
		t.Fatal(err)
	}
	lead.Attempt = 1
	const why = "the Python environment isn't ready on this machine"
	l.in(t, func(tx *store.Tx) error {
		_, err := records.WriteAs(context.Background(), tx, service.Writer{Card: c.ID, AgentRun: lead.ID, Column: "experimentation", Role: "leader", Round: c.Round},
			"hand_in", map[string]any{"agent_run_id": lead.ID, "attempt": 1, "environment_failures": []string{"python"}, "hand_in": map[string]any{"status": "could_not_do", "answer": "", "reason": why, "blocked_by_environment": "python"}})
		return err
	})
	l.settle(t, lead, "completed", "", "")
	if got := l.card(t, c.ID); got.State != "blocked" || got.Round != c.Round {
		t.Fatalf("study did not stop in its current round: %+v", got)
	}
	if n := l.count(t, "SELECT count(*) FROM inbox_item WHERE card_id=$1 AND resolved_at IS NULL AND (jsonb_array_length(options)>0 OR question_set IS NOT NULL OR expires_at IS NOT NULL)", c.ID); n != 0 {
		t.Fatalf("environment failure created %d actionable or expiring decisions", n)
	}
	l.in(t, func(tx *store.Tx) error {
		text, err := tx.Q.CardHoldText(context.Background(), db.CardHoldTextParams{WorkspaceID: l.ws, CardID: &c.ID})
		if err != nil || text == "" {
			t.Errorf("missing recoverable hold reason: %q, %v", text, err)
		}
		return nil
	})
	l.e.OnComment(l.ws, c.ID, l.owner, "Python has been restored.")
	if got := l.card(t, c.ID); got.State != "working" || got.Round != c.Round+1 {
		t.Fatalf("comment failed to restart study: %+v", got)
	}
	if n := l.count(t, "SELECT count(*) FROM inbox_item WHERE card_id=$1 AND kind='waiting_for_machine' AND resolved_at IS NULL", c.ID); n != 0 {
		t.Fatalf("redo left %d operational holds open", n)
	}
}

func TestUnattestedEnvironmentClaimRemainsAResearchDecision(t *testing.T) {
	l := newLab(t)
	c := l.study(t, "experimentation", true, nil)
	l.e.Kick(l.ws, c.ID)
	lead := l.latestRun(t, c.ID, "leader")
	if _, err := l.env.Owner.Exec(t.Context(), "UPDATE agent_run SET attempt=1 WHERE id=$1", lead.ID); err != nil {
		t.Fatal(err)
	}
	lead.Attempt = 1
	l.in(t, func(tx *store.Tx) error {
		_, err := records.WriteAs(context.Background(), tx, service.Writer{Card: c.ID, AgentRun: lead.ID, Column: "experimentation", Role: "leader", Round: c.Round},
			"hand_in", map[string]any{"agent_run_id": lead.ID, "attempt": 1, "environment_failures": []string{"r"}, "hand_in": map[string]any{"status": "could_not_do", "reason": "missing data", "blocked_by_environment": "python"}})
		return err
	})
	l.settle(t, lead, "completed", "", "")
	if n := l.count(t, "SELECT count(*) FROM inbox_item WHERE card_id=$1 AND kind='needs_decision' AND resolved_at IS NULL", c.ID); n != 1 {
		t.Fatalf("unattested claim produced %d research decisions, want 1", n)
	}
}

func TestStaleEnvironmentHandInDoesNotHoldLaterAttempt(t *testing.T) {
	for _, tc := range []struct {
		name          string
		attempt       int32
		recordAttempt int32
		superseded    bool
	}{{"previous attempt", 2, 1, false}, {"superseded", 1, 1, true}, {"missing attempt", 1, 0, false}} {
		t.Run(tc.name, func(t *testing.T) {
			l := newLab(t)
			c := l.study(t, "experimentation", true, nil)
			l.e.Kick(l.ws, c.ID)
			lead := l.latestRun(t, c.ID, "leader")
			l.in(t, func(tx *store.Tx) error {
				_, err := records.WriteAs(context.Background(), tx, service.Writer{Card: c.ID, AgentRun: lead.ID, Column: "experimentation", Role: "leader", Round: c.Round},
					"hand_in", map[string]any{"agent_run_id": lead.ID, "attempt": tc.recordAttempt, "environment_failures": []string{"python"}, "hand_in": map[string]any{"status": "could_not_do", "reason": "Python unavailable", "blocked_by_environment": "python"}})
				return err
			})
			if tc.superseded {
				if _, err := l.env.Owner.Exec(t.Context(), "UPDATE record SET superseded_at=now() WHERE agent_run_id=$1 AND kind='hand_in'", lead.ID); err != nil {
					t.Fatal(err)
				}
			}
			if _, err := l.env.Owner.Exec(t.Context(), "UPDATE agent_run SET attempt=$2 WHERE id=$1", lead.ID, tc.attempt); err != nil {
				t.Fatal(err)
			}
			lead.Attempt = tc.attempt
			l.settle(t, lead, "completed", "", "")
			if got := l.card(t, c.ID); got.State != "working" {
				t.Fatalf("stale hand-in blocked current attempt: state=%s", got.State)
			}
			if n := l.count(t, "SELECT count(*) FROM inbox_item WHERE card_id=$1 AND resolved_at IS NULL AND kind IN ('waiting_for_machine','needs_decision')", c.ID); n != 0 {
				t.Fatalf("stale hand-in created %d holds or decisions", n)
			}
		})
	}
}

func TestPreviousAttemptSummaryDoesNotHideCurrentEnvironmentHold(t *testing.T) {
	l := newLab(t)
	c := l.study(t, "experimentation", true, nil)
	l.e.Kick(l.ws, c.ID)
	lead := l.latestRun(t, c.ID, "leader")
	lead.Attempt = 2
	if _, err := l.env.Owner.Exec(t.Context(), "UPDATE agent_run SET attempt=2 WHERE id=$1", lead.ID); err != nil {
		t.Fatal(err)
	}
	w := service.Writer{Card: c.ID, AgentRun: lead.ID, Column: "experimentation", Role: "leader", Round: c.Round}
	l.in(t, func(tx *store.Tx) error {
		_, err := records.WriteAs(t.Context(), tx, w, "hand_in", map[string]any{"agent_run_id": lead.ID, "attempt": 1, "hand_in": map[string]any{"status": "done"}})
		if err != nil {
			return err
		}
		_, err = records.WriteAs(t.Context(), tx, w, "summary", map[string]any{"text": "Earlier attempt"})
		if err != nil {
			return err
		}
		_, err = records.WriteAs(t.Context(), tx, w, "hand_in", map[string]any{"agent_run_id": lead.ID, "attempt": 2, "environment_failures": []string{"python"}, "hand_in": map[string]any{"status": "could_not_do", "reason": "Python unavailable", "blocked_by_environment": "python"}})
		return err
	})
	l.settle(t, lead, "completed", "", "")
	if got := l.card(t, c.ID); got.State != "blocked" {
		t.Fatalf("stale summary prevented current operational hold: state=%s", got.State)
	}
	if n := l.count(t, "SELECT count(*) FROM inbox_item WHERE card_id=$1 AND kind='waiting_for_machine' AND cause='environment_unavailable' AND resolved_at IS NULL", c.ID); n != 1 {
		t.Fatalf("got %d environment holds, want 1", n)
	}
}

func TestRunHandInRecognizesCurrentAndLegacySummaries(t *testing.T) {
	for _, tc := range []struct {
		name          string
		handInAttempt int32
		wantDone      bool
	}{{"current hand-in", 2, true}, {"only stale hand-in", 1, false}, {"standalone legacy summary", 0, true}} {
		t.Run(tc.name, func(t *testing.T) {
			l := newLab(t)
			c := l.study(t, "grounding", true, nil)
			l.e.Kick(l.ws, c.ID)
			lead := l.latestRun(t, c.ID, "leader")
			lead.Attempt = 2
			w := service.Writer{Card: c.ID, AgentRun: lead.ID, Column: c.BoardColumn, Role: "leader", Round: c.Round}
			l.in(t, func(tx *store.Tx) error {
				if tc.handInAttempt != 0 {
					if _, err := records.WriteAs(t.Context(), tx, w, "hand_in", map[string]any{"attempt": tc.handInAttempt, "hand_in": map[string]any{"status": "done"}}); err != nil {
						return err
					}
				}
				if _, err := records.WriteAs(t.Context(), tx, w, "summary", map[string]any{"text": "Evidence"}); err != nil {
					return err
				}
				_, done, err := runHandIn(t.Context(), tx, lead)
				if err == nil && done != tc.wantDone {
					t.Errorf("done=%v, want %v", done, tc.wantDone)
				}
				return err
			})
		})
	}
}
