package hyp

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"slices"
	"strconv"
	"strings"
	"testing"

	"github.com/google/uuid"

	"lykeion/server/internal/auth"
	"lykeion/server/internal/board"
	"lykeion/server/internal/ground"
	"lykeion/server/internal/jev"
	"lykeion/server/internal/middleware"
	"lykeion/server/internal/records"
	"lykeion/server/internal/service"
	"lykeion/server/internal/sources"
	"lykeion/server/internal/storage"
	"lykeion/server/internal/store"
	"lykeion/server/internal/testutil"
	db "lykeion/server/pkg/db/generated"
)

// fixture is a card in Hypotheses round 2 whose Grounding passed in round 1,
// with the novelty scout's searches and fetch already logged by the sources
// server.
type fixture struct {
	env   *testutil.Env
	st    *store.Store
	blobs storage.Storage
	src   *sources.Hosted
	ws    uuid.UUID
	card  uuid.UUID
	owner uuid.UUID
}

func setup(t *testing.T) *fixture {
	t.Helper()
	env := testutil.Setup(t)
	f := &fixture{env: env, st: &store.Store{Pool: env.App}, blobs: storage.Local{Dir: t.TempDir()}, ws: uuid.New(), card: uuid.New()}
	f.src = &sources.Hosted{Store: f.st, Blobs: f.blobs}
	ctx := context.Background()
	u, _ := f.st.Global().UpsertUserByEmail(ctx, "a@lab.org")
	f.owner = u.ID
	env.Owner.Exec(ctx, `INSERT INTO workspace (id, slug, name) VALUES ($1, 'w', 'w')`, f.ws)
	env.Owner.Exec(ctx, `INSERT INTO card (id, workspace_id, number, title, question, owner_id, board_column, round)
		VALUES ($1, $2, 1, 't', 'Does X raise Y in mice?', $3, 'hypotheses', 2)`, f.card, f.ws, u.ID)
	for _, r := range []struct{ kind, role, key, body string }{
		{"question", "scoper", "", `{"text":"Does X raise Y in mouse liver?"}`},
		{"notes", "methods-scout", "", `{"text":"## Gaps and open questions\n- whether Z mediates\n- dose dependence\n"}`},
		{"evidence", "literature-scout", "E1", `{"claim":"X raises Y","source_id":"europepmc:PMC1","quote":"X raised Y by 40%","stance":"supports"}`},
		{"evidence", "contrarian", "E2", `{"claim":"X does not raise Y","source_id":"openalex:W2","quote":"no effect of X on Y","stance":"challenges"}`},
		{"dataset", "data-scout", "D1", `{"accession":"GSE1","note":"mouse liver"}`},
		{"gate", "server", "1", `{"column":"grounding","round":1,"verdict":"pass","checks":[{"id":"unit.D1","judge":"jev","result":"pass","choice":"biological_replicates"}]}`},
	} {
		f.insert(t, "grounding", r.kind, r.role, r.key, r.body)
	}
	f.search(t, "X Z mediation liver", []string{})
	f.search(t, "assay batch artefact Y", []string{"openalex:W7"})
	f.record(t, NoveltyScout, "fetch", "openalex:W7", map[string]any{"source_id": "openalex:W7"},
		"Batch effects in Y assays can mimic treatment effects of X.")
	return f
}

// tokenFor is the run token role seeds for a run.
func tokenFor(run uuid.UUID) string { return "ltk_" + run.String() }

// role is a running agent run of that role with its token, so the records it
// writes point at a real run and pass the sources server's re-check.
func (f *fixture) role(t *testing.T, name string) middleware.Role {
	t.Helper()
	ctx := context.Background()
	run := uuid.New()
	if _, err := f.env.Owner.Exec(ctx, `INSERT INTO agent_run (id, workspace_id, card_id, responsible_user_id, board_column, round, role, harness, prompt, status, attempt)
		VALUES ($1, $2, $3, $4, 'hypotheses', 2, $5, 'claude', 'p', 'running', 1)`, run, f.ws, f.card, f.owner, name); err != nil {
		t.Fatal(err)
	}
	if _, err := f.env.Owner.Exec(ctx, `INSERT INTO agent_run_token (token_hash, workspace_id, agent_run_id, card_id, role, attempt, expires_at)
		VALUES ($1, $2, $3, $4, $5, 1, now() + interval '1 hour')`, auth.Hash(tokenFor(run)), f.ws, run, f.card, name); err != nil {
		t.Fatal(err)
	}
	return middleware.Role{AgentRunID: run, WorkspaceID: f.ws, CardID: f.card, Role: name, Column: "hypotheses", Round: 2}
}

// record writes through the sources server as a running role, then ends the
// run, so no run of the fixture is left in flight.
func (f *fixture) record(t *testing.T, role, kind, key string, body map[string]any, text string) {
	t.Helper()
	r := f.role(t, role)
	if err := f.src.Record(context.Background(), r, tokenFor(r.AgentRunID), kind, key, body, text); err != nil {
		t.Fatal(err)
	}
	if _, err := f.env.Owner.Exec(context.Background(), `UPDATE agent_run SET status = 'completed', completed_at = now() WHERE id = $1`, r.AgentRunID); err != nil {
		t.Fatal(err)
	}
}

func (f *fixture) search(t *testing.T, query string, results []string) {
	t.Helper()
	f.record(t, NoveltyScout, "search", "", map[string]any{"database": "openalex", "query": query, "results": results}, "")
}

