package round

import (
	"bytes"
	"context"
	"encoding/json"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"

	"lykeion/server/internal/auth"
	"lykeion/server/internal/board"
	"lykeion/server/internal/ground"
	"lykeion/server/internal/hyp"
	"lykeion/server/internal/middleware"
	"lykeion/server/internal/records"
	"lykeion/server/internal/service"
	"lykeion/server/internal/skills"
	"lykeion/server/internal/sources"
	"lykeion/server/internal/store"
	db "lykeion/server/pkg/db/generated"
)

// grounded gives a card the records a passing Grounding leaves.
func (f *fix) grounded(t *testing.T, card uuid.UUID) {
	t.Helper()
	for _, r := range []struct{ kind, role, key, body string }{
		{"question", ground.Scoper, "", `{"text":"Does X raise Y in mouse liver?"}`},
		{"notes", ground.Methods, "", `{"text":"## Gaps and open questions\n- whether Z mediates\n- dose dependence\n"}`},
		{"evidence", ground.Literature, "E1", `{"claim":"X raises Y","source_id":"europepmc:PMC1","quote":"X raised Y by 40%","stance":"supports"}`},
		{"evidence", ground.Contrarian, "E2", `{"claim":"X does not raise Y","source_id":"openalex:W2","quote":"no effect of X on Y","stance":"challenges"}`},
		{"dataset", ground.DataScout, "D1", `{"accession":"GSE1","note":"mouse liver"}`},
		{"gate", "server", "1", `{"column":"grounding","round":1,"verdict":"pass","checks":[{"id":"unit.D1","judge":"jev","result":"pass","choice":"biological_replicates"}]}`},
	} {
		if _, err := f.env.Owner.Exec(context.Background(), `INSERT INTO record (workspace_id, card_id, board_column, round, kind, role, key, body)
			VALUES ($1, $2, 'grounding', 1, $3, $4, $5, $6::jsonb)`, f.ws, card, r.kind, r.role, r.key, r.body); err != nil {
			t.Fatal(err)
		}
	}
}

// recordFor writes a record as the agent run's role would through lyk.
func (f *fix) recordFor(t *testing.T, run uuid.UUID, kind, key, body string) {
	t.Helper()
	if _, err := f.env.Owner.Exec(context.Background(), `INSERT INTO record (workspace_id, card_id, board_column, round, kind, role, key, agent_run_id, body)
		SELECT workspace_id, card_id, board_column, round, $2, role, $3, id, $4::jsonb FROM agent_run WHERE id = $1`, run, kind, key, body); err != nil {
		t.Fatal(err)
	}
}

func byRole(ts []db.AgentRun) map[string]db.AgentRun {
	out := map[string]db.AgentRun{}
	for _, tk := range ts {
		out[tk.Role] = tk
	}
	return out
}

const mechanismBody = `{"statement":"X raises Y by inducing Z","kind":"mechanism","mechanism":[{"text":"X induces Z","claims":["E1"]}],"assumptions":[{"text":"Z is expressed in mouse liver","claims":["E1"]}],"boundary_conditions":[],"uncertainties":[]}`
const artefactBody = `{"statement":"A batch artefact in the Y assay mimics an effect of X","kind":"artefact","mechanism":[{"text":"treated and control samples ran in different assay batches","claims":["E2"]}],"assumptions":[{"text":"the Y assay drifts between batches","claims":["E2"]}],"boundary_conditions":[],"uncertainties":[]}`

func (f *fix) countWhere(t *testing.T, q string, args ...any) int {
	t.Helper()
	var n int
	if err := f.env.Owner.QueryRow(context.Background(), q, args...).Scan(&n); err != nil {
		t.Fatal(err)
	}
	return n
}

