package handler

import (
	"errors"
	"net/http"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"

	"lykeion/server/internal/flow"
	"lykeion/server/internal/service"
)

// daemonRerunResult takes a rerun's result from the daemon that ran it.
func (h *H) daemonRerunResult(w http.ResponseWriter, r *http.Request) {
	id, err := uuid.Parse(chi.URLParam(r, "id"))
	if err != nil {
		writeErr(w, 404, "not found")
		return
	}
	var in struct {
		service.RuntimeRef
		flow.RerunResult
	}
	if err := readJSON(r, &in); err != nil || in.WorkspaceID == uuid.Nil || in.RuntimeID == uuid.Nil || in.Attempt <= 0 {
		writeErr(w, 400, "workspace_id, runtime_id, positive attempt, steps, outputs and failure required")
		return
	}
	err = h.Engine.RerunResult(r.Context(), user(r), in.RuntimeRef, id, in.RerunResult)
	var invalid service.Invalid
	if errors.As(err, &invalid) {
		writeErr(w, 400, string(invalid))
		return
	}
	if err != nil {
		agentRunFail(w, r, err)
		return
	}
	writeJSON(w, 200, map[string]bool{"accepted": true})
}
