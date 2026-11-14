package handler

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"sort"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"lykeion/server/internal/coresearch"
	"lykeion/server/internal/store"
	db "lykeion/server/pkg/db/generated"
)

type ConversationStarter struct {
	Label  string `json:"label"`
	Prompt string `json:"prompt"`
}

func validateConversationStarters(raw json.RawMessage) error {
	var starters []ConversationStarter
	if err := json.Unmarshal(raw, &starters); err != nil || starters == nil || !utf8.Valid(raw) {
		return cardInputError("conversation_starters must be an array")
	}
	if len(starters) > 12 {
		return cardInputError("at most 12 conversation starters")
	}
	for _, s := range starters {
		if strings.TrimSpace(s.Label) == "" || strings.TrimSpace(s.Prompt) == "" || utf8.RuneCountInString(s.Label) > 120 || len(s.Prompt) > 16384 || !utf8.ValidString(s.Label) || !utf8.ValidString(s.Prompt) || strings.ContainsRune(s.Label, 0) || strings.ContainsRune(s.Prompt, 0) {
			return cardInputError("invalid conversation starter label or prompt")
		}
	}
	return nil
}
func (h *H) bootstrapAristotle(w http.ResponseWriter, r *http.Request) {
	var a db.Agent
	var c db.CoresearchConversation
	err := h.inWS(r, func(tx *store.Tx, ws, user uuid.UUID) error {
		ctx := r.Context()
		if err := tx.Q.LockAristotleOwner(ctx); err != nil {
			return err
		}
		var err error
		a, err = tx.Q.EnsureAristotle(ctx)
		if err != nil {
			return err
		}
		c, err = tx.Q.GetWelcomeConversation(ctx)
		if err == nil {
			return nil
		}
		if !errors.Is(err, pgx.ErrNoRows) {
			return err
		}
		c, err = tx.Q.FirstCoresearchConversation(ctx)
		if err == nil {
			return nil
		}
		if !errors.Is(err, pgx.ErrNoRows) {
			return err
		}
		p, err := tx.Q.CreateCoresearchProject(ctx, "Getting started with Aristotle")
		if err != nil {
			return err
		}
		if err = tx.Q.MarkConversationProject(ctx, p); err != nil {
			return err
		}
		chat, err := tx.Q.CreateCoresearchChat(ctx, db.CreateCoresearchChatParams{WorkspaceID: ws, ProjectID: p, OwnerID: user, Cli: "claude"})
		if err != nil {
			return err
		}
		c, err = tx.Q.CreateWelcomeConversation(ctx, db.CreateWelcomeConversationParams{AgentID: a.ID, ProjectID: p, ChatID: chat.ID})
		return err
	})
	if coresearchAnswer(w, r, err) {
		writeJSON(w, 200, map[string]any{"agent": a, "conversation": c})
	}
}

func (h *H) deleteCoresearchConversation(w http.ResponseWriter, r *http.Request) {
	err := h.inWS(r, func(tx *store.Tx, ws, who uuid.UUID) error {
		id, err := coresearchID(r, "conversation")
		if err != nil {
			return err
		}
		projectID, err := tx.Q.CoresearchConversationProject(r.Context(), id)
		if err != nil {
			return err
		}
		project, err := tx.Q.LockCoresearchProject(r.Context(), db.LockCoresearchProjectParams{WorkspaceID: ws, ID: projectID})
		if err != nil {
			return err
		}
		if project.CreatedBy != who {
			return pgx.ErrNoRows
		}
		conversation, err := tx.Q.GetCoresearchConversation(r.Context(), id)
		if err != nil || conversation.ProjectID != projectID {
			return pgx.ErrNoRows
		}
		busy, err := tx.Q.CoresearchProjectBusy(r.Context(), db.CoresearchProjectBusyParams{WorkspaceID: ws, ProjectID: projectID})
		if err != nil {
			return err
		}
		if busy {
			return conflict("project has queued or running work")
		}
		blobKeys, err := tx.Q.EnqueueCoresearchProjectBlobs(r.Context(), projectID)
		if err != nil {
			return err
		}
		n, err := tx.Q.DeleteOwnedCoresearchProject(r.Context(), projectID)
		if err != nil {
			return err
		}
		if n != 1 {
			return pgx.ErrNoRows
		}
		tx.AfterCommit(func() {
			ctx, cancel := context.WithTimeout(context.WithoutCancel(r.Context()), 10*time.Second)
			defer cancel()
			if cleanupErr := coresearch.CleanupBlobs(ctx, h.Store, h.Blobs, ws, blobKeys); cleanupErr != nil {
				slog.Error("Coresearch blob cleanup deferred", "err", cleanupErr)
			}
		})
		return tx.Emit(r.Context(), "coresearch.project.updated", nil, store.User(who), map[string]any{"project_id": projectID})
	})
	if coresearchAnswer(w, r, err) {
		w.WriteHeader(http.StatusNoContent)
	}
}
func (h *H) listCoresearchConversations(w http.ResponseWriter, r *http.Request) {
	type item struct {
		value   any
		updated time.Time
		id      uuid.UUID
	}
	items := []item{}
	out := []any{}
	err := h.inWS(r, func(tx *store.Tx, ws, who uuid.UUID) error {
		rows, e := tx.Q.ListCoresearchConversations(r.Context())
		if e != nil {
			return e
		}
		for _, row := range rows {
			items = append(items, item{row, row.UpdatedAt, row.ID})
		}
		shared, e := tx.Q.ListSharedCowriteSessions(r.Context())
		if e != nil {
			return e
		}
		for _, row := range shared {
			view := map[string]any{"shared": true, "project_id": row.ProjectID, "chat_id": row.ChatID, "title": row.Title, "mode": row.Mode, "updated_at": row.UpdatedAt}
			items = append(items, item{view, row.UpdatedAt, row.ProjectID})
		}
		sort.SliceStable(items, func(i, j int) bool {
			if items[i].updated.Equal(items[j].updated) {
				return items[i].id.String() < items[j].id.String()
			}
			return items[i].updated.After(items[j].updated)
		})
		for _, item := range items {
			out = append(out, item.value)
		}
		return nil
	})
	if coresearchAnswer(w, r, err) {
		writeJSON(w, 200, out)
	}
}
