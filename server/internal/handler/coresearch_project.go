package handler

import (
	"errors"
	"net/http"
	"strings"
	"unicode/utf8"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"lykeion/server/internal/coresearch"
	"lykeion/server/internal/middleware"
	"lykeion/server/internal/service"
	"lykeion/server/internal/store"
	db "lykeion/server/pkg/db/generated"
)

type coresearchProjectView struct {
	db.CoresearchProject
	Role string `json:"role"`
}

func coresearchAnswer(w http.ResponseWriter, r *http.Request, err error) bool {
	if err == nil {
		return true
	}
	var stale coresearch.ErrStale
	var input cardInputError
	switch {
	case errors.As(err, &stale):
		writeJSON(w, 409, map[string]any{"error": "stale_base", "base": stale.Base, "current": stale.Current})
	case errors.Is(err, service.ErrConflict):
		writeErr(w, 409, err.Error())
	case errors.Is(err, coresearch.ErrArchived):
		writeErr(w, 409, err.Error())
	case errors.Is(err, coresearch.ErrQuota):
		writeErr(w, 413, err.Error())
	case errors.Is(err, middleware.ErrForbidden):
		writeErr(w, 403, "project owners only")
	case errors.As(err, &input):
		writeErr(w, 400, string(input))
	default:
		var c conflict
		if errors.As(err, &c) {
			writeErr(w, 409, string(c))
		} else {
			fail(w, r, err)
		}
	}
	return false
}
func coresearchID(r *http.Request, key string) (uuid.UUID, error) {
	id, err := uuid.Parse(chi.URLParam(r, key))
	if err != nil {
		return uuid.Nil, pgx.ErrNoRows
	}
	return id, nil
}
func coresearchProject(r *http.Request, tx *store.Tx, ws, who uuid.UUID, write, owner bool) (db.CoresearchProject, error) {
	var p db.CoresearchProject
	id, err := coresearchID(r, "project")
	if err != nil {
		return p, err
	}
	if write {
		p, err = tx.Q.LockCoresearchProject(r.Context(), db.LockCoresearchProjectParams{WorkspaceID: ws, ID: id})
	} else {
		p, err = tx.Q.GetCoresearchProject(r.Context(), db.GetCoresearchProjectParams{WorkspaceID: ws, ID: id})
	}
	if err != nil {
		return p, err
	}
	if write && p.ArchivedAt != nil {
		return p, coresearch.ErrArchived
	}
	if owner {
		m, err := tx.Q.CoresearchMember(r.Context(), db.CoresearchMemberParams{WorkspaceID: ws, ProjectID: id, UserID: who})
		if err != nil {
			return p, err
		}
		if m.Role != "owner" {
			return p, middleware.ErrForbidden
		}
	}
	return p, nil
}
func (h *H) listCoresearchProjects(w http.ResponseWriter, r *http.Request) {
	out := []db.ListCoresearchProjectsRow{}
	err := h.inWS(r, func(tx *store.Tx, ws, _ uuid.UUID) error {
		var err error
		out, err = tx.Q.ListCoresearchProjects(r.Context(), ws)
		return err
	})
	if coresearchAnswer(w, r, err) {
		if out == nil {
			out = []db.ListCoresearchProjectsRow{}
		}
		writeJSON(w, 200, out)
	}
}
func validCoresearchName(name string) bool {
	return utf8.ValidString(name) && utf8.RuneCountInString(name) > 0 && utf8.RuneCountInString(name) <= 120
}
func (h *H) createCoresearchProject(w http.ResponseWriter, r *http.Request) {
	var out coresearchProjectView
	err := h.inWS(r, func(tx *store.Tx, ws, who uuid.UUID) error {
		var in struct {
			Name string `json:"name"`
		}
		if err := readJSON(r, &in); err != nil {
			return cardInputError("a project name is required")
		}
		in.Name = strings.TrimSpace(in.Name)
		if !validCoresearchName(in.Name) {
			return cardInputError("project name must be between 1 and 120 characters")
		}
		id, err := tx.Q.CreateCoresearchProject(r.Context(), in.Name)
		if err != nil {
			return err
		}
		out.CoresearchProject, err = tx.Q.GetCoresearchProject(r.Context(), db.GetCoresearchProjectParams{WorkspaceID: ws, ID: id})
		if err != nil {
			return err
		}
		out.Role = "owner"
		return tx.Emit(r.Context(), "coresearch.project.created", nil, store.User(who), map[string]any{"project_id": id})
	})
	if coresearchAnswer(w, r, err) {
		writeJSON(w, 201, out)
	}
}
func (h *H) getCoresearchProject(w http.ResponseWriter, r *http.Request) {
	var out coresearchProjectView
	err := h.inWS(r, func(tx *store.Tx, ws, who uuid.UUID) error {
		p, err := coresearchProject(r, tx, ws, who, false, false)
		if err != nil {
			return err
		}
		m, err := tx.Q.CoresearchMember(r.Context(), db.CoresearchMemberParams{WorkspaceID: ws, ProjectID: p.ID, UserID: who})
		out = coresearchProjectView{p, m.Role}
		return err
	})
	if coresearchAnswer(w, r, err) {
		writeJSON(w, 200, out)
	}
}
func (h *H) renameCoresearchProject(w http.ResponseWriter, r *http.Request) {
	var out db.CoresearchProject
	err := h.inWS(r, func(tx *store.Tx, ws, who uuid.UUID) error {
		p, err := coresearchProject(r, tx, ws, who, true, true)
		if err != nil {
			return err
		}
		var in struct {
			Name string `json:"name"`
		}
		if err := readJSON(r, &in); err != nil {
			return cardInputError("a project name is required")
		}
		in.Name = strings.TrimSpace(in.Name)
		if !validCoresearchName(in.Name) {
			return cardInputError("project name must be between 1 and 120 characters")
		}
		out, err = tx.Q.RenameCoresearchProject(r.Context(), db.RenameCoresearchProjectParams{WorkspaceID: ws, ID: p.ID, Name: in.Name})
		if err != nil {
			return err
		}
		return tx.Emit(r.Context(), "coresearch.project.updated", nil, store.User(who), map[string]any{"project_id": p.ID})
	})
	if coresearchAnswer(w, r, err) {
		writeJSON(w, 200, out)
	}
}
func (h *H) archiveCoresearchProject(w http.ResponseWriter, r *http.Request) {
	var out db.CoresearchProject
	err := h.inWS(r, func(tx *store.Tx, ws, who uuid.UUID) error {
		p, err := coresearchProject(r, tx, ws, who, true, true)
		if err != nil {
			return err
		}
		out, err = tx.Q.ArchiveCoresearchProject(r.Context(), db.ArchiveCoresearchProjectParams{WorkspaceID: ws, ID: p.ID})
		if err != nil {
			return err
		}
		return tx.Emit(r.Context(), "coresearch.project.updated", nil, store.User(who), map[string]any{"project_id": p.ID})
	})
	if coresearchAnswer(w, r, err) {
		writeJSON(w, 200, out)
	}
}
func (h *H) listCoresearchMembers(w http.ResponseWriter, r *http.Request) {
	out := []db.CoresearchMember{}
	err := h.inWS(r, func(tx *store.Tx, ws, who uuid.UUID) error {
		p, err := coresearchProject(r, tx, ws, who, false, false)
		if err != nil {
			return err
		}
		out, err = tx.Q.ListCoresearchMembers(r.Context(), db.ListCoresearchMembersParams{WorkspaceID: ws, ProjectID: p.ID})
		return err
	})
	if coresearchAnswer(w, r, err) {
		writeJSON(w, 200, out)
	}
}
func coresearchKeepOwner(r *http.Request, tx *store.Tx, ws, project, target uuid.UUID) error {
	m, err := tx.Q.CoresearchMember(r.Context(), db.CoresearchMemberParams{WorkspaceID: ws, ProjectID: project, UserID: target})
	if errors.Is(err, pgx.ErrNoRows) {
		return nil
	}
	if err != nil {
		return err
	}
	if m.Role != "owner" {
		return nil
	}
	n, err := tx.Q.CountCoresearchOwners(r.Context(), db.CountCoresearchOwnersParams{WorkspaceID: ws, ProjectID: project})
	if err == nil && n <= 1 {
		return conflict("the last project owner cannot be removed")
	}
	return err
}
func (h *H) setCoresearchMember(w http.ResponseWriter, r *http.Request) {
	var out db.CoresearchMember
	err := h.inWS(r, func(tx *store.Tx, ws, who uuid.UUID) error {
		p, err := coresearchProject(r, tx, ws, who, true, true)
		if err != nil {
			return err
		}
		var in struct {
			UserID uuid.UUID `json:"user_id"`
			Role   string    `json:"role"`
		}
		if err := readJSON(r, &in); err != nil || in.UserID == uuid.Nil || (in.Role != "owner" && in.Role != "member") {
			return cardInputError("a workspace member and owner or member role are required")
		}
		if _, err = tx.Q.GetMember(r.Context(), db.GetMemberParams{WorkspaceID: ws, UserID: in.UserID}); errors.Is(err, pgx.ErrNoRows) {
			return cardInputError("user must belong to this workspace")
		} else if err != nil {
			return err
		}
		if in.Role != "owner" {
			if err = coresearchKeepOwner(r, tx, ws, p.ID, in.UserID); err != nil {
				return err
			}
		}
		out, err = tx.Q.SetCoresearchMember(r.Context(), db.SetCoresearchMemberParams{WorkspaceID: ws, ProjectID: p.ID, UserID: in.UserID, Role: in.Role})
		if err != nil {
			return err
		}
		if err = tx.Q.TouchCoresearchProject(r.Context(), db.TouchCoresearchProjectParams{WorkspaceID: ws, ID: p.ID}); err != nil {
			return err
		}
		return tx.Emit(r.Context(), "coresearch.member.updated", nil, store.User(who), map[string]any{"project_id": p.ID, "user_id": in.UserID})
	})
	if coresearchAnswer(w, r, err) {
		writeJSON(w, 201, out)
	}
}
func (h *H) deleteCoresearchMember(w http.ResponseWriter, r *http.Request) {
	err := h.inWS(r, func(tx *store.Tx, ws, who uuid.UUID) error {
		p, err := coresearchProject(r, tx, ws, who, true, true)
		if err != nil {
			return err
		}
		target, err := coresearchID(r, "user")
		if err != nil {
			return err
		}
		if _, err = tx.Q.CoresearchMember(r.Context(), db.CoresearchMemberParams{WorkspaceID: ws, ProjectID: p.ID, UserID: target}); err != nil {
			return err
		}
		if err = coresearchKeepOwner(r, tx, ws, p.ID, target); err != nil {
			return err
		}
		if err = tx.Q.TouchCoresearchProject(r.Context(), db.TouchCoresearchProjectParams{WorkspaceID: ws, ID: p.ID}); err != nil {
			return err
		}
		if err = h.AgentRuns.RevokeCoresearchWork(r.Context(), tx, who, target, &p.ID); err != nil {
			return err
		}
		if err = tx.Q.DeleteCoresearchMember(r.Context(), db.DeleteCoresearchMemberParams{WorkspaceID: ws, ProjectID: p.ID, UserID: target}); err != nil {
			return err
		}
		return tx.Emit(r.Context(), "coresearch.member.updated", nil, store.User(who), map[string]any{"project_id": p.ID, "user_id": target})
	})
	if coresearchAnswer(w, r, err) {
		w.WriteHeader(204)
	}
}
