package flow

import (
	"encoding/json"
	"strings"
	"testing"

	"lykeion/server/internal/board"
	"lykeion/server/internal/review"
	"lykeion/server/internal/service"
	"lykeion/server/internal/store"
	"lykeion/server/pkg/agent"
	db "lykeion/server/pkg/db/generated"
)

func TestFrozenComparisonExperimentWaitsForDaemonUpgrade(t *testing.T) {
	l := newLab(t)
	ctx := t.Context()
	a := l.agent(t, "Lead", "claude", "", "")
	frozen := l.scoped(t, board.Experimentation, board.Validation, a)
	optional := l.scoped(t, board.Experimentation, board.Validation, a)
	h := review.HypothesisBody{
		Text: "Accuracy exceeds baseline", Measure: &review.Measure{ID: "accuracy", Name: "Accuracy", Unit: "fraction"},
		Prediction: review.Prediction{Shape: "outcomes", Outcomes: []string{"above", "below"}, Chosen: "above"},
		Control:    review.Control{Text: "baseline"}, Cites: []string{"r-source"},
	}
	threshold := json.Number("0.5")
	h.Comparison = &review.OutcomeComparison{Threshold: &threshold, Less: "below", Equal: "below", Greater: "above"}
	key := l.put(t, frozen.ID, board.Hypotheses, 1, review.KindHypothesis, "agent", h)
	l.put(t, frozen.ID, board.Hypotheses, 1, "review", "server", review.Result{
		Column: board.Hypotheses, Decision: review.DecidePass,
		Items: []review.Item{{Key: key, Kind: review.KindHypothesis, Status: review.Pass}},
	})
	l.put(t, frozen.ID, board.Hypotheses, 1, "outcome", "server", map[string]any{"verdict": review.DecidePass})
	h.Comparison = nil
	l.put(t, optional.ID, board.Hypotheses, 1, review.KindHypothesis, "agent", h)
	l.e.Kick(l.ws, frozen.ID)
	l.e.Kick(l.ws, optional.ID)

	runtimes, err := l.e.AgentRuns.Register(ctx, l.owner, l.ws, "daemon", "Laptop", []string{"claude"}, nil, agent.Platform{})
	if err != nil || len(runtimes) != 1 {
		t.Fatalf("register: %+v, %v", runtimes, err)
	}
	refs := []service.RuntimeRef{{WorkspaceID: l.ws, RuntimeID: runtimes[0].ID}}
	got, err := l.e.AgentRuns.Claim(ctx, l.owner, refs, 2)
	if err != nil || len(got) != 1 || got[0].AgentRun.CardID == nil || *got[0].AgentRun.CardID != optional.ID || got[0].OutputAttestation != "" {
		t.Fatalf("old daemon must claim only the ordinary experiment; got %d claims, %v", len(got), err)
	}
	l.in(t, func(tx *store.Tx) error {
		runs, err := tx.Q.CardAgentRuns(ctx, db.CardAgentRunsParams{WorkspaceID: l.ws, CardID: &frozen.ID})
		if err != nil {
			return err
		}
		if len(runs) != 1 {
			t.Fatalf("expected one frozen run, got %d", len(runs))
		}
		if runs[0].Status != "queued" || runs[0].Attempt != 0 || !strings.Contains(strings.ToLower(runs[0].Waiting), "upgrade") {
			t.Fatalf("frozen comparison should pause with upgrade guidance before an attempt: %+v", runs[0].Waiting)
		}
		return nil
	})
	if _, err := l.env.Owner.Exec(ctx, "UPDATE runtime SET status = 'offline' WHERE id = $1", runtimes[0].ID); err != nil {
		t.Fatal(err)
	}
	l.in(t, func(tx *store.Tx) error {
		runs, err := tx.Q.CardAgentRuns(ctx, db.CardAgentRunsParams{WorkspaceID: l.ws, CardID: &frozen.ID})
		if err != nil {
			return err
		}
		if len(runs) != 1 || !strings.Contains(runs[0].Waiting, "it is offline") {
			t.Fatal("the known machine status must take precedence")
		}
		cards, err := tx.Q.ListCards(ctx, l.ws)
		if err != nil {
			return err
		}
		for _, c := range cards {
			if c.ID == frozen.ID && strings.Contains(c.Waiting, "it is offline") {
				return nil
			}
		}
		t.Fatal("the board must show the known machine status")
		return nil
	})
	if _, err := l.env.Owner.Exec(ctx, "UPDATE runtime SET status = 'online' WHERE id = $1", runtimes[0].ID); err != nil {
		t.Fatal(err)
	}
	if got, err := l.e.AgentRuns.Claim(ctx, l.owner, refs, 1, service.ClaimOptions{OutputAttestation: "unknown"}); err != nil || len(got) != 0 {
		t.Fatalf("unknown attestation protocol should not claim frozen work; got %d claims, %v", len(got), err)
	}
	got, err = l.e.AgentRuns.Claim(ctx, l.owner, refs, 1, service.ClaimOptions{OutputAttestation: agent.OutputAttestationProtocol})
	if err != nil || len(got) != 1 || got[0].AgentRun.CardID == nil || *got[0].AgentRun.CardID != frozen.ID || got[0].AgentRun.Attempt != 1 || got[0].OutputAttestation != agent.OutputAttestationProtocol {
		t.Fatalf("upgrade should launch the same queued work on its first attempt; got %d claims, %v", len(got), err)
	}
}

func TestFrozenComparisonRerunWaitsForUpgradeAndNoCLI(t *testing.T) {
	l := newLab(t)
	ctx := t.Context()
	a := l.agent(t, "Lead", "claude", "", "")
	c := l.scoped(t, board.Validation, board.Validation, a)
	if _, err := l.env.Owner.Exec(ctx, "UPDATE card SET study_cli = 'claude', auditor_cli = 'codex' WHERE id = $1", c.ID); err != nil {
		t.Fatal(err)
	}
	l.in(t, func(tx *store.Tx) error {
		_, err := l.e.AgentRuns.EnqueueRerun(ctx, tx, l.owner, c.ID, c.Round, true)
		if err != nil {
			return err
		}
		runs, err := tx.Q.CardAgentRuns(ctx, db.CardAgentRunsParams{WorkspaceID: l.ws, CardID: &c.ID})
		if err != nil {
			return err
		}
		if len(runs) != 1 || !strings.Contains(runs[0].Waiting, "output attestation") {
			t.Fatal("a frozen rerun should wait for output attestation, not a CLI")
		}
		cards, err := tx.Q.ListCards(ctx, l.ws)
		if err != nil {
			return err
		}
		if len(cards) != 1 || !strings.Contains(cards[0].Waiting, "output attestation") {
			t.Fatal("the board should show the frozen replay's upgrade guidance")
		}
		return nil
	})
}