// insert writes a record directly, as the server or an earlier column did.
func (f *fixture) insert(t *testing.T, column, kind, role, key, body string) {
	t.Helper()
	if _, err := f.env.Owner.Exec(context.Background(), `INSERT INTO record (workspace_id, card_id, board_column, round, kind, role, key, body)
		VALUES ($1, $2, $3, 1, $4, $5, $6, $7::jsonb)`, f.ws, f.card, column, kind, role, key, body); err != nil {
		t.Fatal(err)
	}
}

// write goes through the kind's rules the way the role API does, and returns
// the key and the body as stored.
func (f *fixture) write(t *testing.T, role, kind string, body any) (string, json.RawMessage, error) {
	t.Helper()
	k, ok := Kinds()[kind]
	if !ok {
		t.Fatalf("no kind %s", kind)
	}
	raw, ok := body.(json.RawMessage)
	if !ok {
		raw, _ = json.Marshal(body)
	}
	ctx := context.Background()
	key := ""
	err := f.st.InWorkspace(ctx, f.ws, uuid.Nil, func(tx *store.Tx) error {
		if !slices.Contains(k.Roles, role) {
			return service.Invalid(role + " may not write " + kind)
		}
		if k.Validate != nil {
			if err := k.Validate(raw); err != nil {
				return err
			}
		}
		if k.KeyPrefix != "" {
			n, err := tx.Q.CountKindRecords(ctx, db.CountKindRecordsParams{WorkspaceID: f.ws, CardID: f.card, BoardColumn: "hypotheses", Kind: kind})
			if err != nil {
				return err
			}
			key = k.KeyPrefix + strconv.FormatInt(n+1, 10)
		}
		if k.Prepare != nil {
			var err error
			if raw, err = k.Prepare(ctx, tx, service.Writer{Card: f.card, Column: "hypotheses", Role: role, Round: 2}, raw); err != nil {
				return err
			}
		}
		_, err := tx.Q.InsertRecord(ctx, db.InsertRecordParams{WorkspaceID: f.ws, CardID: f.card, BoardColumn: "hypotheses",
			Round: 2, Kind: kind, Role: role, Key: key, Body: raw})
		return err
	})
	return key, raw, err
}

func (f *fixture) mustWrite(t *testing.T, role, kind string, body any) string {
	t.Helper()
	key, _, err := f.write(t, role, kind, body)
	if err != nil {
		t.Fatalf("%s %s: %v", role, kind, err)
	}
	return key
}

func refused(t *testing.T, err error, want string) {
	t.Helper()
	if err == nil || !strings.Contains(err.Error(), want) {
		t.Fatalf("want a refusal containing %q, got %v", want, err)
	}
}

var mechanism = map[string]any{"statement": "X raises Y by inducing Z", "kind": "mechanism",
	"mechanism":           []map[string]any{{"text": "X induces Z", "claims": []string{"E1"}}},
	"assumptions":         []map[string]any{{"text": "Z is expressed in mouse liver", "claims": []string{"E1"}}},
	"boundary_conditions": []string{"mouse liver"}, "uncertainties": []string{"Z may only mark the effect"}}

var artefact = map[string]any{"statement": "A batch artefact in the Y assay mimics an effect of X", "kind": "artefact",
	"mechanism":           []map[string]any{{"text": "treated and control samples ran in different assay batches", "claims": []string{"E2"}}},
	"assumptions":         []map[string]any{{"text": "the Y assay drifts between batches", "claims": []string{"E2"}}},
	"boundary_conditions": []string{}, "uncertainties": []string{}}

func decomposition(h string) map[string]any {
	return map[string]any{"candidate": h,
		"assumptions": []map[string]any{{"id": "A1", "text": "Y is measured once per mouse", "claims": []string{"E1"}}},
		"predictions": []map[string]any{{"id": "P1", "text": "Y differs between treated and control mice"}},
		"tests": []map[string]any{{"id": "T1", "targets": "P1", "null": "Y does not differ between groups",
			"alternative": "Y differs between groups", "procedure": "compare Y between groups in GSE1, one mouse per observation"}}}
}

// dedup stands in for the server's dedup step.
func (f *fixture) dedup(t *testing.T, survivors ...string) {
	b, _ := json.Marshal(Dedup{Survivors: survivors, Merged: map[string]string{}, Method: "tfidf-cosine", Threshold: DedupThreshold})
	f.insert(t, "hypotheses", "dedup", "server", "", string(b))
}

const nullOK = "ci_low >= -0.2 and ci_high <= 0.2"

// matrixTmpl takes the null's prediction, R1's candidate and prediction, and
// R2's candidate. With the defaults the three predictions are disjoint
// intervals, each rival's prediction, falsifier and indeterminate region are
// disjoint, and the null holds only inside its equivalence region.
const matrixTmpl = `{"measurement":{"id":"M1","dataset":"D1","description":"log2 fold change of Y, treated against control, one mouse per observation",
 "keys":[{"name":"effect","description":"log2 fold change","unit":"log2FC"},{"name":"ci_low","description":"lower bound of the 95 percent interval","unit":"log2FC"},{"name":"ci_high","description":"upper bound of the 95 percent interval","unit":"log2FC"}],
 "ordered":["ci_low","effect","ci_high"]},
 "rivals":[
  {"id":"R0","kind":"null","statement":"X does not change Y","measurement":"M1","prediction":"%s","equivalence":{"key":"effect","low":-0.2,"high":0.2},"falsifier":"ci_low > 0.2 or ci_high < -0.2","indeterminate":"(ci_low < -0.2 and ci_high >= -0.2) or (ci_high > 0.2 and ci_low <= 0.2)"},
  {"id":"R1","kind":"mechanism","candidate":"%s","statement":"X raises Y by inducing Z","measurement":"M1","prediction":"%s","equivalence":{"key":"effect","low":-0.2,"high":0.2},"falsifier":"ci_high < 0.2","indeterminate":"ci_low <= 0.2 and ci_high >= 0.2"},
  {"id":"R2","kind":"artefact","candidate":"%s","statement":"A batch artefact lowers measured Y","measurement":"M1","prediction":"ci_high < -0.2","equivalence":{"key":"effect","low":-0.2,"high":0.2},"falsifier":"ci_low > -0.2","indeterminate":"ci_low <= -0.2 and ci_high >= -0.2"}],
 "rationale":"the null, the mechanism and the artefact predict disjoint intervals"}`

