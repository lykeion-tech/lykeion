package round

import (
	"context"
	"testing"

	"lykeion/server/internal/board"
)

func TestWIPPullStartsTheTopOfBacklog(t *testing.T) {
	f := setup(t)
	f.card(t, board.Grounding, 1) // one of the two WIP slots is taken
	older := f.card(t, board.Backlog, 0)
	newer := f.card(t, board.Backlog, 0)
	// A person moved the newer card to the top of Backlog.
	if _, err := f.env.Owner.Exec(context.Background(), `UPDATE card SET position = CASE id WHEN $1 THEN 2 ELSE 1 END WHERE id IN ($1, $2)`, older, newer); err != nil {
		t.Fatal(err)
	}
	if err := f.e.WIPPull(context.Background(), f.ws); err != nil {
		t.Fatal(err)
	}
	if f.cardRow(t, newer).BoardColumn != board.Grounding || f.cardRow(t, older).BoardColumn != board.Backlog {
		t.Fatalf("pulled by age, not position: newer %s, older %s", f.cardRow(t, newer).BoardColumn, f.cardRow(t, older).BoardColumn)
	}
}

func TestWIPPullBreaksPositionTiesByNumber(t *testing.T) {
	f := setup(t)
	f.card(t, board.Grounding, 1)
	first := f.card(t, board.Backlog, 0)
	second := f.card(t, board.Backlog, 0)
	// Same position; the second card was created earlier than the first.
	if _, err := f.env.Owner.Exec(context.Background(), `UPDATE card SET position = 3, created_at = CASE id WHEN $1 THEN now() ELSE now() - interval '1 hour' END WHERE id IN ($1, $2)`, first, second); err != nil {
		t.Fatal(err)
	}
	if err := f.e.WIPPull(context.Background(), f.ws); err != nil {
		t.Fatal(err)
	}
	if f.cardRow(t, first).BoardColumn != board.Grounding || f.cardRow(t, second).BoardColumn != board.Backlog {
		t.Fatal("a position tie was not broken by number")
	}
}
