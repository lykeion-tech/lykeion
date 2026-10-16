package round

import (
	"context"
	"encoding/json"
	"slices"
	"strings"
	"testing"

	"github.com/google/uuid"

	"lykeion/server/internal/board"
	"lykeion/server/internal/ground"
	"lykeion/server/internal/hyp"
	"lykeion/server/internal/skills"
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
					body, key := mechanismBody, "H1"
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
			f.recordFor(t, r.ID, "missing_premise", "", `{"gap":"dose saturation premise","step":"literature-scout"}`)
			f.settle(t, r.ID)
			advance()
			c := f.cardRow(t, card)
			if c.BoardColumn != board.Grounding || c.Rerun != ground.Literature {
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
			// Supply the repair and apply a passing Grounding gate through the engine.
			for _, r := range f.agentRuns(t, card) {
				f.recordFor(t, r.ID, "evidence", "E3", `{"claim":"dose saturation is known","source_id":"repair","quote":"saturation","stance":"supports"}`)
				f.settle(t, r.ID)
			}
			err := f.e.Store.InWorkspace(ctx, f.ws, uuid.Nil, func(tx *store.Tx) error {
				c, err := tx.Q.LockCard(ctx, db.LockCardParams{ID: card, WorkspaceID: f.ws})
				if err != nil {
					return err
				}
				g := board.Gate{Column: board.Grounding, Round: c.Round, Verdict: board.VPass}
				return f.e.finish(ctx, tx, c, board.FromCard(c), ParseSettings(json.RawMessage(`{}`)), &g)
			})
			if err != nil {
				t.Fatal(err)
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
			if c := f.cardRow(t, card); c.BoardColumn != board.Hypotheses || c.RouteBacks != 1 {
				t.Fatalf("spurious repeated routeback: %+v", c)
			}
			if n := f.countWhere(t, `SELECT count(*) FROM record WHERE card_id=$1 AND kind='missing_premise' AND superseded_at IS NOT NULL`, card); n != 1 {
				t.Fatalf("lost history: %d", n)
			}
			f.recordFor(t, rerun.ID, "missing_premise", "", `{"gap":"a newly discovered premise","step":"literature-scout"}`)
			f.settle(t, rerun.ID)
			advance()
			if c := f.cardRow(t, card); c.BoardColumn != board.Grounding || c.RouteBacks != 2 {
				t.Fatalf("new premise was suppressed: %+v", c)
			}

		})
	}
}