func matrix(nullPred, top, topPred, other string) json.RawMessage {
	return json.RawMessage(fmt.Sprintf(matrixTmpl, nullPred, top, topPred, other))
}

func TestCandidateRules(t *testing.T) {
	f := setup(t)
	f.insert(t, "hypotheses", "seed", "server", "generator-1", `{"gap":"whether Z mediates","claim":"E1"}`)
	bad := func(edit func(map[string]any)) map[string]any {
		c := map[string]any{}
		for k, v := range mechanism {
			c[k] = v
		}
		edit(c)
		return c
	}
	_, _, err := f.write(t, "generator-1", "candidate", bad(func(c map[string]any) {
		c["assumptions"] = []map[string]any{{"text": "Z is stable", "claims": []string{"E9"}}}
	}))
	refused(t, err, "E9 is not a live Grounding claim")
	_, _, err = f.write(t, "generator-1", "candidate", bad(func(c map[string]any) {
		c["mechanism"] = []map[string]any{{"text": "X induces Z", "claims": []string{}}}
	}))
	refused(t, err, "missing-premise")
	_, _, err = f.write(t, "generator-1", "candidate", bad(func(c map[string]any) { c["seed"] = map[string]string{"gap": "mine", "claim": "E1"} }))
	refused(t, err, "seed is set by the server")
	_, _, err = f.write(t, "generator-1", "candidate", bad(func(c map[string]any) { c["kind"] = "theory" }))
	refused(t, err, "mechanism or artefact")
	_, _, err = f.write(t, "decomposer", "candidate", mechanism)
	refused(t, err, "may not write")

	key, body, err := f.write(t, "generator-1", "candidate", mechanism)
	if err != nil || key != "H1" {
		t.Fatalf("%q %v", key, err)
	}
	var c Candidate
	json.Unmarshal(body, &c)
	if c.Seed == nil || c.Seed.Gap != "whether Z mediates" || c.Seed.Claim != "E1" {
		t.Fatalf("the seed was not copied onto the candidate: %+v", c.Seed)
	}
	f.mustWrite(t, "generator-1", "candidate", mechanism)
	f.mustWrite(t, "generator-1", "candidate", mechanism)
	_, _, err = f.write(t, "generator-1", "candidate", mechanism)
	refused(t, err, "at most 3")
}

func TestSupersededGroundingClaimIsRefused(t *testing.T) {
	f := setup(t)
	f.env.Owner.Exec(context.Background(), `UPDATE record SET superseded_at = now() WHERE card_id = $1 AND key = 'E1'`, f.card)
	_, _, err := f.write(t, "generator-1", "candidate", mechanism)
	refused(t, err, "E1 is not a live Grounding claim")
}

func TestMissingPremiseNamesAGroundingStep(t *testing.T) {
	f := setup(t)
	_, body, err := f.write(t, "generator-2", "missing_premise", map[string]string{"gap": "the dose at which X saturates Y"})
	if err != nil || !strings.Contains(string(body), `"step":"literature-scout"`) {
		t.Fatalf("%s %v", body, err)
	}
	_, _, err = f.write(t, "decomposer", "missing_premise", map[string]string{"gap": "g", "step": "scoper"})
	refused(t, err, "step must be one of")
	_, _, err = f.write(t, "selector", "missing_premise", map[string]string{"gap": " "})
	refused(t, err, "name the gap")
	_, _, err = f.write(t, NoveltyScout, "missing_premise", map[string]string{"gap": "g"})
	refused(t, err, "may not write")
}

func TestDecompositionRules(t *testing.T) {
	f := setup(t)
	f.dedup(t, "H1", "H2")
	_, _, err := f.write(t, Decomposer, "decomposition", decomposition("H3"))
	refused(t, err, "H3 is not a surviving candidate")
	d := decomposition("H1")
	d["assumptions"] = []map[string]any{{"id": "A1", "text": "unlike H2, Y is measured once", "claims": []string{"E1"}}}
	_, _, err = f.write(t, Decomposer, "decomposition", d)
	refused(t, err, "stand on its own")
	d = decomposition("H1")
	d["tests"] = []map[string]any{{"id": "T1", "targets": "A7", "null": "n", "alternative": "a", "procedure": "p"}}
	_, _, err = f.write(t, Decomposer, "decomposition", d)
	refused(t, err, "T1 must target")
	d = decomposition("H1")
	d["tests"] = []map[string]any{}
	_, _, err = f.write(t, Decomposer, "decomposition", d)
	refused(t, err, "at least one assumption, prediction and falsification test")
	f.mustWrite(t, Decomposer, "decomposition", decomposition("H1"))
}

