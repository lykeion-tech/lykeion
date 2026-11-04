package handler

import (
	"encoding/json"
	"errors"
	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"lykeion/server/internal/handin"
	"lykeion/server/internal/labmemory"
	"lykeion/server/internal/middleware"
	"lykeion/server/pkg/agent"
	db "lykeion/server/pkg/db/generated"
	"net/http"
	"strings"
	"unicode/utf8"
)

func (h *H) getManualDraft(w http.ResponseWriter, r *http.Request) {
	tx := middleware.TxFrom(r.Context())
	chat, e := privateLabChat(r, tx)
	if e != nil {
		fail(w, r, e)
		return
	}
	ok, e := labmemory.DraftReadable(r.Context(), tx, chat)
	if e != nil {
		fail(w, r, e)
		return
	}
	if !ok {
		fail(w, r, pgx.ErrNoRows)
		return
	}
	writeJSON(w, 200, map[string]any{"chat_id": chat.ID, "purpose": chat.Purpose, "draft": chat.Draft})
}
func (h *H) saveManualDraft(w http.ResponseWriter, r *http.Request) {
	tx := middleware.TxFrom(r.Context())
	chat, e := privateLabChat(r, tx)
	if e != nil {
		fail(w, r, e)
		return
	}
	var d handin.ManualDraft
	if e = readJSON(r, &d); e != nil || len(handin.ValidateManualDraft(d, true)) > 0 {
		writeErr(w, 400, "invalid draft")
		return
	}
	chat, e = labmemory.SaveDraft(r.Context(), tx, chat.ID, d)
	if errors.Is(e, labmemory.ErrNotFound) {
		e = pgx.ErrNoRows
	}
	if e != nil {
		fail(w, r, e)
		return
	}
	writeJSON(w, 200, map[string]any{"chat_id": chat.ID, "purpose": chat.Purpose, "draft": chat.Draft})
}

func (h *H) askManualDraft(w http.ResponseWriter, r *http.Request, chat db.LabChat) {
	ctx := r.Context()
	tx := middleware.TxFrom(ctx)
	var in struct {
		UserRequest  string              `json:"user_request"`
		CurrentDraft *handin.ManualDraft `json:"current_draft"`
		Harness      string              `json:"harness"`
		Model        string              `json:"model"`
		RequestID    uuid.UUID           `json:"request_id"`
	}
	if e := readJSON(r, &in); e != nil || strings.TrimSpace(in.UserRequest) == "" || utf8.RuneCountInString(in.UserRequest) > 8000 || in.CurrentDraft == nil || in.RequestID == uuid.Nil || len(handin.ValidateManualDraft(*in.CurrentDraft, true)) > 0 {
		writeErr(w, 400, "invalid drafting turn")
		return
	}
	if _, e := tx.Q.LockWorkspaceExecution(ctx, tx.WS); e != nil {
		fail(w, r, e)
		return
	}
	chat, e := tx.Q.GetPrivateLabChat(ctx, chat.ID)
	if e != nil {
		fail(w, r, e)
		return
	}
	readable, e := labmemory.DraftReadable(ctx, tx, chat)
	if e != nil {
		fail(w, r, e)
		return
	}
	if !readable {
		fail(w, r, pgx.ErrNoRows)
		return
	}
	card, e := labmemory.DraftStudy(ctx, tx, user(r), *in.CurrentDraft)
	if e != nil {
		writeErr(w, 400, "context unavailable")
		return
	}
	raw, _ := json.Marshal(labmemory.DraftTurn{UserRequest: strings.TrimSpace(in.UserRequest), CurrentDraft: *in.CurrentDraft})
	old, e := tx.Q.GetManualDraftRequest(ctx, db.GetManualDraftRequestParams{ChatID: chat.ID, RequestID: &in.RequestID})
	if e == nil {
		// The original receipt freezes the request; retrying a key cannot change it.
		if old.Body != string(raw) {
			writeErr(w, 409, "request already used")
			return
		}
		writeJSON(w, 202, map[string]any{"message": labDraftMessage(old)})
		return
	}
	if !errors.Is(e, pgx.ErrNoRows) {
		fail(w, r, e)
		return
	}
	if in.Harness != "claude" && in.Harness != "codex" {
		writeErr(w, 400, "select a supported CLI")
		return
	}
	if e = agent.LabAvailability(in.Harness); e != nil {
		writeJSON(w, 503, map[string]string{"code": "lab_unavailable", "error": e.Error()})
		return
	}
	available, e := tx.Q.AristotleRuntimeAvailable(ctx, db.AristotleRuntimeAvailableParams{Harness: in.Harness, RequireHealthy: true})
	if e != nil {
		fail(w, r, e)
		return
	}
	if !available {
		writeJSON(w, 409, map[string]string{"code": "no_machine", "error": "Your machine is offline"})
		return
	}
	if in.Model != "" {
		ok, e := tx.Q.AristotleRuntimeModelAvailable(ctx, db.AristotleRuntimeModelAvailableParams{Harness: in.Harness, Model: in.Model})
		if e != nil {
			fail(w, r, e)
			return
		}
		if !ok {
			writeErr(w, 400, "model is not offered by an own runtime")
			return
		}
	}
	if _, e = labmemory.SaveDraft(ctx, tx, chat.ID, *in.CurrentDraft); e != nil {
		fail(w, r, e)
		return
	}
	run, e := labmemory.EnqueueWithContext(ctx, tx, h.AgentRuns, chat, user(r), string(raw), card, nil, in.Harness, in.Model)
	if e != nil {
		fail(w, r, e)
		return
	}
	if e = tx.Q.SetManualDraftRequest(ctx, db.SetManualDraftRequestParams{RunID: run.ID, RequestID: in.RequestID}); e != nil {
		fail(w, r, e)
		return
	}
	msg, e := tx.Q.GetLabQuestion(ctx, run.ID)
	if e != nil {
		fail(w, r, e)
		return
	}
	writeJSON(w, 202, map[string]any{"message": labDraftMessage(msg)})
}

