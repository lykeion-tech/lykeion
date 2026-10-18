package handler

import (
	"errors"
	"net/http"
	"strings"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"go.yaml.in/yaml/v3"
	"lykeion/server/internal/middleware"
	db "lykeion/server/pkg/db/generated"

	"lykeion/server/internal/science"
	"lykeion/server/internal/skill"
	"lykeion/server/internal/store"
)

func (h *H) skillService() *skill.Service { return &skill.Service{Store: h.Store, Blobs: h.Blobs} }
func skillAnswer(w http.ResponseWriter, r *http.Request, err error) bool {
	if err == nil {
		return true
	}
	var stale *skill.StaleRevision
	switch {
	case errors.As(err, &stale):
		writeJSON(w, 409, map[string]any{"error": stale.Error(), "code": "stale_revision", "revision": stale.Revision})
	case errors.Is(err, skill.ErrForbidden):
		writeErr(w, 403, err.Error())
	case errors.Is(err, skill.ErrConflict):
		writeErr(w, 409, err.Error())
	case errors.Is(err, skill.ErrInvalid):
		writeErr(w, 400, err.Error())
	default:
		fail(w, r, err)
	}
	return false
}
func (h *H) listSkills(w http.ResponseWriter, r *http.Request) {
	ws, user, ok := h.skillScope(w, r)
	if !ok {
		return
	}
	out, err := h.skillService().List(r.Context(), ws, user)
	if skillAnswer(w, r, err) {
		writeJSON(w, 200, out)
	}
}
func (h *H) getSkill(w http.ResponseWriter, r *http.Request) {
	ws, user, ok := h.skillScope(w, r)
	if !ok {
		return
	}
	out, err := h.skillService().Get(r.Context(), ws, user, chi.URLParam(r, "id"))
	if skillAnswer(w, r, err) {
		writeJSON(w, 200, out)
	}
}
func (h *H) getSkillFile(w http.ResponseWriter, r *http.Request) {
	ws, user, ok := h.skillScope(w, r)
	if !ok {
		return
	}
	data, err := h.skillService().File(r.Context(), ws, user, chi.URLParam(r, "id"), r.URL.Query().Get("path"))
	if !skillAnswer(w, r, err) {
		return
	}
	w.Header().Set("Content-Type", http.DetectContentType(data))
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.Header().Set("Content-Disposition", "attachment")
	w.WriteHeader(200)
	w.Write(data)
}
func (h *H) createSkill(w http.ResponseWriter, r *http.Request) {
	ws, user, ok := h.skillScope(w, r)
	if !ok {
		return
	}
	var in struct {
		Name        string `json:"name"`
		Description string `json:"description"`
	}
	if err := readJSON(r, &in); err != nil {
		writeErr(w, 400, "invalid skill: "+err.Error())
		return
	}
	fm, err := yaml.Marshal(science.Frontmatter{Name: in.Name, Description: in.Description})
	if err != nil {
		writeErr(w, 400, "invalid skill metadata")
		return
	}
	main := append([]byte("---\n"), fm...)
	main = append(main, []byte("---\n")...)
	out, err := h.skillService().Create(r.Context(), ws, user, skill.Bundle{Name: in.Name, Description: in.Description, Files: map[string][]byte{"SKILL.md": main}}, skill.Source{Kind: "manual"})
	if skillAnswer(w, r, err) {
		writeJSON(w, 201, out)
	}
}
func (h *H) updateSkill(w http.ResponseWriter, r *http.Request) {
	ws, user, ok := h.skillScope(w, r)
	if !ok {
		return
	}
	var in struct {
		Revision int64   `json:"revision"`
		Content  *string `json:"content"`
	}
	if err := readJSON(r, &in); err != nil || in.Content == nil {
		writeErr(w, 400, "revision and content are required")
		return
	}
	out, err := h.skillService().Update(r.Context(), ws, user, chi.URLParam(r, "id"), in.Revision, *in.Content)
	if skillAnswer(w, r, err) {
		writeJSON(w, 200, out)
	}
}
func (h *H) deleteSkill(w http.ResponseWriter, r *http.Request) {
	ws, user, ok := h.skillScope(w, r)
	if !ok {
		return
	}
	var in struct {
		Revision int64 `json:"revision"`
	}
	if err := readJSON(r, &in); err != nil {
		writeErr(w, 400, "revision is required")
		return
	}
	if skillAnswer(w, r, h.skillService().Delete(r.Context(), ws, user, chi.URLParam(r, "id"), in.Revision)) {
		w.WriteHeader(204)
	}
}

func (h *H) skillScope(w http.ResponseWriter, r *http.Request) (uuid.UUID, uuid.UUID, bool) {
	raw := middleware.FromHeader(r)
	if raw == "" {
		writeErr(w, 400, "X-Workspace-ID required")
		return uuid.Nil, uuid.Nil, false
	}
	ws, err := uuid.Parse(raw)
	if err != nil {
		writeErr(w, 404, "workspace not found")
		return uuid.Nil, uuid.Nil, false
	}
	if err := h.memberTx(r, ws, false, func(tx *store.Tx) error {
		id := chi.URLParam(r, "id")
		if id == "" {
			return nil
		}
		if name, ok := strings.CutPrefix(id, "bundle:"); ok {
			if !science.Has(name) {
				return pgx.ErrNoRows
			}
			return nil
		}
		parsed, err := uuid.Parse(id)
		if err != nil {
			return pgx.ErrNoRows
		}
		_, err = tx.Q.GetWorkspaceSkill(r.Context(), db.GetWorkspaceSkillParams{WorkspaceID: ws, ID: parsed})
		return err
	}); err != nil {
		fail(w, r, err)
		return uuid.Nil, uuid.Nil, false
	}
	return ws, user(r), true
}
