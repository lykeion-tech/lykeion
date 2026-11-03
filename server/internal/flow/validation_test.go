package flow

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/google/uuid"

	"lykeion/server/internal/handin"
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

const (
	supportedText = "The drug raises the AUC above 0.8."
	controlText   = "Placebo-treated mice"
)

// modeAStudy is a Team study at Validation round 1 whose Hypotheses column
// handed in one range hypothesis and whose Experimentation lead handed in a
// value auc = 0.873 from a script run. With rerun set, a rerun record for
// the round says whether auc was reproduced.
func modeAStudy(t *testing.T, l *lab, reproduced *bool) db.Card {
	t.Helper()
	c := l.study(t, "validation", true, nil)
	// The Team worked Experimentation too.
	if _, err := l.env.Owner.Exec(context.Background(), "UPDATE card SET scope_from = 'experimentation' WHERE id = $1", c.ID); err != nil {
		t.Fatal(err)
	}
	l.in(t, func(tx *store.Tx) error {
		return tx.Q.InsertCardTeam(context.Background(), db.InsertCardTeamParams{WorkspaceID: l.ws, CardID: c.ID, BoardColumn: "experimentation", PackID: l.pack.ID})
	})
	low, high := 0.8, 0.9
	l.put(t, c.ID, "hypotheses", 1, review.KindHypothesis, "leader", review.HypothesisBody{Text: supportedText,
		Prediction: review.Prediction{Shape: "range", Low: &low, High: &high}, Control: review.Control{Text: controlText}, Cites: []string{"r1"}})
	run := l.put(t, c.ID, "experimentation", 1, "run", "leader", script(0))
	l.put(t, c.ID, "experimentation", 1, "value", "leader", map[string]any{"name": "auc", "value": "0.873", "file": "fit.py", "cites": []string{"run:" + run}})
	l.put(t, c.ID, "experimentation", 1, "summary", "leader", map[string]any{"text": "Ran the fit.", "cites": []string{}})
	if reproduced != nil {
		l.putRerun(t, c, *reproduced)
	}
	return c
}

func (l *lab) putRerun(t *testing.T, c db.Card, reproduced bool) {
	t.Helper()
	body := map[string]any{"steps": []any{}, "outputs": []any{}, "reproduced": map[string]bool{"auc": reproduced}, "notes": map[string]string{}, "failure": nil}
	if !reproduced {
		body["notes"] = map[string]string{"auc": "rerun printed 0.5, reported 0.873"}
	}
	l.put(t, c.ID, "validation", l.card(t, c.ID).Round, "rerun", "server", body)
}

// assess has the Validation lead assess the hypothesis against auc, declare
// the column done and settles its auditor, which hands the round to Score.
func (l *lab) assess(t *testing.T, id uuid.UUID) {
	t.Helper()
	l.e.Kick(l.ws, id)
	l.item(t, id, review.KindAssessment, review.Assessment{Hypothesis: supportedText, Measurement: "auc"})
	l.done(t, id)
	l.settleAuditor(t, id)
}

// object files the objector's objections and settles its run.
func (l *lab) object(t *testing.T, id uuid.UUID, objs ...review.Objection) {
	t.Helper()
	c := l.card(t, id)
	run := l.latestRun(t, id, "objector")
	for _, o := range objs {
		l.put(t, id, "validation", c.Round, "objection", "objector", o)
	}
	l.settle(t, run, "completed", "", "")
}

type statusRecord struct {
	Hypothesis string
	Status     string
	Reasons    []string
	Cites      []string
	Final      bool
}

func (l *lab) statuses(t *testing.T, id uuid.UUID, final bool) []statusRecord {
	t.Helper()
	rows, err := l.env.Owner.Query(context.Background(), "SELECT body FROM record WHERE card_id = $1 AND kind = 'validation_status' AND role = 'server' AND (body->>'final')::bool = $2 ORDER BY created_at", id, final)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	var out []statusRecord
	for rows.Next() {
		var b []byte
		var s statusRecord
		if err := rows.Scan(&b); err != nil {
			t.Fatal(err)
		}
		if err := json.Unmarshal(b, &s); err != nil {
			t.Fatal(err)
		}
		out = append(out, s)
	}
	return out
}

