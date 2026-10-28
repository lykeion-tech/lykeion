package handler

import (
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"unicode/utf8"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"lykeion/server/internal/coresearch"
	"lykeion/server/internal/store"
	db "lykeion/server/pkg/db/generated"
)

// A receipt is saved with acceptance, so a lost response can be recovered even
// after the machine goes offline or the member edits their Aristotle settings.
func (h *H) acceptAristotleTurn(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Body      string    `json:"body"`
		RequestID uuid.UUID `json:"request_id"`
	}
	if err := readJSON(r, &in); err != nil || in.RequestID == uuid.Nil || !utf8.ValidString(in.Body) || strings.TrimSpace(in.Body) == "" || len(in.Body) > 262144 {
		writeErr(w, 400, "body and request_id UUID required")
		return
	}
	target := chi.URLParam(r, "conversation")
	var response json.RawMessage
	err := h.inWS(r, func(tx *store.Tx, ws, who uuid.UUID) error {
		ctx := r.Context()
		receipt, e := tx.Q.GetCoresearchTurnReceipt(ctx, in.RequestID)
		if e == nil {
			if receipt.Target != target || receipt.Body != in.Body {
				return conflict("request_id already accepted with a different payload")
			}
			response = receipt.Response
			return nil
		}
		if !errors.Is(e, pgx.ErrNoRows) {
			return e
		}
		var c db.CoresearchConversation
		var a db.Agent
		if target != "" {
			id, e := uuid.Parse(target)
			if e != nil {
				return cardInputError("valid conversation required")
			}
			c, e = tx.Q.GetCoresearchConversation(ctx, id)
			if e != nil {
				return e
			}
		}
		if c.SnapshotHarness == nil {
			if e = tx.Q.LockAristotleOwner(ctx); e != nil {
				return e
			}
			a, e = tx.Q.EnsureAristotle(ctx)
			if e != nil {
				return e
			}
			if target != "" && c.AgentID != a.ID {
				return pgx.ErrNoRows
			}
		}
		harness, model, instructions := a.Harness, a.Model, a.Instructions
		if c.SnapshotHarness != nil {
			harness, model, instructions = *c.SnapshotHarness, *c.SnapshotModel, *c.SnapshotInstructions
		}
		var chat db.CoresearchChat
		if target != "" {
			// Preserve project-before-chat lock order used by legacy execution paths.
			p, e := tx.Q.LockCoresearchProject(ctx, db.LockCoresearchProjectParams{WorkspaceID: ws, ID: c.ProjectID})
			if e != nil {
				return e
			}
			if p.ArchivedAt != nil {
				return coresearch.ErrArchived
			}
			chat, e = tx.Q.LockCoresearchChat(ctx, db.LockCoresearchChatParams{WorkspaceID: ws, ID: c.ChatID})
			if e != nil {
				return e
			}
			if chat.OwnerID != who || chat.ProjectID != c.ProjectID {
				return pgx.ErrNoRows
			}
		}
		available, e := tx.Q.AristotleRuntimeAvailable(ctx, db.AristotleRuntimeAvailableParams{Harness: harness, DaemonID: chat.DaemonID})
		if e != nil {
			return e
		}
		if !available {
			return conflict("No available own machine with " + harness + "; connect a machine in Settings")
		}
		if target == "" {
			titleRunes := []rune(strings.TrimSpace(in.Body))
			title := string(titleRunes[:min(80, len(titleRunes))])
			p, e := tx.Q.CreateCoresearchProject(ctx, title)
			if e != nil {
				return e
			}
			if e = tx.Q.MarkConversationProject(ctx, p); e != nil {
				return e
			}
			chat, e = tx.Q.CreateCoresearchChat(ctx, db.CreateCoresearchChatParams{WorkspaceID: ws, ProjectID: p, OwnerID: who, Cli: harness})
			if e != nil {
				return e
			}
			c, e = tx.Q.CreateCoresearchConversation(ctx, db.CreateCoresearchConversationParams{AgentID: a.ID, ProjectID: p, ChatID: chat.ID, Title: title})
			if e != nil {
				return e
			}
		}
		if c.SnapshotHarness == nil {
			c, e = tx.Q.SnapshotCoresearchConversation(ctx, db.SnapshotCoresearchConversationParams{ID: c.ID, Harness: &harness, Model: &model, Instructions: &instructions})
			if e != nil {
				return e
			}
			if e = tx.Q.SetConversationChatHarness(ctx, db.SetConversationChatHarnessParams{ID: chat.ID, Harness: harness}); e != nil {
				return e
			}
			chat.Cli = harness
		}
		run, e := coresearch.EnqueueAristotleTurn(ctx, tx, h.AgentRuns, chat, who, in.Body, instructions, model)
		if errors.Is(e, coresearch.ErrTurnRunning) {
			return conflict(e.Error())
		}
		if e != nil {
			return e
		}
		if e = tx.Q.TouchCoresearchConversation(ctx, c.ID); e != nil {
			return e
		}
		c, e = tx.Q.GetCoresearchConversation(ctx, c.ID)
		if e != nil {
			return e
		}
		response, e = json.Marshal(map[string]any{"conversation": c, "run": run})
		if e != nil {
			return e
		}
		return tx.Q.InsertCoresearchTurnReceipt(ctx, db.InsertCoresearchTurnReceiptParams{RequestID: in.RequestID, Target: target, Body: in.Body, Response: response})
	})
	if coresearchAnswer(w, r, err) {
		writeJSON(w, 201, response)
	}
}
