package report

import (
	"encoding/json"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"

	"lykeion/server/internal/review"
	db "lykeion/server/pkg/db/generated"
)

var t0 = time.Date(2026, 11, 1, 9, 0, 0, 0, time.UTC)

// study builds an Input record by record; each record is a minute newer.
type study struct {
	in Input
	n  int
}

func newStudy(from, to string) *study {
	return &study{in: Input{
		Card:   db.Card{ID: uuid.New(), Number: 7, Title: "Does X raise Y?", Question: "Does X raise Y in mice?", ScopeFrom: from, ScopeTo: to},
		Agents: map[uuid.UUID]string{},
	}}
}

func (s *study) at() time.Time { s.n++; return t0.Add(time.Duration(s.n) * time.Minute) }

func (s *study) run(column, agent string) uuid.UUID {
	id, agentID := uuid.New(), uuid.New()
	s.in.Agents[agentID] = agent
	s.in.Runs = append(s.in.Runs, db.StudyRunsRow{ID: id, BoardColumn: column, AgentID: &agentID})
	return id
}

func (s *study) rec(column, kind, key string, round int32, run *uuid.UUID, body any) *db.Record {
	b, err := json.Marshal(body)
	if err != nil {
		panic(err)
	}
	s.in.Records = append(s.in.Records, db.Record{ID: uuid.New(), BoardColumn: column, Kind: kind, Key: key, Round: round,
		AgentRunID: run, Body: b, CreatedAt: s.at()})
	return &s.in.Records[len(s.in.Records)-1]
}

func p(f float64) *float64 { return &f }

func nodeOf(t *testing.T, r Report, id string) Node {
	t.Helper()
	for _, n := range r.Nodes {
		if n.ID == id {
			return n
		}
	}
	t.Fatalf("no node %s in %v", id, ids(r))
	return Node{}
}

func ids(r Report) []string {
	var out []string
	for _, n := range r.Nodes {
		out = append(out, n.ID)
	}
	return out
}

func kinds(r Report) map[string]int {
	out := map[string]int{}
	for _, n := range r.Nodes {
		out[n.Kind]++
	}
	return out
}

func hasEdge(r Report, from, to, kind string) bool {
	return slices.Contains(r.Edges, Edge{From: from, To: to, Kind: kind})
}

func TestReportShowsTheReplayForTheFinishedRound(t *testing.T) {
	s := newStudy("experimentation", "validation")
	s.rec("validation", "rerun", "r1", 1, nil, map[string]any{"steps": []map[string]any{{"of": "x1", "exit_status": 0}}})
	s.rec("validation", "rerun", "r2", 2, nil, map[string]any{"steps": []map[string]any{{"of": "x2", "exit_status": 17, "stderr": "missing marker"}}})
	s.rec("validation", "outcome", "r3", 2, nil, map[string]string{"verdict": "high"})
	r := Build(s.in)
	if r.Replay == nil || len(r.Replay.Steps) != 1 {
		t.Fatalf("wrong replay: %+v", r.Replay)
	}
	if step := r.Replay.Steps[0]; step.Of != "x2" || step.ExitStatus != 17 || step.Stderr != "missing marker" {
		t.Fatalf("wrong replay step: %+v", step)
	}
}