func TestNoStatusesBeforeTheRerun(t *testing.T) {
	l := newLab(t)
	l.jev(t, 200, levels(nil))
	l.online(t, "d1", "claude", "codex")
	c := modeAStudy(t, l, nil)
	l.assess(t, c.ID)
	l.e.Kick(l.ws, c.ID)
	if n := l.count(t, "SELECT count(*) FROM agent_run WHERE card_id = $1 AND kind = 'rerun' AND status = 'queued'", c.ID); n != 1 {
		t.Fatalf("%d rerun jobs queued, want 1", n)
	}
	if n := l.count(t, "SELECT count(*) FROM record WHERE card_id = $1 AND kind IN ('validation_status', 'review', 'outcome')", c.ID); n != 0 {
		t.Fatalf("%d status, review or outcome records before the rerun", n)
	}
	if n := l.count(t, "SELECT count(*) FROM agent_run WHERE card_id = $1 AND role = 'objector'", c.ID); n != 0 {
		t.Fatalf("an objector started before the rerun: %d", n)
	}
	if got := l.card(t, c.ID); got.GatingUntil != nil || got.State != "working" {
		t.Fatalf("the held round keeps a lease or left working: %v %s", got.GatingUntil, got.State)
	}
	// The rerun's result moves it on.
	l.putRerun(t, c, true)
	l.e.Kick(l.ws, c.ID)
	if ss := l.statuses(t, c.ID, false); len(ss) != 1 || ss[0].Status != review.Supported {
		t.Fatalf("statuses after the rerun: %+v", ss)
	}
}

func TestModeAEndsWithStatuses(t *testing.T) {
	l := newLab(t)
	stub := l.jev(t, 200, levels(nil))
	l.online(t, "d1", "claude", "codex")
	yes := true
	c := modeAStudy(t, l, &yes)
	l.assess(t, c.ID)
	ss := l.statuses(t, c.ID, false)
	if len(ss) != 1 || ss[0].Status != review.Supported || ss[0].Hypothesis != supportedText || len(ss[0].Cites) != 2 {
		t.Fatalf("code statuses: %+v", ss)
	}
	obj := l.latestRun(t, c.ID, "objector")
	if !strings.Contains(obj.Prompt, "Rival A") || !strings.Contains(obj.Prompt, supportedText) || strings.Contains(obj.Prompt, "member-1") || strings.Contains(obj.Prompt, "You are pi") {
		t.Fatalf("the objector's brief:\n%s", obj.Prompt)
	}
	if len(stub.calls()) != 0 {
		t.Fatalf("Jev asked before the objector handed in: %d", len(stub.calls()))
	}
	if got := l.card(t, c.ID); got.State != "working" || got.GatingUntil != nil {
		t.Fatalf("waiting on the objector: %s lease %v", got.State, got.GatingUntil)
	}
	l.object(t, c.ID,
		review.Objection{Label: "Rival A", Type: "overreach", Problem: "One AUC on one cohort does not show the drug raises it.", Checks: []review.Check{}},
		review.Objection{Label: "Rival Q", Type: "leakage", Problem: "Leak.", Checks: []review.Check{}})
	got := l.card(t, c.ID)
	if got.State != "done" || got.BoardColumn != "validation" {
		t.Fatalf("after the objector: %s %s", got.BoardColumn, got.State)
	}
	final := l.statuses(t, c.ID, true)
	if len(final) != 1 || final[0].Status != review.Caveated || !slices.ContainsFunc(final[0].Reasons, func(r string) bool { return strings.Contains(r, "overreach") }) {
		t.Fatalf("final statuses: %+v", final)
	}
	if n := len(nouls(stub.calls())); n != 1 {
		t.Fatalf("Jev judged %d objections, want only the one the server check let through", n)
	}
	var upheld, dropped int
	rows, err := l.env.Owner.Query(context.Background(), "SELECT body FROM record WHERE card_id = $1 AND kind = 'objection' AND superseded_at IS NULL", c.ID)
	if err != nil {
		t.Fatal(err)
	}
	for rows.Next() {
		var b []byte
		rows.Scan(&b)
		var o review.Objection
		json.Unmarshal(b, &o)
		switch {
		case o.Upheld && o.TargetKey != "" && o.Server == "ok":
			upheld++
		case !o.Upheld && strings.Contains(o.Server, "Rival Q"):
			dropped++
		}
	}
	rows.Close()
	if upheld != 1 || dropped != 1 {
		t.Fatalf("judged objections: %d upheld, %d dropped", upheld, dropped)
	}
	if n := l.count(t, "SELECT count(*) FROM record WHERE card_id = $1 AND kind = 'objection' AND role = 'objector' AND superseded_at IS NULL", c.ID); n != 0 {
		t.Fatalf("%d unjudged objections left live", n)
	}
	if items := l.inbox(t, c.ID, "done"); len(items) != 1 {
		t.Fatalf("done items: %v", items)
	}
	if n := l.count(t, "SELECT count(*) FROM record WHERE card_id = $1 AND kind IN ('review', 'outcome') AND board_column = 'validation'", c.ID); n != 2 {
		t.Fatalf("%d review and outcome records", n)
	}
}

