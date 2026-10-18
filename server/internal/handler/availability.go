package handler

import (
	"errors"
	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"lykeion/server/internal/availability"
	"net/http"
)

func (h *H) setAvailability(w http.ResponseWriter, r *http.Request) {
	ws, u, ok := h.skillScopeForAvailability(w, r)
	if !ok {
		return
	}
	var in struct {
		Enabled  *bool  `json:"enabled"`
		Revision *int64 `json:"revision"`
	}
	if err := readJSON(r, &in); err != nil || in.Enabled == nil || in.Revision == nil {
		writeErr(w, 400, "enabled and revision are required")
		return
	}
	out, err := availability.Set(r.Context(), h.Store, ws, u, chi.URLParam(r, "kind"), chi.URLParam(r, "id"), *in.Revision, *in.Enabled)
	var stale *availability.Stale
	switch {
	case errors.Is(err, availability.ErrForbidden):
		writeErr(w, 403, err.Error())
	case errors.Is(err, availability.ErrInvalid):
		writeErr(w, 400, err.Error())
	case errors.As(err, &stale):
		writeJSON(w, 409, map[string]any{"error": err.Error(), "code": "stale_revision", "revision": stale.Revision})
	case err != nil:
		fail(w, r, err)
	default:
		writeJSON(w, 200, map[string]any{"enabled": out.Enabled, "revision": out.Revision})
	}
}

func (h *H) skillScopeForAvailability(w http.ResponseWriter, r *http.Request) (uuid.UUID, uuid.UUID, bool) {
	if chi.URLParam(r, "kind") == "mcp" {
		ws, ok := h.workspaceMCPScope(w, r, false)
		return ws, user(r), ok
	}
	return h.skillScope(w, r)
}