func TestNoveltyNeverOverclaims(t *testing.T) {
	f := setup(t)
	f.dedup(t, "H1", "H2")
	nov := func(status string, queries []string, closest map[string]string) map[string]any {
		n := map[string]any{"candidate": "H1", "status": status, "queries": queries}
		if closest != nil {
			n["closest"] = closest
		}
		return n
	}
	w7 := map[string]string{"source_id": "openalex:W7", "quote": "Batch effects in Y assays"}
	_, _, err := f.write(t, NoveltyScout, "novelty", nov("novel", []string{"X Z mediation liver"}, nil))
	refused(t, err, "never record novel")
	_, _, err = f.write(t, NoveltyScout, "novelty", nov("unknown", []string{"a query nobody ran"}, nil))
	refused(t, err, "never ran")
	_, _, err = f.write(t, NoveltyScout, "novelty", nov("related", []string{"X Z mediation liver"}, w7))
	refused(t, err, "came back empty")
	_, _, err = f.write(t, NoveltyScout, "novelty", nov("related", []string{"assay batch artefact Y"}, nil))
	refused(t, err, "name the closest source")
	_, _, err = f.write(t, NoveltyScout, "novelty", nov("related", []string{"assay batch artefact Y"}, map[string]string{"source_id": "openalex:W9", "quote": "q"}))
	refused(t, err, "never fetched")
	_, _, err = f.write(t, NoveltyScout, "novelty", nov("unknown", []string{"a", "b", "c", "d"}, nil))
	refused(t, err, "1 to 3 queries")
	f.mustWrite(t, NoveltyScout, "novelty", nov("unknown", []string{"X Z mediation liver"}, nil))
	f.mustWrite(t, NoveltyScout, "novelty", nov("related", []string{"assay batch artefact Y"}, w7))
}

func TestMatrixStructure(t *testing.T) {
	f := setup(t)
	f.dedup(t, "H1", "H2")
	_, body, err := f.write(t, Selector, "matrix", matrix(nullOK, "H1", "ci_low > 0.2", "H2"))
	if err != nil {
		t.Fatal(err)
	}
	var m Matrix
	json.Unmarshal(body, &m)
	if m.Measurement.Unit != "biological_replicates" || m.Measurement.Accession != "GSE1" {
		t.Fatalf("the server must set the unit and accession from Grounding: %+v", m.Measurement)
	}
	for bad, want := range map[string]string{
		string(matrix(nullOK, "H1", "effect == 0", "H2")):                                                                                     "R1 prediction",
		string(matrix(nullOK, "H1", "ci_mid > 0.2", "H2")):                                                                                    "undeclared key ci_mid",
		strings.Replace(string(matrix(nullOK, "H1", "ci_low > 0.2", "H2")), `"dataset":"D1"`, `"dataset":"D9"`, 1):                            "D9 is not a Grounding dataset",
		strings.Replace(string(matrix(nullOK, "H1", "ci_low > 0.2", "H2")), `"dataset":"D1"`, `"dataset":"D1","unit":"technical_repeats"`, 1): "set by the server",
		strings.Replace(string(matrix(nullOK, "H1", "ci_low > 0.2", "H2")), `"id":"R2","kind":"artefact"`, `"id":"R1","kind":"artefact"`, 1):  "unique",
		strings.Replace(string(matrix(nullOK, "H1", "ci_low > 0.2", "H2")), `"kind":"null"`, `"kind":"null","candidate":"H1"`, 1):             "the null rival has no candidate",
	} {
		_, _, err := f.write(t, Selector, "matrix", json.RawMessage(bad))
		refused(t, err, want)
	}
}

func TestServerKindsAreNotRoleKinds(t *testing.T) {
	for _, kind := range []string{"seed", "dedup", "pairwise", "gate", "gate_draft", "search", "fetch", "open_dataset", "evidence"} {
		if _, ok := Kinds()[kind]; ok {
			t.Fatalf("roles may write %s records", kind)
		}
	}
	if got := strings.Join(RoleKinds(Selector), ","); got != "cannot_proceed,comparison,matrix,missing_premise,ranking" {
		t.Fatal(got)
	}
	if got := strings.Join(RoleKinds(Reviewer), ","); got != "review_item,verdict" {
		t.Fatal(got)
	}
}

func TestDecompositionRequiresLiveCitations(t *testing.T) {
	for _, tc := range []struct {
		name   string
		claims []string
		stale  bool
		want   string
	}{
		{name: "nil", want: "missing-premise"},
		{name: "empty", claims: []string{}, want: "missing-premise"},
		{name: "unknown", claims: []string{"E9"}, want: "E9 is not a live Grounding claim"},
		{name: "superseded", claims: []string{"E1"}, stale: true, want: "E1 is not a live Grounding claim"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f := setup(t)
			f.dedup(t, "H1")
			if tc.stale {
				if _, err := f.env.Owner.Exec(context.Background(), `UPDATE record SET superseded_at = now() WHERE card_id = $1 AND key = 'E1'`, f.card); err != nil {
					t.Fatal(err)
				}
			}
			d := decomposition("H1")
			d["assumptions"] = []map[string]any{{"id": "A1", "text": "Y is measured once per mouse", "claims": tc.claims}}
			_, _, err := f.write(t, Decomposer, "decomposition", d)
			refused(t, err, tc.want)
		})
	}
}

func TestComparisonAndServerRanking(t *testing.T) {
	f := setup(t)
	f.dedup(t, "H1", "H2", "H3")
	_, _, err := f.write(t, Selector, "ranking", json.RawMessage(`{}`))
	refused(t, err, "judge these pairs first")
	f.insert(t, "hypotheses", "pairwise", "server", pairKey("H1", "H2"), `{"a":"H1","b":"H2","p_a":0.8,"judge":"jev"}`)
	_, _, err = f.write(t, Selector, "comparison", Comparison{A: "H1", B: "H2", Winner: "H2", Reason: "override"})
	refused(t, err, "Jev already judged")
	_, _, err = f.write(t, Selector, "comparison", Comparison{A: "H1", B: "H9", Winner: "H1", Reason: "stale"})
	refused(t, err, "different surviving candidates")
	for _, a := range []string{"H1", "H2"} {
		f.mustWrite(t, Selector, "comparison", Comparison{A: a, B: "H3", Winner: a, Reason: "more directly testable"})
	}
	_, _, err = f.write(t, Selector, "ranking", json.RawMessage(`{"order":["H3"]}`))
	if err == nil {
		t.Fatal("selector supplied its own ranking")
	}
	_, raw, err := f.write(t, Selector, "ranking", json.RawMessage(`{}`))
	if err != nil {
		t.Fatal(err)
	}
	var got Ranking
	if err := json.Unmarshal(raw, &got); err != nil {
		t.Fatal(err)
	}
	if len(got.Judgments) != 3 || len(got.Scores) != 3 || len(got.Order) != 3 || got.Order[0] != "H1" {
		t.Fatalf("server ranking: %+v", got)
	}
}

