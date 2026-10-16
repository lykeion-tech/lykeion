package hyp

import (
	"context"
	"encoding/json"
	"fmt"
	"regexp"
	"slices"
	"strings"

	"lykeion/server/internal/board"
	"lykeion/server/internal/ground"
	"lykeion/server/internal/predicate"
	"lykeion/server/internal/records"
	"lykeion/server/internal/service"
	"lykeion/server/internal/store"
	db "lykeion/server/pkg/db/generated"
)

const (
	Decomposer   = "decomposer"
	NoveltyScout = "novelty-scout"
	Selector     = "selector"
	Reviewer     = "hypotheses-reviewer"
)

// Generators run in parallel, each from its own seed; they share one skill.
var Generators = []string{"generator-1", "generator-2", "generator-3"}

// Phases: the generators; the decomposer; the novelty scout; the selector.
// Between them the server seeds, removes duplicates and compares (Serve).
var Phases = [][]string{Generators, {Decomposer}, {NoveltyScout}, {Selector}}

// MaxCandidates bounds what one generator proposes in a round.
const MaxCandidates = 3

// Cited is a mechanism step or an assumption with the Grounding claims that
// back it.
type Cited struct {
	Text   string   `json:"text"`
	Claims []string `json:"claims"`
}

// Candidate is one hypothesis record, after K-Dense's hypothesis-generation
// record: a statement, its mechanism, assumptions, boundary conditions and
// uncertainties. Its predictions live in the decomposition and its rivals in
// the matrix.
type Candidate struct {
	Statement          string   `json:"statement"`
	Kind               string   `json:"kind"` // mechanism | artefact
	Mechanism          []Cited  `json:"mechanism"`
	Assumptions        []Cited  `json:"assumptions"`
	BoundaryConditions []string `json:"boundary_conditions"`
	Uncertainties      []string `json:"uncertainties"`
	Seed               *Seed    `json:"seed,omitempty"` // set by the server
}

type MissingPremise struct {
	Gap  string `json:"gap"`
	Step string `json:"step"` // the Grounding role that must find it
}

type Assumption struct {
	ID     string   `json:"id"`
	Text   string   `json:"text"`
	Claims []string `json:"claims"` // live Grounding evidence backing this assumption
}

type Prediction struct {
	ID   string `json:"id"`
	Text string `json:"text"`
}

// Test is a falsification test: a null whose rejection would bear on the
// targeted assumption or prediction, run as an analysis of a Grounding
// dataset.
type Test struct {
	ID          string `json:"id"`
	Targets     string `json:"targets"`
	Null        string `json:"null"`
	Alternative string `json:"alternative"`
	Procedure   string `json:"procedure"`
}

type Decomposition struct {
	Candidate   string       `json:"candidate"`
	Assumptions []Assumption `json:"assumptions"`
	Predictions []Prediction `json:"predictions"`
	Tests       []Test       `json:"tests"`
}

type Closest struct {
	SourceID string `json:"source_id"`
	Quote    string `json:"quote"`
}

type Novelty struct {
	Candidate string   `json:"candidate"`
	Status    string   `json:"status"` // already_stated | related | unknown
	Queries   []string `json:"queries"`
	Closest   *Closest `json:"closest,omitempty"`
}

type Comparison struct {
	A      string `json:"a"`
	B      string `json:"b"`
	Winner string `json:"winner"`
	Reason string `json:"reason"`
}

type Ranking struct {
	Judgments []Judgment         `json:"judgments"`
	Scores    map[string]float64 `json:"scores"`
	Order     []string           `json:"order"`
}

type ResultKey struct {
	Name        string `json:"name"`
	Description string `json:"description"`
	Unit        string `json:"unit"`
}

// Measurement is the one measurement every rival is judged on.
type Measurement struct {
	ID          string      `json:"id"`
	Dataset     string      `json:"dataset"`             // a Grounding dataset key, D1…
	Accession   string      `json:"accession,omitempty"` // set by the server
	Unit        string      `json:"unit,omitempty"`      // set by the server from the Grounding gate
	Description string      `json:"description"`
	Keys        []ResultKey `json:"keys"`
	Ordered     []string    `json:"ordered,omitempty"` // keys that are never decreasing in this order
}

