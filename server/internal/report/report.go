// Package report builds a finished study's report: the graph of its argument,
// made only from recorded rows, each node keeping the row it stands for.
package report

import (
	"encoding/json"
	"slices"
	"sort"
	"strings"
	"time"

	"github.com/google/uuid"

	"lykeion/server/internal/board"
	"lykeion/server/internal/records"
	"lykeion/server/internal/review"
	db "lykeion/server/pkg/db/generated"
)

type Report struct {
	CardID     uuid.UUID  `json:"card_id"`
	Number     int32      `json:"number"`
	Title      string     `json:"title"`
	Question   string     `json:"question"`    // the card's question, or its title when empty
	Stages     []string   `json:"stages"`      // board.Stages from scope_from to scope_to
	FinishedAt time.Time  `json:"finished_at"` // the newest outcome record in the last column
	HandIn     *HandIn    `json:"hand_in"`     // nil when the last column has no summary record
	Nodes      []Node     `json:"nodes"`
	Edges      []Edge     `json:"edges"`
	Steering   []Steering `json:"steering"` // comments and answered decisions, oldest first

	Links   []Link `json:"links"`   // phrases of the hand-in tied to nodes, by start
	Extras  string `json:"extras"`  // concepts and links: pending|ready|none
	Checks  string `json:"checks"`  // figure, table and novelty checks: pending|ready|unavailable|none
	Machine string `json:"machine"` // the machine the report run waits for, while Extras is pending
}

// Link ties the hand-in's bytes [Start, End) to a graph node.
type Link struct {
	Start int     `json:"start"`
	End   int     `json:"end"`
	Node  string  `json:"node"`
	P     float64 `json:"p"`
}

// Fact is a quoted statement about concepts, kept on its subject concept.
type Fact struct {
	Subject    string  `json:"subject"`
	Relation   string  `json:"relation"`
	Object     string  `json:"object"`
	Quote      string  `json:"quote"`
	SourceNode string  `json:"source_node"`
	P          float64 `json:"p"`
}

type HandIn struct {
	Text      string    `json:"text"`
	AgentName string    `json:"agent_name"`
	RunID     uuid.UUID `json:"run_id"`
	At        time.Time `json:"at"`
}

type Node struct {
	ID      string          `json:"id"`   // "question", or "rec:<record key>", "run:<agent_run id>", "out:<output name>", "obj:<record id>", "doubt:<column>:<item key>"
	Kind    string          `json:"kind"` // question|paper|dataset|hypothesis|experiment|output|finding|objection|doubt
	Label   string          `json:"label"`
	Status  string          `json:"status"`          // findings: supported|caveated|refuted|inconclusive|invalid; hypotheses: kept|rejected; objections: upheld|not_upheld; papers, datasets and doubts: pass|unclear|fail; else ""
	Reasons []string        `json:"reasons"`         // recorded reasons only
	Quote   string          `json:"quote"`           // the recorded text the node stands for
	Checks  []review.Check  `json:"checks"`          // checks recorded in the newest review of the node's item
	Rerun   json.RawMessage `json:"rerun,omitempty"` // findings: the Validation rerun body, when one ran
	RunID   *uuid.UUID      `json:"run_id"`
	Agent   string          `json:"agent"` // the producing run's agent name, "" when none
	Column  string          `json:"column"`

	// Facts are a concept's statements.
	Facts []Fact `json:"facts,omitempty"`
	// Novelty is a finding's standing against the grounding papers:
	// contradicts|confirms|new, or "" when not judged.
	Novelty string `json:"novelty,omitempty"`

	// ContentType is an output's recorded media type: it says whether the
	// output is a figure or a table.
	ContentType string `json:"content_type,omitempty"`
}

type Edge struct {
	From string `json:"from"`
	To   string `json:"to"`
	Kind string `json:"kind"` // tests|grounds|tested_by|produced|supports|challenges|contradicts|uses
}

type Steering struct {
	Kind   string    `json:"kind"` // comment|decision
	Author uuid.UUID `json:"author"`
	Text   string    `json:"text"`
	At     time.Time `json:"at"`
}