func TestBuildDistinguishesOutputsWithTheSamePath(t *testing.T) {
	s := newStudy("experimentation", "validation")
	first := s.run("experimentation", "Leader")
	second := s.run("experimentation", "Auditor")
	name := "outputs/summary.csv"
	s.rec("experimentation", "output", "o1", 1, &first, map[string]string{"name": name})
	s.rec("experimentation", "output", "o2", 1, &second, map[string]string{"name": name})
	s.rec("experimentation", "value", "r1", 1, &first, map[string]string{"name": "first slope", "file": name})
	s.rec("experimentation", "value", "r2", 1, &second, map[string]string{"name": "second slope", "file": name})
	s.rec("experimentation", "value", "r3", 1, nil, map[string]string{"name": "ambiguous slope", "file": name})
	for i, f := range []struct{ key, measurement string }{{"r4", "r1"}, {"r5", "r2"}, {"r6", "r3"}} {
		s.rec("validation", "validation_status", f.key, 2, nil, map[string]any{
			"hypothesis": []string{"first", "second", "ambiguous"}[i], "status": "supported", "cites": []string{f.measurement}, "final": true,
		})
	}
	s.in.Build = &db.ReportBuild{Status: "done", ChecksStatus: "done"}
	s.in.Concepts = []db.ReportConcept{{Key: "slope", Label: "Slope"}}
	s.in.Facts = []db.ReportFact{{Subject: "slope", Relation: "mentioned_in", Object: "out:" + name, SourceNode: "out:" + name}}
	s.in.Checks = []db.ReportCheck{
		{NodeID: "rec:r4", CheckID: "table:out:" + name, Status: review.Pass},
		{NodeID: "rec:r5", CheckID: "table:out:" + name, Status: review.Pass},
	}

	r := Build(s.in)
	for _, pair := range []struct {
		id, finding, agent string
		run                uuid.UUID
	}{{"rec:o1", "rec:r4", "Leader", first}, {"rec:o2", "rec:r5", "Auditor", second}} {
		n := nodeOf(t, r, pair.id)
		if n.Kind != "output" || n.RunID == nil || *n.RunID != pair.run || n.Agent != pair.agent {
			t.Fatalf("output provenance: %+v", n)
		}
		if !hasEdge(r, "run:"+pair.run.String(), pair.id, "produced") || !hasEdge(r, pair.id, pair.finding, "supports") {
			t.Errorf("missing provenance edges for %s: %v", pair.id, r.Edges)
		}
		if hasEdge(r, pair.id, "rec:r6", "supports") {
			t.Errorf("ambiguous path should not be attributed to %s", pair.id)
		}
	}
	if nodeOf(t, r, "rec:o1").Label == nodeOf(t, r, "rec:o2").Label {
		t.Error("identical paths need distinguishable labels")
	}
	if hasEdge(r, "rec:o2", "rec:r4", "supports") || hasEdge(r, "rec:o1", "rec:r5", "supports") {
		t.Errorf("an output supported a finding from another run: %v", r.Edges)
	}
	if fact := nodeOf(t, r, "concept:slope").Facts[0]; fact.Object != "rec:o2" || fact.SourceNode != "rec:o2" {
		t.Errorf("historical fact reference: %+v", fact)
	}
	if got := nodeOf(t, r, "rec:r5").Checks; len(got) != 1 || got[0].ID != "table:rec:o2" {
		t.Errorf("historical check reference: %+v", got)
	}
	if got := nodeOf(t, r, "rec:r4").Checks; len(got) != 0 {
		t.Errorf("a historical check of the newest artifact applied to the earlier one: %+v", got)
	}
}

