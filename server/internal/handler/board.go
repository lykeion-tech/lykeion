package handler

import (
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"strings"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"lykeion/server/internal/board"
	"lykeion/server/internal/jev"
	"lykeion/server/internal/middleware"
	"lykeion/server/internal/round"
	"lykeion/server/internal/service"
	"lykeion/server/internal/store"
	db "lykeion/server/pkg/db/generated"
)

var errEnded = errors.New("the card has ended; continue with a new card that follows from it")

// steer applies one person's action to a card under its lock, cancels the
// card's in-flight agent runs when asked, and kicks the engine afterwards. A typed
// input error from act answers 400; other errors retain their server handling. Its routes sit outside
// Scope, like agent-run cancel: Scope's transaction holds the workspace row,
// which the cancellations' own transactions need after this one commits.
func (h *H) steer(w http.ResponseWriter, r *http.Request, cancelAgentRuns bool, act func(tx *store.Tx, c db.Card, s *board.State, set round.Settings) ([]board.Item, error)) {
	id, ok := cardID(w, r)
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
	who := user(r)
	var out db.Card
	var cancel []uuid.UUID
	err = h.Store.InWorkspace(r.Context(), ws, who, func(tx *store.Tx) error {
		// Scope's order: the workspace, then the membership.
		if _, err := tx.Q.LockWorkspaceForMutation(r.Context(), ws); err != nil {
			return err
		}
		if _, err := tx.Q.LockMember(r.Context(), db.LockMemberParams{WorkspaceID: ws, UserID: who}); err != nil {
			return err
		}
		c, err := tx.Q.LockCard(r.Context(), db.LockCardParams{ID: id, WorkspaceID: ws})
		if err != nil {
			return err
		}
		if c.Ending != "" {
			return errEnded
		}
		wsRow, err := tx.Q.GetWorkspace(r.Context(), ws)
		if err != nil {
			return err
		}
		s := board.FromCard(c)
		items, err := act(tx, c, &s, round.ParseSettings(wsRow.Settings))
		if err != nil {
			return err
		}
		if c.Flag == board.OutOfBudget && s.Flag != board.OutOfBudget {
			// the next tick restores runtime_offline where it still holds
			if err := tx.Q.SetCardQueueReason(r.Context(), db.SetCardQueueReasonParams{WorkspaceID: ws, CardID: id}); err != nil {
				return err
			}
		}
		if cancelAgentRuns {
			ts, err := tx.Q.RoundAgentRuns(r.Context(), db.RoundAgentRunsParams{WorkspaceID: ws, CardID: id, BoardColumn: c.BoardColumn, Round: c.Round})
			if err != nil {
				return err
			}
			for _, t := range ts {
				if t.Status == "queued" || t.Status == "dispatched" || t.Status == "running" {
					cancel = append(cancel, t.ID)
				}
			}
		}
		if err := tx.Q.SaveCardBoard(r.Context(), s.Params(id, ws)); err != nil {
			return err
		}
		for _, it := range items {
			if err := tx.Q.InsertInboxItem(r.Context(), db.InsertInboxItemParams{WorkspaceID: ws, CardID: id, UserID: c.OwnerID, Kind: it.Kind, Text: it.Text}); err != nil {
				return err
			}
		}
		if err := tx.Emit(r.Context(), "card.steered", &id, store.User(who), map[string]any{"path": r.URL.Path, "column": s.Column, "flag": s.Flag, "ending": s.Ending}); err != nil {
			return err
		}
		out, err = tx.Q.GetCard(r.Context(), db.GetCardParams{WorkspaceID: ws, ID: id})
		return err
	})
	switch {
	case errors.Is(err, errEnded):
		writeErr(w, 409, err.Error())
		return
	case err != nil:
		var inputErr cardInputError
		if errors.As(err, &inputErr) {
			writeErr(w, 400, inputErr.Error())
			return
		}
		fail(w, r, err)
		return
	}
	incomplete := false
	for _, t := range cancel {
		if err := h.AgentRuns.Cancel(r.Context(), ws, who, t); err != nil && !errors.Is(err, service.ErrConflict) {
			incomplete = true
			slog.Error("steering agent-run cancellation failed", "workspace", ws, "card", id, "agent_run", t, "err", err)
		}
	}
	if h.Engine != nil {
		h.Engine.Kick(ws, id)
	}
	if incomplete {
		writeErr(w, 500, "steering was applied; agent-run cancellation incomplete")
		return
	}
	writeJSON(w, 200, out)
}

func (h *H) sendBack(w http.ResponseWriter, r *http.Request) {
	var in struct {
		To     string `json:"to"`
		Step   string `json:"step"`
		Reason string `json:"reason"`
	}
	if err := readJSON(r, &in); err != nil || strings.TrimSpace(in.Reason) == "" {
		writeErr(w, 400, "to and reason required")
		return
	}
	h.steer(w, r, true, func(_ *store.Tx, _ db.Card, s *board.State, set round.Settings) ([]board.Item, error) {
		items, err := board.SendBack(s, in.To, in.Step, in.Reason, set.Limits)
		if err != nil {
			return nil, cardInputError(err.Error())
		}
		return items, nil
	})
}

