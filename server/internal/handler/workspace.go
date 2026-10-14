package handler

import (
	"context"
	"errors"
	"net/http"
	"regexp"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"lykeion/server/internal/auth"
	"lykeion/server/internal/middleware"
	"lykeion/server/internal/store"
	db "lykeion/server/pkg/db/generated"
)

var slugRe = regexp.MustCompile(`^[a-z0-9][a-z0-9-]{1,38}$`)

const inviteTTL = 7 * 24 * time.Hour

var errLastOwner = errors.New("a workspace needs at least one owner")

func (h *H) listWorkspaces(w http.ResponseWriter, r *http.Request) {
	id := middleware.IdentityFrom(r.Context())
	rows := []db.ListMyWorkspacesRow{}
	err := h.Store.AsUser(r.Context(), id.UserID, id.Email, func(tx *store.Tx) error {
		var err error
		rows, err = tx.Q.ListMyWorkspaces(r.Context(), id.UserID)
		return err
	})
	if err != nil {
		fail(w, r, err)
		return
	}
	if rows == nil {
		rows = []db.ListMyWorkspacesRow{}
	}
	writeJSON(w, 200, rows)
}

func (h *H) createWorkspace(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Name string `json:"name"`
		Slug string `json:"slug"`
	}
	if err := readJSON(r, &in); err != nil || strings.TrimSpace(in.Name) == "" || !slugRe.MatchString(in.Slug) {
		writeErr(w, 400, "name and a lowercase slug (letters, digits, hyphens) are required")
		return
	}
	user := middleware.IdentityFrom(r.Context()).UserID
	wsID := uuid.New()
	var out db.Workspace
	err := h.Store.InWorkspace(r.Context(), wsID, user, func(tx *store.Tx) error {
		var err error
		if out, err = tx.Q.CreateWorkspace(r.Context(), db.CreateWorkspaceParams{ID: wsID, Slug: in.Slug, Name: in.Name}); err != nil {
			return err
		}
		if err := tx.Q.AddMember(r.Context(), db.AddMemberParams{WorkspaceID: wsID, UserID: user, Role: "owner"}); err != nil {
			return err
		}
		return tx.Emit(r.Context(), "workspace.created", nil, store.User(user), map[string]string{"name": in.Name, "slug": in.Slug})
	})
	if isUnique(err) {
		writeErr(w, 409, "slug taken")
		return
	}
	if err != nil {
		fail(w, r, err)
		return
	}
	writeJSON(w, 201, out)
}

// inWS runs fn in the scoped workspace as the caller.
func (h *H) inWS(r *http.Request, fn func(tx *store.Tx, ws uuid.UUID, user uuid.UUID) error) error {
	ws, user := middleware.WorkspaceFrom(r.Context()).ID, middleware.IdentityFrom(r.Context()).UserID
	return middleware.InWorkspace(r.Context(), h.Store, "", func(tx *store.Tx) error { return fn(tx, ws, user) })
}

func (h *H) getWorkspace(w http.ResponseWriter, r *http.Request) {
	var out db.Workspace
	err := h.inWS(r, func(tx *store.Tx, ws, _ uuid.UUID) error {
		var err error
		out, err = tx.Q.GetWorkspace(r.Context(), ws)
		return err
	})
	if err != nil {
		fail(w, r, err)
		return
	}
	writeJSON(w, 200, out)
}

func (h *H) renameWorkspace(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Name string `json:"name"`
	}
	if err := readJSON(r, &in); err != nil || strings.TrimSpace(in.Name) == "" {
		writeErr(w, 400, "name required")
		return
	}
	var out db.Workspace
	err := h.inWS(r, func(tx *store.Tx, ws, user uuid.UUID) error {
		var err error
		if out, err = tx.Q.RenameWorkspace(r.Context(), db.RenameWorkspaceParams{ID: ws, Name: in.Name}); err != nil {
			return err
		}
		return tx.Emit(r.Context(), "workspace.renamed", nil, store.User(user), map[string]string{"name": in.Name})
	})
	if err != nil {
		fail(w, r, err)
		return
	}
	writeJSON(w, 200, out)
}

func (h *H) deleteWorkspace(w http.ResponseWriter, r *http.Request) {
	if err := h.inWS(r, func(tx *store.Tx, ws, user uuid.UUID) error {
		if err := tx.Emit(r.Context(), "workspace.deleted", nil, store.User(user), nil); err != nil {
			return err
		}
		return tx.Q.DeleteWorkspace(r.Context(), ws)
	}); err != nil {
		fail(w, r, err)
		return
	}
	w.WriteHeader(204)
}

