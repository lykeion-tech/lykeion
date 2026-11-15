package handin

import (
	"lykeion/server/internal/board"
	db "lykeion/server/pkg/db/generated"
	"testing"
)

func TestFailedSourceCallCannotSupplyEvidence(t *testing.T) {
	e := NewEvidence([]db.Record{rec(board.Grounding, "call", "c1", map[string]any{"server": "sources", "error": "429", "answer": "doi:10.1000/failed"})})
	if got := e.Sources("doi:10.1000/failed"); len(got) != 0 {
		t.Fatalf("failed call supplied evidence: %v", got)
	}
}