// serve runs Serve as the engine does: read in one transaction, Serve with
// none open, then write its batch in another.
func (f *fixture) serve(t *testing.T, next []string, j *jev.Client) bool {
	t.Helper()
	ctx := context.Background()
	var c db.Card
	var v *records.View
	err := f.st.InWorkspace(ctx, f.ws, uuid.Nil, func(tx *store.Tx) (err error) {
		if c, err = tx.Q.GetCard(ctx, db.GetCardParams{WorkspaceID: f.ws, ID: f.card}); err != nil {
			return err
		}
		v, err = records.Load(ctx, tx, f.blobs, f.card, "hypotheses")
		return err
	})
	if err != nil {
		t.Fatal(err)
	}
	b, now, err := Serve(ctx, c, v, next, j)
	if err != nil {
		t.Fatal(err)
	}
	if now && (len(b.Put) != 0 || len(b.Retire) != 0) {
		t.Fatalf("immediate gate returned a nonempty batch: %+v", b)
	}
	if err := f.st.InWorkspace(ctx, f.ws, uuid.Nil, func(tx *store.Tx) error { return b.Write(ctx, tx, c) }); err != nil {
		t.Fatal(err)
	}
	return now
}

func (f *fixture) live(t *testing.T, kind string) []db.Record {
	t.Helper()
	ctx := context.Background()
	var out []db.Record
	err := f.st.InWorkspace(ctx, f.ws, uuid.Nil, func(tx *store.Tx) error {
		recs, err := tx.Q.ListRecords(ctx, db.ListRecordsParams{WorkspaceID: f.ws, CardID: f.card, BoardColumn: "hypotheses"})
		for _, r := range recs {
			if r.Kind == kind {
				out = append(out, r)
			}
		}
		return err
	})
	if err != nil {
		t.Fatal(err)
	}
	return out
}

// fakeJev answers every question the column asks: the first-listed candidate
// wins every pair, every test is relevant, prior work never states the
// hypothesis, and every quote supports its assumption. With fail it is down.
func fakeJev(t *testing.T, fail bool) *jev.Client {
	s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if fail {
			w.WriteHeader(503)
			return
		}
		var body struct {
			State     string
			Questions map[string]struct{ Criteria map[string]string }
		}
		json.NewDecoder(r.Body).Decode(&body)
		q := body.Questions["q"].Criteria
		choice := "supports"
		switch {
		case q["first"] != "":
			choice = "first"
		case q["relevant"] != "":
			choice = "relevant"
		case q["states_it"] != "":
			choice = "does_not"
		}
		json.NewEncoder(w).Encode(map[string]any{"model": "jev-1.13.0", "answers": map[string]any{
			"q": map[string]any{"choice": choice, "probabilities": map[string]float64{choice: 0.9}, "confidence": 0.9}}})
	}))
	t.Cleanup(s.Close)
	return &jev.Client{BaseURL: s.URL, Key: "k", Model: "jev-1.13.0"}
}

var third = map[string]any{"statement": "Z suppresses a repressor of Y in hepatocytes", "kind": "mechanism",
	"mechanism":           []map[string]any{{"text": "Z binds the repressor", "claims": []string{"E1"}}},
	"assumptions":         []map[string]any{{"text": "the repressor is active in liver", "claims": []string{"E1"}}},
	"boundary_conditions": []string{}, "uncertainties": []string{}}

func TestSeedsSpreadOverGapsAndClaims(t *testing.T) {
	f := setup(t)
	if f.serve(t, Generators, nil) {
		t.Fatal("seeding never calls the gate")
	}
	got := map[Seed]string{}
	for _, r := range f.live(t, "seed") {
		got[records.Body[Seed](r)] = r.Key
	}
	want := []Seed{{"whether Z mediates", "E1"}, {"dose dependence", "E2"}, {"whether Z mediates", "E2"}}
	for i, s := range want {
		if got[s] != Generators[i] {
			t.Fatalf("seeds %v", got)
		}
	}
	f.serve(t, Generators, nil)
	if n := len(f.live(t, "seed")); n != 3 {
		t.Fatalf("seeding again must replace the seeds: %d live", n)
	}
}

func TestDedupBeforeTheDecomposer(t *testing.T) {
	f := setup(t)
	for _, next := range [][]string{{Decomposer}, {NoveltyScout}, {Selector}} {
		if !f.serve(t, next, nil) {
			t.Fatal("with no candidate the gate must judge at once")
		}
	}
	f.mustWrite(t, "generator-1", "candidate", mechanism)
	f.mustWrite(t, "generator-2", "candidate", artefact)
	f.mustWrite(t, "generator-3", "candidate", mechanism)
	if f.serve(t, []string{Decomposer}, nil) {
		t.Fatal("candidates exist: the decomposer must be queued")
	}
	dd := f.live(t, "dedup")
	if len(dd) != 1 {
		t.Fatalf("%d dedup records", len(dd))
	}
	if d := records.Body[Dedup](dd[0]); !slices.Equal(d.Survivors, []string{"H1", "H2"}) || d.Merged["H3"] != "H1" || d.Method != "tfidf-cosine" {
		t.Fatalf("%+v", d)
	}
	f.mustWrite(t, "generator-2", "missing_premise", map[string]string{"gap": "the dose at which X saturates Y"})
	for _, next := range [][]string{{Decomposer}, {NoveltyScout}, {Selector}} {
		if !f.serve(t, next, nil) {
			t.Fatalf("%v: a missing premise must send the card to the gate", next)
		}
	}
}

