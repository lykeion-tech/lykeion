package handler

import (
	"errors"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

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
	h.startGitHubSetup(w, r, "")
}

func (h *H) connectExistingGitHub(w http.ResponseWriter, r *http.Request) {
	var in struct {
		InstallationURL string `json:"installation_url"`
	}
	if readJSON(r, &in) != nil || strings.TrimSpace(in.InstallationURL) == "" {
		writeErr(w, 400, "A GitHub installation settings URL is required.")
		return
	}
	h.startGitHubSetup(w, r, in.InstallationURL)
}

func (h *H) startGitHubSetup(w http.ResponseWriter, r *http.Request, settingsURL string) {
	if h.GitHub == nil || !h.GitHub.Config.CanConnect() {
		writeErr(w, 503, githubNotConfigured)
		return
	}
	ctx := r.Context()
	u, err := h.GitHub.InstallURL(middleware.WorkspaceFrom(ctx).ID, middleware.IdentityFrom(ctx).UserID, settingsURL)
	var ue *github.UserError
	if errors.As(err, &ue) {
		writeErr(w, 400, ue.Msg)
		return
	}
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
	workspace, err := h.completeGitHubSetup(r)
	var result string
	if err != nil {
		msg := "GitHub connection failed. Try again."
		var ue *github.UserError
		if errors.As(err, &ue) {
			msg = ue.Msg
		}
		result = "github_error=" + url.QueryEscape(msg)
	} else {
		result = "github=connected&github_workspace=" + workspace.String()
	}
	http.Redirect(w, r, dest+result, http.StatusFound)
}

func (h *H) completeGitHubSetup(r *http.Request) (uuid.UUID, error) {
	if h.GitHub == nil {
		return uuid.Nil, errors.New("GitHub unavailable")
	}
	var user uuid.UUID
	if cookie, err := r.Cookie(middleware.SessionCookie); err == nil {
		if id, _, err := h.JWT.Parse(cookie.Value); err == nil {
			user = id
		}
	}
	query := r.URL.Query()
	return h.GitHub.Complete(r.Context(), query.Get("state"), query.Get("code"), query["installation_id"], user)
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

func (h *H) retryGitHubImport(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.ParseInt(chi.URLParam(r, "installation"), 10, 64)
	if err != nil || id <= 0 {
		writeErr(w, 404, "connection not found")
		return
	}
	if h.GitHub == nil || h.GitHub.Config.AppID <= 0 || h.GitHub.Config.PrivateKey == nil {
		writeErr(w, 503, githubNotConfigured)
		return
	}
	ctx := r.Context()
	err = h.GitHub.RetryImport(ctx, middleware.TxFrom(ctx), id, user(r))
	if errors.Is(err, github.ErrImportNotFailed) {
		writeErr(w, 409, "Only a failed import can be retried.")
		return
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

func (h *H) githubWebhook(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	if h.GitHub == nil || h.GitHub.Config.WebhookSecret == "" {
		writeErr(w, 503, githubNotConfigured)
		return
	}
	ok, err := h.Limit.Allow(ctx, "github:wh:ip:"+h.clientIP(r), webhookPerIP, time.Minute)
	if err != nil {
		writeErr(w, 503, "try again later")
		return
	}
	if !ok {
		writeErr(w, 429, "too many requests")
		return
	}
	body, err := io.ReadAll(http.MaxBytesReader(w, r.Body, maxWebhookBody))
	var tooBig *http.MaxBytesError
	if errors.As(err, &tooBig) {
		writeErr(w, 413, "payload too large: at most 1 MiB")
		return
	}
	if err != nil {
		writeErr(w, 400, "unreadable body")
		return
	}
	if !github.VerifySignature(h.GitHub.Config.WebhookSecret, r.Header.Get("X-Hub-Signature-256"), body) {
		writeErr(w, 401, "invalid GitHub signature")
		return
	}
	err = h.GitHub.Handle(ctx, r.Header.Get("X-GitHub-Event"), r.Header.Get("X-GitHub-Delivery"), body)
	switch {
	case errors.Is(err, github.ErrWebhookPayload):
		writeErr(w, 400, err.Error())
	case err != nil:
		writeErr(w, 500, "GitHub delivery failed")
	default:
		w.WriteHeader(http.StatusOK)
	}
}

func (h *H) listStudyPRs(w http.ResponseWriter, r *http.Request) {
	card, ok := cardID(w, r)
	if !ok {
		return
	}
	ctx := r.Context()
	tx := middleware.TxFrom(ctx)
	if _, err := tx.Q.GetCard(ctx, db.GetCardParams{WorkspaceID: tx.WS, ID: card}); !answer(w, r, err) {
		return
	}
	rows, err := tx.Q.ListPullRequestsForCard(ctx, db.ListPullRequestsForCardParams{WorkspaceID: tx.WS, CardID: card})
	if !answer(w, r, err) {
		return
	}
	if rows == nil {
		rows = []db.ListPullRequestsForCardRow{}
	}
	writeJSON(w, 200, rows)
}

func (h *H) linkStudyPR(w http.ResponseWriter, r *http.Request) {
	card, ok := cardID(w, r)
	if !ok {
		return
	}
	raw := middleware.FromHeader(r)
	if raw == "" {
		writeErr(w, 400, "X-Workspace-ID required")
		return
	}
	ws, err := uuid.Parse(raw)
	if err != nil {
		writeErr(w, 404, "workspace not found")
		return
	}
	var in struct {
		URL string `json:"url"`
	}
	if err := readJSON(r, &in); err != nil {
		in.URL = ""
	}
	svc := h.GitHub
	if svc == nil {
		svc = &github.Service{Store: h.Store}
	}
	row, err := svc.LinkByURL(r.Context(), ws, user(r), card, in.URL)
	var ue *github.UserError
	if errors.As(err, &ue) {
		writeErr(w, 400, ue.Msg)
		return
	}
	if answer(w, r, err) {
		writeJSON(w, 201, row)
	}
}

func (h *H) unlinkStudyPR(w http.ResponseWriter, r *http.Request) {
	card, ok := cardID(w, r)
	if !ok {
		return
	}
	pr, err := uuid.Parse(chi.URLParam(r, "pr"))
	if err != nil {
		writeErr(w, 404, "not found")
		return
	}
	ctx := r.Context()
	tx := middleware.TxFrom(ctx)
	n, err := tx.Q.DeleteGithubLink(ctx, db.DeleteGithubLinkParams{WorkspaceID: tx.WS, CardID: card, PrID: pr})
	if err == nil && n == 0 {
		err = pgx.ErrNoRows
	}
	if !answer(w, r, err) {
		return
	}
	err = tx.Q.InsertGithubUnlink(ctx, db.InsertGithubUnlinkParams{WorkspaceID: tx.WS, CardID: card, PrID: pr})
	if err == nil {
		err = tx.Emit(ctx, "github.pr.changed", &card, store.User(user(r)), nil)
	}
	if answer(w, r, err) {
		w.WriteHeader(http.StatusNoContent)
	}
}
