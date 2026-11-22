package handler

import (
	"context"
	"errors"
	"net/http"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"lykeion/server/internal/middleware"
	"lykeion/server/internal/seal"
	"lykeion/server/internal/slack"
	"lykeion/server/internal/store"
	db "lykeion/server/pkg/db/generated"
)

const slackVisibilityNote = "An answer to a question asked in a Slack channel is visible to everyone in that channel, including people outside this workspace."

func (h *H) slackReady(w http.ResponseWriter) bool {
	if h.Slack == nil {
		writeErr(w, 503, "Slack is unavailable")
		return false
	}
	return true
}

// slackKeyed refuses paths that seal or open Slack tokens on a server
// without LYK_SECRET_KEY.
func (h *H) slackKeyed(w http.ResponseWriter) bool {
	if len(h.Slack.Key) == 0 {
		writeErr(w, 503, "Slack is unavailable: this server has no LYK_SECRET_KEY")
		return false
	}
	return true
}

type slackBindingView struct {
	SlackUserID string `json:"slack_user_id"`
}

type slackView struct {
	Connected       bool              `json:"connected"`
	Status          string            `json:"status,omitempty"`
	TeamName        string            `json:"team_name,omitempty"`
	DraftingAgentID *uuid.UUID        `json:"drafting_agent_id"`
	MyBinding       *slackBindingView `json:"my_binding"`
	VisibilityNote  string            `json:"visibility_note"`
}

// getSlack reports whether Slack is connected and the caller's own link. It
// never carries a token.
func (h *H) getSlack(w http.ResponseWriter, r *http.Request) {
	if !h.slackReady(w) {
		return
	}
	out := slackView{VisibilityNote: slackVisibilityNote}
	err := h.inWS(r, func(tx *store.Tx, _, _ uuid.UUID) error {
		inst, err := tx.Q.GetSlackInstallation(r.Context())
		if errors.Is(err, pgx.ErrNoRows) {
			return nil
		} else if err != nil {
			return err
		}
		out.Connected, out.Status, out.TeamName, out.DraftingAgentID = true, inst.Status, inst.TeamName, inst.DraftingAgentID
		mine, err := tx.Q.ListMySlackBindings(r.Context())
		if err != nil {
			return err
		}
		for _, b := range mine {
			if b.InstallationID == inst.ID {
				out.MyBinding = &slackBindingView{SlackUserID: b.SlackUserID}
				break
			}
		}
		return nil
	})
	if err != nil {
		fail(w, r, err)
		return
	}
	writeJSON(w, 200, out)
}

