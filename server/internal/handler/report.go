package handler

import (
	"net/http"

	"github.com/google/uuid"

	"lykeion/server/internal/report"
	"lykeion/server/internal/store"
	db "lykeion/server/pkg/db/generated"
)

// cardReport answers a finished study's report. Studies that are unfinished,
// stopped or run a Workflow have none.
func (h *H) cardReport(w http.ResponseWriter, r *http.Request) {
	id, ok := cardID(w, r)
	if !ok {
		return
	}
	var rep report.Report
	noReport := false
	err := h.inWS(r, func(tx *store.Tx, ws, _ uuid.UUID) error {
		card, err := tx.Q.GetCard(r.Context(), db.GetCardParams{WorkspaceID: ws, ID: id})
		if err != nil {
			return err
		}
		if card.State != "done" || card.Ending != "" || card.Workflow != nil {
			noReport = true
			return nil
		}
		in, err := report.Load(r.Context(), tx, card)
		if err != nil {
			return err
		}
		rep = report.Build(in)
		return nil
	})
	if err != nil {
		fail(w, r, err)
		return
	}
	if noReport {
		writeErr(w, http.StatusConflict, "this study has no report")
		return
	}
	writeJSON(w, http.StatusOK, rep)
}
