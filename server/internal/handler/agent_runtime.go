package handler

import (
	"errors"
	"net/http"

	"github.com/google/uuid"

	"lykeion/server/internal/middleware"
	"lykeion/server/internal/service"
	"lykeion/server/internal/store"
	db "lykeion/server/pkg/db/generated"
)

// bindAgentRuntime chooses the one machine a laboratory agent's runs go to.
// Only a workspace owner chooses it, and only among machines that allow the
// agent's owner.
func (h *H) bindAgentRuntime(w http.ResponseWriter, r *http.Request) {
	id, ok := cardID(w, r)
	if !ok {
		return
	}
	var in struct {
		RuntimeID uuid.UUID `json:"runtime_id"`
	}
	if err := readJSON(r, &in); err != nil || in.RuntimeID == uuid.Nil {
		writeErr(w, 400, "runtime_id is required")
		return
	}
	if middleware.WorkspaceFrom(r.Context()).Role != "owner" {
		writeErr(w, 403, "only the workspace owner can choose a laboratory agent's machine")
		return
	}
	var out db.Agent
	err := h.inWS(r, func(tx *store.Tx, ws, user uuid.UUID) error {
		if _, err := tx.Q.LockWorkspaceExecution(r.Context(), ws); err != nil {
			return err
		}
		if err := tx.Q.LockWorkspaceEvents(r.Context(), ws); err != nil {
			return err
		}
		a, err := tx.Q.GetAgentForUpdate(r.Context(), db.GetAgentForUpdateParams{WorkspaceID: ws, ID: id})
		if err != nil {
			return err
		}
		rt, err := tx.Q.GetRuntime(r.Context(), db.GetRuntimeParams{WorkspaceID: ws, ID: in.RuntimeID})
		if err != nil {
			return err
		}
		if err := service.BindLaboratoryRuntime(r.Context(), tx, a, rt); err != nil {
			return err
		}
		out, err = tx.Q.GetAgent(r.Context(), db.GetAgentParams{WorkspaceID: ws, ID: id})
		return err
	})
	switch {
	case errors.Is(err, service.ErrNotLaboratory), errors.Is(err, service.ErrRuntimeNotAllowed), errors.Is(err, service.ErrHarnessUnsupported):
		writeErr(w, 400, err.Error())
	case err != nil:
		fail(w, r, err)
	default:
		writeJSON(w, 200, out)
	}
}