func TestBuildFullStudy(t *testing.T) {
	s := newStudy("grounding", "validation")
	gRun := s.run("grounding", "Librarian")
	s.rec("grounding", "paper", "r1", 1, &gRun, review.PaperBody{ID: "arxiv:2101.00001", Text: "X raised Y in rats.", Cites: []string{"c1"}})
	s.rec("grounding", "dataset", "r2", 1, &gRun, review.DatasetBody{Accession: "GSE1", Text: "Mouse expression data."})
	s.rec("grounding", "review", "r3", 1, nil, review.Result{Column: "grounding", Decision: review.DecideCarry, Items: []review.Item{
		{Key: "r1", Kind: "paper", Label: "arxiv:2101.00001", Status: review.Unclear, Checks: []review.Check{
			{ID: "relevant", By: "jev", Question: "Is it relevant?", P: p(0.5), Status: review.Unclear}}},
		{Key: "r2", Kind: "dataset", Label: "GSE1", Status: review.Unclear, Checks: []review.Check{
			{ID: "fits", By: "jev", Question: "Does it fit?", P: p(0.6), Status: review.Unclear}}},
	}})
	hRun := s.run("hypotheses", "Theorist")
	s.rec("hypotheses", "hypothesis", "r4", 2, &hRun, review.HypothesisBody{Text: "X raises Y", Cites: []string{"r1"}})
	s.rec("hypotheses", "review", "r5", 2, nil, review.Result{Column: "hypotheses", Decision: review.DecidePass, Items: []review.Item{
		{Key: "r4", Kind: "hypothesis", Status: review.Pass, Checks: []review.Check{{ID: "falsifiable", By: "jev", P: p(0.9), Status: review.Pass}}}}})
	eRun := s.run("experimentation", "Experimenter")
	idle := s.run("experimentation", "Idle")
	_ = idle
	s.rec("experimentation", "output", "o1", 3, &eRun, map[string]any{"name": "fig.png", "size_bytes": 10})
	s.rec("experimentation", "value", "r6", 3, &eRun, map[string]any{"name": "m", "value": "1.2", "file": "fig.png", "cites": []string{}})
	s.rec("experimentation", "summary", "r7", 3, &eRun, map[string]any{"text": "Ran it.", "cites": []string{}})
	s.rec("validation", "assessment", "r8", 4, nil, review.Assessment{Hypothesis: "X raises Y", Measurement: "m"})
	s.rec("validation", "rerun", "r9", 4, nil, map[string]any{"reproduced": map[string]bool{"m": true}})
	obj := s.rec("validation", "objection", "r10", 4, nil, review.Objection{TargetKey: "r6", Label: "m", Type: "leakage", Problem: "Test rows reached the fit.",
		Server: "ok", Checks: []review.Check{{ID: "wrong", By: "jev", P: p(0.8), Status: review.Pass}, {ID: "specific", By: "jev", P: p(0.4), Status: review.Unclear}}})
	s.rec("validation", "objection", "r11", 4, nil, review.Objection{TargetKey: "", Label: "Rival Q", Type: "overreach", Problem: "x", Server: "no such target"})
	s.rec("validation", "validation_status", "r12", 4, nil, map[string]any{"hypothesis": "X raises Y ", "status": "caveated",
		"reasons": []string{"no control: none available", "an upheld leakage objection"}, "cites": []string{"r6", "r10"}, "final": true})
	s.rec("validation", "review", "r13", 4, nil, review.Result{Column: "validation", Decision: review.DecidePass, Items: []review.Item{
		{Key: "r4", Kind: "hypothesis", Status: "caveated", Checks: []review.Check{}}}})
	vRun := s.run("validation", "Validator")
	s.rec("validation", "summary", "r14", 4, &vRun, map[string]any{"text": "Older answer."})
	hand := s.rec("validation", "summary", "r15", 4, &vRun, map[string]any{"text": "X raises Y, with caveats."})
	out := s.rec("validation", "outcome", "r16", 4, nil, map[string]any{"verdict": "pass"})
	author, decider := uuid.New(), uuid.New()
	decidedAt := t0.Add(30 * time.Minute)
	s.in.Comments = []db.Comment{{AuthorID: author, Body: "Use the mouse data.", CreatedAt: t0.Add(40 * time.Minute)}}
	s.in.Decided = []db.InboxItem{{Text: "Keep going?", ChosenOption: "yes", Guidance: "but log it", DecidedBy: &decider, DecidedAt: &decidedAt}}

	r := Build(s.in)

	want := map[string]int{"question": 1, "paper": 1, "dataset": 1, "hypothesis": 1, "experiment": 1, "output": 1, "finding": 1, "objection": 1, "doubt": 2}
	if got := kinds(r); !maps(got, want) {
		t.Fatalf("node kinds %v, want %v (%v)", got, want, ids(r))
	}
	if r.Question != "Does X raise Y in mice?" || !slices.Equal(r.Stages, []string{"grounding", "hypotheses", "experimentation", "validation"}) || !r.FinishedAt.Equal(out.CreatedAt) {
		t.Fatalf("header: %q %v %v", r.Question, r.Stages, r.FinishedAt)
	}
	if ids(r)[0] != "question" || nodeOf(t, r, "rec:r1").Label != "arxiv:2101.00001" || nodeOf(t, r, "rec:r2").Label != "GSE1" {
		t.Fatalf("nodes: %v", ids(r))
	}
	paper := nodeOf(t, r, "rec:r1")
	if paper.Quote != "X raised Y in rats." || paper.Status != review.Unclear || len(paper.Checks) != 1 || paper.Agent != "Librarian" || *paper.RunID != gRun {
		t.Fatalf("paper: %+v", paper)
	}
	if h := nodeOf(t, r, "rec:r4"); h.Status != "kept" || h.Quote != "X raises Y" || len(h.Checks) != 1 {
		t.Fatalf("hypothesis: %+v", h)
	}
	if e := nodeOf(t, r, "run:"+eRun.String()); e.Label != "Experimenter" || e.Kind != "experiment" {
		t.Fatalf("experiment: %+v", e)
	}
	f := nodeOf(t, r, "rec:r12")
	if f.Label != "X raises Y " || f.Status != "caveated" || !slices.Equal(f.Reasons, []string{"no control: none available", "an upheld leakage objection"}) {
		t.Fatalf("finding: %+v", f)
	}
	if string(f.Rerun) != `{"reproduced":{"m":true}}` {
		t.Fatalf("finding rerun: %s", f.Rerun)
	}
	if o := nodeOf(t, r, "obj:"+obj.ID.String()); o.Quote != "Test rows reached the fit." || o.Status != "not_upheld" || len(o.Checks) != 2 {
		t.Fatalf("objection: %+v", o)
	}
	finding, exp := "rec:r12", "run:"+eRun.String()
	for _, e := range []Edge{
		{"question", "rec:r1", "uses"}, {"question", "rec:r2", "uses"}, {"question", "rec:r4", "tests"},
		{"rec:r1", "rec:r4", "grounds"}, {"rec:r4", exp, "tested_by"}, {exp, "rec:o1", "produced"},
		{"rec:r4", finding, "tests"}, {"rec:o1", finding, "supports"}, {"obj:" + obj.ID.String(), finding, "challenges"},
		{"doubt:grounding:r1", finding, "challenges"}, {"doubt:grounding:r2", "question", "challenges"},
	} {
		if !hasEdge(r, e.From, e.To, e.Kind) {
			t.Errorf("missing edge %v in %v", e, r.Edges)
		}
	}
	if len(r.Edges) != 11 {
		t.Errorf("edges: %v", r.Edges)
	}
	if r.HandIn == nil || r.HandIn.Text != "X raises Y, with caveats." || r.HandIn.AgentName != "Validator" || r.HandIn.RunID != vRun || !r.HandIn.At.Equal(hand.CreatedAt) {
		t.Fatalf("hand-in: %+v", r.HandIn)
	}
	if len(r.Steering) != 2 || r.Steering[0].Kind != "decision" || r.Steering[0].Author != decider || r.Steering[1].Kind != "comment" || r.Steering[1].Text != "Use the mouse data." {
		t.Fatalf("steering: %+v", r.Steering)
	}
	if r.Steering[0].Text != "Keep going?\nyes\nbut log it" {
		t.Fatalf("decision text: %q", r.Steering[0].Text)
	}
}