func TestHypothesesSeedsGeneratorsThenDedupsBeforeTheDecomposer(t *testing.T) {
	f := setup(t)
	ctx := context.Background()
	card := f.card(t, board.Hypotheses, 2)
	f.grounded(t, card)
	f.e.Advance(ctx, f.ws, card)
	ts := f.agentRuns(t, card)
	var roles []string
	for _, tk := range ts {
		roles = append(roles, tk.Role)
		if tk.Skill == "" || tk.Skill != skills.Role(board.Hypotheses, "generator") {
			t.Fatalf("%s did not get the generator skill", tk.Role)
		}
		// Each generator resumes its own session, never a sibling's.
		if want := "card:" + card.String() + "/role:" + tk.Role; tk.ContinuityScope != want {
			t.Fatalf("%s has scope %q, want %q", tk.Role, tk.ContinuityScope, want)
		}
	}
	slices.Sort(roles)
	if !slices.Equal(roles, hyp.Generators) {
		t.Fatalf("queued %v", roles)
	}
	if n := f.countWhere(t, `SELECT count(DISTINCT body) FROM record WHERE card_id = $1 AND kind = 'seed' AND superseded_at IS NULL`, card); n != 3 {
		t.Fatalf("%d distinct seeds", n)
	}
	g := byRole(ts)
	f.recordFor(t, g["generator-1"].ID, "candidate", "H1", mechanismBody)
	f.recordFor(t, g["generator-2"].ID, "candidate", "H2", artefactBody)
	f.recordFor(t, g["generator-3"].ID, "candidate", "H3", mechanismBody)
	for _, tk := range ts {
		f.settle(t, tk.ID)
	}
	f.e.Advance(ctx, f.ws, card)
	var survivors string
	if err := f.env.Owner.QueryRow(ctx, `SELECT body->>'survivors' FROM record WHERE card_id = $1 AND kind = 'dedup' AND superseded_at IS NULL`, card).Scan(&survivors); err != nil {
		t.Fatal(err)
	}
	if survivors != `["H1", "H2"]` {
		t.Fatalf("survivors %s", survivors)
	}
	ts = f.agentRuns(t, card)
	if last := ts[len(ts)-1]; last.Role != hyp.Decomposer || last.Skill != skills.Role(board.Hypotheses, hyp.Decomposer) {
		t.Fatalf("%+v", last)
	}
}

func TestMissingPremiseSendsTheCardBackAndKeepsHypothesesRecords(t *testing.T) {
	f := setup(t)
	ctx := context.Background()
	card := f.card(t, board.Hypotheses, 2)
	f.grounded(t, card)
	f.e.Advance(ctx, f.ws, card)
	ts := f.agentRuns(t, card)
	g := byRole(ts)
	f.recordFor(t, g["generator-1"].ID, "missing_premise", "", `{"gap":"the dose at which X saturates Y","step":"literature-scout"}`)
	f.recordFor(t, g["generator-2"].ID, "candidate", "H1", mechanismBody)
	f.recordFor(t, g["generator-3"].ID, "candidate", "H2", artefactBody)
	for _, tk := range ts {
		f.settle(t, tk.ID)
	}
	f.e.Advance(ctx, f.ws, card)

	c := f.cardRow(t, card)
	if c.BoardColumn != board.Grounding || c.Rerun != ground.Literature || !slices.Contains(c.Stale, board.Hypotheses) {
		t.Fatalf("%+v", c)
	}
	if n := f.countWhere(t, `SELECT count(*) FROM agent_run WHERE card_id = $1 AND role = 'decomposer'`, card); n != 0 {
		t.Fatal("the decomposer ran on a column missing a premise")
	}
	if n := f.countWhere(t, `SELECT count(*) FROM record WHERE card_id = $1 AND board_column = 'hypotheses' AND kind = 'candidate' AND superseded_at IS NULL`, card); n != 2 {
		t.Fatalf("Hypotheses' records must be kept while the card is in Grounding: %d live candidates", n)
	}
	if n := f.countWhere(t, `SELECT count(*) FROM record WHERE card_id = $1 AND board_column = 'hypotheses' AND kind = 'gate'
		AND body->>'verdict' = 'fail' AND body->'route'->>'column' = 'grounding'`, card); n != 1 {
		t.Fatal("no gate routed the card to Grounding")
	}
	ts = f.agentRuns(t, card)
	if len(ts) != 1 || ts[0].Role != ground.Literature || !strings.Contains(ts[0].HandoffNote, "the dose at which X saturates Y") ||
		!strings.Contains(ts[0].Prompt, "the dose at which X saturates Y") {
		t.Fatalf("the literature scout must be redone with the gap: %+v", ts)
	}
}