// Cancellation retains the private question and form. It never restores text
// into the composer or changes another turn in the conversation.
func (h *H) cancelManualDraftQuestion(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	tx := middleware.TxFrom(ctx)
	if _, err := tx.Q.LockWorkspaceExecution(ctx, tx.WS); err != nil {
		fail(w, r, err)
		return
	}
	chat, err := privateLabChat(r, tx)
	if err != nil {
		fail(w, r, err)
		return
	}
	if chat.Purpose != labmemory.ManualDraftPurpose {
		fail(w, r, pgx.ErrNoRows)
		return
	}
	id, err := uuid.Parse(chi.URLParam(r, "question"))
	if err != nil {
		fail(w, r, pgx.ErrNoRows)
		return
	}
	question, err := tx.Q.GetPrivateManualDraftQuestion(ctx, db.GetPrivateManualDraftQuestionParams{ChatID: chat.ID, ID: id})
	if err != nil {
		fail(w, r, err)
		return
	}
	if question.AgentRunID == nil {
		fail(w, r, pgx.ErrNoRows)
		return
	}
	run, err := tx.Q.LockAgentRun(ctx, db.LockAgentRunParams{WorkspaceID: tx.WS, ID: *question.AgentRunID})
	if err != nil {
		fail(w, r, err)
		return
	}
	if run.Kind != "aristotle" || run.LabPurpose != labmemory.ManualDraftPurpose || run.LabChatID == nil || *run.LabChatID != chat.ID || run.RequesterID == nil || *run.RequesterID != user(r) || run.ResponsibleUserID != user(r) {
		fail(w, r, pgx.ErrNoRows)
		return
	}
	switch run.Status {
	case "completed", "failed", "cancelled":
		w.WriteHeader(http.StatusNoContent)
		return
	}
	if err = h.AgentRuns.CancelInTx(ctx, tx, user(r), run); err != nil {
		fail(w, r, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}
