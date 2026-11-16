package handler

import (
	"net/http"

	"github.com/google/uuid"

	"lykeion/server/internal/report"
	"lykeion/server/internal/store"
	db "lykeion/server/pkg/db/generated"
)

// cardReport answers a finished study's report. Studies that are unfinished,
// stopped, run a Workflow or finished before reports shipped have none.
func (h *H) cardReport(w http.ResponseWriter, r *http.Request) {
	id, ok := cardID(w, r)
	if !ok {
		return
	}
	var rep report.Report
	var input report.Input
	noReport := false
	err := h.inWS(r, func(tx *store.Tx, ws, _ uuid.UUID) error {
		card, err := tx.Q.GetCard(r.Context(), db.GetCardParams{WorkspaceID: ws, ID: id})
		if err != nil {
			return err
		}
		has, err := report.Has(r.Context(), tx, card)
		if err != nil || !has {
			noReport = true
			return err
		}
		in, err := report.Load(r.Context(), tx, card)
		if err != nil {
			return err
		}
		input = in
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
	h.resolveReportPaperTitles(r.Context(), &rep, input.Records)
	writeJSON(w, http.StatusOK, rep)
}
