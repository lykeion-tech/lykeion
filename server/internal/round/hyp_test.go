package round

import (
	"context"
	"slices"
	"strings"
	"testing"

	"github.com/google/uuid"

	"lykeion/server/internal/board"
	"lykeion/server/internal/ground"
	"lykeion/server/internal/hyp"
	"lykeion/server/internal/skills"
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
	f.env.Owner.QueryRow(ctx, `SELECT body->>'survivors' FROM record WHERE card_id = $1 AND kind = 'dedup' AND superseded_at IS NULL`, card).Scan(&survivors)
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