// Region is an equivalence region on one result key: low <= key <= high.
type Region struct {
	Key  string  `json:"key"`
	Low  float64 `json:"low"`
	High float64 `json:"high"`
}

type Rival struct {
	ID            string `json:"id"`
	Kind          string `json:"kind"` // null | mechanism | artefact
	Candidate     string `json:"candidate,omitempty"`
	Statement     string `json:"statement"`
	Measurement   string `json:"measurement"`
	Prediction    string `json:"prediction"`
	Equivalence   Region `json:"equivalence"`
	Falsifier     string `json:"falsifier"`
	Indeterminate string `json:"indeterminate"`
}

type Matrix struct {
	Measurement Measurement `json:"measurement"`
	Rivals      []Rival     `json:"rivals"`
	Rationale   string      `json:"rationale"`
}

type Dedup struct {
	Survivors []string          `json:"survivors"`
	Merged    map[string]string `json:"merged"`
	Method    string            `json:"method"`
	Threshold float64           `json:"threshold"`
}

type searchLog struct {
	Query   string   `json:"query"`
	Results []string `json:"results"`
}

var (
	assumptionID  = regexp.MustCompile(`^A[0-9]{1,3}$`)
	predictionID  = regexp.MustCompile(`^P[0-9]{1,3}$`)
	testID        = regexp.MustCompile(`^T[0-9]{1,3}$`)
	rivalID       = regexp.MustCompile(`^R[0-9]{1,2}$`)
	measurementID = regexp.MustCompile(`^M[0-9]{1,2}$`)
	candidateRef  = regexp.MustCompile(`\bH[0-9]+\b`)
)

var premiseSteps = []string{ground.Literature, ground.Contrarian, ground.DataScout, ground.Methods}

func blank(s string) bool { return strings.TrimSpace(s) == "" }

// column lists another column's live records for the writer's card.
func column(ctx context.Context, tx *store.Tx, w service.Writer, col string) ([]db.Record, error) {
	return tx.Q.ListRecords(ctx, db.ListRecordsParams{WorkspaceID: tx.WS, CardID: w.Card, BoardColumn: col})
}

func last(recs []db.Record, kind, key string) (db.Record, bool) {
	for i := len(recs) - 1; i >= 0; i-- {
		if recs[i].Kind == kind && recs[i].Key == key {
			return recs[i], true
		}
	}
	return db.Record{}, false
}

func keysOf(recs []db.Record, kind string) map[string]bool {
	out := map[string]bool{}
	for _, r := range recs {
		if r.Kind == kind {
			out[r.Key] = true
		}
	}
	return out
}

func survivorsIn(recs []db.Record) []string {
	r, ok := last(recs, "dedup", "")
	if !ok {
		return nil
	}
	return records.Body[Dedup](r).Survivors
}

func pairKey(a, b string) string {
	if b < a {
		a, b = b, a
	}
	return a + "|" + b
}

func checkCited(what string, cs []Cited, ev map[string]bool) error {
	if len(cs) == 0 {
		return service.Invalid("at least one " + what + " is required")
	}
	for _, c := range cs {
		if blank(c.Text) {
			return service.Invalid(what + " text is required")
		}
		if len(c.Claims) == 0 {
			return service.Invalid(fmt.Sprintf("the %s %q cites no Grounding claim; if no claim backs it, record `lyk hyp missing-premise --gap \"…\"`", what, c.Text))
		}
		for _, id := range c.Claims {
			if !ev[id] {
				return service.Invalid(id + " is not a live Grounding claim; if the premise is missing, record `lyk hyp missing-premise --gap \"…\"`")
			}
		}
	}
	return nil
}