func maps(a, b map[string]int) bool {
	if len(a) != len(b) {
		return false
	}
	for k, v := range a {
		if b[k] != v {
			return false
		}
	}
	return true
}

func TestBuildGroundingOnly(t *testing.T) {
	s := newStudy("grounding", "grounding")
	s.in.Card.Question = ""
	run := s.run("grounding", "Librarian")
	s.rec("grounding", "paper", "r1", 1, &run, review.PaperBody{ID: "doi:10.1/x", Text: "A paper."})
	s.rec("grounding", "dataset", "r2", 1, &run, review.DatasetBody{Accession: "GSE2", Text: "A dataset."})
	s.rec("grounding", "summary", "r3", 1, &run, map[string]any{"text": "Found one of each."})
	r := Build(s.in)
	if got := kinds(r); !maps(got, map[string]int{"question": 1, "paper": 1, "dataset": 1}) {
		t.Fatalf("kinds: %v", got)
	}
	if !slices.Equal(r.Stages, []string{"grounding"}) || r.Question != "Does X raise Y?" {
		t.Fatalf("stages %v question %q", r.Stages, r.Question)
	}
	if r.HandIn == nil || r.HandIn.Text != "Found one of each." || r.HandIn.AgentName != "Librarian" {
		t.Fatalf("hand-in: %+v", r.HandIn)
	}
}

func TestBuildIgnoresSupersededAndOldRounds(t *testing.T) {
	s := newStudy("hypotheses", "validation")
	gone := t0
	s.rec("hypotheses", "hypothesis", "r1", 1, nil, review.HypothesisBody{Text: "Old wording"}).SupersededAt = &gone
	s.rec("hypotheses", "hypothesis", "r2", 2, nil, review.HypothesisBody{Text: "New wording"})
	s.rec("validation", "validation_status", "r3", 1, nil, map[string]any{"hypothesis": "New wording", "status": "refuted", "final": true})
	s.rec("validation", "validation_status", "r4", 2, nil, map[string]any{"hypothesis": "New wording", "status": "supported", "final": false})
	s.rec("validation", "validation_status", "r5", 2, nil, map[string]any{"hypothesis": "New wording", "status": "supported", "final": true})
	r := Build(s.in)
	got := ids(r)
	if !slices.Equal(got, []string{"question", "rec:r2", "rec:r5"}) {
		t.Fatalf("nodes: %v", got)
	}
	if !hasEdge(r, "rec:r2", "rec:r5", "tests") {
		t.Fatalf("edges: %v", r.Edges)
	}
}