func TestJevComparesEverySurvivingPair(t *testing.T) {
	f := setup(t)
	f.mustWrite(t, "generator-1", "candidate", mechanism)
	f.mustWrite(t, "generator-2", "candidate", artefact)
	f.serve(t, []string{Decomposer}, nil)
	f.serve(t, []string{Selector}, fakeJev(t, false))
	pw := f.live(t, "pairwise")
	if len(pw) != 1 || pw[0].Key != "H1|H2" || pw[0].Role != "server" {
		t.Fatalf("%+v", pw)
	}
	// The single pair is asked in swapped order, and the fake picks the first.
	if j := records.Body[Judgment](pw[0]); j.A != "H2" || j.B != "H1" || j.PA != 1 || j.Judge != "jev" || j.Model != "jev-1.13.0" {
		t.Fatalf("%+v", j)
	}
	_, _, err := f.write(t, Selector, "comparison", map[string]string{"a": "H1", "b": "H2", "winner": "H1", "reason": "sharper"})
	refused(t, err, "Jev already judged")
	_, body, err := f.write(t, Selector, "ranking", map[string]any{})
	var rk Ranking
	json.Unmarshal(body, &rk)
	if err != nil || !slices.Equal(rk.Order, []string{"H2", "H1"}) || len(rk.Judgments) != 1 {
		t.Fatalf("%+v %v", rk, err)
	}
}

func TestUnjudgedPairsFallToTheSelector(t *testing.T) {
	f := setup(t)
	f.mustWrite(t, "generator-1", "candidate", mechanism)
	f.mustWrite(t, "generator-2", "candidate", artefact)
	f.mustWrite(t, "generator-3", "candidate", third)
	f.serve(t, []string{Decomposer}, nil)
	f.serve(t, []string{Selector}, fakeJev(t, true))
	if n := len(f.live(t, "pairwise")); n != 0 {
		t.Fatalf("Jev was down, yet %d judgments were recorded", n)
	}
	_, _, err := f.write(t, Selector, "ranking", map[string]any{})
	refused(t, err, "H1|H2, H1|H3, H2|H3")
	for _, c := range [][3]string{{"H1", "H2", "H1"}, {"H1", "H3", "H1"}, {"H3", "H2", "H2"}} {
		f.mustWrite(t, Selector, "comparison", map[string]string{"a": c[0], "b": c[1], "winner": c[2], "reason": "discriminates better"})
	}
	_, body, err := f.write(t, Selector, "ranking", map[string]any{})
	var rk Ranking
	json.Unmarshal(body, &rk)
	if err != nil || !slices.Equal(rk.Order, []string{"H1", "H2", "H3"}) {
		t.Fatalf("%+v %v", rk, err)
	}
	for _, j := range rk.Judgments {
		if j.Judge != "selector" {
			t.Fatalf("%+v", j)
		}
	}
}

func (f *fixture) gate(t *testing.T, j *jev.Client) *board.Gate {
	t.Helper()
	ctx := context.Background()
	var v *records.View
	err := f.st.InWorkspace(ctx, f.ws, uuid.Nil, func(tx *store.Tx) (err error) {
		v, err = records.Load(ctx, tx, f.blobs, f.card, "hypotheses")
		return err
	})
	if err != nil {
		t.Fatal(err)
	}
	g, err := Check(ctx, v, j)
	if err != nil {
		t.Fatal(err)
	}
	return g
}

func (f *fixture) finalize(t *testing.T, g *board.Gate) {
	t.Helper()
	ctx := context.Background()
	if err := f.st.InWorkspace(ctx, f.ws, uuid.Nil, func(tx *store.Tx) error {
		v, err := records.Load(ctx, tx, f.blobs, f.card, "hypotheses")
		if err == nil {
			ground.Finalize(v, g)
		}
		return err
	}); err != nil {
		t.Fatal(err)
	}
}

func check(g *board.Gate, id string) board.Check {
	for _, ch := range g.Checks {
		if ch.ID == id {
			return ch
		}
	}
	return board.Check{ID: id, Result: "missing"}
}

// complete runs the whole column the way its roles and the server would and
// returns the top-ranked survivor and the other one. With j nil, Jev egress
// is off and the selector judges the one pair.
func (f *fixture) complete(t *testing.T, j *jev.Client) (string, string) {
	t.Helper()
	f.serve(t, Generators, j)
	f.mustWrite(t, "generator-1", "candidate", mechanism)
	f.mustWrite(t, "generator-2", "candidate", artefact)
	f.mustWrite(t, "generator-3", "candidate", mechanism)
	f.serve(t, []string{Decomposer}, j)
	for _, h := range []string{"H1", "H2"} {
		f.mustWrite(t, Decomposer, "decomposition", decomposition(h))
	}
	f.serve(t, []string{NoveltyScout}, j)
	f.mustWrite(t, NoveltyScout, "novelty", map[string]any{"candidate": "H1", "status": "unknown", "queries": []string{"X Z mediation liver"}})
	f.mustWrite(t, NoveltyScout, "novelty", map[string]any{"candidate": "H2", "status": "related", "queries": []string{"assay batch artefact Y"},
		"closest": map[string]string{"source_id": "openalex:W7", "quote": "Batch effects in Y assays can mimic treatment effects"}})
	f.serve(t, []string{Selector}, j)
	if j == nil {
		f.mustWrite(t, Selector, "comparison", map[string]string{"a": "H1", "b": "H2", "winner": "H1", "reason": "a sharper prediction"})
	}
	_, body, err := f.write(t, Selector, "ranking", map[string]any{})
	if err != nil {
		t.Fatal(err)
	}
	var rk Ranking
	json.Unmarshal(body, &rk)
	f.mustWrite(t, Selector, "matrix", matrix(nullOK, rk.Order[0], "ci_low > 0.2", rk.Order[1]))
	return rk.Order[0], rk.Order[1]
}