func TestHypothesesKindsAreRegistered(t *testing.T) {
	k := Kinds()
	if !slices.Equal(k["candidate"].Roles, hyp.Generators) || k["candidate"].KeyPrefix != "H" {
		t.Fatalf("%+v", k["candidate"])
	}
	for _, name := range []string{"verdict", "review_item"} {
		if !slices.Contains(k[name].Roles, ground.Reviewer) || !slices.Contains(k[name].Roles, hyp.Reviewer) {
			t.Fatalf("%s: %v", name, k[name].Roles)
		}
	}
	if !slices.Contains(k["cannot_proceed"].Roles, ground.Scoper) || !slices.Contains(k["cannot_proceed"].Roles, hyp.Selector) {
		t.Fatalf("%v", k["cannot_proceed"].Roles)
	}
	for _, name := range []string{"seed", "dedup", "pairwise"} {
		if _, ok := k[name]; ok {
			t.Fatalf("roles may write %s", name)
		}
	}
	for _, role := range append(slices.Clone(hyp.Generators), hyp.Decomposer, hyp.NoveltyScout, hyp.Selector, hyp.Reviewer) {
		if !slices.Contains(k["nothing_found"].Roles, role) || !slices.Contains(k["failed"].Roles, role) {
			t.Fatalf("%s cannot end its agent run with a result", role)
		}
	}
}

