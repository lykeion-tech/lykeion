package handler

import (
	"errors"
	"net/http"
	"slices"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"

	"lykeion/server/internal/flow"
	"lykeion/server/internal/middleware"
	"lykeion/server/internal/store"
	db "lykeion/server/pkg/db/generated"
)

// decisionView is an Inbox item as the dashboard and the CLI read it.
type decisionView struct {
	db.InboxItem
	CanAnswer bool `json:"can_answer"`
}

func viewDecision(item db.InboxItem, mayAnswer bool) decisionView {
	return decisionView{InboxItem: item, CanAnswer: mayAnswer && item.Status == "open" && item.ResolvedAt == nil && item.ArchivedAt == nil}
}

// listDecisions lists the open items (snoozed ones last), or with
// ?view=history everything else, newest first.
func (h *H) listDecisions(w http.ResponseWriter, r *http.Request) {
	history := r.URL.Query().Get("view") == "history"
	out := []decisionView{}
	err := h.inWS(r, func(tx *store.Tx, ws, user uuid.UUID) error {
		rows, err := tx.Q.ListDecisions(r.Context(), db.ListDecisionsParams{WorkspaceID: ws, Viewer: user, IsAdmin: agentAdmin(r), History: history})
		for _, row := range rows {
			out = append(out, viewDecision(row.InboxItem, row.MayAnswer))
		}
		return err
	})
	if err != nil {
		fail(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, out)
}

// countDecisions counts the open items the caller may answer now.
func (h *H) countDecisions(w http.ResponseWriter, r *http.Request) {
	var n int64
	err := h.inWS(r, func(tx *store.Tx, ws, user uuid.UUID) (err error) {
		n, err = tx.Q.CountDecisions(r.Context(), db.CountDecisionsParams{WorkspaceID: ws, Viewer: user, IsAdmin: agentAdmin(r)})
		return err
	})
	if err != nil {
		fail(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]int64{"open": n})
}

// readResolves are the Inbox kinds that only inform: reading one resolves it.
var readResolves = []string{"done", "middle_score", "no_tool", "autoresearch_card", "autoresearch_report"}

func (h *H) readDecision(w http.ResponseWriter, r *http.Request) {
	id, ok := cardID(w, r)
	if !ok {
		return
	}
	err := h.inWS(r, func(tx *store.Tx, ws, user uuid.UUID) error {
		item, err := tx.Q.MarkDecisionRead(r.Context(), db.MarkDecisionReadParams{ID: id, WorkspaceID: ws, UserID: user})
		if err != nil {
			return err
		}
		if err := tx.Emit(r.Context(), "inbox.read", nil, store.User(user), map[string]string{"inbox_item_id": id.String()}); err != nil {
			return err
		}
		if !slices.Contains(readResolves, item.Kind) {
			return nil
		}
		return tx.Emit(r.Context(), "inbox.resolved", item.CardID, store.User(user), map[string]string{"inbox_item_id": id.String()})
	})
	if err != nil {
		fail(w, r, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// manageDecision runs a triage write on an item the caller may organise: one
// they may answer, or one raised for them. An item they cannot see is 404.
func (h *H) manageDecision(w http.ResponseWriter, r *http.Request, write func(tx *store.Tx, ws, id uuid.UUID) error) {
	id, ok := cardID(w, r)
	if !ok {
		return
	}
	forbidden := false
	err := h.inWS(r, func(tx *store.Tx, ws, user uuid.UUID) error {
		row, err := tx.Q.GetDecision(r.Context(), db.GetDecisionParams{ID: id, WorkspaceID: ws, Viewer: user, IsAdmin: agentAdmin(r)})
		if err != nil {
			return err
		}
		if !row.MayAnswer && row.InboxItem.UserID != user {
			forbidden = true
			return nil
		}
		return write(tx, ws, id)
	})
	switch {
	case forbidden:
		writeErr(w, http.StatusForbidden, "not yours to organise")
	case err != nil:
		fail(w, r, err)
	default:
		w.WriteHeader(http.StatusNoContent)
	}
}

// triageDecision sets when an item should be decided and whether it is
// snoozed; a missing snoozed_until clears the snooze.
func (h *H) triageDecision(w http.ResponseWriter, r *http.Request) {
	var in struct {
		DecideBy     string     `json:"decide_by"`
		DecideByDate string     `json:"decide_by_date"`
		SnoozedUntil *time.Time `json:"snoozed_until"`
	}
	if err := readJSON(r, &in); err != nil {
		writeErr(w, http.StatusBadRequest, "invalid request body")
		return
	}
	if !slices.Contains([]string{"", "today", "this_week", "whenever", "date"}, in.DecideBy) {
		writeErr(w, http.StatusBadRequest, "decide_by must be empty, today, this_week, whenever or date")
		return
	}
	var date pgtype.Date
	if in.DecideBy == "date" {
		d, err := time.Parse(time.DateOnly, in.DecideByDate)
		if err != nil {
			writeErr(w, http.StatusBadRequest, "decide_by_date must be YYYY-MM-DD")
			return
		}
		date = pgtype.Date{Time: d, Valid: true}
	}
	h.manageDecision(w, r, func(tx *store.Tx, ws, id uuid.UUID) error {
		return tx.Q.SetDecisionTriage(r.Context(), db.SetDecisionTriageParams{ID: id, WorkspaceID: ws, DecideBy: in.DecideBy, DecideByDate: date, SnoozedUntil: in.SnoozedUntil})
	})
}

func (h *H) keepDecision(w http.ResponseWriter, r *http.Request) {
	h.manageDecision(w, r, func(tx *store.Tx, ws, id uuid.UUID) error {
		return tx.Q.SetDecisionKept(r.Context(), db.SetDecisionKeptParams{ID: id, WorkspaceID: ws})
	})
}

func (h *H) archiveDecision(w http.ResponseWriter, r *http.Request) {
	h.setArchived(w, r, true)
}

func (h *H) reviveDecision(w http.ResponseWriter, r *http.Request) {
	h.setArchived(w, r, false)
}

func (h *H) setArchived(w http.ResponseWriter, r *http.Request, archived bool) {
	h.manageDecision(w, r, func(tx *store.Tx, ws, id uuid.UUID) error {
		return tx.Q.SetDecisionArchived(r.Context(), db.SetDecisionArchivedParams{ID: id, WorkspaceID: ws, Archived: archived})
	})
}

// decideDecision answers a decision with one of its options and returns the
// decided item. It opens its own transaction, outside Scope's, because
// answering may kick the study's engine.
func (h *H) decideDecision(w http.ResponseWriter, r *http.Request) {
	id, ok := cardID(w, r)
	if !ok {
		return
	}
	var in struct {
		Option   string `json:"option"`
		Guidance string `json:"guidance"`
	}
	if err := readJSON(r, &in); err != nil {
		writeErr(w, http.StatusBadRequest, "invalid request body")
		return
	}
	ws, err := uuid.Parse(middleware.FromHeader(r))
	if err != nil {
		writeErr(w, http.StatusBadRequest, "X-Workspace-ID required")
		return
	}
	who := user(r)
	err = h.Engine.Decide(r.Context(), ws, id, who, in.Option, in.Guidance)
	var opt flow.OptionError
	switch {
	case errors.As(err, &opt):
		writeErr(w, http.StatusBadRequest, opt.Error())
		return
	case errors.Is(err, flow.ErrNotAnswerer):
		writeErr(w, http.StatusForbidden, err.Error())
		return
	case errors.Is(err, flow.ErrDecisionClosed), errors.Is(err, flow.ErrNotWaiting):
		writeErr(w, http.StatusConflict, err.Error())
		return
	case err != nil:
		fail(w, r, err)
		return
	}
	var out decisionView
	err = h.Store.InWorkspace(r.Context(), ws, who, func(tx *store.Tx) error {
		row, err := tx.Q.GetDecision(r.Context(), db.GetDecisionParams{ID: id, WorkspaceID: ws, Viewer: who})
		out = viewDecision(row.InboxItem, row.MayAnswer)
		return err
	})
	if err != nil {
		fail(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, out)
}
