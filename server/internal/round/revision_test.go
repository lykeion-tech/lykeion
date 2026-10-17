package round

import (
	"errors"
	"testing"

	"github.com/google/uuid"

	"lykeion/server/internal/board"
	"lykeion/server/internal/gate"
	"lykeion/server/internal/store"
	db "lykeion/server/pkg/db/generated"
)

// revisable is a card in Hypotheses with a live result to cite.
func (f *fix) revisable(t *testing.T, kind string) (uuid.UUID, string) {
	t.Helper()
	card := f.card(t, board.Hypotheses, 2)
	if _, err := f.env.Owner.Exec(t.Context(), `UPDATE card SET start_kind = $2 WHERE id = $1`, card, kind); err != nil {
		t.Fatal(err)
	}
	var rec string
	if err := f.env.Owner.QueryRow(t.Context(), `INSERT INTO record (workspace_id, card_id, board_column, round, kind, role, body)
		VALUES ($1, $2, 'hypotheses', 2, 'result', 'tester', '{}') RETURNING id`, f.ws, card).Scan(&rec); err != nil {
		t.Fatal(err)
	}
	return card, rec
}

func (f *fix) revise(t *testing.T, card uuid.UUID, r Revision, drift gate.Choice) error {
	t.Helper()
	return f.e.Store.InWorkspace(t.Context(), f.ws, uuid.Nil, func(tx *store.Tx) error {
		c, err := tx.Q.LockCard(t.Context(), db.LockCardParams{ID: card, WorkspaceID: f.ws})
		if err != nil {
			return err
		}
		_, err = f.e.Revise(t.Context(), tx, c, r, drift)
		return err
	})
}

var same = gate.Choice{Question: "drift", Judge: "jev", Answer: "same"}

func TestReviseClaimRefused(t *testing.T) {
	f := setup(t)
	card, rec := f.revisable(t, "claim")
	if err := f.revise(t, card, Revision{Text: "Does X lower Y?", Cites: rec, Reason: "question"}, same); !errors.Is(err, ErrRevisionRefused) {
		t.Fatalf("a claim was revised: %v", err)
	}
	if c := f.cardRow(t, card); c.Question != "Does X raise Y?" || c.RouteBacks != 0 ||
		f.count(t, card, `SELECT count(*) FROM record WHERE card_id = $1 AND kind = 'starting_point'`) != 0 {
		t.Fatalf("a refused revision changed the card: %+v", c)
	}
}

func TestReviseDataset(t *testing.T) {
	f := setup(t)
	card, rec := f.revisable(t, "dataset")
	if err := f.revise(t, card, Revision{Text: "another dataset", Cites: rec, Reason: "dataset"}, same); !errors.Is(err, ErrRevisionRefused) {
		t.Fatalf("a dataset card revised more than its question: %v", err)
	}
	if err := f.revise(t, card, Revision{Text: "Does X raise Y in mice?", Cites: rec, Reason: "question"}, same); err != nil {
		t.Fatalf("a dataset card's question: %v", err)
	}
	if c := f.cardRow(t, card); c.Question != "Does X raise Y in mice?" {
		t.Fatalf("question: %q", c.Question)
	}
}

func TestReviseCitesALiveRecord(t *testing.T) {
	f := setup(t)
	card, rec := f.revisable(t, "question")
	for _, cites := range []string{"", uuid.NewString(), "not-a-uuid"} {
		if err := f.revise(t, card, Revision{Text: "new", Cites: cites, Reason: "question"}, same); !errors.Is(err, ErrRevisionRefused) {
			t.Fatalf("cites %q: %v", cites, err)
		}
	}
	_, otherRec := f.revisable(t, "question")
	if err := f.revise(t, card, Revision{Text: "new", Cites: otherRec, Reason: "question"}, same); !errors.Is(err, ErrRevisionRefused) {
		t.Fatalf("cited another card's record: %v", err)
	}
	if _, err := f.env.Owner.Exec(t.Context(), `UPDATE record SET superseded_at = now() WHERE id = $1`, rec); err != nil {
		t.Fatal(err)
	}
	if err := f.revise(t, card, Revision{Text: "new", Cites: rec, Reason: "question"}, same); !errors.Is(err, ErrRevisionRefused) {
		t.Fatalf("cited a superseded record: %v", err)
	}
}

