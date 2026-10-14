package ground

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"

	"github.com/google/uuid"

	"lykeion/server/internal/auth"
	"lykeion/server/internal/board"
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
	env.Owner.Exec(ctx, `INSERT INTO card (id, workspace_id, number, title, question, owner_id, board_column, round) VALUES ($1, $2, 1, 't', 'q', $3, 'grounding', 1)`, f.card, f.ws, u.ID)
	f.record(t, Literature, "search", "", map[string]any{"database": "europepmc", "query": "x y"}, "")
	f.record(t, Contrarian, "search", "", map[string]any{"database": "openalex", "query": "x no effect"}, "")
	f.record(t, Literature, "fetch", "europepmc:PMC1", map[string]any{"source_id": "europepmc:PMC1", "full_text": true}, "In treated mice, X raised\n Y by 40% over controls.")
	f.record(t, Contrarian, "fetch", "openalex:W2", map[string]any{"source_id": "openalex:W2"}, "We found no effect of X on Y in rats.")
	f.record(t, DataScout, "open_dataset", "GSE1", map[string]any{"accession": "GSE1"}, "accession: GSE1\nsamples (2):\n- GSM1: mouse 1 liver\n- GSM2: mouse 2 liver\n")
	return f
}

func tokenFor(run uuid.UUID) string { return "ltk_" + run.String() }

func (f *fixture) role(t *testing.T, name string) middleware.Role {
	t.Helper()
	ctx := context.Background()
	run := uuid.New()
	if _, err := f.env.Owner.Exec(ctx, `INSERT INTO agent_run (id, workspace_id, card_id, responsible_user_id, board_column, round, role, harness, prompt, status, attempt)
		VALUES ($1, $2, $3, $4, 'grounding', 1, $5, 'claude', 'p', 'running', 1)`, run, f.ws, f.card, f.owner, name); err != nil {
		t.Fatal(err)
	}
	if _, err := f.env.Owner.Exec(ctx, `INSERT INTO agent_run_token (token_hash, workspace_id, agent_run_id, card_id, role, attempt, expires_at)
		VALUES ($1, $2, $3, $4, $5, 1, now() + interval '1 hour')`, auth.Hash(tokenFor(run)), f.ws, run, f.card, name); err != nil {
		t.Fatal(err)
	}
	return middleware.Role{AgentRunID: run, WorkspaceID: f.ws, CardID: f.card, Role: name, Column: "grounding", Round: 1}
}

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

func (f *fixture) write(t *testing.T, role, kind string, body any) (string, error) {
	t.Helper()
	k, ok := Kinds()[kind]
	if !ok {
		t.Fatalf("no kind %s", kind)
	}
	raw, _ := json.Marshal(body)
	key := ""
	err := f.st.InWorkspace(context.Background(), f.ws, uuid.Nil, func(tx *store.Tx) error {
		if !contains(k.Roles, role) {
			return service.Invalid(role + " may not write " + kind)
		}
		if k.Validate != nil {
			if err := k.Validate(raw); err != nil {
				return err
			}
		}
		if k.KeyPrefix != "" {
			n, err := tx.Q.CountKindRecords(context.Background(), countParams(f, kind))
			if err != nil {
				return err
			}
			key = k.KeyPrefix + itoa(n+1)
		}
		if k.Prepare != nil {
			var err error
			raw, err = k.Prepare(context.Background(), tx, service.Writer{Card: f.card, Column: "grounding", Role: role, Round: 1}, raw)
			if err != nil {
				return err
			}
		}
		_, err := tx.Q.InsertRecord(context.Background(), insertParams(f, role, kind, key, raw))
		return err
	})
	return key, err
}