func TestBuildRejectedHypothesis(t *testing.T) {
	s := newStudy("hypotheses", "validation")
	s.rec("hypotheses", "hypothesis", "r1", 1, nil, review.HypothesisBody{Text: "Withdrawn one"})
	s.rec("hypotheses", "hypothesis", "r2", 1, nil, review.HypothesisBody{Text: "Failed one"})
	s.rec("hypotheses", "hypothesis", "r3", 1, nil, review.HypothesisBody{Text: "Refuted one"})
	s.rec("hypotheses", "withdrawn", "r4", 1, nil, review.WithdrawnBody{Item: "r1", Reason: "duplicate"})
	s.rec("hypotheses", "review", "r5", 1, nil, review.Result{Column: "hypotheses", Decision: review.DecideRedo, Items: []review.Item{
		{Key: "r2", Status: review.Pass}, {Key: "r3", Status: review.Fail}}})
	s.rec("hypotheses", "review", "r6", 2, nil, review.Result{Column: "hypotheses", Decision: review.DecidePass, Items: []review.Item{
		{Key: "r2", Status: review.Fail}, {Key: "r3", Status: review.Pass}}})
	s.rec("validation", "validation_status", "r7", 4, nil, map[string]any{"hypothesis": "Refuted one", "status": "refuted", "reasons": []string{"moved the wrong way"}, "final": true})
	r := Build(s.in)
	for key, want := range map[string]string{"r1": "rejected", "r2": "rejected", "r3": "kept"} {
		if got := nodeOf(t, r, "rec:"+key).Status; got != want {
			t.Errorf("%s: %s, want %s", key, got, want)
		}
	}
	if f := nodeOf(t, r, "rec:r7"); f.Status != "refuted" || !hasEdge(r, "rec:r3", "rec:r7", "tests") {
		t.Fatalf("finding: %+v %v", f, r.Edges)
	}
}

func TestBuildNoHandIn(t *testing.T) {
	s := newStudy("grounding", "hypotheses")
	s.rec("grounding", "summary", "r1", 1, nil, map[string]any{"text": "Grounding's answer, not the last column's."})
	if r := Build(s.in); r.HandIn != nil {
		t.Fatalf("hand-in: %+v", r.HandIn)
	}
}

func TestBuildUnmatchedObjectionLinksToQuestion(t *testing.T) {
	s := newStudy("hypotheses", "validation")
	s.rec("hypotheses", "hypothesis", "r1", 1, nil, review.HypothesisBody{Text: "H"})
	s.rec("validation", "validation_status", "r2", 4, nil, map[string]any{"hypothesis": "H", "status": "supported", "final": true})
	o := s.rec("validation", "objection", "r3", 4, nil, review.Objection{TargetKey: "r99", Type: "wrong_test", Problem: "Wrong test.", Server: "ok"})
	r := Build(s.in)
	id := "obj:" + o.ID.String()
	if !hasEdge(r, id, "question", "challenges") || hasEdge(r, id, "rec:r2", "challenges") {
		t.Fatalf("edges: %v", r.Edges)
	}
}

// objectionStudy is a finished study with one hypothesis "H" (r1), an
// experiment run that saved fig.png (r2) and a value "m" read from it (r3),
// and a final finding on H (r5) measured by m.
func objectionStudy() *study {
	s := newStudy("hypotheses", "validation")
	s.rec("hypotheses", "hypothesis", "r1", 1, nil, review.HypothesisBody{Text: "H"})
	run := s.run("experimentation", "Experimenter")
	s.rec("experimentation", "output", "r2", 3, &run, map[string]any{"name": "fig.png"})
	s.rec("experimentation", "value", "r3", 3, &run, map[string]any{"name": "m", "value": "1", "file": "fig.png"})
	s.rec("validation", "assessment", "r4", 4, nil, review.Assessment{Hypothesis: "H", Measurement: "m"})
	s.rec("validation", "validation_status", "r5", 4, nil, map[string]any{"hypothesis": "H", "status": "supported", "final": true})
	return s
}

func TestBuildObjectionThroughHypothesisIsUpheld(t *testing.T) {
	s := objectionStudy()
	o := s.rec("validation", "objection", "r6", 4, nil, review.Objection{TargetKey: "r1", Type: "overreach", Problem: "Claims too much.", Server: "ok",
		Upheld: true, Checks: []review.Check{{ID: "wrong", By: "jev", Status: review.Pass}, {ID: "specific", By: "jev", Status: review.Pass}}})
	r := Build(s.in)
	id := "obj:" + o.ID.String()
	if n := nodeOf(t, r, id); n.Status != "upheld" {
		t.Fatalf("status %q, want upheld", n.Status)
	}
	if !hasEdge(r, id, "rec:r5", "challenges") || hasEdge(r, id, "question", "challenges") {
		t.Fatalf("edges: %v", r.Edges)
	}
}

func TestBuildObjectionThroughOutput(t *testing.T) {
	s := objectionStudy()
	o := s.rec("validation", "objection", "r6", 4, nil, review.Objection{TargetKey: "r2", Type: "unit_mismatch", Problem: "Units differ.", Server: "ok",
		Checks: []review.Check{{ID: "wrong", By: "jev", Status: review.Fail}, {ID: "specific", By: "jev", Status: review.Pass}}})
	r := Build(s.in)
	id := "obj:" + o.ID.String()
	if n := nodeOf(t, r, id); n.Status != "not_upheld" {
		t.Fatalf("status %q, want not_upheld", n.Status)
	}
	if !hasEdge(r, id, "rec:r5", "challenges") || hasEdge(r, id, "question", "challenges") {
		t.Fatalf("edges: %v", r.Edges)
	}
}

