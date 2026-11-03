package flow

import (
	"context"
	"encoding/json"
	"slices"
	"testing"

	"github.com/google/uuid"

	"lykeion/server/internal/handin"
	"lykeion/server/internal/records"
	"lykeion/server/internal/review"
	"lykeion/server/internal/service"
	"lykeion/server/internal/statcheck"
	"lykeion/server/internal/store"
	"lykeion/server/pkg/agent"
	db "lykeion/server/pkg/db/generated"
)

// claimOne registers a daemon with both CLIs, claims the card's queued run
// of kind and starts it, as a daemon would.
func (l *lab) claimOne(t *testing.T, card uuid.UUID, kind string) (service.RuntimeRef, db.AgentRun) {
	t.Helper()
	ref, c := l.claim(t, card, kind)
	return ref, c.AgentRun
}

func (l *lab) claim(t *testing.T, card uuid.UUID, kind string) (service.RuntimeRef, service.Claimed) {
	t.Helper()
	ctx := context.Background()
	rts, err := l.e.AgentRuns.Register(ctx, l.owner, l.ws, "d1", "laptop", []string{"claude", "codex"}, nil, agent.Platform{})
	if err != nil {
		t.Fatal(err)
	}
	var refs []service.RuntimeRef
	for _, r := range rts {
		refs = append(refs, service.RuntimeRef{WorkspaceID: l.ws, RuntimeID: r.ID})
	}
	claims, err := l.e.AgentRuns.Claim(ctx, l.owner, refs, 4)
	if err != nil {
		t.Fatal(err)
	}
	i := slices.IndexFunc(claims, func(c service.Claimed) bool { return *c.AgentRun.CardID == card && c.AgentRun.Kind == kind })
	if i < 0 {
		t.Fatalf("no %s run claimed among %d", kind, len(claims))
	}
	run := claims[i].AgentRun
	ref := service.RuntimeRef{WorkspaceID: l.ws, RuntimeID: *run.RuntimeID, Attempt: run.Attempt}
	if err := l.e.AgentRuns.Start(ctx, l.owner, ref, run.ID); err != nil {
		t.Fatal(err)
	}
	return ref, claims[i]
}

// putBy stores a record as the sources server or the door would for run.
func (l *lab) putBy(t *testing.T, run db.AgentRun, kind string, body any) string {
	t.Helper()
	b, _ := json.Marshal(body)
	var key string
	l.in(t, func(tx *store.Tx) (err error) {
		if key, err = records.NextRecordKey(context.Background(), tx, *run.CardID); err != nil {
			return err
		}
		_, err = tx.Q.InsertRecord(context.Background(), db.InsertRecordParams{WorkspaceID: l.ws, CardID: *run.CardID, BoardColumn: run.BoardColumn,
			Round: run.Round, Kind: kind, Role: run.Role, Key: key, Body: b, AgentRunID: &run.ID})
		return err
	})
	return key
}