type builder struct {
	in    Input
	live  []db.Record
	byKey map[string]db.Record
	runs  map[uuid.UUID]db.StudyRunsRow
	nodes []Node
	order map[string]string // node id → the record or item key it sorts by
	edges []Edge
}

// Build makes the report from in's rows alone: no model, nothing inferred.
// A row a rule does not name stays out of the graph.
func Build(in Input) Report {
	c := in.Card
	b := &builder{in: in, byKey: map[string]db.Record{}, runs: map[uuid.UUID]db.StudyRunsRow{}, order: map[string]string{}}
	for _, r := range in.Records {
		if r.SupersededAt == nil {
			b.live = append(b.live, r)
			if r.Key != "" {
				b.byKey[r.Key] = r
			}
		}
	}
	for _, r := range in.Runs {
		b.runs[r.ID] = r
	}
	rep := Report{CardID: c.ID, Number: c.Number, Title: c.Title, Question: c.Question, Stages: scope(c), Steering: []Steering{}}
	if strings.TrimSpace(rep.Question) == "" {
		rep.Question = c.Title
	}
	if rep.Stages == nil {
		rep.Stages = []string{}
	}
	b.add(Node{ID: "question", Kind: "question", Label: rep.Question, Quote: rep.Question}, "")

	// Papers and datasets.
	grounding, _ := b.newestReview(board.Grounding, false)
	papers := map[string]db.Record{} // node id → paper record
	for _, r := range b.of(board.Grounding, review.KindPaper, review.KindDataset) {
		n := b.recNode(r, r.Kind)
		n.Label = review.LabelOf(r)
		n.Quote = records.Body[struct{ Text string }](r).Text
		if it, ok := itemOf(grounding, r.Key); ok {
			n.Status, n.Checks = it.Status, it.Checks
		}
		b.add(n, r.Key)
		b.edge("question", n.ID, "uses")
		if r.Kind == review.KindPaper {
			papers[n.ID] = r
		}
	}

	// Hypotheses.
	withdrawn := map[string]bool{}
	for _, r := range b.live {
		if r.Kind == review.KindWithdrawn {
			withdrawn[records.Body[review.WithdrawnBody](r).Item] = true
		}
	}
	hypReview, _ := b.newestReview(board.Hypotheses, false)
	type hyp struct {
		id      string
		rec     db.Record
		kept    bool
		grounds []string // the keys of the papers it cites
	}
	var hyps []hyp
	for _, r := range b.of(board.Hypotheses, review.KindHypothesis) {
		body := records.Body[review.HypothesisBody](r)
		n := b.recNode(r, "hypothesis")
		n.Label, n.Quote, n.Status = body.Text, body.Text, "kept"
		it, reviewed := itemOf(hypReview, r.Key)
		if reviewed {
			n.Checks = it.Checks
		}
		if withdrawn[r.Key] || reviewed && it.Status == review.Fail {
			n.Status = "rejected"
		}
		b.add(n, r.Key)
		b.edge("question", n.ID, "tests")
		h := hyp{id: n.ID, rec: r, kept: n.Status == "kept"}
		// Stored cites hold the cited paper's record key; a paper id is matched too.
		for _, pid := range sortedKeys(papers) {
			p := papers[pid]
			if slices.ContainsFunc(body.Cites, func(c string) bool {
				return c == p.Key || strings.EqualFold(strings.TrimSpace(c), review.LabelOf(p))
			}) {
				b.edge(pid, n.ID, "grounds")
				h.grounds = append(h.grounds, p.Key)
			}
		}
		hyps = append(hyps, h)
	}

	// Experiments: Experimentation runs that recorded something.
	firstKey := map[uuid.UUID]string{}
	for _, r := range b.live {
		if r.AgentRunID != nil && r.Key != "" {
			if k, ok := firstKey[*r.AgentRunID]; !ok || keyLess(r.Key, k) {
				firstKey[*r.AgentRunID] = r.Key
			}
		}
	}
	for _, run := range b.in.Runs {
		key, produced := firstKey[run.ID]
		if run.BoardColumn != board.Experimentation || !produced {
			continue
		}
		id := run.ID
		n := Node{ID: "run:" + id.String(), Kind: "experiment", Label: b.agentOf(&id), RunID: &id, Agent: b.agentOf(&id), Column: run.BoardColumn}
		b.add(n, key)
		for _, h := range hyps {
			if h.kept {
				b.edge(h.id, n.ID, "tested_by")
			}
		}
	}

	// Outputs.
	for _, r := range b.live {
		if r.Kind != "output" {
			continue
		}
		body := records.Body[struct {
			Name        string `json:"name"`
			ContentType string `json:"content_type"`
		}](r)
		n := b.recNode(r, "output")
		// ponytail: two outputs saved under one name share a node, the newest kept.
		n.ID, n.Label, n.ContentType = "out:"+body.Name, body.Name, body.ContentType
		b.add(n, r.Key)
		if r.AgentRunID != nil && b.has("run:"+r.AgentRunID.String()) {
			b.edge("run:"+r.AgentRunID.String(), n.ID, "produced")
		}
	}

	// Findings: the final statuses of the newest round that has them.
	type status struct {
		Hypothesis string   `json:"hypothesis"`
		Status     string   `json:"status"`
		Reasons    []string `json:"reasons"`
		Cites      []string `json:"cites"`
		Final      bool     `json:"final"`
	}
	var round int32 = -1
	for _, r := range b.of(board.Validation, "validation_status") {
		if records.Body[status](r).Final {
			round = max(round, r.Round)
		}
	}
	var rerun json.RawMessage
	for _, r := range b.of(board.Validation, "rerun") {
		if r.Round == round {
			rerun = r.Body
		}
	}
	valReview, _ := b.newestReview(board.Validation, false)
	assessments := map[string]review.Assessment{} // by hypothesis text, trimmed
	assessmentKeys := map[string][]string{}
	for _, r := range b.of(board.Validation, review.KindAssessment) {
		a := records.Body[review.Assessment](r)
		t := strings.TrimSpace(a.Hypothesis)
		assessments[t] = a
		assessmentKeys[t] = append(assessmentKeys[t], r.Key)
	}
	type finding struct {
		id    string
		text  string          // the hypothesis text, trimmed
		chain map[string]bool // the keys of the records it rests on
	}
	var findings []finding
	for _, r := range b.of(board.Validation, "validation_status") {
		s := records.Body[status](r)
		if !s.Final || r.Round != round {
			continue
		}
		n := b.recNode(r, "finding")
		n.Label, n.Quote, n.Status, n.Reasons, n.Rerun = s.Hypothesis, s.Hypothesis, s.Status, s.Reasons, rerun
		f := finding{id: n.ID, text: strings.TrimSpace(s.Hypothesis), chain: map[string]bool{}}
		for _, k := range s.Cites {
			f.chain[k] = true
		}
		for _, k := range assessmentKeys[f.text] {
			f.chain[k] = true
		}
		for _, h := range hyps {
			if strings.TrimSpace(records.Body[review.HypothesisBody](h.rec).Text) != f.text {
				continue
			}
			if it, ok := itemOf(valReview, h.rec.Key); ok {
				n.Checks = it.Checks
			}
			f.chain[h.rec.Key] = true
			for _, k := range h.grounds {
				f.chain[k] = true
			}
		}
		b.add(n, r.Key)
		for _, h := range hyps {
			if strings.TrimSpace(records.Body[review.HypothesisBody](h.rec).Text) == f.text {
				b.edge(h.id, n.ID, "tests")
			}
		}
		for _, k := range s.Cites {
			if v, ok := b.byKey[k]; ok && v.Kind == "value" {
				if file := records.Body[struct{ File string }](v).File; file != "" && b.has("out:"+file) {
					b.edge("out:"+file, n.ID, "supports")
				}
			}
		}
		findings = append(findings, f)
	}

	// Objections the server accepted, from the findings' round.
	values := b.of("", "value")
	measures := func(f finding) []string {
		a, ok := assessments[f.text]
		if !ok {
			return nil
		}
		out := []string{a.Measurement}
		if a.Control != nil {
			out = append(out, *a.Control)
		}
		return out
	}
	for _, r := range b.of(board.Validation, "objection") {
		o := records.Body[review.Objection](r)
		if o.Server != "ok" || r.Round != round {
			continue
		}
		n := b.recNode(r, "objection")
		n.ID, n.Label, n.Quote, n.Checks = "obj:"+r.ID.String(), strings.ReplaceAll(o.Type, "_", " "), o.Problem, o.Checks
		n.Status = "not_upheld"
		if o.Upheld {
			n.Status = "upheld"
		}
		b.add(n, r.Key)
		target, found := b.byKey[o.TargetKey]
		linked := false
		for _, f := range findings {
			hit := false
			switch {
			case !found:
			case target.Kind == review.KindHypothesis:
				hit = strings.TrimSpace(records.Body[review.HypothesisBody](target).Text) == f.text
			case target.Kind == "value":
				hit = slices.Contains(measures(f), records.Body[struct{ Name string }](target).Name)
			case target.Kind == "output":
				name := records.Body[struct{ Name string }](target).Name
				hit = slices.ContainsFunc(values, func(v db.Record) bool {
					x := records.Body[struct{ Name, File string }](v)
					return x.File == name && slices.Contains(measures(f), x.Name)
				})
			}
			if hit {
				b.edge(n.ID, f.id, "challenges")
				linked = true
			}
		}
		if !linked {
			b.edge(n.ID, "question", "challenges")
		}
	}

	// Open doubts: the unclear Jev questions of each column's newest passed review.
	for _, col := range board.Stages {
		res, ok := b.newestReview(col, true)
		if !ok {
			continue
		}
		for _, it := range res.Items {
			d := review.OpenDoubts(review.Result{Items: []review.Item{it}})
			if len(d) != 1 {
				continue
			}
			n := Node{ID: "doubt:" + col + ":" + it.Key, Kind: "doubt", Label: it.Label, Status: it.Status, Quote: d[0], Checks: it.Checks, Column: col}
			b.add(n, it.Key)
			linked := false
			for _, f := range findings {
				if f.chain[it.Key] {
					b.edge(n.ID, f.id, "challenges")
					linked = true
				}
			}
			if !linked {
				b.edge(n.ID, "question", "challenges")
			}
		}
	}

	b.judgedChecks()

	if r, ok := b.newest(c.ScopeTo, "summary"); ok {
		rep.HandIn = &HandIn{Text: records.Body[struct{ Text string }](r).Text, AgentName: b.agentOf(r.AgentRunID), At: r.CreatedAt}
		if r.AgentRunID != nil {
			rep.HandIn.RunID = *r.AgentRunID
		}
	}
	if r, ok := b.newest(c.ScopeTo, "outcome"); ok {
		rep.FinishedAt = r.CreatedAt
	}
	for _, cm := range in.Comments {
		rep.Steering = append(rep.Steering, Steering{Kind: "comment", Author: cm.AuthorID, Text: cm.Body, At: cm.CreatedAt})
	}
	for _, it := range in.Decided {
		s := Steering{Kind: "decision", At: it.CreatedAt}
		if it.DecidedBy != nil {
			s.Author = *it.DecidedBy
		}
		if it.DecidedAt != nil {
			s.At = *it.DecidedAt
		}
		var parts []string
		for _, p := range []string{it.Text, it.ChosenOption, it.Guidance} {
			if p = strings.TrimSpace(p); p != "" {
				parts = append(parts, p)
			}
		}
		s.Text = strings.Join(parts, "\n")
		rep.Steering = append(rep.Steering, s)
	}
	sort.SliceStable(rep.Steering, func(i, j int) bool { return rep.Steering[i].At.Before(rep.Steering[j].At) })

	sort.SliceStable(b.nodes, func(i, j int) bool {
		a, c := b.nodes[i], b.nodes[j]
		if sa, sc := board.StageIndex(a.Column), board.StageIndex(c.Column); sa != sc {
			return sa < sc
		}
		return keyLess(b.order[a.ID], b.order[c.ID])
	})
	b.concepts()
	rep.Nodes, rep.Edges = b.nodes, b.edges
	rep.Links, rep.Extras, rep.Checks, rep.Machine = b.links(rep.HandIn), "none", "none", ""
	if bd := in.Build; bd != nil {
		switch bd.Status {
		case "queued", "judging":
			rep.Extras, rep.Machine = "pending", in.Machine
		case "done":
			rep.Extras = "ready"
		}
		rep.Checks = map[string]string{"pending": "pending", "done": "ready", "unavailable": "unavailable"}[bd.ChecksStatus]
		if rep.Checks == "" {
			rep.Checks = "none"
		}
	}
	if rep.Edges == nil {
		rep.Edges = []Edge{}
	}
	return rep
}