func TestBuildFindingIgnoresAnotherRoundsRerun(t *testing.T) {
	s := newStudy("hypotheses", "validation")
	s.rec("hypotheses", "hypothesis", "r1", 1, nil, review.HypothesisBody{Text: "H"})
	s.rec("validation", "rerun", "r2", 1, nil, map[string]any{"reproduced": map[string]bool{"m": true}})
	s.rec("validation", "validation_status", "r3", 2, nil, map[string]any{"hypothesis": "H", "status": "supported", "final": true})
	if f := nodeOf(t, Build(s.in), "rec:r3"); f.Rerun != nil {
		t.Fatalf("rerun from round 1 on a round-2 finding: %s", f.Rerun)
	}
}

// judgedStudy has one paper, one finding and a hand-in of 24 bytes.
func judgedStudy() *study {
	s := newStudy("grounding", "validation")
	s.rec("grounding", "paper", "r1", 1, nil, review.PaperBody{ID: "arxiv:1", Text: "abstract"})
	s.rec("validation", "validation_status", "r2", 2, nil, map[string]any{"hypothesis": "X raises Y", "status": "supported", "final": true})
	s.rec("validation", "summary", "r3", 2, nil, map[string]any{"text": "X raises Y, as found."})
	return s
}

func TestBuildAddsConceptsFactsAndContradicts(t *testing.T) {
	s := judgedStudy()
	s.in.Build = &db.ReportBuild{Status: "done", ChecksStatus: "done"}
	s.in.Concepts = []db.ReportConcept{{Key: "x", Label: "X", Type: "drug"}, {Key: "y", Label: "Y", Type: "disease"}}
	s.in.Facts = []db.ReportFact{
		{Subject: "x", Relation: "increases", Object: "y", Quote: "X raises Y", SourceNode: "rec:r2", P: 0.9},
		{Subject: "x", Relation: "mentioned_in", Object: "rec:r1", Quote: "abstract", SourceNode: "rec:r1", P: 0.8},
	}
	s.in.Checks = []db.ReportCheck{{NodeID: "rec:r2", CheckID: "novelty:rec:r1", Question: "q", Choice: "contradicts", P: 0.9, Status: review.Pass, Evidence: "abstract", Basis: "text"}}
	r := Build(s.in)
	x := nodeOf(t, r, "concept:x")
	if x.Kind != "concept" || x.Label != "X" || x.Status != "drug" || len(x.Facts) != 2 || x.Facts[0].Relation != "increases" {
		t.Fatalf("concept x %+v", x)
	}
	for _, e := range [][3]string{{"concept:x", "concept:y", "increases"}, {"concept:x", "rec:r1", "mentioned_in"}, {"rec:r2", "rec:r1", "contradicts"}} {
		if !hasEdge(r, e[0], e[1], e[2]) {
			t.Fatalf("missing edge %v in %+v", e, r.Edges)
		}
	}
	f := nodeOf(t, r, "rec:r2")
	if f.Novelty != "contradicts" || len(f.Checks) != 1 || f.Checks[0].Detail != "choice: contradicts\nabstract" || f.Checks[0].Basis != "text" || f.Checks[0].By != "jev" {
		t.Fatalf("finding %+v", f)
	}
}

func TestBuildNoveltyNewOnlyWhenAllUnrelated(t *testing.T) {
	s := judgedStudy()
	s.rec("grounding", "paper", "r4", 1, nil, review.PaperBody{ID: "arxiv:2", Text: "other"})
	chk := func(paper, choice, status string) db.ReportCheck {
		return db.ReportCheck{NodeID: "rec:r2", CheckID: "novelty:rec:" + paper, Choice: choice, P: 0.9, Status: status}
	}
	s.in.Checks = []db.ReportCheck{chk("r1", "unrelated", review.Pass), chk("r4", "unrelated", review.Pass)}
	if got := nodeOf(t, Build(s.in), "rec:r2").Novelty; got != "new" {
		t.Fatalf("all unrelated: %q", got)
	}
	s.in.Checks = []db.ReportCheck{chk("r1", "unrelated", review.Pass), chk("r4", "unrelated", review.Unclear)}
	if got := nodeOf(t, Build(s.in), "rec:r2").Novelty; got != "" {
		t.Fatalf("one unclear: %q", got)
	}
	s.in.Checks = []db.ReportCheck{chk("r1", "unrelated", review.Pass)} // r4 was never asked
	if got := nodeOf(t, Build(s.in), "rec:r2").Novelty; got != "" {
		t.Fatalf("unasked paper: %q", got)
	}
	s.in.Checks = []db.ReportCheck{chk("r1", "confirms", review.Pass), chk("r4", "unrelated", review.Pass)}
	if got := nodeOf(t, Build(s.in), "rec:r2").Novelty; got != "confirms" {
		t.Fatalf("confirms: %q", got)
	}
}