func TestInvalidRoutesBackOnceThenAsks(t *testing.T) {
	l := newLab(t)
	l.jev(t, 200, levels(nil))
	l.online(t, "d1", "claude", "codex")
	no := false
	c := modeAStudy(t, l, &no)
	l.assess(t, c.ID)
	l.object(t, c.ID)
	got := l.card(t, c.ID)
	if got.BoardColumn != "experimentation" || got.State != "working" || got.Round != c.Round+1 || got.Redos != 0 {
		t.Fatalf("after the first invalid: %s %s round %d redos %d", got.BoardColumn, got.State, got.Round, got.Redos)
	}
	const want = "Validation sent this back: " + supportedText + ": the measurement \"auc\" was not reproduced: rerun printed 0.5, reported 0.873"
	if got.HandoffNote != want {
		t.Fatalf("route-back note %q, want %q", got.HandoffNote, want)
	}
	if n := l.count(t, "SELECT count(*) FROM record WHERE card_id = $1 AND kind = 'route_back' AND role = 'server' AND board_column = 'validation' AND body->>'from' = 'validation' AND body->>'to' = 'experimentation' AND jsonb_array_length(body->'reasons') = 1", c.ID); n != 1 {
		t.Fatalf("%d route_back records", n)
	}
	if q := l.runs(t, c.ID); !slices.ContainsFunc(q, func(r queued) bool {
		return r.Column == "experimentation" && r.Role == "leader" && strings.Contains(r.Prompt, want)
	}) {
		t.Fatalf("Experimentation did not restart with the note: %+v", q)
	}
	// Experimentation comes back with the same work, and Validation finds it invalid again.
	c = l.move(t, c.ID, "validation", got.Round+1)
	l.putRerun(t, c, false)
	l.assess(t, c.ID)
	l.object(t, c.ID)
	got = l.card(t, c.ID)
	if got.BoardColumn != "validation" || got.State != "blocked" || got.BlockKind != "" {
		t.Fatalf("after the second invalid: %s %s %q", got.BoardColumn, got.State, got.BlockKind)
	}
	var text string
	if err := l.env.Owner.QueryRow(context.Background(), "SELECT text FROM inbox_item WHERE card_id = $1 AND kind = 'needs_decision' AND cause = 'validation_invalid' AND resolved_at IS NULL", c.ID).Scan(&text); err != nil {
		t.Fatalf("no validation_invalid decision: %v", err)
	}
	if !strings.Contains(text, supportedText+": the measurement \"auc\" was not reproduced") {
		t.Fatalf("decision text %q", text)
	}
	if n := l.count(t, "SELECT count(*) FROM record WHERE card_id = $1 AND kind = 'route_back'", c.ID); n != 1 {
		t.Fatalf("routed back %d times", n)
	}
	// The owner's comment restarts Validation and answers the decision.
	l.e.OnComment(l.ws, c.ID, l.owner, "The rerun machine was misconfigured; try again.")
	if got = l.card(t, c.ID); got.State != "working" || got.BoardColumn != "validation" {
		t.Fatalf("after the owner's comment: %s %s", got.BoardColumn, got.State)
	}
	if n := l.count(t, "SELECT count(*) FROM inbox_item WHERE card_id = $1 AND cause = 'validation_invalid' AND resolved_at IS NULL", c.ID); n != 0 {
		t.Fatalf("the comment left %d decisions open", n)
	}
}