func TestLaterMissingPremiseRecoversAfterGrounding(t *testing.T) {
	for _, owner := range []string{hyp.Decomposer, hyp.Selector} {
		t.Run(owner, func(t *testing.T) {
			f := setup(t)
			ctx := context.Background()
			card := f.card(t, board.Hypotheses, 2)
			f.grounded(t, card)
			f.genericRepairBaseline(t, card)
			advance := func() {
				t.Helper()
				if err := f.e.Advance(ctx, f.ws, card); err != nil {
					t.Fatal(err)
				}
			}
			generators := func() {
				t.Helper()
				for _, r := range f.agentRuns(t, card) {
					if !slices.Contains(hyp.Generators, r.Role) {
						continue
					}
					body, key := strings.ReplaceAll(mechanismBody, "mouse liver", "sensor stations"), "H1"
					if r.Role == "generator-2" {
						body, key = artefactBody, "H2"
					}
					if r.Role == "generator-3" {
						f.result(t, r.ID, "nothing_found", "no more")
					} else {
						f.recordFor(t, r.ID, "candidate", key, body)
					}
					f.settle(t, r.ID)
				}
			}
			finishUntil := func(role string) db.AgentRun {
				t.Helper()
				for i := 0; i < 4; i++ {
					advance()
					rs := f.agentRuns(t, card)
					if len(rs) == 0 {
						t.Fatalf("no runs in %+v", f.cardRow(t, card))
					}
					r := rs[len(rs)-1]
					if r.Role == role {
						return r
					}
					if r.Role != hyp.Decomposer && r.Role != hyp.NoveltyScout {
						t.Fatalf("wanted %s, got %s in %+v", role, r.Role, f.cardRow(t, card))
					}
					if r.Round > 2 && f.countWhere(t, `SELECT count(*) FROM record WHERE card_id=$1 AND kind='missing_premise' AND superseded_at IS NULL`, card) != 1 {
						t.Fatal("another role retired the retained premise before its owner reran")
					}
					f.result(t, r.ID, "nothing_found", "no extra result")
					f.settle(t, r.ID)
				}
				t.Fatal("role never queued")
				return db.AgentRun{}
			}
			advance()
			generators()
			r := finishUntil(owner)
			raw, err := f.prepareHyp(t, card, r, "missing_premise", `{"gap":"observation station identity","kind":"dataset_metadata","dataset":"D1","why_required":"station identity distinguishes independent observations from repeated readings"}`)
			if err != nil {
				t.Fatal(err)
			}
			f.recordFor(t, r.ID, "missing_premise", "", string(raw))
			f.settle(t, r.ID)
			advance()
			c := f.cardRow(t, card)
			if c.BoardColumn != board.Grounding || c.Rerun != ground.DataScout {
				t.Fatalf("first premise did not route immediately: %+v", c)
			}
			livePremises := func() int {
				return f.countWhere(t, `SELECT count(*) FROM record WHERE card_id=$1 AND kind='missing_premise' AND superseded_at IS NULL`, card)
			}
			if livePremises() != 1 {
				t.Fatal("premise history retired before its owner reran")
			}
			if n := f.countWhere(t, `SELECT count(*) FROM record WHERE card_id=$1 AND kind='candidate' AND superseded_at IS NULL`, card); n != 2 {
				t.Fatalf("retained candidates: %d", n)
			}
			runs := f.agentRuns(t, card)
			if len(runs) != 1 || runs[0].Role != ground.DataScout {
				t.Fatalf("metadata target: %+v", runs)
			}
			scout := runs[0]
			for _, field := range []string{"observation station identity", "dataset_metadata", "D1", "station identity distinguishes independent observations from repeated readings"} {
				if !strings.Contains(scout.HandoffNote, field) || !strings.Contains(scout.Prompt, field) {
					t.Fatalf("missing handoff field %q: %+v", field, scout)
				}
			}
			metadata := "Station identity: readings belong to independently installed sensor stations."
			f.sourceFor(t, scout, "fetch", "geo:GSE1", map[string]any{"source_id": "geo:GSE1", "source_kind": "dataset_metadata", "source_url": "https://example.invalid/deposit/GSE1", "full_text": false}, metadata)
			f.sourceFor(t, scout, "open_dataset", "GSE1", map[string]any{"accession": "GSE1"}, "Readings from independent sensor stations share a calibration standard.")
			f.recordFor(t, scout.ID, "dataset", "D1", `{"accession":"GSE1","note":"independent sensor stations; recorded station identity permits interpretation"}`)
			f.settle(t, scout.ID)
			advance()
			// Metadata alone cannot replace the literature evidence requirement.
			if c := f.cardRow(t, card); c.BoardColumn != board.Grounding || c.Rerun != ground.Literature {
				t.Fatalf("missing literature escaped actual Grounding checks: %+v", c)
			}
			runs = f.agentRuns(t, card)
			if len(runs) != 1 || runs[0].Role != ground.Literature {
				t.Fatalf("literature repair: %+v", runs)
			}
			literature := runs[0]
			f.sourceFor(t, literature, "fetch", "europepmc:PMC1", map[string]any{"source_id": "europepmc:PMC1", "full_text": false}, "X raised Y by 40%")
			for _, database := range []string{"openalex", "europepmc"} {
				f.sourceFor(t, literature, "search", "", map[string]any{"database": database, "query": "sensor calibration prior work"}, "")
			}
			f.recordFor(t, literature.ID, "evidence", "E1", `{"claim":"X raises Y","source_id":"europepmc:PMC1","quote":"X raised Y by 40%","stance":"supports"}`)
			f.recordFor(t, literature.ID, "evidence", "E3", `{"claim":"readings belong to independent stations","source_id":"geo:GSE1","quote":"readings belong to independently installed sensor stations","stance":"supports"}`)
			f.settle(t, literature.ID)
			advance()
			if c := f.cardRow(t, card); c.BoardColumn != board.Grounding || c.State != board.InReview {
				t.Fatalf("metadata bypassed reviewer: %+v", c)
			}
			reviewer := byRole(f.agentRuns(t, card))[ground.Reviewer]
			var draft board.Gate
			var draftRaw []byte
			if err := f.env.Owner.QueryRow(ctx, `SELECT body FROM record WHERE card_id=$1 AND kind='gate_draft' AND superseded_at IS NULL ORDER BY created_at DESC LIMIT 1`, card).Scan(&draftRaw); err != nil {
				t.Fatal(err)
			}
			if err := json.Unmarshal(draftRaw, &draft); err != nil {
				t.Fatal(err)
			}
			expected := map[string]bool{"quote.E3": false, "support.E3": false, "origin.E3": false, "search.databases": false, "search.contrary": false, "unit.D1": false}
			for _, check := range draft.Checks {
				if _, ok := expected[check.ID]; ok {
					expected[check.ID] = true
				}
				if check.Judge == board.Code && check.Result != board.Pass {
					t.Fatalf("actual code check failed: %+v", check)
				}
				if check.Result != board.Attention {
					continue
				}
				choice := ""
				if strings.HasPrefix(check.ID, "unit.") {
					choice = "shared_calibration"
				}
				body, _ := json.Marshal(ground.ReviewItem{Check: check.ID, Result: "pass", Choice: choice, Note: "synthetic source passage and station structure inspected"})
				f.recordFor(t, reviewer.ID, "review_item", "", string(body))
			}
			for id, present := range expected {
				if !present {
					t.Fatalf("actual Grounding check missing: %s", id)
				}
			}
			f.recordFor(t, reviewer.ID, "verdict", "", `{"result":"pass","reason":"source-backed scoped sensor comparison is feasible"}`)
			f.settle(t, reviewer.ID)
			advance()
			if c := f.cardRow(t, card); c.BoardColumn != board.Hypotheses {
				t.Fatalf("actual Grounding gate did not pass: %+v", c)
			}
			if n := f.countWhere(t, `SELECT count(*) FROM record WHERE card_id=$1 AND kind='evidence' AND key='E3' AND role='literature-scout' AND superseded_at IS NULL`, card); n != 1 {
				t.Fatal("metadata E repair lost literature ownership")
			}
			if n := f.countWhere(t, `SELECT count(*) FROM record WHERE card_id=$1 AND kind='fetch' AND key='geo:GSE1' AND body->>'source_kind'='dataset_metadata' AND body->>'sha256' <> '' AND blob_key <> ''`, card); n != 1 {
				t.Fatal("repair fetch was not server recorded")
			}

			advance()
			if livePremises() != 1 {
				t.Fatal("new generators retired later role premise")
			}
			generators()
			rerun := finishUntil(owner)
			if rerun.ID == r.ID || rerun.Round <= r.Round {
				t.Fatalf("owner did not rerun: %+v", rerun)
			}
			if livePremises() != 0 {
				t.Fatal("owner queue did not supersede its old premise")
			}
			// The cap counts the route-back and the Grounding redo for the
			// missing literature; a repeated route-back would make it 3.
			if c := f.cardRow(t, card); c.BoardColumn != board.Hypotheses || c.RouteBacks != 2 || c.Flag != "" {
				t.Fatalf("spurious repeated routeback: %+v", c)
			}
			if n := f.countWhere(t, `SELECT count(*) FROM record WHERE card_id=$1 AND kind='missing_premise' AND superseded_at IS NOT NULL`, card); n != 1 {
				t.Fatalf("lost history: %d", n)
			}
			f.recordFor(t, rerun.ID, "missing_premise", "", `{"gap":"a newly discovered premise","step":"literature-scout"}`)
			f.settle(t, rerun.ID)
			advance()
			if c := f.cardRow(t, card); c.BoardColumn != board.Grounding || c.RouteBacks != 3 || c.Flag != "" {
				t.Fatalf("new premise was suppressed: %+v", c)
			}

		})
	}
}