func TestEvidenceRules(t *testing.T) {
	f := setup(t)
	if _, err := f.write(t, Literature, "evidence", map[string]string{"claim": "c", "source_id": "openalex:W9", "quote": "q", "stance": "supports"}); err == nil || !strings.Contains(err.Error(), "never fetched") {
		t.Fatalf("unfetched source: %v", err)
	}
	if _, err := f.write(t, Contrarian, "evidence", map[string]string{"claim": "c", "source_id": "openalex:W2", "quote": "no effect", "stance": "supports"}); err == nil {
		t.Fatal("the contrarian recorded supporting evidence")
	}
	key, err := f.write(t, Literature, "evidence", map[string]string{"claim": "X raises Y", "source_id": "europepmc:PMC1", "quote": "X raised Y", "stance": "supports"})
	if err != nil || key != "E1" {
		t.Fatalf("%q %v", key, err)
	}
	if _, err := f.write(t, DataScout, "dataset", map[string]string{"accession": "GSE9"}); err == nil {
		t.Fatal("a dataset never opened was accepted")
	}
	if key, err := f.write(t, DataScout, "dataset", map[string]string{"accession": "GSE1", "note": "liver"}); err != nil || key != "D1" {
		t.Fatalf("%q %v", key, err)
	}
	if _, err := f.write(t, Reviewer, "verdict", map[string]string{"result": "fail"}); err == nil {
		t.Fatal("a failing verdict without a reason was accepted")
	}
	for _, to := range []string{"hypotheses", "grounding", "done", "nowhere"} {
		if _, err := f.write(t, Reviewer, "verdict", map[string]string{"result": "fail", "to": to, "reason": "r"}); err == nil || !strings.Contains(err.Error(), "not a stage before") {
			t.Fatalf("verdict routed to %s: %v", to, err)
		}
	}
	if _, err := f.write(t, Reviewer, "verdict", map[string]string{"result": "fail", "step": "contrarian", "reason": "r"}); err != nil {
		t.Fatalf("a verdict that stays in the stage: %v", err)
	}
}

func TestRoleKindsExcludeServerRecords(t *testing.T) {
	for _, k := range RoleKinds(Literature) {
		if k == "search" || k == "fetch" {
			t.Fatal("a rerun would supersede the role's fetched sources")
		}
	}
	if !contains(RoleKinds(Literature), "evidence") {
		t.Fatal("evidence must be superseded on rerun")
	}
}

func TestSourceRecordSchemasDecodeStoredMetadata(t *testing.T) {
	fetch := records.Body[Fetch](db.Record{Body: []byte(`{"source_id":"europepmc:PMC1","full_text":true,"retracted":true}`)})
	if fetch.SourceID != "europepmc:PMC1" || !fetch.FullText || !fetch.Retracted {
		t.Fatalf("decoded fetch metadata: %+v", fetch)
	}
	search := records.Body[Search](db.Record{Body: []byte(`{"database":"openalex","query":"x no effect"}`)})
	if search.Database != "openalex" || search.Query != "x no effect" {
		t.Fatalf("decoded search metadata: %+v", search)
	}
}

func contains(xs []string, x string) bool {
	for _, y := range xs {
		if y == x {
			return true
		}
	}
	return false
}
func itoa(n int64) string { return strconv.FormatInt(n, 10) }
func countParams(f *fixture, kind string) db.CountKindRecordsParams {
	return db.CountKindRecordsParams{WorkspaceID: f.ws, CardID: f.card, BoardColumn: "grounding", Kind: kind}
}
func insertParams(f *fixture, role, kind, key string, body []byte) db.InsertRecordParams {
	return db.InsertRecordParams{WorkspaceID: f.ws, CardID: f.card, BoardColumn: "grounding", Round: 1, Kind: kind, Role: role, Key: key, Body: body}
}

