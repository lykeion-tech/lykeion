package handler

import (
	"errors"
	"fmt"
	"io"
	"mime"
	"mime/multipart"
	"net/http"
	"path"
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
	var fetch *skill.FetchError
	switch {
	case errors.As(err, &fetch):
		writeErr(w, fetch.Status, fetch.Error())
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

func (h *H) importSkill(w http.ResponseWriter, r *http.Request) {
	ws, u, ok := h.skillScope(w, r)
	if !ok {
		return
	}
	mediaType, params, err := mime.ParseMediaType(r.Header.Get("Content-Type"))
	if err != nil && r.Header.Get("Content-Type") != "" {
		writeErr(w, 400, "invalid import content type")
		return
	}
	var out skill.Detail
	if mediaType == "multipart/form-data" {
		out, err = h.importSkillMultipart(r, ws, u, params["boundary"])
	} else {
		var in struct {
			URL string `json:"url"`
		}
		if err = readJSON(r, &in); err == nil {
			out, err = skill.ImportURL(r.Context(), h.skillService(), ws, u, in.URL)
		} else {
			err = fmt.Errorf("%w: invalid URL import body", skill.ErrInvalid)
		}
	}
	if skillAnswer(w, r, err) {
		writeJSON(w, 201, out)
	}
}
func (h *H) importSkillMultipart(r *http.Request, ws, u uuid.UUID, boundary string) (skill.Detail, error) {
	reader := multipart.NewReader(r.Body, boundary)
	files := map[string][]byte{}
	archiveName := ""
	var archive []byte
	total := 0
	for {
		part, err := reader.NextRawPart()
		if err == io.EOF {
			break
		}
		if err != nil {
			return skill.Detail{}, fmt.Errorf("%w: invalid multipart upload", skill.ErrInvalid)
		}
		_, params, err := mime.ParseMediaType(part.Header.Get("Content-Disposition"))
		if err != nil {
			return skill.Detail{}, fmt.Errorf("%w: invalid file disposition", skill.ErrInvalid)
		}
		name := params["filename"]
		field := params["name"]
		if name == "" || (field != "file" && field != "files") {
			return skill.Detail{}, fmt.Errorf("%w: use file for an archive or files for folder entries", skill.ErrInvalid)
		}
		limit := skill.FileBytes
		isArchive := field == "file"
		if isArchive {
			limit = skill.ArchiveBytes
		}
		data, err := io.ReadAll(io.LimitReader(part, int64(limit)+1))
		part.Close()
		if err != nil || len(data) > limit {
			return skill.Detail{}, fmt.Errorf("%w: file %q exceeds size limit or could not be read", skill.ErrInvalid, name)
		}
		if isArchive {
			if archiveName != "" || len(files) > 0 {
				return skill.Detail{}, fmt.Errorf("%w: upload one archive or one folder", skill.ErrInvalid)
			}
			archiveName = name
			archive = data
		} else {
			if archiveName != "" {
				return skill.Detail{}, fmt.Errorf("%w: upload one archive or one folder", skill.ErrInvalid)
			}
			if _, exists := files[name]; exists {
				return skill.Detail{}, fmt.Errorf("%w: duplicate file path %q", skill.ErrInvalid, name)
			}
			total += len(data)
			if len(files) >= skill.SupportingFiles+1 || total > skill.SupportingBytes+skill.FileBytes {
				return skill.Detail{}, fmt.Errorf("%w: folder exceeds supporting file limits", skill.ErrInvalid)
			}
			files[name] = data
		}
	}
	if archiveName != "" {
		return skill.ImportArchive(r.Context(), h.skillService(), ws, u, archive, skill.Source{Kind: "local", Location: path.Base(archiveName)})
	}
	return skill.ImportFolder(r.Context(), h.skillService(), ws, u, files)
}
