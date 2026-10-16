package ground

import (
	"context"
	"strings"
	"testing"

	"lykeion/server/internal/board"
)

// An add-on server returns a citation, "lab-index:42", with a passage. The
// role never fetched it through the sources server, so it cannot be evidence:
// the write is refused, and evidence that reached the records any other way
// fails the gate's quote check.
func TestAddonServerCitationIsNeverEvidence(t *testing.T) {
	f := setup(t)
	cite := map[string]string{"claim": "X raises Y", "source_id": "lab-index:42", "quote": "X raised Y by 40%", "stance": "supports"}
	if _, err := f.write(t, Literature, "evidence", cite); err == nil || !strings.Contains(err.Error(), "never fetched") {
		t.Fatalf("an add-on citation was recorded as evidence: %v", err)
	}
	for _, kind := range []string{"search", "fetch", "open_dataset"} {
		if _, ok := Kinds()[kind]; ok {
			t.Fatalf("roles may write %s records", kind)
		}
	}

	f.complete(t, "X raised Y by 40%")
	if _, err := f.env.Owner.Exec(context.Background(), `INSERT INTO record (workspace_id, card_id, board_column, round, kind, role, key, body)
		VALUES ($1, $2, 'grounding', 1, 'evidence', $3, 'E9', $4)`, f.ws, f.card, Literature,
		`{"claim":"X raises Y","source_id":"lab-index:42","quote":"X raised Y by 40%","stance":"supports"}`); err != nil {
		t.Fatalf("insert add-on evidence fixture: %v", err)
	}
	g := f.gate(t, fakeJev(t, false))
	if result(g, "quote.E9") != board.Fail || g.Verdict != board.VFail {
		t.Fatalf("the gate accepted an add-on citation: %s %+v", g.Verdict, g.Checks)
	}
}