func (h *H) listMembers(w http.ResponseWriter, r *http.Request) {
	out := []db.ListMembersRow{}
	err := h.inWS(r, func(tx *store.Tx, ws, _ uuid.UUID) error {
		var err error
		out, err = tx.Q.ListMembers(r.Context(), ws)
		return err
	})
	if err != nil {
		fail(w, r, err)
		return
	}
	if out == nil {
		out = []db.ListMembersRow{}
	}
	writeJSON(w, 200, out)
}

// keepAnOwner refuses a change that would leave the workspace without an owner.
func keepAnOwner(ctx context.Context, tx *store.Tx, ws, target uuid.UUID) error {
	m, err := tx.Q.GetMember(ctx, db.GetMemberParams{WorkspaceID: ws, UserID: target})
	if err != nil {
		return err
	}
	if m.Role != "owner" {
		return nil
	}
	n, err := tx.Q.CountOwners(ctx, ws)
	if err != nil {
		return err
	}
	if n <= 1 {
		return errLastOwner
	}
	return nil
}

func memberFail(w http.ResponseWriter, r *http.Request, err error) {
	if errors.Is(err, errLastOwner) {
		writeErr(w, 409, err.Error())
		return
	}
	fail(w, r, err)
}

func (h *H) setMemberRole(w http.ResponseWriter, r *http.Request) {
	target, err := uuid.Parse(chi.URLParam(r, "user"))
	if err != nil {
		writeErr(w, 404, "not found")
		return
	}
	var in struct {
		Role string `json:"role"`
	}
	if err := readJSON(r, &in); err != nil || (in.Role != "owner" && in.Role != "admin" && in.Role != "member") {
		writeErr(w, 400, "role must be owner, admin or member")
		return
	}
	if in.Role == "owner" && middleware.WorkspaceFrom(r.Context()).Role != "owner" {
		writeErr(w, 403, "only owners can make owners")
		return
	}
	err = h.inWS(r, func(tx *store.Tx, ws, user uuid.UUID) error {
		if in.Role != "owner" {
			if err := keepAnOwner(r.Context(), tx, ws, target); err != nil {
				return err
			}
		}
		n, err := tx.Q.SetMemberRole(r.Context(), db.SetMemberRoleParams{WorkspaceID: ws, UserID: target, Role: in.Role})
		if err != nil {
			return err
		}
		if n == 0 {
			return pgx.ErrNoRows
		}
		return tx.Emit(r.Context(), "member.role_changed", nil, store.User(user), map[string]string{"user_id": target.String(), "role": in.Role})
	})
	if err != nil {
		memberFail(w, r, err)
		return
	}
	w.WriteHeader(204)
}

func (h *H) removeMember(w http.ResponseWriter, r *http.Request) {
	target, err := uuid.Parse(chi.URLParam(r, "user"))
	if err != nil {
		writeErr(w, 404, "not found")
		return
	}
	h.remove(w, r, target)
}

func (h *H) leave(w http.ResponseWriter, r *http.Request) {
	h.remove(w, r, middleware.IdentityFrom(r.Context()).UserID)
}

func (h *H) remove(w http.ResponseWriter, r *http.Request, target uuid.UUID) {
	err := h.inWS(r, func(tx *store.Tx, ws, user uuid.UUID) error {
		if err := keepAnOwner(r.Context(), tx, ws, target); err != nil {
			return err
		}
		if _, err := tx.Q.RemoveMember(r.Context(), db.RemoveMemberParams{WorkspaceID: ws, UserID: target}); err != nil {
			return err
		}
		return tx.Emit(r.Context(), "member.removed", nil, store.User(user), map[string]string{"user_id": target.String()})
	})
	if err != nil {
		memberFail(w, r, err)
		return
	}
	w.WriteHeader(204)
}