// of are the live records of the kinds, in column ("" for any), oldest first.
func (b *builder) of(column string, kinds ...string) []db.Record {
	var out []db.Record
	for _, r := range b.live {
		if (column == "" || r.BoardColumn == column) && slices.Contains(kinds, r.Kind) {
			out = append(out, r)
		}
	}
	return out
}

// newest is the live record of the kind in column with the highest round,
// then the latest.
func (b *builder) newest(column, kind string) (db.Record, bool) {
	var best db.Record
	found := false
	for _, r := range b.of(column, kind) {
		if !found || r.Round > best.Round || r.Round == best.Round && !r.CreatedAt.Before(best.CreatedAt) {
			best, found = r, true
		}
	}
	return best, found
}

// newestReview is column's newest review; passed keeps only reviews that let
// the study move on.
func (b *builder) newestReview(column string, passed bool) (review.Result, bool) {
	var best db.Record
	found := false
	for _, r := range b.of(column, "review") {
		if d := records.Body[review.Result](r).Decision; passed && d != review.DecidePass && d != review.DecideCarry {
			continue
		}
		if !found || r.Round > best.Round || r.Round == best.Round && !r.CreatedAt.Before(best.CreatedAt) {
			best, found = r, true
		}
	}
	if !found {
		return review.Result{}, false
	}
	return records.Body[review.Result](best), true
}

