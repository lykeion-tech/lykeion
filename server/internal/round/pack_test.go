package round

import (
	"context"
	"slices"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/google/uuid"

	"lykeion/server/internal/board"
	"lykeion/server/internal/pack"
	"lykeion/server/internal/pack/check"
	"lykeion/server/internal/records"
	"lykeion/server/internal/store"
	"lykeion/server/internal/testutil"
	db "lykeion/server/pkg/db/generated"
)

const (
	scoutSkill = "Scout the premise and record a note."
	revSkill   = "Review the scout's note."
	// noteCheck fails with premise_unsupported unless a note was recorded.
	noteCheck = `def check(records):
    if [r for r in records if r["kind"] == "note"]:
        return [{"id": "premise", "ok": True}]
    return [{"id": "premise", "ok": False, "failure": "premise_unsupported", "note": "no note"}]
`
)

// fieldPack is a pack whose grounding column runs one scout and a reviewer;
// extraKinds adds kind declarations for the scout.
func fieldPack(t *testing.T, checkSrc string, extraKinds ...string) []byte {
	kinds := `"note": {"roles": ["scout"]}`
	for _, k := range extraKinds {
		kinds += `, "` + k + `": {"roles": ["scout"]}`
	}
	return testutil.Zip(t, map[string]string{
		"pack.json": `{"name": "field", "version": "1", "start_kinds": ["question"],
			"columns": {"grounding": {"phases": [["scout"]], "reviewer": "rev", "kinds": {` + kinds + `}}},
			"failures": {"premise_unsupported": "grounding"}}`,
		"skills/grounding/scout/SKILL.md": scoutSkill,
		"skills/grounding/rev/SKILL.md":   revSkill,
		"checks/grounding.star":           checkSrc,
	})
}

// packCard stores the pack in the workspace and pins a new grounding card to it.
func (f *fix) packCard(t *testing.T, z []byte) uuid.UUID {
	t.Helper()
	p, err := pack.Parse(z)
	if err != nil {
		t.Fatal(err)
	}
	var row db.FieldPack
	if err := f.e.Store.InWorkspace(t.Context(), f.ws, uuid.Nil, func(tx *store.Tx) (err error) {
		row, err = pack.Insert(t.Context(), tx, f.e.Blobs, z, p, nil)
		return err
	}); err != nil {
		t.Fatal(err)
	}
	card := f.card(t, board.Grounding, 1)
	if _, err := f.env.Owner.Exec(t.Context(), `UPDATE card SET pack_id = $1 WHERE id = $2`, row.ID, card); err != nil {
		t.Fatal(err)
	}
	return card
}

// countChecks counts the pack check runs while the test lasts.
func countChecks(t *testing.T) *atomic.Int32 {
	var n atomic.Int32
	prev := runPackCheck
	runPackCheck = func(p *pack.Pack, ctx context.Context, column string, recs []check.Record) ([]check.Result, error) {
		n.Add(1)
		return prev(p, ctx, column, recs)
	}
	t.Cleanup(func() { runPackCheck = prev })
	return &n
}

func (f *fix) gateBody(t *testing.T, card uuid.UUID, round int32) board.Gate {
	t.Helper()
	var g board.Gate
	f.e.Store.InWorkspace(t.Context(), f.ws, uuid.Nil, func(tx *store.Tx) error {
		v, err := records.Load(t.Context(), tx, f.e.Blobs, card, board.Grounding)
		if err != nil {
			t.Fatal(err)
		}
		for _, r := range v.Kind("gate") {
			if r.Round == round {
				g = records.Body[board.Gate](r)
			}
		}
		return nil
	})
	return g
}

func TestPackCardQueuesPackRoles(t *testing.T) {
	f := setup(t)
	card := f.packCard(t, fieldPack(t, noteCheck))
	if err := f.e.Advance(t.Context(), f.ws, card); err != nil {
		t.Fatal(err)
	}
	runs := f.agentRuns(t, card)
	if len(runs) != 1 || runs[0].Role != "scout" || runs[0].Skill != scoutSkill {
		t.Fatalf("%+v", runs)
	}
}

func TestPackCheckFailRoutes(t *testing.T) {
	f := setup(t)
	card := f.packCard(t, fieldPack(t, noteCheck))
	f.e.Advance(t.Context(), f.ws, card)
	scout := f.agentRuns(t, card)[0]
	f.result(t, scout.ID, "nothing_found", "no support found")
	f.settle(t, scout.ID)
	if err := f.e.Advance(t.Context(), f.ws, card); err != nil {
		t.Fatal(err)
	}
	c := f.cardRow(t, card)
	if c.BoardColumn != board.Grounding || c.Round != 2 || c.RouteBacks != 1 || c.BlockKind != "" {
		t.Fatalf("%+v", c)
	}
	g := f.gateBody(t, card, 1)
	if g.Verdict != board.VFail || len(g.Checks) != 1 || g.Checks[0].Judge != board.Code || g.Checks[0].To != board.Grounding ||
		g.Checks[0].Note != "no note" || g.Route == nil || g.Route.Column != board.Grounding {
		t.Fatalf("gate %+v", g)
	}
	if runs := f.agentRuns(t, card); len(runs) != 1 || runs[0].Role != "scout" || runs[0].Round != 2 {
		t.Fatalf("redo %+v", runs)
	}
}

