package handler

import (
	"bytes"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"lykeion/server/internal/labmemory"
	"lykeion/server/internal/store"
	db "lykeion/server/pkg/db/generated"
)

type manualDecisionInput struct {
	Title       string     `json:"title"`
	Context     string     `json:"context"`
	Deadline    *string    `json:"deadline"`
	CardID      *uuid.UUID `json:"card_id"`
	DraftChatID *uuid.UUID `json:"draft_chat_id,omitempty"`
}

var errManualClosed = errors.New("manual decision is closed")
var errDraftReceiptConflict = errors.New("draft already used for another decision")

func parseManualDecision(r *http.Request) (manualDecisionInput, pgtype.Date, error) {
	var in manualDecisionInput
	var date pgtype.Date
	if err := readJSON(r, &in); err != nil {
		return in, date, err
	}
	if strings.TrimSpace(in.Title) == "" || utf8.RuneCountInString(in.Title) > 200 || strings.ContainsAny(in.Title, "\r\n") || utf8.RuneCountInString(in.Context) > 4000 {
		return in, date, errors.New("invalid title or context")
	}
	if in.Deadline != nil {
		d, err := time.Parse(time.DateOnly, *in.Deadline)
		if err != nil {
			return in, date, err
		}
		date = pgtype.Date{Time: d, Valid: true}
	}
	return in, date, nil
}
func (h *H) createManualDecision(w http.ResponseWriter, r *http.Request) {
	h.saveManualDecision(w, r, true)
}
func (h *H) editManualDecision(w http.ResponseWriter, r *http.Request) {
	h.saveManualDecision(w, r, false)
}
func (h *H) saveManualDecision(w http.ResponseWriter, r *http.Request, create bool) {
	in, date, err := parseManualDecision(r)
	if err != nil {
		writeErr(w, 400, "invalid request body")
		return
	}
	if !create && in.DraftChatID != nil {
		writeErr(w, 400, "draft is only accepted when creating")
		return
	}
	encoded, err := json.Marshal(in)
	if err != nil {
		fail(w, r, err)
		return
	}
	hash := sha256.Sum256(encoded)
	var id uuid.UUID
	if !create {
		var ok bool
		id, ok = cardID(w, r)
		if !ok {
			return
		}
	}
	var out db.InboxItem
	err = h.inWS(r, func(tx *store.Tx, ws, user uuid.UUID) error {
		if !create {
			row, err := tx.Q.GetDecision(r.Context(), db.GetDecisionParams{ID: id, WorkspaceID: ws, Viewer: user})
			if err != nil {
				return err
			}
			if row.InboxItem.Kind != "manual_decision" || row.InboxItem.UserID != user {
				return pgx.ErrNoRows
			}
		}
		// Lock the target study before the note, matching study mutation lock order.
		if in.CardID != nil {
			if _, err := tx.Q.LockCard(r.Context(), db.LockCardParams{ID: *in.CardID, WorkspaceID: ws}); err != nil {
				return err
			}
		}
		if create && in.DraftChatID != nil {
			if err := tx.LockDecisionDraft(r.Context(), *in.DraftChatID, user); err != nil {
				return err
			}
			chat, e := tx.Q.GetPrivateLabChat(r.Context(), *in.DraftChatID)
			if e != nil {
				return e
			}
			readable, e := labmemory.DraftReadable(r.Context(), tx, chat)
			if e != nil {
				return e
			}
			if !readable {
				return pgx.ErrNoRows
			}
			decision, previous, e := tx.DecisionDraftReceipt(r.Context(), *in.DraftChatID, user)
			if e == nil {
				if !bytes.Equal(previous, hash[:]) {
					return errDraftReceiptConflict
				}
				row, e := tx.Q.GetDecision(r.Context(), db.GetDecisionParams{ID: decision, WorkspaceID: ws, Viewer: user})
				if e != nil {
					return e
				}
				out = row.InboxItem
				return nil
			}
			if !errors.Is(e, pgx.ErrNoRows) {
				return e
			}
			if chat.ArchivedAt != nil {
				return errDraftReceiptConflict
			}
		}
		if create {
			out, err = tx.Q.InsertManualDecision(r.Context(), db.InsertManualDecisionParams{WorkspaceID: ws, UserID: user, Title: in.Title, Context: in.Context, Deadline: date, CardID: in.CardID})
		} else {
			item, e := tx.Q.LockDecision(r.Context(), db.LockDecisionParams{ID: id, WorkspaceID: ws})
			if e != nil {
				return e
			}
			if item.Kind != "manual_decision" || item.UserID != user {
				return pgx.ErrNoRows
			}
			if item.Status != "open" || item.ResolvedAt != nil {
				return errManualClosed
			}
			out, err = tx.Q.EditManualDecision(r.Context(), db.EditManualDecisionParams{ID: id, WorkspaceID: ws, UserID: user, Title: in.Title, Context: in.Context, Deadline: date, CardID: in.CardID})
		}
		if err != nil {
			return err
		}
		if create && in.DraftChatID != nil {
			if err := tx.InsertDecisionDraftReceipt(r.Context(), *in.DraftChatID, user, out.ID, hash[:]); err != nil {
				return err
			}
		}
		typ := "inbox.updated"
		if create {
			typ = "inbox.created"
		}
		return tx.EmitPrivate(r.Context(), user, typ, store.User(user), map[string]string{"inbox_item_id": out.ID.String()})
	})
	if errors.Is(err, errManualClosed) || errors.Is(err, errDraftReceiptConflict) {
		writeErr(w, 409, err.Error())
		return
	}
	if err != nil {
		fail(w, r, err)
		return
	}
	status := http.StatusOK
	if create {
		status = http.StatusCreated
	}
	writeJSON(w, status, viewDecision(out, false))
}
func (h *H) dismissManualDecision(w http.ResponseWriter, r *http.Request) {
	h.closeManualDecision(w, r, "dismissed")
}
func (h *H) closeManualDecision(w http.ResponseWriter, r *http.Request, status string) {
	id, ok := cardID(w, r)
	if !ok {
		return
	}
	// Closing a note never accepts an answer or an outcome.
	var body struct{}
	if err := readJSON(r, &body); err != nil && !errors.Is(err, io.EOF) {
		writeErr(w, 400, "invalid request body")
		return
	}
	var out db.InboxItem
	err := h.inWS(r, func(tx *store.Tx, ws, user uuid.UUID) error {
		item, err := tx.Q.LockDecision(r.Context(), db.LockDecisionParams{ID: id, WorkspaceID: ws})
		if err != nil {
			return err
		}
		if item.Kind != "manual_decision" || item.UserID != user {
			return pgx.ErrNoRows
		}
		if item.Status != "open" || item.ResolvedAt != nil {
			return errManualClosed
		}
		out, err = tx.Q.CloseManualDecision(r.Context(), db.CloseManualDecisionParams{ID: id, WorkspaceID: ws, UserID: &user, Status: status})
		if err != nil {
			return err
		}
		return tx.EmitPrivate(r.Context(), user, "inbox.resolved", store.User(user), map[string]string{"inbox_item_id": id.String()})
	})
	if errors.Is(err, errManualClosed) {
		writeErr(w, 409, err.Error())
		return
	}
	if err != nil {
		fail(w, r, err)
		return
	}
	writeJSON(w, 200, viewDecision(out, false))
}