func prepareCandidate(ctx context.Context, tx *store.Tx, w service.Writer, raw json.RawMessage) (json.RawMessage, error) {
	c, err := ground.Decode[Candidate](raw)
	switch {
	case err != nil:
		return nil, err
	case c.Seed != nil:
		return nil, service.Invalid("the seed is set by the server")
	case blank(c.Statement):
		return nil, service.Invalid("statement is required")
	case c.Kind != "mechanism" && c.Kind != "artefact":
		return nil, service.Invalid("kind must be mechanism or artefact")
	}
	g, err := column(ctx, tx, w, board.Grounding)
	if err != nil {
		return nil, err
	}
	ev := keysOf(g, "evidence")
	if err := checkCited("mechanism step", c.Mechanism, ev); err != nil {
		return nil, err
	}
	if err := checkCited("assumption", c.Assumptions, ev); err != nil {
		return nil, err
	}
	own, err := column(ctx, tx, w, w.Column)
	if err != nil {
		return nil, err
	}
	n := 0
	for _, r := range own {
		if r.Kind == "candidate" && r.Role == w.Role {
			n++
		}
	}
	if n >= MaxCandidates {
		return nil, service.Invalid(fmt.Sprintf("a generator proposes at most %d candidates", MaxCandidates))
	}
	if s, ok := last(own, "seed", w.Role); ok {
		seed := records.Body[Seed](s)
		c.Seed = &seed
	}
	return json.Marshal(c)
}

func prepareMissing(_ context.Context, _ *store.Tx, _ service.Writer, raw json.RawMessage) (json.RawMessage, error) {
	m, err := ground.Decode[MissingPremise](raw)
	if err != nil {
		return nil, err
	}
	if blank(m.Gap) {
		return nil, service.Invalid("name the gap: the fact Grounding must establish")
	}
	if m.Step == "" {
		m.Step = ground.Literature
	}
	if !slices.Contains(premiseSteps, m.Step) {
		return nil, service.Invalid("step must be one of " + strings.Join(premiseSteps, ", "))
	}
	return json.Marshal(m)
}

func standsAlone(what, text string) error {
	if blank(text) {
		return service.Invalid(what + " text is required")
	}
	if candidateRef.MatchString(text) {
		return service.Invalid(what + " must stand on its own: do not refer to candidates by id")
	}
	return nil
}

func prepareDecomposition(ctx context.Context, tx *store.Tx, w service.Writer, raw json.RawMessage) (json.RawMessage, error) {
	d, err := ground.Decode[Decomposition](raw)
	if err != nil {
		return nil, err
	}
	own, err := column(ctx, tx, w, w.Column)
	if err != nil {
		return nil, err
	}
	if !slices.Contains(survivorsIn(own), d.Candidate) {
		return nil, service.Invalid(d.Candidate + " is not a surviving candidate: read the dedup record")
	}
	if len(d.Assumptions) == 0 || len(d.Predictions) == 0 || len(d.Tests) == 0 {
		return nil, service.Invalid("record at least one assumption, prediction and falsification test")
	}
	g, err := column(ctx, tx, w, board.Grounding)
	if err != nil {
		return nil, err
	}
	ev := keysOf(g, "evidence")
	seen := map[string]bool{}
	for _, a := range d.Assumptions {
		if !assumptionID.MatchString(a.ID) || seen[a.ID] {
			return nil, service.Invalid("assumption ids are A1, A2… and unique")
		}
		seen[a.ID] = true
		if err := standsAlone("assumption "+a.ID, a.Text); err != nil {
			return nil, err
		}
		if err := checkCited("assumption "+a.ID, []Cited{{Text: a.Text, Claims: a.Claims}}, ev); err != nil {
			return nil, err
		}
	}
	for _, p := range d.Predictions {
		if !predictionID.MatchString(p.ID) || seen[p.ID] {
			return nil, service.Invalid("prediction ids are P1, P2… and unique")
		}
		seen[p.ID] = true
		if err := standsAlone("prediction "+p.ID, p.Text); err != nil {
			return nil, err
		}
	}
	for _, t := range d.Tests {
		if !testID.MatchString(t.ID) || seen[t.ID] {
			return nil, service.Invalid("test ids are T1, T2… and unique")
		}
		if !seen[t.Targets] || testID.MatchString(t.Targets) {
			return nil, service.Invalid(t.ID + " must target one of this record's assumptions or predictions")
		}
		seen[t.ID] = true
		if blank(t.Null) || blank(t.Alternative) || blank(t.Procedure) {
			return nil, service.Invalid(t.ID + " needs a null, an alternative and a procedure")
		}
	}
	return raw, nil
}