func TestBuildExtrasStates(t *testing.T) {
	for _, c := range []struct {
		build          *db.ReportBuild
		extras, checks string
		machine        string
	}{
		{&db.ReportBuild{Status: "queued", ChecksStatus: "pending"}, "pending", "pending", "lab-mac"},
		{&db.ReportBuild{Status: "judging", ChecksStatus: "pending"}, "pending", "pending", "lab-mac"},
		{&db.ReportBuild{Status: "done", ChecksStatus: "done"}, "ready", "ready", ""},
		{&db.ReportBuild{Status: "failed", ChecksStatus: "unavailable"}, "none", "unavailable", ""},
		{&db.ReportBuild{Status: "skipped", ChecksStatus: "done"}, "none", "ready", ""},
		{nil, "none", "none", ""},
	} {
		s := judgedStudy()
		s.in.Build, s.in.Machine = c.build, "lab-mac"
		r := Build(s.in)
		if r.Extras != c.extras || r.Checks != c.checks || r.Machine != c.machine {
			t.Fatalf("%+v: got extras %q checks %q machine %q", c.build, r.Extras, r.Checks, r.Machine)
		}
	}
}

func TestBuildLinksSortedAndInsideHandIn(t *testing.T) {
	s := judgedStudy() // hand-in "X raises Y, as found." is 21 bytes
	s.in.Build = &db.ReportBuild{Status: "done", ChecksStatus: "done"}
	s.in.Links = []db.ReportLink{
		{StartByte: 12, EndByte: 21, NodeID: "rec:r1", P: 0.8},
		{StartByte: 0, EndByte: 9, NodeID: "rec:r1", P: 0.9},
		{StartByte: 15, EndByte: 99, NodeID: "rec:r1", P: 0.9},    // past the end
		{StartByte: 3, EndByte: 3, NodeID: "rec:r1", P: 0.9},      // empty
		{StartByte: 0, EndByte: 5, NodeID: "rec:missing", P: 0.9}, // no such node
	}
	r := Build(s.in)
	if len(r.Links) != 2 || r.Links[0] != (Link{0, 9, "rec:r1", 0.9}) || r.Links[1].Start != 12 {
		t.Fatalf("links %+v", r.Links)
	}
}

func TestBuildKeepsOnlyLinksToGroundingSources(t *testing.T) {
	s := judgedStudy() // rec:r1 is a paper, rec:r2 a finding
	s.in.Build = &db.ReportBuild{Status: "done", ChecksStatus: "done"}
	s.in.Links = []db.ReportLink{
		{StartByte: 0, EndByte: 9, NodeID: "rec:r2", P: 0.9},
		{StartByte: 12, EndByte: 21, NodeID: "rec:r1", P: 0.8},
		{StartByte: 10, EndByte: 11, NodeID: "question", P: 0.9},
	}
	r := Build(s.in)
	if len(r.Links) != 1 || r.Links[0] != (Link{12, 21, "rec:r1", 0.8}) {
		t.Fatalf("links %+v, want only the paper's", r.Links)
	}
}