// sourceFor uses the server's source writer for an active fixture role.
func (f *fix) sourceFor(t *testing.T, run db.AgentRun, kind, key string, body map[string]any, text string) {
	t.Helper()
	token := "ltk_" + uuid.NewString()
	run.Attempt = 1
	if _, err := f.env.Owner.Exec(t.Context(), `UPDATE agent_run SET status='running', attempt=1 WHERE id=$1`, run.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := f.env.Owner.Exec(t.Context(), `INSERT INTO agent_run_token(token_hash,workspace_id,agent_run_id,card_id,role,attempt,expires_at) VALUES($1,$2,$3,$4,$5,$6,now()+interval '1 hour')`, auth.Hash(token), f.ws, run.ID, run.CardID, run.Role, run.Attempt); err != nil {
		t.Fatal(err)
	}
	role := middleware.Role{AgentRunID: run.ID, WorkspaceID: f.ws, CardID: *run.CardID, Column: run.BoardColumn, Round: run.Round, Role: run.Role}
	h := sources.Hosted{Store: f.e.Store, Blobs: f.e.Blobs}
	if err := h.Record(t.Context(), role, token, kind, key, body, text); err != nil {
		t.Fatal(err)
	}
}

func (f *fix) genericRepairBaseline(t *testing.T, card uuid.UUID) {
	t.Helper()
	ctx := t.Context()
	if _, err := f.env.Owner.Exec(ctx, `UPDATE record SET body=replace(replace(body::text,'mouse liver','sensor stations'),'biological_replicates','shared_calibration')::jsonb WHERE card_id=$1`, card); err != nil {
		t.Fatal(err)
	}
	blob := f.ws.String() + "/" + card.String() + "/baseline-contrary"
	text := "no effect of X on Y"
	if err := f.e.Blobs.Put(ctx, blob, bytes.NewBufferString(text), int64(len(text))); err != nil {
		t.Fatal(err)
	}
	if _, err := f.env.Owner.Exec(ctx, `INSERT INTO record(workspace_id,card_id,board_column,round,kind,role,key,body,blob_key) VALUES($1,$2,'grounding',1,'fetch','contrarian','openalex:W2','{"source_id":"openalex:W2"}',$3),($1,$2,'grounding',1,'search','contrarian','','{"database":"openalex","query":"sensor calibration counterevidence"}','')`, f.ws, card, blob); err != nil {
		t.Fatal(err)
	}
}

func TestMetadataChangesDiscardPendingGate(t *testing.T) {
	f := setup(t)
	card := f.atGate(t)
	entered, release, checks := f.slowJev(t)
	ctx, cancel := context.WithTimeout(t.Context(), 8*time.Second)
	defer cancel()
	var p *pending
	if err := f.e.Store.InWorkspace(ctx, f.ws, uuid.Nil, func(tx *store.Tx) (err error) { p, err = f.e.read(ctx, tx, card); return err }); err != nil {
		t.Fatal(err)
	}
	if p == nil {
		t.Fatal("no pending gate")
	}
	type judgment struct {
		gate *board.Gate
		err  error
	}
	done := make(chan judgment, 1)
	go func() { g, err := f.e.call(ctx, p); done <- judgment{g, err} }()
	select {
	case <-entered:
	case <-ctx.Done():
		t.Fatal(ctx.Err())
	}
	// The controlled running writer is separate from the settled stage scouts.
	// No ordinary settled role is allowed to reopen itself or write during gating.
	role, token := f.recordRole(t, card)
	h := sources.Hosted{Store: f.e.Store, Blobs: f.e.Blobs}
	if err := h.Record(ctx, role, token, "fetch", "geo:GSE1", map[string]any{"source_id": "geo:GSE1", "source_kind": "dataset_metadata", "source_url": "https://example.invalid/deposit/GSE1", "full_text": false}, "Station identity: independent sensor stations."); err != nil {
		t.Fatal(err)
	}
	close(release)
	judged := <-done
	if judged.err != nil {
		t.Fatal(judged.err)
	}
	if judged.gate.InputDigest != p.v.Digest(ground.ReviewKinds...) || checks.Load() != 1 {
		t.Fatalf("judgment did not retain original snapshot: %+v", judged.gate)
	}
	var kick bool
	if err := f.e.Store.InWorkspace(ctx, f.ws, uuid.Nil, func(tx *store.Tx) (err error) { kick, err = f.e.write(ctx, tx, p, judged.gate); return err }); err != nil {
		t.Fatal(err)
	}
	if !kick {
		t.Fatal("changed metadata did not discard pending judgment")
	}
	if n := f.count(t, card, `SELECT count(*) FROM record WHERE card_id=$1 AND kind IN ('gate','gate_draft')`); n != 0 {
		t.Fatalf("old digest committed: %d", n)
	}
	if n := f.count(t, card, `SELECT count(*) FROM agent_run WHERE card_id=$1 AND role='grounding-reviewer'`); n != 0 {
		t.Fatal("reviewer queued on old digest")
	}
	if c := f.cardRow(t, card); c.BoardColumn != board.Grounding || c.GatingUntil != nil {
		t.Fatalf("pending metadata gate advanced or retained lease: %+v", c)
	}
	// Only after proving metadata alone invalidated the digest, finish the writer
	// and let the engine judge the new snapshot.
	f.result(t, role.AgentRunID, "nothing_found", "controlled source writer finished")
	f.settle(t, role.AgentRunID)
	if err := f.e.Advance(ctx, f.ws, card); err != nil {
		t.Fatal(err)
	}
	var digest string
	if err := f.env.Owner.QueryRow(ctx, `SELECT body->>'input_digest' FROM record WHERE card_id=$1 AND kind='gate_draft'`, card).Scan(&digest); err != nil {
		t.Fatal(err)
	}
	if digest == judged.gate.InputDigest || checks.Load() != 2 {
		t.Fatalf("new metadata was not judged afresh: %s checks %d", digest, checks.Load())
	}
}

func (f *fix) prepareHyp(t *testing.T, card uuid.UUID, run db.AgentRun, kind, raw string) (json.RawMessage, error) {
	t.Helper()
	var out json.RawMessage
	err := f.e.Store.InWorkspace(t.Context(), f.ws, uuid.Nil, func(tx *store.Tx) (err error) {
		out, err = hyp.Kinds()[kind].Prepare(t.Context(), tx, service.Writer{Card: card, AgentRun: run.ID, Column: board.Hypotheses, Role: run.Role, Round: run.Round}, json.RawMessage(raw))
		return err
	})
	return out, err
}

func TestMetadataRepairPreservesStaleEvidenceRules(t *testing.T) {
	t.Run("owner supersession retains history but refuses old citations", func(t *testing.T) {
		f := setup(t)
		card := f.card(t, board.Hypotheses, 2)
		f.grounded(t, card)
		f.genericRepairBaseline(t, card)
		if err := f.e.Advance(t.Context(), f.ws, card); err != nil {
			t.Fatal(err)
		}
		generators := f.agentRuns(t, card)
		f.recordFor(t, generators[0].ID, "candidate", "H1", strings.ReplaceAll(mechanismBody, "mouse liver", "sensor stations"))
		// The engine queues the evidence owner in a real Grounding repair visit.
		if _, err := f.env.Owner.Exec(t.Context(), `UPDATE card SET board_column='grounding',round=3,rerun='literature-scout' WHERE id=$1`, card); err != nil {
			t.Fatal(err)
		}
		if err := f.e.Advance(t.Context(), f.ws, card); err != nil {
			t.Fatal(err)
		}
		if n := f.countWhere(t, `SELECT count(*) FROM record WHERE card_id=$1 AND kind='evidence' AND key='E1' AND superseded_at IS NOT NULL`, card); n != 1 {
			t.Fatalf("owner did not supersede E1: %d", n)
		}
		if n := f.countWhere(t, `SELECT count(*) FROM record WHERE card_id=$1 AND kind='candidate' AND key='H1' AND body->>'statement'<>''`, card); n != 1 {
			t.Fatal("historical hypothesis became unreadable")
		}
		if _, err := f.prepareHyp(t, card, generators[0], "candidate", strings.ReplaceAll(mechanismBody, "mouse liver", "sensor stations")); err == nil || !strings.Contains(err.Error(), "E1 is not a live Grounding claim") {
			t.Fatalf("stale E citation accepted: %v", err)
		}
		var v *records.View
		if err := f.e.Store.InWorkspace(t.Context(), f.ws, uuid.Nil, func(tx *store.Tx) (err error) {
			v, err = records.Load(t.Context(), tx, f.e.Blobs, card, board.Hypotheses)
			return err
		}); err != nil {
			t.Fatal(err)
		}
		gate, err := hyp.Check(t.Context(), v, nil)
		if err != nil {
			t.Fatal(err)
		}
		refused := false
		for _, check := range gate.Checks {
			if check.ID == "claims.H1" && check.Result == board.Fail {
				refused = true
			}
		}
		if !refused || gate.Verdict == board.VPass {
			t.Fatalf("retained hypothesis fed new gate: %+v", gate)
		}
	})
	t.Run("same visit unresolved metadata routes before decomposer", func(t *testing.T) {
		f := setup(t)
		card := f.card(t, board.Hypotheses, 2)
		f.grounded(t, card)
		f.genericRepairBaseline(t, card)
		if err := f.e.Advance(t.Context(), f.ws, card); err != nil {
			t.Fatal(err)
		}
		runs := f.agentRuns(t, card)
		raw, err := f.prepareHyp(t, card, runs[0], "missing_premise", `{"gap":"station collection identity","kind":"dataset_metadata","dataset":"D1","why_required":"independence must be interpretable"}`)
		if err != nil {
			t.Fatal(err)
		}
		f.recordFor(t, runs[0].ID, "missing_premise", "", string(raw))
		for _, run := range runs {
			f.result(t, run.ID, "nothing_found", "no additional candidate")
			f.settle(t, run.ID)
		}
		if err := f.e.Advance(t.Context(), f.ws, card); err != nil {
			t.Fatal(err)
		}
		if c := f.cardRow(t, card); c.BoardColumn != board.Grounding || c.Rerun != ground.DataScout || c.RouteBacks != 1 {
			t.Fatalf("unresolved premise suppressed: %+v", c)
		}
		if n := f.countWhere(t, `SELECT count(*) FROM agent_run WHERE card_id=$1 AND role IN ('decomposer','novelty-scout','selector','hypotheses-reviewer')`, card); n != 0 {
			t.Fatalf("later phase queued despite unresolved metadata: %d", n)
		}
		if n := f.countWhere(t, `SELECT count(*) FROM record WHERE card_id=$1 AND kind='missing_premise' AND superseded_at IS NULL`, card); n != 1 {
			t.Fatal("unresolved premise cleared")
		}
	})
}