// putSlack verifies both tokens, seals them and connects the workspace.
func (h *H) putSlack(w http.ResponseWriter, r *http.Request) {
	if !h.slackReady(w) {
		return
	}
	var in struct {
		BotToken        string    `json:"bot_token"`
		AppToken        string    `json:"app_token"`
		DraftingAgentID uuid.UUID `json:"drafting_agent_id"`
	}
	if err := readJSON(r, &in); err != nil || in.BotToken == "" || in.AppToken == "" || in.DraftingAgentID == uuid.Nil {
		writeErr(w, 400, "bot token, app token and drafting agent are required")
		return
	}
	if !h.slackKeyed(w) {
		return
	}
	identity, err := h.Slack.Verify(r.Context(), in.BotToken, in.AppToken)
	if err != nil {
		writeErr(w, 400, err.Error())
		return
	}
	var out slackView
	err = h.inWS(r, func(tx *store.Tx, ws, user uuid.UUID) error {
		ctx := r.Context()
		if _, err := tx.Q.LockWorkspaceExecution(ctx, ws); err != nil {
			return err
		}
		if err := tx.Q.LockWorkspaceEvents(ctx, ws); err != nil {
			return err
		}
		// A revoked installation is replaced: in place for the same app, so
		// bindings survive; otherwise it is disconnected first.
		id := uuid.New()
		old, err := tx.Q.GetSlackInstallation(ctx)
		switch {
		case err == nil && old.Status != "revoked":
			return conflict("Slack is already connected")
		case err == nil && old.AppID == identity.AppID:
			id = old.ID
		case err == nil:
			if err := h.disconnectSlackInTx(ctx, tx, ws, user, old); err != nil {
				return err
			}
		case !errors.Is(err, pgx.ErrNoRows):
			return err
		}
		if err := DraftingAgentUsable(ctx, tx, ws, in.DraftingAgentID); err != nil {
			return err
		}
		bot, err := h.Slack.Seal(ws, id, "bot", in.BotToken)
		if err != nil {
			return err
		}
		app, err := h.Slack.Seal(ws, id, "app", in.AppToken)
		if err != nil {
			return err
		}
		var inst db.SlackInstallation
		if id == old.ID {
			inst, err = tx.Q.ReconnectSlackInstallation(ctx, db.ReconnectSlackInstallationParams{ID: id, TeamID: identity.TeamID,
				TeamName: identity.TeamName, BotUserID: identity.BotUserID, BotTokenCiphertext: bot, AppTokenCiphertext: app,
				DraftingAgentID: &in.DraftingAgentID, InstalledBy: user})
		} else {
			inst, err = tx.Q.CreateSlackInstallation(ctx, db.CreateSlackInstallationParams{ID: id, AppID: identity.AppID, TeamID: identity.TeamID,
				TeamName: identity.TeamName, BotUserID: identity.BotUserID, BotTokenCiphertext: bot, AppTokenCiphertext: app,
				DraftingAgentID: &in.DraftingAgentID, InstalledBy: user})
		}
		if err != nil {
			return err
		}
		out = slackView{Connected: true, Status: inst.Status, TeamName: inst.TeamName, DraftingAgentID: inst.DraftingAgentID, VisibilityNote: slackVisibilityNote}
		return tx.Emit(ctx, "slack.changed", nil, store.User(user), nil)
	})
	switch {
	case isUnique(err):
		err = conflict("That Slack app is already connected")
	case errors.Is(err, seal.ErrNoKey):
		err = cardInputError(err.Error())
	}
	if answer(w, r, err) {
		writeJSON(w, 200, out)
	}
}

// patchSlack changes the agent that drafts studies.
func (h *H) patchSlack(w http.ResponseWriter, r *http.Request) {
	if !h.slackReady(w) {
		return
	}
	var in struct {
		DraftingAgentID uuid.UUID `json:"drafting_agent_id"`
	}
	if err := readJSON(r, &in); err != nil || in.DraftingAgentID == uuid.Nil {
		writeErr(w, 400, "choose a drafting agent")
		return
	}
	err := h.inWS(r, func(tx *store.Tx, ws, user uuid.UUID) error {
		ctx := r.Context()
		if err := tx.Q.LockWorkspaceEvents(ctx, ws); err != nil {
			return err
		}
		if _, err := tx.Q.GetSlackInstallation(ctx); err != nil {
			return err
		}
		if err := DraftingAgentUsable(ctx, tx, ws, in.DraftingAgentID); err != nil {
			return err
		}
		if _, err := tx.Q.SetSlackDraftingAgent(ctx, &in.DraftingAgentID); err != nil {
			return err
		}
		return tx.Emit(ctx, "slack.changed", nil, store.User(user), nil)
	})
	if answer(w, r, err) {
		w.WriteHeader(204)
	}
}

// deleteSlack disconnects Slack: it stops the runs Slack asked for and removes
// the installation with its bindings, tokens, chats and posts. Lab chats stay.
func (h *H) deleteSlack(w http.ResponseWriter, r *http.Request) {
	if !h.slackReady(w) {
		return
	}
	err := h.inWS(r, func(tx *store.Tx, ws, user uuid.UUID) error {
		ctx := r.Context()
		if _, err := tx.Q.LockWorkspaceExecution(ctx, ws); err != nil {
			return err
		}
		if err := tx.Q.LockWorkspaceEvents(ctx, ws); err != nil {
			return err
		}
		inst, err := tx.Q.GetSlackInstallation(ctx)
		if err != nil {
			return err
		}
		if err := h.disconnectSlackInTx(ctx, tx, ws, user, inst); err != nil {
			return err
		}
		return tx.Emit(ctx, "slack.changed", nil, store.User(user), nil)
	})
	if answer(w, r, err) {
		w.WriteHeader(204)
	}
}

