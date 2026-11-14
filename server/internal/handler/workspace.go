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
	"lykeion/server/internal/pack"
	"lykeion/server/internal/service"
	"lykeion/server/internal/store"
	"lykeion/server/internal/team"
	db "lykeion/server/pkg/db/generated"
)

var slugRe = regexp.MustCompile(`^[a-z0-9][a-z0-9-]{1,38}$`)

const inviteTTL = 7 * 24 * time.Hour

var errLastOwner = errors.New("a workspace needs at least one owner")

var prefixRe = regexp.MustCompile(`^([A-Z]{2,6})?$`)

// displayPrefix is the prefix card numbers show with: the workspace's own,
// else the first four letters of its slug in capitals, else LYK when the slug
// has fewer than two letters.
func displayPrefix(prefix, slug string) string {
	if prefix != "" {
		return prefix
	}
	var b strings.Builder
	for _, c := range slug {
		if c >= 'a' && c <= 'z' && b.Len() < 4 {
			b.WriteRune(c - 'a' + 'A')
		}
	}
	if b.Len() < 2 {
		return "LYK"
	}
	return b.String()
}

// workspaceView is a workspace with the prefix its card numbers show and its
// newest event id, read in the same transaction: a browser's first /ws
// subscription starts from that cursor instead of replaying the history.
type workspaceView struct {
	db.Workspace
	DisplayPrefix string `json:"display_prefix"`
	EventCursor   int64  `json:"event_cursor"`
}

type myWorkspace struct {
	db.ListMyWorkspacesRow
	DisplayPrefix string `json:"display_prefix"`
	EventCursor   int64  `json:"event_cursor"`
}

func (h *H) listWorkspaces(w http.ResponseWriter, r *http.Request) {
	id := middleware.IdentityFrom(r.Context())
	out := []myWorkspace{}
	err := h.Store.AsUser(r.Context(), id.UserID, id.Email, func(tx *store.Tx) error {
		rows, err := tx.Q.ListMyWorkspaces(r.Context(), id.UserID)
		if err != nil {
			return err
		}
		for _, row := range rows {
			// Events are visible only inside their workspace's scope.
			if err := tx.Workspace(r.Context(), row.ID); err != nil {
				return err
			}
			cursor, err := tx.Q.LatestEventID(r.Context(), row.ID)
			if err != nil {
				return err
			}
			out = append(out, myWorkspace{row, displayPrefix(row.CardPrefix, row.Slug), cursor})
		}
		return nil
	})
	if err != nil {
		fail(w, r, err)
		return
	}
	writeJSON(w, 200, out)
}

