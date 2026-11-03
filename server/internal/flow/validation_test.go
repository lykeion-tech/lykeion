package flow

import (
	"context"
	"strings"
	"testing"

	"lykeion/server/internal/review"
	"lykeion/server/internal/service"
	"lykeion/server/internal/store"
	"lykeion/server/pkg/agent"
	db "lykeion/server/pkg/db/generated"
)

// validationLead is a Validation study whose lead ran on harness.
func (l *lab) validationLead(t *testing.T, harness string) db.Card {
	t.Helper()
	c := l.study(t, "validation", true, nil)
	l.in(t, func(tx *store.Tx) error {
		_, err := l.e.AgentRuns.Enqueue(context.Background(), tx, l.owner, service.NewAgentRun{Card: c.ID, Column: c.BoardColumn, Round: c.Round,
			Role: "leader", Harness: harness, Prompt: "p", ContinuityScope: ContinuityScope(c.ID, "leader", c.BoardColumn, c.Round)})
		return err
	})
	return c
}

func (l *lab) online(t *testing.T, daemon string, harnesses ...string) {
	t.Helper()
	if _, err := l.e.AgentRuns.Register(context.Background(), l.owner, l.ws, daemon, "laptop", harnesses, nil, agent.Platform{}); err != nil {
		t.Fatal(err)
	}
}

func (l *lab) objector(t *testing.T, c db.Card, brief string) (db.AgentRun, bool) {
	t.Helper()
	var same bool
	l.in(t, func(tx *store.Tx) (err error) {
		same, err = l.e.enqueueObjector(context.Background(), tx, c, brief)
		return err
	})
	return l.latestRun(t, c.ID, "objector"), same
}

func TestSameFamilyObjectorWhenTheOtherIsOffline(t *testing.T) {
	l := newLab(t)
	l.online(t, "d1", "claude", "codex")
	c := l.validationLead(t, "codex")
	run, same := l.objector(t, c, "brief")
	if run.Harness != "claude" || same || run.AgentID != nil || run.RequestedModel != "" {
		t.Fatalf("other family online: harness %q, same %v, agent %v, model %q", run.Harness, same, run.AgentID, run.RequestedModel)
	}
	if _, err := l.env.Owner.Exec(context.Background(), "UPDATE runtime SET status = 'offline' WHERE harness = 'claude'"); err != nil {
		t.Fatal(err)
	}
	c = l.validationLead(t, "codex")
	run, same = l.objector(t, c, "brief")
	if run.Harness != "codex" || !same {
		t.Fatalf("only codex online: harness %q, same %v", run.Harness, same)
	}
	lead := l.latestRun(t, c.ID, "leader")
	if run.ContinuityScope == lead.ContinuityScope || run.ContinuityScope == ContinuityScope(c.ID, "objector", c.BoardColumn, c.Round) {
		t.Fatalf("the objector's session is not fresh: %q", run.ContinuityScope)
	}
	// With neither family online the objector waits on the other family.
	if _, err := l.env.Owner.Exec(context.Background(), "UPDATE runtime SET status = 'offline'"); err != nil {
		t.Fatal(err)
	}
	c = l.validationLead(t, "claude")
	if run, same = l.objector(t, c, "brief"); run.Harness != "codex" || same {
		t.Fatalf("none online: harness %q, same %v", run.Harness, same)
	}
}

func TestObjectorGetsNoLyk(t *testing.T) {
	l := newLab(t)
	c := l.validationLead(t, "claude")
	hs := []review.HypothesisBody{{Text: "Dose raises slope", Prediction: review.Prediction{Shape: "direction", Direction: "increase"}, Control: review.Control{Text: "saline"}}}
	ss := []review.StatusResult{{Hypothesis: "Dose raises slope", Status: review.Supported, Reasons: []string{"the effect 0.3 is in the predicted direction (increase) and significant (p < 0.05)"}}}
	brief, _ := review.ObjectorBrief(ss, hs, []string{"slope"}, []string{"fit.txt"}, int64(c.Round))
	run, _ := l.objector(t, c, brief)
	for what, text := range map[string]string{"prompt": run.Prompt, "skill": run.Skill, "assignment": run.Assignment} {
		if found := protocolIn(text); len(found) > 0 {
			t.Errorf("the objector's %s names the protocol (%s):\n%s", what, strings.Join(found, "; "), text)
		}
	}
	if run.Prompt != brief {
		t.Errorf("the prompt is not the brief:\n%s", run.Prompt)
	}
}

func TestObjectionsAreServerWrittenAndNeverWork(t *testing.T) {
	if service.MayWrite("leader", "objection") == nil {
		t.Error("a run may write an objection record")
	}
	if !bookkeeping("objection") {
		t.Error("objection records reach Jev as work")
	}
}