func (f *fixture) complete(t *testing.T, quote string) {
	t.Helper()
	for _, w := range []struct {
		role, kind string
		body       any
	}{
		{Scoper, "question", map[string]string{"text": "Does X raise Y in mice?"}},
		{Literature, "evidence", map[string]string{"claim": "X raises Y", "source_id": "europepmc:PMC1", "quote": quote, "stance": "supports"}},
		{Contrarian, "evidence", map[string]string{"claim": "X does not raise Y", "source_id": "openalex:W2", "quote": "no effect of X on Y", "stance": "challenges"}},
		{DataScout, "dataset", map[string]string{"accession": "GSE1", "note": "liver"}},
		{Methods, "notes", map[string]string{"text": "DESeq2"}},
	} {
		if _, err := f.write(t, w.role, w.kind, w.body); err != nil {
			t.Fatal(err)
		}
	}
}

func (f *fixture) view(t *testing.T) *records.View {
	t.Helper()
	var v *records.View
	if err := f.st.InWorkspace(context.Background(), f.ws, uuid.Nil, func(tx *store.Tx) error {
		var err error
		v, err = records.Load(context.Background(), tx, f.blobs, f.card, "grounding")
		return err
	}); err != nil {
		t.Fatal(err)
	}
	return v
}

func (f *fixture) gate(t *testing.T, j *jev.Client) *board.Gate {
	t.Helper()
	g, err := Check(context.Background(), f.view(t), j)
	if err != nil {
		t.Fatal(err)
	}
	return g
}

func (f *fixture) finalize(t *testing.T, g *board.Gate) {
	Finalize(f.view(t), g)
}

func fakeJev(t *testing.T, fail bool) *jev.Client {
	s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if fail {
			w.WriteHeader(http.StatusServiceUnavailable)
			return
		}
		var body struct {
			State     string
			Questions map[string]struct{ Criteria map[string]string }
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Errorf("decode Jev request: %v", err)
		}
		choice := "supports"
		switch {
		case body.Questions["q"].Criteria["others_work"] != "" && strings.Contains(body.State, "et al. reported"):
			choice = "others_work"
		case body.Questions["q"].Criteria["others_work"] != "":
			choice = "own_result"
		case body.Questions["q"].Criteria["biological_replicates"] != "" && strings.Contains(body.State, "GSM1: mouse 1 liver"):
			choice = "biological_replicates"
		case body.Questions["q"].Criteria["biological_replicates"] != "":
			choice = "unclear"
		case strings.Contains(body.State, "no effect") != strings.Contains(body.State, "does not"):
			choice = "challenges"
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"model": "jev-1.13.0", "answers": map[string]any{
			"q": map[string]any{"choice": choice, "probabilities": map[string]float64{choice: 0.9}, "confidence": 0.9},
		}})
	}))
	t.Cleanup(s.Close)
	return &jev.Client{BaseURL: s.URL, Key: "k", Model: "jev-1.13.0"}
}

func result(g *board.Gate, id string) board.Result {
	for _, ch := range g.Checks {
		if ch.ID == id {
			return ch.Result
		}
	}
	return "missing"
}

func TestQuoteMatchesAcrossCaseAndLineBreaks(t *testing.T) {
	f := setup(t)
	f.complete(t, "x raised y by 40%")
	g := f.gate(t, fakeJev(t, false))
	if result(g, "quote.E1") != board.Pass || g.Verdict != "" {
		t.Fatalf("%+v", g)
	}
	for _, id := range []string{"support.E1", "support.E2", "unit.D1"} {
		if result(g, id) != board.Pass {
			t.Fatalf("%s: %+v", id, g.Checks)
		}
	}
}

func TestParaphraseFailsAndRoutesToItsScout(t *testing.T) {
	f := setup(t)
	f.complete(t, "X increased Y by forty percent")
	g := f.gate(t, fakeJev(t, false))
	if g.Verdict != board.VFail || g.Route.Step != Literature {
		t.Fatalf("%+v %+v", g.Verdict, g.Route)
	}
}

func TestRetractedSourceFails(t *testing.T) {
	f := setup(t)
	f.record(t, Contrarian, "fetch", "openalex:W2", map[string]any{"source_id": "openalex:W2", "retracted": true}, "We found no effect of X on Y in rats.")
	f.complete(t, "X raised Y by 40%")
	if g := f.gate(t, fakeJev(t, false)); result(g, "retracted.E2") != board.Fail {
		t.Fatalf("%+v", g.Checks)
	}
}