func prepareNovelty(ctx context.Context, tx *store.Tx, w service.Writer, raw json.RawMessage) (json.RawMessage, error) {
	n, err := ground.Decode[Novelty](raw)
	if err != nil {
		return nil, err
	}
	switch n.Status {
	case "already_stated", "related", "unknown":
	case "novel":
		return nil, service.Invalid("never record novel: a search that finds no prior work records unknown")
	default:
		return nil, service.Invalid("status must be already_stated, related or unknown")
	}
	if len(n.Queries) == 0 || len(n.Queries) > 3 {
		return nil, service.Invalid("list the 1 to 3 queries you ran for this candidate")
	}
	own, err := column(ctx, tx, w, w.Column)
	if err != nil {
		return nil, err
	}
	if !slices.Contains(survivorsIn(own), n.Candidate) {
		return nil, service.Invalid(n.Candidate + " is not a surviving candidate: read the dedup record")
	}
	found := map[string]bool{} // query → returned something
	for _, r := range own {
		if r.Kind == "search" && r.Role == NoveltyScout {
			s := records.Body[searchLog](r)
			found[s.Query] = found[s.Query] || len(s.Results) > 0
		}
	}
	hits := false
	for _, q := range n.Queries {
		hit, ran := found[q]
		if !ran {
			return nil, service.Invalid(fmt.Sprintf("the query %q never ran through search_literature", q))
		}
		hits = hits || hit
	}
	if n.Status == "unknown" {
		if n.Closest != nil {
			return nil, service.Invalid("unknown names no closest source")
		}
		return raw, nil
	}
	if !hits {
		return nil, service.Invalid("every query came back empty, so the status is unknown")
	}
	if n.Closest == nil || blank(n.Closest.Quote) {
		return nil, service.Invalid("already_stated and related name the closest source and quote it")
	}
	if _, ok := last(own, "fetch", n.Closest.SourceID); !ok {
		return nil, service.Invalid(n.Closest.SourceID + " was never fetched through the sources server")
	}
	return raw, nil
}

func prepareComparison(ctx context.Context, tx *store.Tx, w service.Writer, raw json.RawMessage) (json.RawMessage, error) {
	c, err := ground.Decode[Comparison](raw)
	if err != nil {
		return nil, err
	}
	own, err := column(ctx, tx, w, w.Column)
	if err != nil {
		return nil, err
	}
	sv := survivorsIn(own)
	switch {
	case c.A == c.B || !slices.Contains(sv, c.A) || !slices.Contains(sv, c.B):
		return nil, service.Invalid("compare two different surviving candidates")
	case c.Winner != c.A && c.Winner != c.B:
		return nil, service.Invalid("the winner must be a or b")
	case blank(c.Reason):
		return nil, service.Invalid("give the reason")
	}
	if _, ok := last(own, "pairwise", pairKey(c.A, c.B)); ok {
		return nil, service.Invalid("Jev already judged " + pairKey(c.A, c.B) + ": judge only the pairs `lyk hyp rank` names")
	}
	return raw, nil
}

// judgments gathers every pairwise judgment: Jev's, and the selector's for
// the pairs Jev did not judge. A later judgment of a pair replaces an earlier.
func judgments(recs []db.Record) []Judgment {
	out := []Judgment{}
	at := map[string]int{}
	add := func(j Judgment) {
		k := pairKey(j.A, j.B)
		if i, ok := at[k]; ok {
			out[i] = j
			return
		}
		at[k] = len(out)
		out = append(out, j)
	}
	for _, r := range recs {
		switch r.Kind {
		case "pairwise":
			add(records.Body[Judgment](r))
		case "comparison":
			c := records.Body[Comparison](r)
			pa := 0.0
			if c.Winner == c.A {
				pa = 1
			}
			add(Judgment{A: c.A, B: c.B, PA: pa, Judge: "selector", Reason: c.Reason})
		}
	}
	return out
}