func itemOf(res review.Result, key string) (review.Item, bool) {
	for _, it := range res.Items {
		if it.Key == key && key != "" {
			return it, true
		}
	}
	return review.Item{}, false
}

func (b *builder) recNode(r db.Record, kind string) Node {
	return Node{ID: "rec:" + r.Key, Kind: kind, RunID: r.AgentRunID, Agent: b.agentOf(r.AgentRunID), Column: r.BoardColumn}
}

func (b *builder) agentOf(run *uuid.UUID) string {
	if run == nil {
		return ""
	}
	if r, ok := b.runs[*run]; ok && r.AgentID != nil {
		return b.in.Agents[*r.AgentID]
	}
	return ""
}

// add keeps n, replacing any node with its id.
func (b *builder) add(n Node, key string) {
	if n.Reasons == nil {
		n.Reasons = []string{}
	}
	if n.Checks == nil {
		n.Checks = []review.Check{}
	}
	b.order[n.ID] = key
	for i := range b.nodes {
		if b.nodes[i].ID == n.ID {
			b.nodes[i] = n
			return
		}
	}
	b.nodes = append(b.nodes, n)
}

func (b *builder) has(id string) bool {
	_, ok := b.order[id]
	return ok
}

func (b *builder) edge(from, to, kind string) {
	e := Edge{From: from, To: to, Kind: kind}
	if !slices.Contains(b.edges, e) {
		b.edges = append(b.edges, e)
	}
}