func TestBuildShowsBoundMeasurementsOnExactOutputAndFinding(t *testing.T) {
	for _, keyless := range []bool{false, true} {
		mode := "keyed"
		if keyless {
			mode = "keyless"
		}
		t.Run(mode, func(t *testing.T) {
			s := newStudy("experimentation", "validation")
			run := s.run("experimentation", "Experimenter")
			file := "outputs/result.json"
			s.rec("experimentation", "output", "o1", 1, &run, map[string]string{"name": file})
			s.rec("experimentation", "output", "o2", 1, &run, map[string]string{"name": file})
			s.rec("experimentation", "value", "v1", 1, &run, map[string]any{
				"name": "treated", "file": file, "field": "/difference/value", "measure": "difference", "value": "0.0150",
			})
			s.rec("experimentation", "value", "v2", 1, &run, map[string]any{
				"name": "unchecked", "file": file, "field": "/other/value", "measure": "difference", "value": "9",
			})
			res := review.Result{Decision: review.DecidePass, Items: []review.Item{{
				Key: "v1", Kind: "value", Status: review.Pass, Output: "o1",
				Measure: &review.Measure{ID: "difference", Name: "Mean difference", Unit: "seconds"},
				Checks: []review.Check{
					{ID: "bound", By: "server", Status: review.Pass},
					{ID: "measure", By: "server", Status: review.Pass},
				},
			}}}
			if keyless {
				res.Decision = ""
				s.rec("experimentation", "server_checks", "s1", 1, nil, map[string]any{
					"complete": true, "approval_decision": "other", "checks": map[string]any{},
				}).Role = "server"
				s.rec("experimentation", "server_checks", "s2", 1, nil, map[string]any{
					"complete": true, "approval_decision": "approved", "checks": res,
				}).Role = "server"
				s.rec("experimentation", "outcome", "a1", 1, nil, map[string]string{
					"verdict": "approved_by_owner", "decision_id": "approved",
				}).Role = "server"
			} else {
				s.rec("experimentation", "review", "a1", 1, nil, res).Role = "server"
			}
			s.rec("validation", "validation_status", "r1", 2, nil, map[string]any{
				"hypothesis": "A measurable difference", "status": "supported", "cites": []string{"v1", "v2", "v1"}, "final": true,
			})

			r := Build(s.in)
			want := Measurement{Record: "v1", Name: "treated", Value: "0.0150", Field: "/difference/value",
				Measure: review.Measure{ID: "difference", Name: "Mean difference", Unit: "seconds"}, Output: "o1"}
			for _, key := range []string{"rec:o1", "rec:r1"} {
				if got := nodeOf(t, r, key).Measurements; !slices.Equal(got, []Measurement{want}) {
					t.Errorf("%s measurements = %+v, want %+v", key, got, want)
				}
			}
			if got := nodeOf(t, r, "rec:o2").Measurements; len(got) != 0 {
				t.Errorf("same path from another saved output is not proof: %+v", got)
			}
			if !hasEdge(r, "rec:o1", "rec:r1", "supports") || hasEdge(r, "rec:o2", "rec:r1", "supports") {
				t.Errorf("report should link only the verified output: %+v", r.Edges)
			}
		})
	}
}

func TestBuildEstimateFinding(t *testing.T) {
	s := newStudy("hypotheses", "validation")
	lo, hi := 0.1, 0.2
	s.rec("hypotheses", "hypothesis", "r1", 1, nil, review.HypothesisBody{Text: "PR at 65", Prediction: review.Prediction{Shape: "estimate"},
		Measure: &review.Measure{ID: "prob_pr_65", Name: "PR probability at 65", Unit: "fraction"}, Method: "logistic regression on age",
		Reported: &review.ReportedValue{Value: 0.39, Source: "doi:10.1/a", Key: "p1"}})
	s.rec("hypotheses", "hypothesis", "r2", 1, nil, review.HypothesisBody{Text: "A range", Prediction: review.Prediction{Shape: "range", Low: &lo, High: &hi, Uncertainty: "u"}})
	s.rec("hypotheses", "review", "r3", 1, nil, review.Result{Column: "hypotheses", Decision: review.DecidePass, Items: []review.Item{
		{Key: "r1", Status: review.Pass}, {Key: "r2", Status: review.Pass}}})
	s.rec("validation", "validation_status", "r4", 4, nil, map[string]any{"hypothesis": "PR at 65", "status": "supported", "reasons": []string{"measured 0.395 fraction by logistic regression on age; reproduced on replay"}, "final": true})
	s.rec("validation", "validation_status", "r5", 4, nil, map[string]any{"hypothesis": "A range", "status": "supported", "final": true})
	r := Build(s.in)
	f := nodeOf(t, r, "rec:r4")
	want := NodeEstimate{Unit: "fraction", Method: "logistic regression on age", Reported: &review.ReportedValue{Value: 0.39, Source: "doi:10.1/a", Key: "p1"}}
	if f.Status != "supported" || f.Estimate == nil || f.Estimate.Unit != want.Unit || f.Estimate.Method != want.Method || *f.Estimate.Reported != *want.Reported {
		t.Fatalf("estimate finding: %+v %+v", f, f.Estimate)
	}
	if g := nodeOf(t, r, "rec:r5"); g.Estimate != nil {
		t.Fatalf("range finding got an estimate: %+v", g.Estimate)
	}
	raw, _ := json.Marshal(nodeOf(t, r, "rec:r5"))
	if strings.Contains(string(raw), `"estimate"`) {
		t.Fatalf("a range finding's JSON names estimate: %s", raw)
	}
}