// rank fits the survivors' judgments, naming the pairs nobody judged.
func rank(items []string, js []Judgment) (Ranking, error) {
	if len(items) == 0 {
		return Ranking{}, service.Invalid("no surviving candidates to rank")
	}
	use := []Judgment{}
	have := map[string]bool{}
	for _, j := range js {
		if slices.Contains(items, j.A) && slices.Contains(items, j.B) {
			use = append(use, j)
			have[pairKey(j.A, j.B)] = true
		}
	}
	var missing []string
	for i, a := range items {
		for _, b := range items[i+1:] {
			if !have[pairKey(a, b)] {
				missing = append(missing, pairKey(a, b))
			}
		}
	}
	if len(missing) > 0 {
		return Ranking{}, service.Invalid("judge these pairs first with `lyk hyp compare`: " + strings.Join(missing, ", "))
	}
	scores, err := Fit(items, use)
	if err != nil {
		return Ranking{}, err
	}
	return Ranking{Judgments: use, Scores: scores, Order: Order(scores)}, nil
}

// prepareRanking takes an empty body: the server fits the ranking.
func prepareRanking(ctx context.Context, tx *store.Tx, w service.Writer, raw json.RawMessage) (json.RawMessage, error) {
	if _, err := ground.Decode[struct{}](raw); err != nil {
		return nil, err
	}
	own, err := column(ctx, tx, w, w.Column)
	if err != nil {
		return nil, err
	}
	rk, err := rank(survivorsIn(own), judgments(own))
	if err != nil {
		return nil, err
	}
	return json.Marshal(rk)
}

// groundingUnit is the unit of analysis the passing Grounding gate chose for
// a dataset: what one observation represents, and so what may count as n.
func groundingUnit(recs []db.Record, dataset string) string {
	unit := ""
	for _, r := range recs {
		if r.Kind != "gate" {
			continue
		}
		g := records.Body[board.Gate](r)
		if g.Verdict != board.VPass {
			continue
		}
		unit = ""
		for _, ch := range g.Checks {
			if ch.ID == "unit."+dataset && ch.Result == board.Pass {
				unit = ch.Choice
			}
		}
	}
	if unit == "unclear" {
		return ""
	}
	return unit
}

// shape checks the matrix's structure: ids, kinds, one measurement, declared
// keys, and predicates that parse over them. What the predicates mean is the
// gate's to judge.
func shape(m Matrix) error {
	me := m.Measurement
	if !measurementID.MatchString(me.ID) || blank(me.Description) {
		return service.Invalid("the measurement needs an id M1… and a description")
	}
	if len(me.Keys) == 0 || len(me.Keys) > 8 {
		return service.Invalid("declare 1 to 8 result keys")
	}
	declared := map[string]bool{}
	for _, k := range me.Keys {
		if !predicate.ValidKey(k.Name) || declared[k.Name] || blank(k.Description) {
			return service.Invalid(fmt.Sprintf("result keys are unique lowercase names (a-z, 0-9, _) with a description: %q", k.Name))
		}
		declared[k.Name] = true
	}
	for _, k := range me.Ordered {
		if !declared[k] {
			return service.Invalid("ordered names an undeclared key " + k)
		}
	}
	if len(m.Rivals) == 0 {
		return service.Invalid("no rivals")
	}
	seen := map[string]bool{}
	for _, r := range m.Rivals {
		if !rivalID.MatchString(r.ID) || seen[r.ID] {
			return service.Invalid("rival ids are R0, R1… and unique")
		}
		seen[r.ID] = true
		switch {
		case r.Kind == "null" && r.Candidate != "":
			return service.Invalid(r.ID + ": the null rival has no candidate")
		case r.Kind != "null" && r.Kind != "mechanism" && r.Kind != "artefact":
			return service.Invalid(r.ID + ": kind must be null, mechanism or artefact")
		case r.Kind != "null" && r.Candidate == "":
			return service.Invalid(r.ID + ": name the candidate it carries")
		case blank(r.Statement):
			return service.Invalid(r.ID + ": statement is required")
		case r.Measurement != me.ID:
			return service.Invalid(r.ID + " must use the matrix's measurement " + me.ID)
		case !declared[r.Equivalence.Key] || !(r.Equivalence.Low < r.Equivalence.High):
			return service.Invalid(r.ID + ": the equivalence region needs a declared key and low < high")
		}
		for _, p := range []struct{ name, text string }{{"prediction", r.Prediction}, {"falsifier", r.Falsifier}, {"indeterminate", r.Indeterminate}} {
			pr, err := predicate.Parse(p.text)
			if err != nil {
				return service.Invalid(fmt.Sprintf("%s %s: %v", r.ID, p.name, err))
			}
			for _, k := range pr.Keys() {
				if !declared[k] {
					return service.Invalid(fmt.Sprintf("%s %s reads undeclared key %s", r.ID, p.name, k))
				}
			}
		}
	}
	return nil
}