func (h *H) createInvitation(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Email string `json:"email"`
		Role  string `json:"role"`
	}
	if err := readJSON(r, &in); err != nil || !strings.Contains(in.Email, "@") || (in.Role != "admin" && in.Role != "member") {
		writeErr(w, 400, "email and role (admin or member) required")
		return
	}
	email := normEmail(in.Email)
	plain, hash, _ := auth.NewToken("inv_")
	var inv db.CreateInvitationRow
	err := h.inWS(r, func(tx *store.Tx, ws, user uuid.UUID) error {
		var err error
		inv, err = tx.Q.CreateInvitation(r.Context(), db.CreateInvitationParams{
			WorkspaceID: ws, Email: email, Role: in.Role, TokenHash: hash, InvitedBy: user, ExpiresAt: time.Now().Add(inviteTTL),
		})
		if err != nil {
			return err
		}
		return tx.Emit(r.Context(), "invitation.created", nil, store.User(user), map[string]string{"email": email, "role": in.Role})
	})
	if err != nil {
		fail(w, r, err)
		return
	}
	body := "You have been invited to a Lykeion workspace. Accept with:\n  lyk workspace accept " + plain +
		"\nor POST /api/invitations/" + plain + "/accept"
	if err := h.Mail.Send(r.Context(), email, "Lykeion invitation", body); err != nil {
		fail(w, r, err)
		return
	}
	writeJSON(w, 201, map[string]any{"id": inv.ID, "token": plain, "email": inv.Email, "role": inv.Role, "expires_at": inv.ExpiresAt})
}

func (h *H) listInvitations(w http.ResponseWriter, r *http.Request) {
	out := []db.ListInvitationsRow{}
	err := h.inWS(r, func(tx *store.Tx, ws, _ uuid.UUID) error {
		var err error
		out, err = tx.Q.ListInvitations(r.Context(), ws)
		return err
	})
	if err != nil {
		fail(w, r, err)
		return
	}
	if out == nil {
		out = []db.ListInvitationsRow{}
	}
	writeJSON(w, 200, out)
}

func (h *H) revokeInvitation(w http.ResponseWriter, r *http.Request) {
	inv, err := uuid.Parse(chi.URLParam(r, "inv"))
	if err != nil {
		writeErr(w, 404, "not found")
		return
	}
	err = h.inWS(r, func(tx *store.Tx, ws, user uuid.UUID) error {
		n, err := tx.Q.RevokeInvitation(r.Context(), db.RevokeInvitationParams{ID: inv, WorkspaceID: ws})
		if err == nil && n == 0 {
			return pgx.ErrNoRows
		}
		if err != nil {
			return err
		}
		return tx.Emit(r.Context(), "invitation.revoked", nil, store.User(user), map[string]string{"invitation_id": inv.String()})
	})
	if err != nil {
		fail(w, r, err)
		return
	}
	w.WriteHeader(204)
}

// acceptInvitation discovers only the caller's invitation, then sets its workspace
// on the same transaction before taking the workspace and invitation locks.
func (h *H) acceptInvitation(w http.ResponseWriter, r *http.Request) {
	id := middleware.IdentityFrom(r.Context())
	var inv db.WorkspaceInvitation
	var role string
	err := h.Store.AsUser(r.Context(), id.UserID, id.Email, func(tx *store.Tx) error {
		hash := auth.Hash(chi.URLParam(r, "token"))
		var err error
		inv, err = tx.Q.InvitationByToken(r.Context(), hash)
		if err != nil {
			return err
		}
		if inv.Email != id.Email {
			return pgx.ErrNoRows
		}
		if err = tx.Q.SetInvitationWorkspace(r.Context(), inv.WorkspaceID.String()); err != nil {
			return err
		}
		tx.WS = inv.WorkspaceID
		if _, err = tx.Q.LockWorkspaceForMutation(r.Context(), inv.WorkspaceID); err != nil {
			return err
		}
		inv, err = tx.Q.LockInvitationByToken(r.Context(), hash)
		if err != nil {
			return err
		}
		if inv.Email != id.Email {
			return pgx.ErrNoRows
		}
		m, err := tx.Q.LockMember(r.Context(), db.LockMemberParams{WorkspaceID: inv.WorkspaceID, UserID: id.UserID})
		joined := errors.Is(err, pgx.ErrNoRows)
		if err != nil && !joined {
			return err
		}
		role = m.Role
		if joined {
			role = inv.Role
			if err = tx.Q.AddMember(r.Context(), db.AddMemberParams{WorkspaceID: inv.WorkspaceID, UserID: id.UserID, Role: role}); err != nil {
				return err
			}
		}
		if err = tx.Q.AcceptInvitation(r.Context(), inv.ID); err != nil {
			return err
		}
		if err = tx.Emit(r.Context(), "invitation.accepted", nil, store.User(id.UserID), map[string]any{"invitation_id": inv.ID, "role": role}); err != nil {
			return err
		}
		if joined {
			return tx.Emit(r.Context(), "member.joined", nil, store.User(id.UserID), map[string]string{"user_id": id.UserID.String(), "role": role})
		}
		return nil
	})
	if err != nil {
		fail(w, r, err)
		return
	}
	writeJSON(w, 200, map[string]any{"workspace_id": inv.WorkspaceID, "role": role})
}