func TestMissingContrarySearchFails(t *testing.T) {
	f := setup(t)
	if err := f.st.InWorkspace(context.Background(), f.ws, uuid.Nil, func(tx *store.Tx) error {
		_, err := tx.Q.InsertRecord(context.Background(), insertParams(f, Literature, "search", "", []byte(`{"database":"openalex","query":"q"}`)))
		return err
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := f.env.Owner.Exec(context.Background(), `UPDATE record SET superseded_at = now() WHERE kind = 'search' AND role = 'contrarian'`); err != nil {
		t.Fatal(err)
	}
	f.complete(t, "X raised Y by 40%")
	if g := f.gate(t, fakeJev(t, false)); result(g, "search.contrary") != board.Fail {
		t.Fatalf("%+v", g.Checks)
	}
}

// A claim's stance is its direction against the card question; this check asks
// whether the passage states that claim as written.
func TestSupportAsksWhetherThePassageStatesTheClaim(t *testing.T) {
	f := setup(t)
	f.complete(t, "X raised Y by 40%")
	if _, err := f.write(t, Literature, "evidence", map[string]string{"claim": "X does not raise Y", "source_id": "europepmc:PMC1", "quote": "X raised Y by 40%", "stance": "challenges"}); err != nil {
		t.Fatal(err)
	}
	g := f.gate(t, fakeJev(t, false))
	if result(g, "support.E2") != board.Pass || result(g, "support.E3") != board.Fail {
		t.Fatalf("%+v", g.Checks)
	}
}

// A passage retelling another study's result is flagged for the reviewer,
// never failed by Jev; the paper's own result passes.
func TestSecondHandEvidenceNeedsTheReviewer(t *testing.T) {
	f := setup(t)
	f.record(t, Literature, "fetch", "europepmc:PMC3", map[string]any{"source_id": "europepmc:PMC3", "full_text": true}, "Smith et al. reported that X raised Y in rats.")
	f.complete(t, "X raised Y by 40%")
	if _, err := f.write(t, Literature, "evidence", map[string]string{"claim": "X raises Y in rats", "source_id": "europepmc:PMC3", "quote": "X raised Y in rats", "stance": "supports"}); err != nil {
		t.Fatal(err)
	}
	g := f.gate(t, fakeJev(t, false))
	if result(g, "origin.E1") != board.Pass || result(g, "origin.E2") != board.Pass || result(g, "origin.E3") != board.Attention || g.Verdict != "" {
		t.Fatalf("%+v", g.Checks)
	}
	if _, err := f.write(t, Reviewer, "review_item", map[string]string{"check": "origin.E3", "result": "fail", "note": "Smith's result, retold"}); err != nil {
		t.Fatal(err)
	}
	if _, err := f.write(t, Reviewer, "verdict", map[string]string{"result": "pass", "reason": "fits"}); err != nil {
		t.Fatal(err)
	}
	f.finalize(t, g)
	if g.Verdict != board.VFail || g.Route == nil || g.Route.Step != Literature {
		t.Fatalf("a confirmed second-hand source must send the evidence back to its role: %+v", g)
	}
}

func TestJevDownNeedsReviewerNeverPasses(t *testing.T) {
	f := setup(t)
	f.complete(t, "X raised Y by 40%")
	g := f.gate(t, fakeJev(t, true))
	if result(g, "support.E1") != board.Attention {
		t.Fatalf("%+v", g.Checks)
	}
	if _, err := f.write(t, Reviewer, "verdict", map[string]string{"result": "pass", "reason": "fits"}); err != nil {
		t.Fatal(err)
	}
	f.finalize(t, g)
	if g.Verdict != board.VFail {
		t.Fatal("unsettled attention passed the gate")
	}
}

func TestEgressOffReviewerAnswers(t *testing.T) {
	f := setup(t)
	f.complete(t, "X raised Y by 40%")
	g := f.gate(t, nil)
	for _, id := range []string{"support.E1", "support.E2", "origin.E1", "origin.E2"} {
		if _, err := f.write(t, Reviewer, "review_item", map[string]string{"check": id, "result": "pass", "note": "read it"}); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := f.write(t, Reviewer, "review_item", map[string]string{"check": "unit.D1", "result": "pass", "choice": "biological_replicates", "note": "one mouse per sample"}); err != nil {
		t.Fatal(err)
	}
	if _, err := f.write(t, Reviewer, "verdict", map[string]string{"result": "pass", "reason": "fits"}); err != nil {
		t.Fatal(err)
	}
	f.finalize(t, g)
	if g.Verdict != board.VPass {
		t.Fatalf("%+v", g)
	}
	for _, ch := range g.Checks {
		if (strings.HasPrefix(ch.ID, "support.") || strings.HasPrefix(ch.ID, "origin.")) && ch.Judge != board.Reviewer {
			t.Fatalf("egress-off check not recorded as reviewer: %+v", ch)
		}
		if ch.ID == "unit.D1" && ch.Choice != "biological_replicates" {
			t.Fatalf("unit not recorded: %+v", ch)
		}
	}
}

func TestCannotProceedShortCircuits(t *testing.T) {
	f := setup(t)
	if _, err := f.write(t, Scoper, "cannot_proceed", map[string]string{"reason": "no viable question: needs wet-lab work"}); err != nil {
		t.Fatal(err)
	}
	if g := f.gate(t, nil); g.Verdict != board.CannotProceed || !strings.Contains(g.Reason, "no viable question") {
		t.Fatalf("%+v", g)
	}
}

func (f *fixture) nothingFound(t *testing.T, role, reason string) {
	t.Helper()
	body, err := json.Marshal(map[string]string{"reason": reason})
	if err != nil {
		t.Fatal(err)
	}
	if err := f.st.InWorkspace(context.Background(), f.ws, uuid.Nil, func(tx *store.Tx) error {
		_, err := tx.Q.InsertRecord(context.Background(), insertParams(f, role, "nothing_found", "", body))
		return err
	}); err != nil {
		t.Fatal(err)
	}
}

func TestDataScoutNothingFoundStopsWithNoUsableData(t *testing.T) {
	f := setup(t)
	if _, err := f.write(t, Scoper, "question", map[string]string{"text": "Does X raise Y in mice?"}); err != nil {
		t.Fatal(err)
	}
	f.record(t, DataScout, "search", "", map[string]any{"database": "geo", "query": "mouse Y"}, "")
	f.nothingFound(t, DataScout, "nothing measures Y")
	if g := f.gate(t, nil); g.Verdict != board.CannotProceed || g.Reason != "no usable data: nothing measures Y" {
		t.Fatalf("%+v", g)
	}
}

func TestDataScoutNothingFoundRequiresLiveGEOSearch(t *testing.T) {
	for _, tc := range []struct {
		name, role, database, query string
		superseded                  bool
	}{
		{name: "no search"},
		{name: "wrong role", role: Literature, database: "geo", query: "mouse"},
		{name: "wrong database", role: DataScout, database: "openalex", query: "mouse"},
		{name: "empty query", role: DataScout, database: "geo", query: "  "},
		{name: "superseded search", role: DataScout, database: "geo", query: "mouse", superseded: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f := setup(t)
			if tc.role != "" {
				f.record(t, tc.role, "search", "", map[string]any{"database": tc.database, "query": tc.query}, "")
			}
			if tc.superseded {
				if _, err := f.env.Owner.Exec(context.Background(), `UPDATE record SET superseded_at=now() WHERE card_id=$1 AND kind='search' AND role=$2`, f.card, DataScout); err != nil {
					t.Fatal(err)
				}
			}
			f.nothingFound(t, DataScout, "nothing measures Y")
			g := f.gate(t, nil)
			if g.Verdict != board.VFail || result(g, "search.datasets") != board.Fail || g.Route == nil || g.Route.Step != DataScout {
				t.Fatalf("unsearched no-data result: %+v", g)
			}
		})
	}
}

func TestContrarianNothingFoundIsAResult(t *testing.T) {
	f := setup(t)
	f.complete(t, "X raised Y by 40%")
	f.nothingFound(t, Contrarian, "no null results after five searches")
	g := f.gate(t, fakeJev(t, false))
	if result(g, "search.contrary") != board.Pass || g.Verdict != "" {
		t.Fatalf("%+v", g)
	}
}

func TestLiteratureNothingFoundFailsEvidence(t *testing.T) {
	f := setup(t)
	if _, err := f.write(t, Scoper, "question", map[string]string{"text": "Does X raise Y in mice?"}); err != nil {
		t.Fatal(err)
	}
	f.nothingFound(t, Literature, "no paper measures Y")
	if g := f.gate(t, nil); g.Verdict != board.VFail || result(g, "evidence.present") != board.Fail {
		t.Fatalf("%+v", g)
	}
}

func TestMissingReviewerVerdictFails(t *testing.T) {
	f := setup(t)
	f.complete(t, "X raised Y by 40%")
	g := f.gate(t, fakeJev(t, false))
	f.finalize(t, g)
	if g.Verdict != board.VFail || result(g, "review.verdict") != board.Fail {
		t.Fatalf("%+v", g)
	}
}

func TestGateDigestIgnoresGateRecordsButIncludesEvidence(t *testing.T) {
	f := setup(t)
	f.complete(t, "X raised Y by 40%")
	before := f.view(t).Digest(ReviewKinds...)
	if err := f.st.InWorkspace(context.Background(), f.ws, uuid.Nil, func(tx *store.Tx) error {
		for _, kind := range []string{"gate_draft", "gate"} {
			if _, err := tx.Q.InsertRecord(context.Background(), insertParams(f, Reviewer, kind, "", []byte(`{}`))); err != nil {
				return err
			}
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	afterGates := f.view(t).Digest(ReviewKinds...)
	if afterGates != before {
		t.Fatalf("gate records changed the scientific input digest: before %s after %s", before, afterGates)
	}
	if _, err := f.write(t, Literature, "evidence", map[string]string{"claim": "X raises Y in another model", "source_id": "europepmc:PMC1", "quote": "X raised Y by 40%", "stance": "supports"}); err != nil {
		t.Fatal(err)
	}
	afterEvidence := f.view(t).Digest(ReviewKinds...)
	if afterEvidence == afterGates {
		t.Fatal("new evidence did not change the scientific input digest")
	}
}

func TestLiteratureNothingFoundFailsEvidenceDespiteContrarianEvidence(t *testing.T) {
	f := setup(t)
	if _, err := f.write(t, Scoper, "question", map[string]string{"text": "Does X raise Y in mice?"}); err != nil {
		t.Fatal(err)
	}
	if _, err := f.write(t, DataScout, "dataset", map[string]string{"accession": "GSE1", "note": "liver"}); err != nil {
		t.Fatal(err)
	}
	if _, err := f.write(t, Methods, "notes", map[string]string{"text": "DESeq2"}); err != nil {
		t.Fatal(err)
	}
	if _, err := f.write(t, Contrarian, "evidence", map[string]string{"claim": "X does not raise Y", "source_id": "openalex:W2", "quote": "no effect of X on Y", "stance": "challenges"}); err != nil {
		t.Fatal(err)
	}
	f.nothingFound(t, Literature, "no paper measures Y")
	g := f.gate(t, nil)
	if g.Verdict != board.VFail || result(g, "evidence.present") != board.Fail {
		t.Fatalf("contrarian evidence satisfied the literature requirement: %+v", g)
	}
}