// keyLess orders record keys as numbers: r2 before r10.
func keyLess(a, b string) bool {
	if len(a) != len(b) {
		return len(a) < len(b)
	}
	return a < b
}

func sortedKeys(m map[string]db.Record) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Slice(out, func(i, j int) bool { return keyLess(out[i], out[j]) })
	return out
}

// judgedChecks appends each recorded figure, table and novelty check to its
// node, and sets each finding's novelty from its papers' checks.
func (b *builder) judgedChecks() {
	unrelated := map[string]map[string]bool{} // finding id → papers with a passed unrelated check
	contradicted := map[string]bool{}
	confirmed := map[string]bool{}
	for _, c := range b.in.Checks {
		i := slices.IndexFunc(b.nodes, func(n Node) bool { return n.ID == c.NodeID })
		if i < 0 {
			continue
		}
		ck := review.Check{ID: c.CheckID, By: "jev", Question: c.Question, P: &c.P, Status: c.Status, Detail: c.Evidence, Basis: c.Basis}
		if paper, ok := strings.CutPrefix(c.CheckID, "novelty:"); ok {
			ck.Detail = "choice: " + c.Choice + "\n" + c.Evidence
			if c.Status == review.Pass {
				switch c.Choice {
				case "contradicts":
					contradicted[c.NodeID] = true
					if b.has(paper) {
						b.edge(c.NodeID, paper, "contradicts")
					}
				case "confirms":
					confirmed[c.NodeID] = true
				case "unrelated":
					if unrelated[c.NodeID] == nil {
						unrelated[c.NodeID] = map[string]bool{}
					}
					unrelated[c.NodeID][paper] = true
				}
			}
		}
		b.nodes[i].Checks = append(b.nodes[i].Checks, ck)
	}
	for i, n := range b.nodes {
		switch {
		case contradicted[n.ID]:
			b.nodes[i].Novelty = "contradicts"
		case confirmed[n.ID]:
			b.nodes[i].Novelty = "confirms"
		case b.allPapersUnrelated(unrelated[n.ID]):
			b.nodes[i].Novelty = "new"
		}
	}
}