func TestPackCheckPasses(t *testing.T) {
	f := setup(t)
	card := f.packCard(t, fieldPack(t, noteCheck))
	f.e.Advance(t.Context(), f.ws, card)
	scout := f.agentRuns(t, card)[0]
	f.recordFor(t, scout.ID, "note", "", `{"text":"the premise holds"}`)
	f.settle(t, scout.ID)
	f.e.Advance(t.Context(), f.ws, card)
	rev := byRole(f.agentRuns(t, card))["rev"]
	if rev.Skill != revSkill || f.cardRow(t, card).State != board.InReview {
		t.Fatalf("reviewer %+v", rev)
	}
	f.recordFor(t, rev.ID, "verdict", "", `{"result":"pass","reason":"the note supports the premise"}`)
	f.settle(t, rev.ID)
	if err := f.e.Advance(t.Context(), f.ws, card); err != nil {
		t.Fatal(err)
	}
	if c := f.cardRow(t, card); c.BoardColumn != board.Hypotheses || c.RouteBacks != 0 {
		t.Fatalf("%+v", c)
	}
}

func TestPackCheckCrashBlocksInfra(t *testing.T) {
	f := setup(t)
	n := countChecks(t)
	card := f.packCard(t, fieldPack(t, "def check(records):\n    fail(\"boom\")\n"))
	f.e.Advance(t.Context(), f.ws, card)
	scout := f.agentRuns(t, card)[0]
	f.result(t, scout.ID, "nothing_found", "nothing")
	f.settle(t, scout.ID)
	if err := f.e.Advance(t.Context(), f.ws, card); err != nil {
		t.Fatal(err)
	}
	c := f.cardRow(t, card)
	if n.Load() != 3 || c.BlockKind != board.BlockInfra || c.RouteBacks != 0 {
		t.Fatalf("attempts %d, %+v", n.Load(), c)
	}
	if g := f.gateBody(t, card, 1); g.Verdict != board.CannotProceed || g.Block != board.BlockInfra || !strings.Contains(g.Reason, "boom") {
		t.Fatalf("gate %+v", g)
	}
}

func TestRecordKindsPerCard(t *testing.T) {
	f := setup(t)
	packed := f.cardRow(t, f.packCard(t, fieldPack(t, noteCheck, "fetch")))
	legacy := f.cardRow(t, f.card(t, board.Grounding, 1))
	var pk, lk map[string][]string
	f.e.Store.InWorkspace(t.Context(), f.ws, uuid.Nil, func(tx *store.Tx) error {
		for _, x := range []struct {
			c   db.Card
			out *map[string][]string
		}{{packed, &pk}, {legacy, &lk}} {
			kinds, err := KindsFor(t.Context(), tx, f.e.Blobs, x.c)
			if err != nil {
				t.Fatal(err)
			}
			*x.out = map[string][]string{}
			for name, k := range kinds {
				(*x.out)[name] = k.Roles
			}
		}
		return nil
	})
	want := map[string][]string{"note": {"scout"}, "review_item": {"rev"}, "verdict": {"rev"},
		"failed": {"scout", "rev"}, "nothing_found": {"scout", "rev"}}
	if len(pk) != len(want) {
		t.Fatalf("pack kinds %v", pk)
	}
	for name, roles := range want {
		if !slices.Equal(pk[name], roles) {
			t.Fatalf("pack kinds %v", pk)
		}
	}
	all := Kinds()
	if len(lk) != len(all) {
		t.Fatalf("legacy kinds %v", lk)
	}
	for name, k := range all {
		// Kinds merges the stages in map order, so compare the roles as sets.
		if got, want := slices.Sorted(slices.Values(lk[name])), slices.Sorted(slices.Values(k.Roles)); !slices.Equal(got, want) {
			t.Fatalf("legacy kind %s: %v, want %v", name, lk[name], k.Roles)
		}
	}
}

// fetchCheck passes only when the server recorded a fetch on the card.
const fetchCheck = `def check(records):
    if [r for r in records if r["kind"] == "fetch" and r["role"] == "scout"]:
        return [{"id": "fetched", "ok": True}]
    return [{"id": "fetched", "ok": False, "failure": "premise_unsupported"}]
`

func TestServerKindsOnPackCard(t *testing.T) {
	f := setup(t)
	card := f.packCard(t, fieldPack(t, fetchCheck, "fetch"))
	f.e.Advance(t.Context(), f.ws, card)
	scout := f.agentRuns(t, card)[0]
	f.sourceFor(t, scout, "fetch", "openalex:W1", map[string]any{"source_id": "openalex:W1"}, "the full text")
	if n := f.countWhere(t, `SELECT count(*) FROM record WHERE card_id = $1 AND kind = 'fetch' AND role = 'scout'`, card); n != 1 {
		t.Fatalf("fetch records %d", n)
	}
	f.result(t, scout.ID, "nothing_found", "nothing")
	f.settle(t, scout.ID)
	f.e.Advance(t.Context(), f.ws, card)
	if c := f.cardRow(t, card); c.State != board.InReview || c.RouteBacks != 0 {
		t.Fatalf("the check did not see the fetch: %+v", c)
	}
	// The pack declares fetch for its scout, but a role may never write a server kind.
	var kinds map[string]bool
	c := f.cardRow(t, card)
	f.e.Store.InWorkspace(t.Context(), f.ws, uuid.Nil, func(tx *store.Tx) error {
		ks, err := KindsFor(t.Context(), tx, f.e.Blobs, c)
		if err != nil {
			t.Fatal(err)
		}
		kinds = map[string]bool{}
		for name := range ks {
			kinds[name] = true
		}
		return nil
	})
	for _, k := range ServerKinds {
		if kinds[k] {
			t.Fatalf("roles may write server kind %s", k)
		}
	}
}