func TestModeBRedoesOnce(t *testing.T) {
	l := newLab(t)
	l.jev(t, 200, levels(nil))
	c := l.study(t, "validation", true, nil)
	l.put(t, c.ID, "validation", 1, "call", "server", map[string]any{"server": "attachments", "tool": "attach", "args": map[string]any{"name": "paper.txt"},
		"answer": "The drug lowered systolic blood pressure by 10 mmHg."})
	for range 2 {
		l.e.Kick(l.ws, c.ID)
		l.item(t, c.ID, review.KindVerdict, review.VerdictBody{Claim: "The drug lowers blood pressure.", Verdict: "supported", Source: "paper.txt", Quote: "lowered diastolic pressure"})
		l.done(t, c.ID)
		l.settleAuditor(t, c.ID)
	}
	got := l.card(t, c.ID)
	if got.State != "blocked" || got.BlockKind != "low_score" || got.Redos != 1 {
		t.Fatalf("after two failed attempts: %s %q redos %d", got.State, got.BlockKind, got.Redos)
	}
	rs := l.reviews(t, c.ID, "validation")
	if len(rs) != 2 || rs[0].Decision != review.DecideRedo || rs[1].Decision != review.DecideBlock || rs[0].Items[0].Checks[0].Status != review.Fail {
		t.Fatalf("reviews: %+v", rs)
	}
	if n := l.count(t, "SELECT count(*) FROM agent_run WHERE card_id = $1 AND role IN ('objector', 'rerun')", c.ID); n != 0 {
		t.Fatalf("a claims study started %d objector or rerun runs", n)
	}
}

func TestARestartedValidationRoundUsesTheHandIn(t *testing.T) {
	l := newLab(t)
	c := l.study(t, "validation", true, nil)
	// What migration 0045 does to a study mid-Validation.
	if _, err := l.env.Owner.Exec(context.Background(), "UPDATE card SET round = round + 1, redos = 0, legacy_column = '', handoff_note = 'Validation restarted under the new review rules.' WHERE id = $1", c.ID); err != nil {
		t.Fatal(err)
	}
	l.e.Kick(l.ws, c.ID)
	run := l.latestRun(t, c.ID, "leader")
	k, ok := handin.KindFor(run.Kind, run.Role, run.BoardColumn)
	if !ok || k != handin.Validation || handin.Schema(k) == "" {
		t.Fatalf("the restarted lead owes no Validation hand-in: %q %v", k, ok)
	}
	if !strings.Contains(run.Prompt, "Validation restarted under the new review rules.") {
		t.Fatalf("the restart note is missing:\n%s", run.Prompt)
	}
	for what, text := range map[string]string{"prompt": run.Prompt, "skill": run.Skill} {
		if found := protocolIn(text); len(found) > 0 {
			t.Errorf("the restarted lead's %s names the protocol (%s):\n%s", what, strings.Join(found, "; "), text)
		}
	}
}

func TestValidationCommentDoesNotWake(t *testing.T) {
	l := newLab(t)
	c := l.study(t, "validation", true, nil)
	l.e.Kick(l.ws, c.ID)
	if _, err := l.env.Owner.Exec(context.Background(), "UPDATE agent_run SET status = 'running' WHERE card_id = $1 AND role = 'leader'", c.ID); err != nil {
		t.Fatal(err)
	}
	before := l.count(t, "SELECT count(*) FROM agent_run WHERE card_id = $1", c.ID)
	const note = "Check the second cohort too."
	l.in(t, func(tx *store.Tx) error {
		_, err := tx.Q.CreateComment(context.Background(), db.CreateCommentParams{WorkspaceID: l.ws, CardID: c.ID, AuthorID: l.owner, Body: note})
		return err
	})
	l.e.OnComment(l.ws, c.ID, l.owner, note)
	if n := l.count(t, "SELECT count(*) FROM agent_run WHERE card_id = $1", c.ID); n != before {
		t.Fatalf("a comment on a working Validation study queued %d runs", n-before)
	}
	// The lead's next run, its redo after a failure, carries the comment.
	l.settle(t, l.latestRun(t, c.ID, "leader"), "failed", "agent_error", "crashed")
	if q := l.runs(t, c.ID); len(q) != 1 || !strings.Contains(q[0].Prompt, note) {
		t.Fatalf("the next run lacks the comment: %+v", q)
	}
}

func TestNoProtocolLeft(t *testing.T) {
	files, err := filepath.Glob("*.go")
	if err != nil {
		t.Fatal(err)
	}
	for _, f := range files {
		if strings.HasSuffix(f, "_test.go") {
			continue
		}
		b, err := os.ReadFile(f)
		if err != nil {
			t.Fatal(err)
		}
		for _, gone := range []string{"afterProtocolRun", "validationProtocol", "lyk team"} {
			if strings.Contains(string(b), gone) {
				t.Errorf("%s still holds %q", f, gone)
			}
		}
	}
}