// disconnectSlackInTx cancels the runs Slack asked for and deletes the
// installation. The caller holds the execution and event locks.
func (h *H) disconnectSlackInTx(ctx context.Context, tx *store.Tx, ws, user uuid.UUID, inst db.SlackInstallation) error {
	ids, err := tx.Q.ListSlackRunIDs(ctx, inst.ID)
	if err != nil {
		return err
	}
	for _, id := range ids {
		run, err := tx.Q.LockAgentRun(ctx, db.LockAgentRunParams{ID: id, WorkspaceID: ws})
		if errors.Is(err, pgx.ErrNoRows) {
			continue
		} else if err != nil {
			return err
		}
		if run.Status == "queued" || run.Status == "dispatched" || run.Status == "running" {
			if err := h.AgentRuns.CancelInTx(ctx, tx, user, run); err != nil {
				return err
			}
		}
	}
	return tx.Q.DeleteSlackInstallation(ctx, inst.ID)
}

// deleteSlackBinding unlinks the caller's own Slack account.
func (h *H) deleteSlackBinding(w http.ResponseWriter, r *http.Request) {
	if !h.slackReady(w) {
		return
	}
	err := h.inWS(r, func(tx *store.Tx, ws, user uuid.UUID) error {
		ctx := r.Context()
		if err := tx.Q.LockWorkspaceEvents(ctx, ws); err != nil {
			return err
		}
		mine, err := tx.Q.ListMySlackBindings(ctx)
		if err != nil {
			return err
		}
		for _, b := range mine {
			if _, err := tx.Q.DeleteMySlackBinding(ctx, db.DeleteMySlackBindingParams{InstallationID: b.InstallationID, SlackUserID: b.SlackUserID}); err != nil {
				return err
			}
		}
		return tx.Emit(ctx, "slack.changed", nil, store.User(user), nil)
	})
	if answer(w, r, err) {
		w.WriteHeader(204)
	}
}

// SlackDraft queues a study draft asked for in Slack. The drafting agent is
// checked on every call; there is no fallback.
func (h *H) SlackDraft(ctx context.Context, ws, user uuid.UUID, in slack.DraftIn) (uuid.UUID, error) {
	err := h.Store.InWorkspace(ctx, ws, user, func(tx *store.Tx) error { return DraftingAgentUsable(ctx, tx, ws, in.AgentID) })
	var id uuid.UUID
	if err == nil {
		id, err = h.DraftStudy(ctx, ws, user, StudyDraftIn{AgentID: in.AgentID, Prompt: in.Prompt, Priority: in.Priority})
	}
	var refused cardInputError
	if errors.As(err, &refused) {
		return uuid.Nil, slack.UserError(refused)
	}
	return id, err
}

// postSlackLink redeems a link token for the caller. The email that Slack
// confirms must be the one on the caller's own account.
func (h *H) postSlackLink(w http.ResponseWriter, r *http.Request) {
	if !h.slackReady(w) || !h.slackKeyed(w) {
		return
	}
	var in struct {
		Token string `json:"token"`
	}
	if err := readJSON(r, &in); err != nil || in.Token == "" {
		writeErr(w, 400, slack.ErrLinkExpired.Error())
		return
	}
	var email, team string
	err := h.inWS(r, func(tx *store.Tx, _, user uuid.UUID) error {
		u, err := tx.Q.GetUser(r.Context(), user)
		if err != nil {
			return err
		}
		email = u.Email
		inst, err := tx.Q.GetSlackInstallation(r.Context())
		team = inst.TeamName
		return err
	})
	if errors.Is(err, pgx.ErrNoRows) {
		err = cardInputError(slack.ErrLinkExpired.Error())
	}
	var slackUser string
	if err == nil {
		slackUser, err = h.Slack.Redeem(r.Context(), middleware.WorkspaceFrom(r.Context()).ID, middleware.IdentityFrom(r.Context()).UserID, email, in.Token)
		if errors.Is(err, slack.ErrLinkExpired) || errors.Is(err, slack.ErrEmailMismatch) {
			err = cardInputError(err.Error())
		} else if isUnique(err) {
			err = conflict("That Slack user is already linked")
		}
	}
	if answer(w, r, err) {
		writeJSON(w, 200, map[string]string{"slack_user_id": slackUser, "team_name": team})
	}
}
