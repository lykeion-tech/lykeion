package handler

import (
	"errors"
	"net/http"
	"net/url"
	"strconv"
	"strings"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"lykeion/server/internal/github"
	"lykeion/server/internal/middleware"
	"lykeion/server/internal/store"
	db "lykeion/server/pkg/db/generated"
)

const githubNotConfigured = "GitHub isn't configured on this server."

type githubConnectionView struct {
	InstallationID   int64     `json:"installation_id"`
	AccountLogin     string    `json:"account_login"`
	AccountType      string    `json:"account_type"`
	AccountAvatarURL string    `json:"account_avatar_url"`
	ConnectedBy      uuid.UUID `json:"connected_by"`
	Status           string    `json:"status"`
	ImportState      string    `json:"import_state"`
}

func (h *H) getGitHub(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	tx := middleware.TxFrom(ctx)
	autoLink, err := tx.Q.GetGithubAutoLink(ctx, tx.WS)
	if err != nil {
		fail(w, r, err)
		return
	}
	rows, err := tx.Q.ListGithubConnections(ctx, tx.WS)
	if err != nil {
		fail(w, r, err)
		return
	}
	connections := make([]githubConnectionView, 0, len(rows))
	for _, row := range rows {
		connections = append(connections, githubConnectionView{
			InstallationID: row.InstallationID, AccountLogin: row.AccountLogin,
			AccountType: row.AccountType, AccountAvatarURL: row.AccountAvatarUrl,
			ConnectedBy: row.ConnectedBy, Status: row.Status, ImportState: row.ImportState,
		})
	}
	writeJSON(w, 200, map[string]any{"configured": h.GitHub != nil && h.GitHub.Config.CanConnect(), "auto_link": autoLink, "connections": connections})
}

func (h *H) connectGitHub(w http.ResponseWriter, r *http.Request) {
	if h.GitHub == nil || !h.GitHub.Config.CanConnect() {
		writeErr(w, 503, githubNotConfigured)
		return
	}
	ctx := r.Context()
	u, err := h.GitHub.InstallURL(middleware.WorkspaceFrom(ctx).ID, middleware.IdentityFrom(ctx).UserID)
	if answer(w, r, err) {
		writeJSON(w, 200, map[string]string{"url": u})
	}
}

func (h *H) setupGitHub(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("Referrer-Policy", "no-referrer")
	base := h.PublicURL
	if h.GitHub != nil {
		base = h.GitHub.PublicURL
	}
	dest := strings.TrimRight(base, "/") + "/app/#/settings/code?"
	result := "github=connected"
	err := h.completeGitHubSetup(r)
	if err != nil {
		msg := "GitHub connection failed. Try again."
		var ue *github.UserError
		if errors.As(err, &ue) {
			msg = ue.Msg
		}
		result = "github_error=" + url.QueryEscape(msg)
	}
	http.Redirect(w, r, dest+result, http.StatusFound)
}

func (h *H) completeGitHubSetup(r *http.Request) error {
	if h.GitHub == nil {
		return errors.New("GitHub unavailable")
	}
	var user uuid.UUID
	if cookie, err := r.Cookie(middleware.SessionCookie); err == nil {
		if id, _, err := h.JWT.Parse(cookie.Value); err == nil {
			user = id
		}
	}
	query := r.URL.Query()
	state := query.Get("state")
	if err := h.GitHub.CheckSetupSession(state, user); err != nil {
		return err
	}
	installation, err := strconv.ParseInt(query.Get("installation_id"), 10, 64)
	if err != nil {
		return err
	}
	_, err = h.GitHub.Complete(r.Context(), state, query.Get("code"), installation)
	return err
}

func (h *H) patchGitHub(w http.ResponseWriter, r *http.Request) {
	var in struct {
		AutoLink *bool `json:"auto_link"`
	}
	if err := readJSON(r, &in); err != nil || in.AutoLink == nil {
		writeErr(w, 400, "auto_link is required")
		return
	}
	ctx := r.Context()
	tx := middleware.TxFrom(ctx)
	err := tx.Q.SetGithubAutoLink(ctx, db.SetGithubAutoLinkParams{WorkspaceID: tx.WS, Enabled: *in.AutoLink})
	if err == nil {
		err = tx.Emit(ctx, "github.changed", nil, store.User(middleware.IdentityFrom(ctx).UserID), nil)
	}
	if answer(w, r, err) {
		w.WriteHeader(http.StatusNoContent)
	}
}

func (h *H) disconnectGitHub(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.ParseInt(chi.URLParam(r, "installation"), 10, 64)
	if err != nil || id <= 0 {
		writeErr(w, 404, "connection not found")
		return
	}
	ctx := r.Context()
	tx := middleware.TxFrom(ctx)
	n, err := tx.Q.DeleteGithubConnection(ctx, db.DeleteGithubConnectionParams{WorkspaceID: tx.WS, InstallationID: id})
	if err == nil {
		if n == 0 {
			err = pgx.ErrNoRows
		} else {
			err = tx.Emit(ctx, "github.changed", nil, store.User(middleware.IdentityFrom(ctx).UserID), nil)
		}
	}
	if answer(w, r, err) {
		w.WriteHeader(http.StatusNoContent)
	}
}
