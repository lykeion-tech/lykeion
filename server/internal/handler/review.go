package handler

import (
	"encoding/json"
	"net/http"
	"sort"

	"github.com/google/uuid"

	"lykeion/server/internal/board"
	"lykeion/server/internal/store"
	db "lykeion/server/pkg/db/generated"
)

// cardReview answers each column's review attempts, oldest first, in board order.
func (h *H) cardReview(w http.ResponseWriter, r *http.Request) {
	id, ok := cardID(w, r)
	if !ok {
		return
	}
	type column struct {
		Column   string            `json:"column"`
		Attempts []json.RawMessage `json:"attempts"`
	}
	cols := make([]column, len(board.Stages))
	for i, s := range board.Stages {
		cols[i] = column{Column: s, Attempts: []json.RawMessage{}}
	}
	err := h.inWS(r, func(tx *store.Tx, ws, _ uuid.UUID) error {
		if _, err := tx.Q.GetCard(r.Context(), db.GetCardParams{WorkspaceID: ws, ID: id}); err != nil {
			return err
		}
		recs, err := tx.Q.CardKindRecords(r.Context(), db.CardKindRecordsParams{WorkspaceID: ws, CardID: id, Kind: "review"})
		// Rounds are the attempt order; created_at can tie inside one transaction.
		sort.SliceStable(recs, func(i, j int) bool { return recs[i].Round < recs[j].Round })
		for _, rec := range recs {
			if i := board.StageIndex(rec.BoardColumn); i >= 0 {
				cols[i].Attempts = append(cols[i].Attempts, rec.Body)
			}
		}
		return err
	})
	if err != nil {
		fail(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"columns": cols})
}