func (h *H) createWorkspace(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Name string `json:"name"`
		Slug string `json:"slug"`
		// Laboratory is the built-in one when empty.
		Laboratory string `json:"laboratory"`
	}
	if err := readJSON(r, &in); err == nil && in.Laboratory != "" && in.Laboratory != "built-in" {
		writeErr(w, 400, "unknown laboratory")
		return
	} else if err != nil || strings.TrimSpace(in.Name) == "" || !slugRe.MatchString(in.Slug) {
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
		if err := pack.InstallShipped(r.Context(), tx, h.Blobs, h.Shipped); err != nil {
			return err
		}
		if err := team.InstallLaboratory(r.Context(), tx); err != nil {
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
	var out workspaceView
	err := h.inWS(r, func(tx *store.Tx, ws, _ uuid.UUID) error {
		var err error
		if out.Workspace, err = tx.Q.GetWorkspace(r.Context(), ws); err != nil {
			return err
		}
		out.DisplayPrefix = displayPrefix(out.CardPrefix, out.Slug)
		out.EventCursor, err = tx.Q.LatestEventID(r.Context(), ws)
		return err
	})
	if err != nil {
		fail(w, r, err)
		return
	}
	writeJSON(w, 200, out)
}

// updateWorkspace changes any of the workspace's name, description, research
// context and card prefix. The slug never changes.
func (h *H) updateWorkspace(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Name            *string `json:"name"`
		Description     *string `json:"description"`
		ResearchContext *string `json:"research_context"`
		CardPrefix      *string `json:"card_prefix"`
	}
	if err := readJSON(r, &in); err != nil {
		writeErr(w, 400, "invalid workspace: "+err.Error())
		return
	}
	switch {
	case in.Name == nil && in.Description == nil && in.ResearchContext == nil && in.CardPrefix == nil:
		writeErr(w, 400, "nothing to change")
		return
	case in.Name != nil && strings.TrimSpace(*in.Name) == "":
		writeErr(w, 400, "name cannot be empty")
		return
	case in.CardPrefix != nil && !prefixRe.MatchString(*in.CardPrefix):
		writeErr(w, 400, "card_prefix must be 2 to 6 capital letters, or empty")
		return
	}
	var out workspaceView
	err := h.inWS(r, func(tx *store.Tx, ws, user uuid.UUID) error {
		cur, err := tx.Q.GetWorkspace(r.Context(), ws)
		if err != nil {
			return err
		}
		p := db.UpdateWorkspaceParams{ID: ws, Name: cur.Name, Description: cur.Description, ResearchContext: cur.ResearchContext, CardPrefix: cur.CardPrefix}
		var fields []string
		if in.Name != nil {
			p.Name, fields = *in.Name, append(fields, "name")
		}
		if in.Description != nil {
			p.Description, fields = *in.Description, append(fields, "description")
		}
		if in.ResearchContext != nil {
			p.ResearchContext, fields = *in.ResearchContext, append(fields, "research_context")
		}
		if in.CardPrefix != nil {
			p.CardPrefix, fields = *in.CardPrefix, append(fields, "card_prefix")
		}
		if out.Workspace, err = tx.Q.UpdateWorkspace(r.Context(), p); err != nil {
			return err
		}
		if err := tx.Emit(r.Context(), "workspace.updated", nil, store.User(user), map[string]any{"fields": fields}); err != nil {
			return err
		}
		out.DisplayPrefix = displayPrefix(out.CardPrefix, out.Slug)
		out.EventCursor, err = tx.Q.LatestEventID(r.Context(), ws)
		return err
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
		files, err := tx.Q.WorkspaceAttachments(r.Context(), ws)
		if err != nil {
			return err
		}
		if err := tx.Q.DeleteWorkspace(r.Context(), ws); err != nil {
			return err
		}
		h.deleteFilesAfterCommit(r, tx, files)
		return nil
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

var errProjectOwnerDeparture = errors.New("transfer project ownership to another member before leaving the workspace")

func memberFail(w http.ResponseWriter, r *http.Request, err error) {
	if errors.Is(err, errLastOwner) || errors.Is(err, errProjectOwnerDeparture) {
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
		if err := service.KeepLaboratoryOwned(r.Context(), tx); err != nil {
			return err
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
		if _, err := tx.Q.LockWorkspaceExecution(r.Context(), ws); err != nil {
			return err
		}
		if err := tx.Q.LockWorkspaceEvents(r.Context(), ws); err != nil {
			return err
		}
		if err := keepAnOwner(r.Context(), tx, ws, target); err != nil {
			return err
		}
		allowed, err := tx.Q.CoresearchDepart(r.Context(), target)
		if err != nil {
			return err
		}
		if !allowed {
			return errProjectOwnerDeparture
		}
		if err := h.AgentRuns.RemoveMemberSharing(r.Context(), tx, user, target); err != nil {
			return err
		}
		if _, err := tx.Q.RemoveMember(r.Context(), db.RemoveMemberParams{WorkspaceID: ws, UserID: target}); err != nil {
			return err
		}
		cards, err := tx.Q.ClearDepartedMemberAssignees(r.Context(), db.ClearDepartedMemberAssigneesParams{WorkspaceID: ws, UserID: &target})
		if err != nil {
			return err
		}
		for _, c := range cards {
			if err := tx.Emit(r.Context(), "card.properties_updated", &c.ID, store.User(user), map[string]any{
				"fields": []string{"assignee_type", "assignee_id"}, "research_id": c.ResearchID, "previous_research_id": c.ResearchID,
				"assignee_type": nil, "assignee_id": nil,
			}); err != nil {
				return err
			}
		}
		projects, err := tx.Q.ClearDepartedMemberProjectLeads(r.Context(), db.ClearDepartedMemberProjectLeadsParams{WorkspaceID: ws, UserID: &target})
		if err != nil {
			return err
		}
		for _, p := range projects {
			if err := tx.Emit(r.Context(), "research.updated", nil, store.User(user), map[string]any{"research_id": p.ID}); err != nil {
				return err
			}
		}
		if err := service.KeepLaboratoryOwned(r.Context(), tx); err != nil {
			return err
		}
		agents, err := tx.Q.ArchiveOwnedAgents(r.Context(), db.ArchiveOwnedAgentsParams{WorkspaceID: ws, OwnerID: target})
		if err != nil {
			return err
		}
		for _, a := range agents {
			if err := tx.Emit(r.Context(), "agent.archived", nil, store.User(user), map[string]any{"agent_id": a.ID}); err != nil {
				return err
			}
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
	body := "You have been invited to a Lykeion workspace. Open " + h.PublicURL + "/app/#/invite/" + plain +
		"\nor accept with:\n  lyk workspace accept " + plain + "\nor POST /api/invitations/" + plain + "/accept"
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
