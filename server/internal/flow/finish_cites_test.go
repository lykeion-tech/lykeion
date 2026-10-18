package flow

import (
	"context"
	"strings"
	"testing"

	"lykeion/server/internal/store"
	db "lykeion/server/pkg/db/generated"
)

func TestASummaryMayCiteACallAsCallKeyOrAsKey(t *testing.T) {
	l := newLab(t)
	l.jev(t, 200, levels(nil))
	a := l.agent(t, "Curie", "claude", "", "You are Curie.")
	for _, cite := range []string{"call:c1", "c1"} {
		c := l.scoped(t, "grounding", "grounding", a)
		l.in(t, func(tx *store.Tx) error {
			_, err := tx.Q.InsertRecord(context.Background(), db.InsertRecordParams{WorkspaceID: l.ws, CardID: c.ID, BoardColumn: "grounding",
				Round: c.Round, Kind: "call", Role: "agent", Key: "c1", Body: []byte(`{}`)})
			return err
		})
		if err := l.declare(t, c.ID, "call:c9"); err == nil || !strings.Contains(err.Error(), "call:c9") {
			t.Fatalf("an unknown call: %v", err)
		}
		if err := l.declare(t, c.ID, cite); err != nil {
			t.Fatalf("%s refused: %v", cite, err)
		}
	}
}