func TestCompleteColumnPassesCodeAndJev(t *testing.T) {
	f := setup(t)
	top, other := f.complete(t, fakeJev(t, false))
	g := f.gate(t, fakeJev(t, false))
	if g.Verdict != "" {
		t.Fatalf("decided without the reviewer: %+v", g)
	}
	for _, ch := range g.Checks {
		if ch.Result != board.Pass {
			t.Fatalf("%+v", ch)
		}
	}
	rid := map[string]string{top: "R1", other: "R2"}["H2"]
	for _, id := range []string{"candidates.present", "claims.H1", "claims.H2", "claims.H3", "claims.H1.decomposition", "matrix.schema",
		"rivals.count", "rivals.null", "rivals.survivors", "ranking.complete", "ranking.current", "select.top", "measurement.dataset",
		"measurement.unit", "stated.R0", "consistent.R0", "consistent.R1", "equivalence.R0", "equivalence.R2", "disagree.R0.R1",
		"disagree.R0.R2", "disagree.R1.R2", "decomposition.R1", "novelty.R2", "novelty." + rid + ".quote", "prior." + rid,
		"assumption." + top + ".A1.E1", "test." + other + ".T1"} {
		if ch := check(g, id); ch.Result != board.Pass {
			t.Errorf("%s: %+v", id, ch)
		}
	}
	if ch := check(g, "prior."+rid); ch.Judge != board.Jev || ch.Choice != "does_not" {
		t.Fatalf("%+v", ch)
	}
	if check(g, "ranking.judges").Result != "missing" {
		t.Fatal("Jev judged every pair; the reviewer has nothing to confirm")
	}
	f.mustWrite(t, Reviewer, "verdict", map[string]string{"result": "pass", "reason": "honest rivals and a feasible measurement"})
	f.finalize(t, g)
	if g.Verdict != board.VPass {
		t.Fatalf("%+v", g)
	}
}

func TestOverlappingPredictionsFailAtTheSelector(t *testing.T) {
	f := setup(t)
	top, other := f.complete(t, fakeJev(t, false))
	f.mustWrite(t, Selector, "matrix", matrix(nullOK, top, "ci_low > -0.5", other))
	g := f.gate(t, fakeJev(t, false))
	if ch := check(g, "disagree.R0.R1"); ch.Result != board.Fail || !strings.Contains(ch.Note, "both hold at") {
		t.Fatalf("%+v", ch)
	}
	if g.Verdict != board.VFail || g.Route == nil || g.Route.Step != Selector || g.Route.Column != board.Hypotheses {
		t.Fatalf("%+v %+v", g.Verdict, g.Route)
	}
}

func TestBoundaryTouchIsOverlap(t *testing.T) {
	f := setup(t)
	top, other := f.complete(t, fakeJev(t, false))
	// The null allows ci_low = ci_high = 0.2; this prediction allows it too.
	f.mustWrite(t, Selector, "matrix", matrix(nullOK, top, "ci_low >= 0.2", other))
	if ch := check(f.gate(t, nil), "disagree.R0.R1"); ch.Result != board.Fail {
		t.Fatalf("%+v", ch)
	}
}

func TestNullMustBeSupportable(t *testing.T) {
	f := setup(t)
	top, other := f.complete(t, fakeJev(t, false))
	f.mustWrite(t, Selector, "matrix", matrix("ci_high <= 0.2", top, "ci_low > 0.2", other))
	if ch := check(f.gate(t, nil), "equivalence.R0"); ch.Result != board.Fail {
		t.Fatalf("a one-sided null can only be un-refuted, never supported: %+v", ch)
	}
}

func TestTopRankedCandidateMustBeARival(t *testing.T) {
	f := setup(t)
	top, other := f.complete(t, fakeJev(t, false))
	f.mustWrite(t, Selector, "matrix", matrix(nullOK, other, "ci_low > 0.2", other))
	if ch := check(f.gate(t, nil), "select.top"); ch.Result != board.Fail || !strings.Contains(ch.Note, top) {
		t.Fatalf("%+v", ch)
	}
}

func TestNoveltyQuoteMustBeInTheFetchedText(t *testing.T) {
	f := setup(t)
	top, other := f.complete(t, fakeJev(t, false))
	f.mustWrite(t, NoveltyScout, "novelty", map[string]any{"candidate": "H2", "status": "related", "queries": []string{"assay batch artefact Y"},
		"closest": map[string]string{"source_id": "openalex:W7", "quote": "batch effects explain every result"}})
	rid := map[string]string{top: "R1", other: "R2"}["H2"]
	if ch := check(f.gate(t, nil), "novelty."+rid+".quote"); ch.Result != board.Fail || ch.Owner != NoveltyScout {
		t.Fatalf("%+v", ch)
	}
}