// concepts adds a node per recorded concept, keeping its facts, and an edge
// per fact whose ends are in the graph.
func (b *builder) concepts() {
	known := map[string]bool{}
	for _, c := range b.in.Concepts {
		known[c.Key] = true
	}
	factsOf := map[string][]Fact{}
	for _, f := range b.in.Facts {
		factsOf[f.Subject] = append(factsOf[f.Subject], Fact{f.Subject, f.Relation, f.Object, f.Quote, f.SourceNode, f.P})
		from := "concept:" + f.Subject
		switch {
		case !known[f.Subject]:
		case f.Relation == "mentioned_in" && b.has(f.Object):
			b.edge(from, f.Object, f.Relation)
		case f.Relation != "mentioned_in" && known[f.Object]:
			b.edge(from, "concept:"+f.Object, f.Relation)
		}
	}
	cs := slices.Clone(b.in.Concepts)
	sort.Slice(cs, func(i, j int) bool { return cs[i].Key < cs[j].Key })
	for _, c := range cs {
		// Concepts have no column, so they come after every stage's nodes.
		b.order["concept:"+c.Key] = c.Key
		b.nodes = append(b.nodes, Node{ID: "concept:" + c.Key, Kind: "concept", Label: c.Label, Status: c.Type,
			Reasons: []string{}, Checks: []review.Check{}, Facts: factsOf[c.Key]})
	}
}

// links are the recorded links that lie inside the hand-in and name a node,
// by start.
func (b *builder) links(h *HandIn) []Link {
	out := []Link{}
	if h == nil {
		return out
	}
	for _, l := range b.in.Links {
		if l.StartByte >= 0 && l.StartByte < l.EndByte && int(l.EndByte) <= len(h.Text) && b.has(l.NodeID) {
			out = append(out, Link{int(l.StartByte), int(l.EndByte), l.NodeID, l.P})
		}
	}
	sort.SliceStable(out, func(i, j int) bool { return out[i].Start < out[j].Start })
	return out
}

// allPapersUnrelated says whether every paper in the graph is in done; a
// paper Jev was never asked about keeps a finding from being called new.
func (b *builder) allPapersUnrelated(done map[string]bool) bool {
	papers := 0
	for _, n := range b.nodes {
		if n.Kind == "paper" {
			if !done[n.ID] {
				return false
			}
			papers++
		}
	}
	return papers > 0
}
