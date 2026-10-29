package handler

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"unicode/utf8"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"lykeion/server/internal/coresearch"
	"lykeion/server/internal/coresearch/doc"
	"lykeion/server/internal/store"
	db "lykeion/server/pkg/db/generated"
)

type acceptancePayload struct {
	Body, Mode, Template string
	Model                *string
	Attachments          []coresearchAttachment
	Selection            *coresearch.Selection `json:"Selection,omitempty"`
	Queue                bool                  `json:",omitempty"`
}

func sameAcceptancePayload(stored, want string) bool {
	var payload acceptancePayload
	if json.Unmarshal([]byte(stored), &payload) != nil {
		return false
	}
	body, _ := json.Marshal(payload)
	return string(body) == want
}

// A receipt is saved with acceptance, so a lost response can be recovered even
// after the machine goes offline or the member edits their Aristotle settings.
func (h *H) acceptAristotleTurn(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Body        string                 `json:"body"`
		RequestID   uuid.UUID              `json:"request_id"`
		Mode        string                 `json:"mode,omitempty"`
		Template    string                 `json:"template,omitempty"`
		Model       *string                `json:"model,omitempty"`
		Attachments []coresearchAttachment `json:"attachments,omitempty"`
		Selection   *coresearch.Selection  `json:"selection,omitempty"`
		// Queue lets a Cowrite turn wait behind the one in progress.
		Queue bool `json:"queue,omitempty"`
	}
	if err := readJSON(r, &in); err != nil || in.RequestID == uuid.Nil || !utf8.ValidString(in.Body) || strings.TrimSpace(in.Body) == "" || len(in.Body) > 262144 {
		writeErr(w, 400, "body and request_id UUID required")
		return
	}
	target := chi.URLParam(r, "conversation")
	ws, err := uuid.Parse(r.Header.Get("X-Workspace-ID"))
	if err != nil {
		writeErr(w, 404, "workspace not found")
		return
	}
	who := user(r)
	if len(in.Attachments) == 0 {
		in.Attachments = nil
	}
	payload, _ := json.Marshal(acceptancePayload{in.Body, in.Mode, in.Template, in.Model, in.Attachments, in.Selection, in.Queue})
	receiptBody := string(payload)
	var response json.RawMessage
	// Staging may outlive another identical request's upload claim. Only a
	// committed owner-scoped receipt with the full same payload recovers it.
	replayAfterError := func(original error) error {
		recovered := false
		lookupErr := h.memberTx(r, ws, false, func(tx *store.Tx) error {
			receipt, e := tx.Q.GetCoresearchTurnReceipt(r.Context(), in.RequestID)
			if errors.Is(e, pgx.ErrNoRows) {
				return nil
			}
			if e != nil {
				return e
			}
			if receipt.Target != target || !sameAcceptancePayload(receipt.Body, receiptBody) {
				return conflict("request_id already accepted with a different payload")
			}
			response = receipt.Response
			recovered = true
			return nil
		})
		if lookupErr != nil {
			return lookupErr
		}
		if recovered {
			return nil
		}
		return original
	}
	var existing bool
	mode := in.Mode
	if mode == "" {
		mode = "experiment"
	}
	err = h.memberTx(r, ws, false, func(tx *store.Tx) error {
		receipt, e := tx.Q.GetCoresearchTurnReceipt(r.Context(), in.RequestID)
		if e == nil {
			if receipt.Target != target || !sameAcceptancePayload(receipt.Body, receiptBody) {
				return conflict("request_id already accepted with a different payload")
			}
			response = receipt.Response
			existing = true
			return nil
		}
		if !errors.Is(e, pgx.ErrNoRows) {
			return e
		}
		if target != "" {
			id, e := uuid.Parse(target)
			if e != nil {
				return pgx.ErrNoRows
			}
			c, e := tx.Q.GetCoresearchConversation(r.Context(), id)
			if e != nil {
				return e
			}
			p, e := tx.Q.GetCoresearchProject(r.Context(), db.GetCoresearchProjectParams{WorkspaceID: ws, ID: c.ProjectID})
			if e != nil {
				return e
			}
			mode = p.Mode
			if in.Mode != "" && in.Mode != mode {
				return coresearchInputError("session mode is immutable")
			}
		}
		return nil
	})
	if !coresearchAnswer(w, r, err) {
		return
	}
	if existing {
		writeJSON(w, 201, response)
		return
	}
	if mode != "experiment" && mode != "cowrite" {
		writeErr(w, 422, "mode must be experiment or cowrite")
		return
	}
	if in.Queue && mode != "cowrite" {
		writeErr(w, 422, "queue is only valid in Cowrite")
		return
	}
	if in.Template != "" && (mode != "cowrite" || target != "") {
		writeErr(w, 422, "template is only valid when starting Cowrite")
		return
	}
	template := in.Template
	if template == "" {
		template = "blank"
	}
	uploads := []coresearch.UploadBlob{}
	if target == "" && mode == "cowrite" {
		uploads, err = coresearch.ScaffoldBlobs(template)
		if err != nil {
			writeErr(w, 422, err.Error())
			return
		}
	}
	attachments, staged, textHeads, err := h.stageCoresearchAttachments(r, ws, target, mode, in.Attachments)
	if err != nil {
		if coresearchAnswer(w, r, replayAfterError(err)) {
			writeJSON(w, 201, response)
		}
		return
	}
	uploads = append(uploads, staged...)
	save := func() error {
		return h.coresearchSaveTx(r, ws, func(tx *store.Tx) error {
			ctx := r.Context()
			receipt, e := tx.Q.GetCoresearchTurnReceipt(ctx, in.RequestID)
			if e == nil {
				if receipt.Target != target || !sameAcceptancePayload(receipt.Body, receiptBody) {
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
			if c.SnapshotHarness == nil && in.Model != nil {
				model = *in.Model
				if model != "" {
					valid, e := tx.Q.AristotleRuntimeModelAvailable(ctx, db.AristotleRuntimeModelAvailableParams{Harness: harness, Model: model})
					if e != nil {
						return e
					}
					if !valid {
						return coresearchInputError("model is not offered by an own runtime")
					}
				}
			}
			if c.SnapshotHarness != nil {
				harness, model, instructions = *c.SnapshotHarness, *c.SnapshotModel, *c.SnapshotInstructions
				if in.Model != nil && *in.Model != model {
					return coresearchInputError("conversation model is frozen")
				}
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
				if mode == "experiment" {
					if e = tx.Q.MarkConversationProject(ctx, p); e != nil {
						return e
					}
				} else {
					if e = tx.Q.SetCoresearchProjectMode(ctx, db.SetCoresearchProjectModeParams{ID: p, Mode: mode}); e != nil {
						return e
					}
					if e = coresearch.Scaffold(ctx, tx, h.Store, h.Blobs, ws, p, who, template); e != nil {
						return e
					}
				}
				chat, e = tx.Q.CreateCoresearchChat(ctx, db.CreateCoresearchChatParams{WorkspaceID: ws, ProjectID: p, OwnerID: who, Cli: harness, Mode: mode})
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
			byID := map[uuid.UUID]db.Attachment{}
			for _, a := range attachments {
				byID[a.ID] = a
			}
			for _, a := range in.Attachments {
				row := byID[a.UploadID]
				n, e := tx.Q.ClaimCoresearchUpload(ctx, db.ClaimCoresearchUploadParams{ID: row.ID, Sha256: row.Sha256, BlobKey: row.BlobKey})
				if e != nil {
					return e
				}
				if n != 1 {
					return conflict("upload no longer available")
				}
				path, _ := coresearch.AttachPath(mode, a.Name)
				if _, e = coresearch.SaveAttachment(ctx, tx, coresearch.SaveIn{Workspace: ws, Project: chat.ProjectID, Author: &who, Path: path, Sha256: row.Sha256, Size: row.SizeBytes}, textHeads); e != nil {
					return e
				}
			}
			run, e := coresearch.EnqueueAristotleTurn(ctx, tx, h.AgentRuns, chat, who, in.Body, instructions, model, in.Queue, in.Selection)
			if errors.Is(e, doc.ErrInvalid) {
				return coresearchInputError("invalid selection version or UTF-16 bounds")
			}
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
			response, e = json.Marshal(map[string]any{"conversation": c, "run": run, "project_id": c.ProjectID, "chat_id": c.ChatID})
			if e != nil {
				return e
			}
			return tx.Q.InsertCoresearchTurnReceipt(ctx, db.InsertCoresearchTurnReceiptParams{RequestID: in.RequestID, Target: target, Body: receiptBody, Response: response})
		})
	}
	if len(uploads) > 0 {
		err = coresearch.WithBlobs(r.Context(), h.Store, h.Blobs, ws, who, uploads, save)
	} else {
		err = save()
	}
	if err != nil {
		err = replayAfterError(err)
	}
	if coresearchAnswer(w, r, err) {
		for _, row := range attachments {
			_ = h.Blobs.Delete(context.WithoutCancel(r.Context()), row.BlobKey)
		}
		writeJSON(w, 201, response)
	}
}