func TestMissingPremiseRoutesToGrounding(t *testing.T) {
	f := setup(t)
	f.mustWrite(t, "generator-1", "candidate", mechanism)
	f.mustWrite(t, "generator-2", "missing_premise", map[string]string{"gap": "the dose at which X saturates Y"})
	g := f.gate(t, nil)
	if g.Verdict != board.VFail || g.Route == nil || g.Route.Column != board.Grounding || g.Route.Step != "literature-scout" ||
		!strings.Contains(g.Reason, "missing premise: the dose at which X saturates Y") {
		t.Fatalf("%+v %+v", g, g.Route)
	}
}

func TestSelectorCannotProceed(t *testing.T) {
	f := setup(t)
	f.mustWrite(t, Selector, "cannot_proceed", map[string]string{"reason": "no candidate can be measured on GSE1"})
	if g := f.gate(t, nil); g.Verdict != board.CannotProceed || g.Reason != "no viable hypothesis: no candidate can be measured on GSE1" {
		t.Fatalf("%+v", g)
	}
}

func TestGeneratorsFindingNothingStopsTheCard(t *testing.T) {
	f := setup(t)
	for _, gen := range Generators {
		f.insert(t, "hypotheses", "nothing_found", gen, "", `{"reason":"nothing follows from the seed"}`)
	}
	if g := f.gate(t, nil); g.Verdict != board.CannotProceed || !strings.HasPrefix(g.Reason, "no viable hypothesis: nothing follows from the seed") {
		t.Fatalf("%+v", g)
	}
}

func TestEgressOffSelectorJudgesAndTheReviewerConfirms(t *testing.T) {
	f := setup(t)
	f.complete(t, nil)
	g := f.gate(t, nil)
	if ch := check(g, "ranking.judges"); ch.Result != board.Attention || ch.Judge != board.Reviewer {
		t.Fatalf("%+v", ch)
	}
	var open []string
	for _, ch := range g.Checks {
		if ch.Result == board.Attention {
			if ch.Judge != board.Reviewer {
				t.Fatalf("egress is off, yet %s is not the reviewer's: %+v", ch.ID, ch)
			}
			open = append(open, ch.ID)
		}
	}
	if len(open) < 4 {
		t.Fatalf("want the ranking, assumption, test and prior-work checks open: %v", open)
	}
	f.mustWrite(t, Reviewer, "verdict", map[string]string{"result": "pass", "reason": "fair selection"})
	unsettled := f.gate(t, nil)
	f.finalize(t, unsettled)
	if unsettled.Verdict != board.VFail {
		t.Fatal("open checks passed without the reviewer settling them")
	}
	for _, id := range open {
		f.mustWrite(t, Reviewer, "review_item", map[string]string{"check": id, "result": "pass", "note": "read and confirmed"})
	}
	settled := f.gate(t, nil)
	f.finalize(t, settled)
	if settled.Verdict != board.VPass {
		t.Fatalf("%+v", settled)
	}
}

func TestGateRejectsDecompositionWithoutLiveCitations(t *testing.T) {
	for _, tc := range []struct {
		name   string
		claims []string
		stale  bool
	}{
		{name: "nil"}, {name: "empty", claims: []string{}},
		{name: "unknown", claims: []string{"E9"}}, {name: "superseded", claims: []string{"E1"}, stale: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f := setup(t)
			f.complete(t, nil)
			d := decomposition("H1")
			d["assumptions"] = []map[string]any{{"id": "A1", "text": "Y is measured once per mouse", "claims": tc.claims}}
			body, err := json.Marshal(d)
			if err != nil {
				t.Fatal(err)
			}
			// Bypass write validation to verify the gate independently protects provenance.
			if _, err := f.env.Owner.Exec(context.Background(), `UPDATE record SET body = $1::jsonb WHERE card_id = $2 AND kind = 'decomposition' AND body->>'candidate' = 'H1' AND superseded_at IS NULL`, string(body), f.card); err != nil {
				t.Fatal(err)
			}
			if tc.stale {
				if _, err := f.env.Owner.Exec(context.Background(), `UPDATE record SET superseded_at = now() WHERE card_id = $1 AND board_column = 'grounding' AND key = 'E1'`, f.card); err != nil {
					t.Fatal(err)
				}
			}
			g := f.gate(t, nil)
			ch := check(g, "claims.H1.decomposition")
			if ch.Result != board.Fail || ch.Owner != Decomposer || g.Verdict != board.VFail {
				t.Fatalf("gate admitted malformed decomposition: check=%+v gate=%+v", ch, g)
			}
		})
	}
}

func TestGateCodeFailureSkipsJev(t *testing.T) {
	f := setup(t)
	top, other := f.complete(t, nil)
	f.mustWrite(t, Selector, "matrix", matrix(nullOK, top, "ci_low >= 0.2", other))
	calls := 0
	s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { calls++; w.WriteHeader(http.StatusServiceUnavailable) }))
	defer s.Close()
	g := f.gate(t, &jev.Client{BaseURL: s.URL, Key: "k", Model: "jev-1.13.0"})
	if g.Verdict != board.VFail || calls != 0 {
		t.Fatalf("verdict=%s Jev calls=%d", g.Verdict, calls)
	}
}

func TestGateJevFailureRequiresReviewerSettlement(t *testing.T) {
	f := setup(t)
	top, _ := f.complete(t, nil)
	g := f.gate(t, fakeJev(t, true))
	if ch := check(g, "assumption."+top+".A1.E1"); ch.Result != board.Attention || !strings.Contains(ch.Note, "jev unavailable") {
		t.Fatalf("%+v", ch)
	}
	f.mustWrite(t, Reviewer, "verdict", map[string]string{"result": "pass", "reason": "reviewed"})
	f.finalize(t, g)
	if g.Verdict != board.VFail {
		t.Fatalf("unsettled Jev failure passed: %+v", g)
	}
}