func TestReviseDriftBlocks(t *testing.T) {
	f := setup(t)
	card, rec := f.revisable(t, "question")
	r := Revision{Text: "Does Z raise W?", Cites: rec, Reason: "question"}
	if err := f.revise(t, card, r, gate.Choice{Question: "drift", Judge: "fallback"}); err != nil {
		t.Fatal(err)
	}
	if c := f.cardRow(t, card); c.Question != "Does X raise Y?" || c.Flag != "" || c.RouteBacks != 0 {
		t.Fatalf("an unanswered drift check applied the revision: %+v", c)
	}
	if err := f.revise(t, card, r, gate.Choice{Question: "drift", Judge: "jev", Answer: "changes"}); err != nil {
		t.Fatal(err)
	}
	c := f.cardRow(t, card)
	if c.Question != "Does X raise Y?" || c.Flag != board.Escalated || c.BlockKind != board.BlockDrift {
		t.Fatalf("drift: %+v", c)
	}
}

func TestReviseAppliesAndReverts(t *testing.T) {
	f := setup(t)
	card, rec := f.revisable(t, "question")
	if err := f.revise(t, card, Revision{Text: "Does X raise Y in liver?", Cites: rec, Reason: "question"}, same); err != nil {
		t.Fatal(err)
	}
	if err := f.revise(t, card, Revision{Text: "Does X raise Y in adult liver?", Cites: rec, Reason: "question"}, same); err != nil {
		t.Fatal(err)
	}
	c := f.cardRow(t, card)
	if c.Question != "Does X raise Y in adult liver?" || c.RouteBacks != 2 {
		t.Fatalf("revised: %+v", c)
	}
	if n := f.count(t, card, `SELECT count(*) FROM record WHERE card_id = $1 AND kind = 'starting_point' AND key IN ('0', '1', '2') AND superseded_at IS NULL`); n != 3 {
		t.Fatalf("starting points: %d", n)
	}
	var items []board.Item
	f.inWS(t, func(tx *store.Tx) error {
		c, err := tx.Q.LockCard(t.Context(), db.LockCardParams{ID: card, WorkspaceID: f.ws})
		if err != nil {
			return err
		}
		s := board.FromCard(c)
		items, err = RevertQuestion(t.Context(), tx, c, &s)
		if err != nil {
			return err
		}
		return tx.Q.SaveCardBoard(t.Context(), s.Params(c.ID, f.ws))
	})
	c = f.cardRow(t, card)
	if c.Question != "Does X raise Y?" || c.RouteBacks != 2 || c.BoardColumn != board.Grounding || len(items) != 1 || items[0].Kind != "revised" {
		t.Fatalf("reverted: %+v %+v", c, items)
	}
}

func TestReviseUsesTheCap(t *testing.T) {
	f := setup(t)
	card, rec := f.revisable(t, "question")
	if _, err := f.env.Owner.Exec(t.Context(), `UPDATE card SET cap = 1, route_backs = 1 WHERE id = $1`, card); err != nil {
		t.Fatal(err)
	}
	if err := f.revise(t, card, Revision{Text: "new", Cites: rec, Reason: "question"}, same); err != nil {
		t.Fatal(err)
	}
	if c := f.cardRow(t, card); c.Question != "Does X raise Y?" || c.BlockKind != board.BlockCap || c.RouteBacks != 2 || c.Round != 2 {
		t.Fatalf("a revision past the cap: %+v", c)
	}
}
