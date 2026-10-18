package handler

import (
	"errors"
	"net/http"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"lykeion/server/internal/connectors"
	"lykeion/server/internal/middleware"
	"lykeion/server/internal/store"
	"lykeion/server/internal/workspacemcp"
	db "lykeion/server/pkg/db/generated"
)

func (h *H) workspaceMCPService() *workspacemcp.Service {
	return &workspacemcp.Service{Store: h.Store, Secrets: h.Connectors}
}
func workspaceMCPAnswer(w http.ResponseWriter, r *http.Request, err error) bool {
	if err == nil {
		return true
	}
	var stale *workspacemcp.StaleRevision
	switch {
	case errors.As(err, &stale):
		writeJSON(w, 409, map[string]any{"error": stale.Error(), "code": "stale_revision", "revision": stale.Revision})
	case errors.Is(err, workspacemcp.ErrInvalid):
		writeErr(w, 400, "invalid MCP name or configuration")
	case errors.Is(err, workspacemcp.ErrForbidden):
		writeErr(w, 403, err.Error())
	case errors.Is(err, workspacemcp.ErrConflict):
		writeErr(w, 409, err.Error())
	case errors.Is(err, connectors.ErrNoSecret):
		writeErr(w, 400, "MCP configuration requires the server's LYK_SECRET_KEY")
	default:
		fail(w, r, err)
	}
	return false
}
func (h *H) workspaceMCPScope(w http.ResponseWriter, r *http.Request, write bool) (uuid.UUID, bool) {
	raw := middleware.FromHeader(r)
	if raw == "" {
		writeErr(w, 400, "X-Workspace-ID required")
		return uuid.Nil, false
	}
	ws, err := uuid.Parse(raw)
	if err != nil {
		writeErr(w, 404, "workspace not found")
		return uuid.Nil, false
	}
	// Authentication here accepts human session/personal tokens, never run tokens.
	err = h.memberTx(r, ws, write, func(tx *store.Tx) error {
		rawID := chi.URLParam(r, "id")
		if rawID == "" {
			return nil
		}
		id, err := uuid.Parse(rawID)
		if err != nil {
			return pgx.ErrNoRows
		}
		_, err = tx.Q.GetWorkspaceMCPServer(r.Context(), db.GetWorkspaceMCPServerParams{WorkspaceID: ws, ID: id})
		return err
	})
	if errors.Is(err, middleware.ErrForbidden) {
		writeErr(w, 403, "admins only")
		return uuid.Nil, false
	}
	if err != nil {
		fail(w, r, err)
		return uuid.Nil, false
	}
	return ws, true
}
func (h *H) listWorkspaceMCP(w http.ResponseWriter, r *http.Request) {
	ws, ok := h.workspaceMCPScope(w, r, false)
	if !ok {
		return
	}
	out, err := h.workspaceMCPService().List(r.Context(), ws, user(r))
	if workspaceMCPAnswer(w, r, err) {
		writeJSON(w, 200, out)
	}
}

type workspaceMCPIn struct {
	Name     string               `json:"name"`
	Revision *int64               `json:"revision,omitempty"`
	Config   *workspacemcp.Config `json:"config"`
}

func (h *H) saveWorkspaceMCP(create bool) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		ws, ok := h.workspaceMCPScope(w, r, true)
		if !ok {
			return
		}
		var in workspaceMCPIn
		if err := readJSON(r, &in); err != nil || in.Config == nil || (!create && (in.Revision == nil || *in.Revision < 1)) {
			writeErr(w, 400, "name, full config and an update revision are required")
			return
		}
		var out workspacemcp.Summary
		var err error
		if create {
			out, err = h.workspaceMCPService().Create(r.Context(), ws, user(r), in.Name, *in.Config)
		} else {
			id, e := uuid.Parse(chi.URLParam(r, "id"))
			if e != nil {
				writeErr(w, 404, "MCP server not found")
				return
			}
			out, err = h.workspaceMCPService().Update(r.Context(), ws, user(r), id, *in.Revision, in.Name, *in.Config)
		}
		if workspaceMCPAnswer(w, r, err) {
			status := 200
			if create {
				status = 201
			}
			writeJSON(w, status, out)
		}
	}
}
func (h *H) deleteWorkspaceMCP(w http.ResponseWriter, r *http.Request) {
	ws, ok := h.workspaceMCPScope(w, r, true)
	if !ok {
		return
	}
	id, err := uuid.Parse(chi.URLParam(r, "id"))
	if err != nil {
		writeErr(w, 404, "MCP server not found")
		return
	}
	var in struct {
		Revision *int64 `json:"revision"`
	}
	if err := readJSON(r, &in); err != nil || in.Revision == nil || *in.Revision < 1 {
		writeErr(w, 400, "revision is required")
		return
	}
	if workspaceMCPAnswer(w, r, h.workspaceMCPService().Delete(r.Context(), ws, user(r), id, *in.Revision)) {
		w.WriteHeader(204)
	}
}
