package handler

import (
	"errors"
	"net/http"
	"strconv"
	"strings"
	"unicode/utf8"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"lykeion/server/internal/coresearch"
	"lykeion/server/internal/middleware"
	"lykeion/server/internal/store"
	db "lykeion/server/pkg/db/generated"
)

func coresearchChat(r *http.Request, tx *store.Tx, ws, who uuid.UUID, write, owner bool) (db.CoresearchChat, error) {
	var c db.CoresearchChat
	p, e := coresearchProject(r, tx, ws, who, write, false)
	if e != nil {
		return c, e
	}
	id, e := coresearchID(r, "chat")
	if e != nil {
		return c, e
	}
	if write {
		c, e = tx.Q.LockCoresearchChat(r.Context(), db.LockCoresearchChatParams{WorkspaceID: ws, ID: id})
	} else {
		c, e = tx.Q.GetCoresearchChat(r.Context(), db.GetCoresearchChatParams{WorkspaceID: ws, ProjectID: p.ID, ID: id})
	}
	if e != nil {
		return c, e
	}
	if c.ProjectID != p.ID {
		return c, pgx.ErrNoRows
	}
	if owner && c.OwnerID != who {
		return c, middleware.ErrForbidden
	}
	return c, nil
}
func (h *H) listCoresearchChats(w http.ResponseWriter, r *http.Request) {
	out := []db.ListCoresearchChatsRow{}
	e := h.inWS(r, func(tx *store.Tx, ws, who uuid.UUID) error {
		p, e := coresearchProject(r, tx, ws, who, false, false)
		if e != nil {
			return e
		}
		out, e = tx.Q.ListCoresearchChats(r.Context(), db.ListCoresearchChatsParams{WorkspaceID: ws, ProjectID: p.ID})
		return e
	})
	if coresearchAnswer(w, r, e) {
		if out == nil {
			out = []db.ListCoresearchChatsRow{}
		}
		writeJSON(w, 200, out)
	}
}
func (h *H) createCoresearchChat(w http.ResponseWriter, r *http.Request) {
	var out db.CoresearchChat
	e := h.inWS(r, func(tx *store.Tx, ws, who uuid.UUID) error {
		p, e := coresearchProject(r, tx, ws, who, true, false)
		if e != nil {
			return e
		}
		var in struct {
			Cli string `json:"cli"`
		}
		if e = readJSON(r, &in); e != nil || (in.Cli != "claude" && in.Cli != "codex") {
			return cardInputError("cli must be claude or codex")
		}
		out, e = tx.Q.CreateCoresearchChat(r.Context(), db.CreateCoresearchChatParams{WorkspaceID: ws, ProjectID: p.ID, OwnerID: who, Cli: in.Cli})
		if e != nil {
			return e
		}
		return tx.Emit(r.Context(), "coresearch.chat.created", nil, store.User(who), map[string]any{"project_id": p.ID, "chat_id": out.ID})
	})
	if coresearchAnswer(w, r, e) {
		writeJSON(w, 201, out)
	}
}
func (h *H) coresearchMessages(w http.ResponseWriter, r *http.Request) {
	out := []db.ListCoresearchMessagesRow{}
	e := h.inWS(r, func(tx *store.Tx, ws, who uuid.UUID) error {
		c, e := coresearchChat(r, tx, ws, who, false, false)
		if e != nil {
			return e
		}
		out, e = tx.Q.ListCoresearchMessages(r.Context(), db.ListCoresearchMessagesParams{WorkspaceID: ws, ChatID: c.ID})
		return e
	})
	if coresearchAnswer(w, r, e) {
		if out == nil {
			out = []db.ListCoresearchMessagesRow{}
		}
		writeJSON(w, 200, out)
	}
}
func coresearchBody(r *http.Request) (string, error) {
	var in struct {
		Body string `json:"body"`
	}
	if e := readJSON(r, &in); e != nil || !utf8.ValidString(in.Body) || strings.TrimSpace(in.Body) == "" || len(in.Body) > 262144 {
		return "", cardInputError("body must contain between 1 and 262144 bytes")
	}
	return in.Body, nil
}
func (h *H) coresearchTurn(w http.ResponseWriter, r *http.Request) {
	var out db.AgentRun
	e := h.inWS(r, func(tx *store.Tx, ws, who uuid.UUID) error {
		c, e := coresearchChat(r, tx, ws, who, true, true)
		if e != nil {
			return e
		}
		body, e := coresearchBody(r)
		if e != nil {
			return e
		}
		out, e = coresearch.EnqueueTurn(r.Context(), tx, h.AgentRuns, c, who, body)
		if errors.Is(e, coresearch.ErrTurnRunning) {
			return conflict(e.Error())
		}
		return e
	})
	if coresearchAnswer(w, r, e) {
		writeJSON(w, 201, out)
	}
}
func (h *H) coresearchComment(w http.ResponseWriter, r *http.Request) {
	var out db.CoresearchMessage
	e := h.inWS(r, func(tx *store.Tx, ws, who uuid.UUID) error {
		c, e := coresearchChat(r, tx, ws, who, true, false)
		if e != nil {
			return e
		}
		body, e := coresearchBody(r)
		if e != nil {
			return e
		}
		out, e = tx.Q.InsertCoresearchMessage(r.Context(), db.InsertCoresearchMessageParams{WorkspaceID: ws, ChatID: c.ID, Kind: "comment", AuthorUserID: &who, Body: body})
		if e != nil {
			return e
		}
		return tx.Emit(r.Context(), "coresearch.message.created", nil, store.User(who), map[string]any{"project_id": c.ProjectID, "chat_id": c.ID, "message_id": out.ID})
	})
	if coresearchAnswer(w, r, e) {
		writeJSON(w, 201, out)
	}
}
func coresearchTurnRun(r *http.Request, tx *store.Tx, ws uuid.UUID, c db.CoresearchChat) (db.AgentRun, error) {
	var out db.AgentRun
	id, e := coresearchID(r, "run")
	if e != nil {
		return out, e
	}
	out, e = tx.Q.GetAgentRun(r.Context(), db.GetAgentRunParams{WorkspaceID: ws, ID: id})
	if e == nil && (out.Kind != "coresearch" || out.CoresearchChatID == nil || *out.CoresearchChatID != c.ID) {
		e = pgx.ErrNoRows
	}
	return out, e
}
func (h *H) coresearchSteps(w http.ResponseWriter, r *http.Request) {
	out := []db.AgentRunMessage{}
	e := h.inWS(r, func(tx *store.Tx, ws, who uuid.UUID) error {
		c, e := coresearchChat(r, tx, ws, who, false, false)
		if e != nil {
			return e
		}
		run, e := coresearchTurnRun(r, tx, ws, c)
		if e != nil {
			return e
		}
		attempt := run.Attempt
		after := int64(0)
		if raw := r.URL.Query().Get("attempt"); raw != "" {
			n, e := strconv.ParseInt(raw, 10, 32)
			if e != nil || n < 1 || n > int64(run.Attempt) {
				return cardInputError("valid attempt required")
			}
			attempt = int32(n)
		}
		if raw := r.URL.Query().Get("after"); raw != "" {
			after, e = strconv.ParseInt(raw, 10, 32)
			if e != nil || after < 0 {
				return cardInputError("valid after required")
			}
		}
		out, e = tx.Q.AgentRunMessagesAfter(r.Context(), db.AgentRunMessagesAfterParams{WorkspaceID: ws, AgentRunID: run.ID, Attempt: attempt, After: int32(after)})
		return e
	})
	if coresearchAnswer(w, r, e) {
		if out == nil {
			out = []db.AgentRunMessage{}
		}
		writeJSON(w, 200, out)
	}
}
func (h *H) cancelCoresearchTurn(w http.ResponseWriter, r *http.Request) {
	e := h.inWS(r, func(tx *store.Tx, ws, who uuid.UUID) error {
		c, e := coresearchChat(r, tx, ws, who, true, true)
		if e != nil {
			return e
		}
		run, e := coresearchTurnRun(r, tx, ws, c)
		if e != nil {
			return e
		}
		return h.AgentRuns.CancelInTx(r.Context(), tx, who, run)
	})
	if coresearchAnswer(w, r, e) {
		w.WriteHeader(204)
	}
}
func (h *H) releaseCoresearchChat(w http.ResponseWriter, r *http.Request) {
	e := h.inWS(r, func(tx *store.Tx, ws, who uuid.UUID) error {
		c, e := coresearchChat(r, tx, ws, who, true, true)
		if e != nil {
			return e
		}
		if e := coresearch.Expire(r.Context(), tx); e != nil {
			return e
		}
		busy, e := tx.Q.CoresearchChatBusy(r.Context(), db.CoresearchChatBusyParams{WorkspaceID: ws, ChatID: c.ID})
		if e != nil {
			return e
		}
		if busy {
			return conflict("work is still running in this chat")
		}
		if e = tx.Q.ReleaseCoresearchChat(r.Context(), db.ReleaseCoresearchChatParams{WorkspaceID: ws, ID: c.ID}); e != nil {
			return e
		}
		return tx.Emit(r.Context(), "coresearch.chat.updated", nil, store.User(who), map[string]any{"project_id": c.ProjectID, "chat_id": c.ID})
	})
	if coresearchAnswer(w, r, e) {
		w.WriteHeader(204)
	}
}