// stopCard ends a card with a reason, or as a duplicate of another card.
func (h *H) stopCard(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Reason      string `json:"reason"`
		DuplicateOf int32  `json:"duplicate_of"`
	}
	if err := readJSON(r, &in); err != nil || (strings.TrimSpace(in.Reason) == "" && in.DuplicateOf == 0) {
		writeErr(w, 400, "reason or duplicate_of required")
		return
	}
	h.steer(w, r, true, func(tx *store.Tx, c db.Card, s *board.State, _ round.Settings) ([]board.Item, error) {
		reason := in.Reason
		if in.DuplicateOf != 0 {
			o, err := tx.Q.GetCardByNumber(r.Context(), db.GetCardByNumberParams{WorkspaceID: tx.WS, Number: in.DuplicateOf})
			if errors.Is(err, pgx.ErrNoRows) || (err == nil && o.ID == c.ID) {
				return nil, cardInputError(fmt.Sprintf("duplicate_of: no other card #%d", in.DuplicateOf))
			}
			if err != nil {
				return nil, err
			}
			reason = fmt.Sprintf("duplicate of #%d", o.Number)
		}
		board.Stop(s, reason)
		return nil, nil
	})
}

func (h *H) budgetCard(w http.ResponseWriter, r *http.Request) {
	var in struct {
		USD float64 `json:"usd"`
	}
	if err := readJSON(r, &in); err != nil || in.USD < 0 {
		writeErr(w, 400, "usd required")
		return
	}
	h.steer(w, r, false, func(_ *store.Tx, _ db.Card, s *board.State, _ round.Settings) ([]board.Item, error) {
		s.BudgetUSD = in.USD
		if s.Flag == board.OutOfBudget {
			s.Flag = ""
		}
		return nil, nil
	})
}

func (h *H) resumeCard(w http.ResponseWriter, r *http.Request) {
	h.steer(w, r, false, func(_ *store.Tx, _ db.Card, s *board.State, _ round.Settings) ([]board.Item, error) {
		board.Resume(s)
		return nil, nil
	})
}

func (h *H) cardRecords(w http.ResponseWriter, r *http.Request) {
	id, ok := cardID(w, r)
	if !ok {
		return
	}
	var out []db.Record
	err := h.inWS(r, func(tx *store.Tx, ws, _ uuid.UUID) error {
		c, err := tx.Q.GetCard(r.Context(), db.GetCardParams{WorkspaceID: ws, ID: id})
		if err != nil {
			return err
		}
		col := r.URL.Query().Get("column")
		if col == "" {
			col = c.BoardColumn
		}
		out, err = tx.Q.ListRecords(r.Context(), db.ListRecordsParams{WorkspaceID: ws, CardID: id, BoardColumn: col})
		return err
	})
	if err != nil {
		fail(w, r, err)
		return
	}
	writeJSON(w, 200, out)
}

func (h *H) cardGates(w http.ResponseWriter, r *http.Request) {
	id, ok := cardID(w, r)
	if !ok {
		return
	}
	var out []db.Record
	err := h.inWS(r, func(tx *store.Tx, ws, _ uuid.UUID) error {
		if _, err := tx.Q.GetCard(r.Context(), db.GetCardParams{WorkspaceID: ws, ID: id}); err != nil {
			return err
		}
		var err error
		out, err = tx.Q.CardGates(r.Context(), db.CardGatesParams{WorkspaceID: ws, CardID: id})
		return err
	})
	if err != nil {
		fail(w, r, err)
		return
	}
	writeJSON(w, 200, out)
}

func (h *H) setSettings(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Settings json.RawMessage `json:"settings"`
	}
	if err := readJSON(r, &in); err != nil || len(in.Settings) == 0 || in.Settings[0] != '{' {
		writeErr(w, 400, "settings object required")
		return
	}
	var pin struct {
		Jev struct {
			Model *string `json:"model"`
		} `json:"jev"`
	}
	if json.Unmarshal(in.Settings, &pin) == nil && pin.Jev.Model != nil && *pin.Jev.Model != jev.Model {
		writeErr(w, 400, "the Jev model is pinned to "+jev.Model)
		return
	}
	err := h.inWS(r, func(tx *store.Tx, ws, user uuid.UUID) error {
		if err := tx.Q.SetWorkspaceSettings(r.Context(), db.SetWorkspaceSettingsParams{Settings: in.Settings, ID: ws}); err != nil {
			return err
		}
		return tx.Emit(r.Context(), "workspace.settings_changed", nil, store.User(user), nil)
	})
	if err != nil {
		fail(w, r, err)
		return
	}
	w.WriteHeader(204)
}