// An Experimentation hand-in names one value by the saved output it is in
// and one by the script that printed it; Validation reruns that script and
// sets the hypothesis's status from those records alone.
func TestAnExperimentationHandInIsValidatedFromItsOwnRecords(t *testing.T) {
	ctx := context.Background()
	l := newLab(t)
	l.jev(t, 200, levels(nil))
	c := l.study(t, "validation", true, nil)
	if _, err := l.env.Owner.Exec(ctx, "UPDATE card SET scope_from = 'experimentation' WHERE id = $1", c.ID); err != nil {
		t.Fatal(err)
	}
	l.in(t, func(tx *store.Tx) error {
		return tx.Q.InsertCardTeam(ctx, db.InsertCardTeamParams{WorkspaceID: l.ws, CardID: c.ID, BoardColumn: "experimentation", PackID: l.pack.ID})
	})
	const hyp = "Fertilizer makes seedlings taller."
	l.put(t, c.ID, "hypotheses", 1, review.KindHypothesis, "leader", review.HypothesisBody{Text: hyp,
		Prediction: review.Prediction{Shape: "direction", Direction: "increase"}, Control: review.Control{Text: "untreated seedlings"}, Cites: []string{"r1"}})

	l.move(t, c.ID, "experimentation", 1)
	l.e.Kick(l.ws, c.ID)
	ref, lead := l.claimOne(t, c.ID, "role")
	const printed = "n 20\ndifference 4.465\nt = 8.698, df = 36.88, p = 1.83e-10\n"
	script := l.putBy(t, lead, "run", map[string]any{"tool": "run_script", "language": "python", "path": "analysis.py", "script": "print(...)", "exit_status": 0, "output": printed})
	l.putBy(t, lead, "output", map[string]any{"name": "results.csv", "size_bytes": 10, "sha256": "s"})
	in := map[string]any{"status": "done", "answer": "Fertilized seedlings were taller.", "reason": nil, "replies": []any{}, "outputs": []string{"results.csv"},
		"values": []any{map[string]any{"name": "difference", "value": "4.465", "file": "results.csv"}, map[string]any{"name": "n", "value": "20", "file": "analysis.py"}}}
	raw, _ := json.Marshal(in)
	hs := handin.HandIns{Runs: l.e.AgentRuns, SentBack: l.e.SentBack}
	if probs, err := hs.Submit(ctx, l.owner, ref, lead.ID, raw); err != nil || len(probs) > 0 {
		t.Fatalf("the Experimentation hand-in: %v %v", probs, err)
	}

	l.move(t, c.ID, "validation", 2)
	l.e.Kick(l.ws, c.ID)
	l.item(t, c.ID, review.KindAssessment, review.Assessment{Hypothesis: hyp, Measurement: "difference",
		Test: &statcheck.Test{Kind: "t", Statistic: 8.698, StatDigits: 3, DF1: 36.88, P: 1.83e-10, PDigits: 12, PBound: "="}})
	l.done(t, c.ID)
	l.settleAuditor(t, c.ID)
	ref, rerun := l.claimOne(t, c.ID, "rerun")
	if err := l.e.RerunResult(ctx, l.owner, ref, rerun.ID, RerunResult{Steps: []RerunStepResult{{Key: script, ExitStatus: 0, Output: printed}}, Outputs: []RerunOutput{}}); err != nil {
		t.Fatal(err)
	}
	l.e.Kick(l.ws, c.ID)
	l.object(t, c.ID)
	final := l.statuses(t, c.ID, true)
	if len(final) != 1 || final[0].Status != review.Supported {
		t.Fatalf("final statuses: %+v", final)
	}
}

// A targeted Experimentation redo hands in only the value it fixed; the
// value that passed in the first round still stands, and Validation reruns
// the script it rests on.
func TestValidationRerunsAValueCarriedFromAnEarlierRound(t *testing.T) {
	ctx := context.Background()
	l := newLab(t)
	c := l.study(t, "experimentation", true, nil)
	auc := l.put(t, c.ID, "experimentation", 1, "run", "leader", script(0))
	l.put(t, c.ID, "experimentation", 1, "value", "leader", map[string]any{"name": "auc", "value": "0.873", "file": "fit.py", "cites": []string{"run:" + auc}})
	old := l.put(t, c.ID, "experimentation", 1, "run", "leader", map[string]any{"tool": "run_script", "language": "python", "path": "f1.py", "script": "print(0.5)", "exit_status": 0, "output": "0.5"})
	l.put(t, c.ID, "experimentation", 1, "value", "leader", map[string]any{"name": "f1", "value": "0.61", "file": "f1.py", "cites": []string{"run:" + old}})
	fixed := l.put(t, c.ID, "experimentation", 2, "run", "leader", map[string]any{"tool": "run_script", "language": "python", "path": "f1.py", "script": "print(0.61)", "exit_status": 0, "output": "0.61"})
	l.put(t, c.ID, "experimentation", 2, "value", "leader", map[string]any{"name": "f1", "value": "0.61", "file": "f1.py", "cites": []string{"run:" + fixed}})
	c = l.move(t, c.ID, "validation", 3)
	l.startRerun(t, c.ID)
	ref, claimed := l.claim(t, c.ID, "rerun")
	run := claimed.AgentRun
	var steps []string
	for _, s := range claimed.Rerun.Steps {
		steps = append(steps, s.Key)
	}
	if !slices.Equal(steps, []string{auc, fixed}) {
		t.Fatalf("replayed %v, want %v", steps, []string{auc, fixed})
	}
	if err := l.e.RerunResult(ctx, l.owner, ref, run.ID, RerunResult{Steps: []RerunStepResult{{Key: auc, Output: "0.873"}, {Key: fixed, Output: "0.61"}}, Outputs: []RerunOutput{}}); err != nil {
		t.Fatal(err)
	}
	var body []byte
	if err := l.env.Owner.QueryRow(ctx, "SELECT body FROM record WHERE card_id = $1 AND kind = 'rerun'", c.ID).Scan(&body); err != nil {
		t.Fatal(err)
	}
	var rb rerunBody
	_ = json.Unmarshal(body, &rb)
	if !rb.Reproduced["auc"] || !rb.Reproduced["f1"] {
		t.Fatalf("rerun record %s", body)
	}
}