func prepareMatrix(ctx context.Context, tx *store.Tx, w service.Writer, raw json.RawMessage) (json.RawMessage, error) {
	m, err := ground.Decode[Matrix](raw)
	if err != nil {
		return nil, err
	}
	if m.Measurement.Accession != "" || m.Measurement.Unit != "" {
		return nil, service.Invalid("the measurement's accession and unit are set by the server")
	}
	g, err := column(ctx, tx, w, board.Grounding)
	if err != nil {
		return nil, err
	}
	ds, ok := last(g, "dataset", m.Measurement.Dataset)
	if !ok {
		return nil, service.Invalid(m.Measurement.Dataset + " is not a Grounding dataset (D1, D2…)")
	}
	m.Measurement.Accession = records.Body[ground.Dataset](ds).Accession
	if m.Measurement.Unit = groundingUnit(g, m.Measurement.Dataset); m.Measurement.Unit == "" {
		return nil, service.Invalid("Grounding settled no unit of analysis for " + m.Measurement.Dataset)
	}
	if err := shape(m); err != nil {
		return nil, err
	}
	return json.Marshal(m)
}

func Kinds() map[string]service.Kind {
	// Kinds Grounding also registers keep Grounding's rules; only the roles
	// differ, and round.Kinds unions them.
	shared := ground.Kinds()
	as := func(name string, roles ...string) service.Kind {
		k := shared[name]
		k.Roles = roles
		return k
	}
	return map[string]service.Kind{
		"candidate":       {Roles: Generators, KeyPrefix: "H", Prepare: prepareCandidate},
		"missing_premise": {Roles: slices.Concat(Generators, []string{Decomposer, Selector}), Prepare: prepareMissing},
		"decomposition":   {Roles: []string{Decomposer}, Prepare: prepareDecomposition},
		"novelty":         {Roles: []string{NoveltyScout}, Prepare: prepareNovelty},
		"comparison":      {Roles: []string{Selector}, Prepare: prepareComparison},
		"ranking":         {Roles: []string{Selector}, Prepare: prepareRanking},
		"matrix":          {Roles: []string{Selector}, Prepare: prepareMatrix},
		"cannot_proceed":  as("cannot_proceed", Selector),
		"review_item":     as("review_item", Reviewer),
		"verdict":         as("verdict", Reviewer),
	}
}

// RoleKinds are what a rerun supersedes: the role's own records, never the
// server's seeds, dedup and comparisons, nor the searches it ran.
func RoleKinds(role string) []string {
	var out []string
	for name, k := range Kinds() {
		if slices.Contains(k.Roles, role) {
			out = append(out, name)
		}
	}
	slices.Sort(out)
	return out
}
