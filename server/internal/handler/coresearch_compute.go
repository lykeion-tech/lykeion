package handler

import (
	"net/http"

	"github.com/google/uuid"
	"lykeion/server/internal/store"
	db "lykeion/server/pkg/db/generated"
)

func (h *H) coresearchCompute(w http.ResponseWriter, r *http.Request) {
	out := struct {
		Kernels  []db.ListOwnCoresearchKernelsRow `json:"kernels"`
		Machines []db.ListOwnComputeMachinesRow   `json:"machines"`
	}{Kernels: []db.ListOwnCoresearchKernelsRow{}, Machines: []db.ListOwnComputeMachinesRow{}}
	err := h.inWS(r, func(tx *store.Tx, ws, who uuid.UUID) error {
		p, e := coresearchProject(r, tx, ws, who, false, false)
		if e != nil {
			return e
		}
		rows, e := tx.Q.ListOwnCoresearchKernels(r.Context(), db.ListOwnCoresearchKernelsParams{WorkspaceID: ws, ProjectID: p.ID, OwnerID: who})
		if e != nil {
			return e
		}
		if rows != nil {
			out.Kernels = rows
		}
		machines, e := tx.Q.ListOwnComputeMachines(r.Context(), db.ListOwnComputeMachinesParams{WorkspaceID: ws, OwnerID: who})
		if e == nil && machines != nil {
			out.Machines = machines
		}
		return e
	})
	if coresearchAnswer(w, r, err) {
		writeJSON(w, 200, out)
	}
}
