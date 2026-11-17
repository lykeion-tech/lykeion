package handler

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"maps"
	"net/http"
	"slices"
	"strings"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"lykeion/server/internal/board"
	"lykeion/server/internal/flow"
	"lykeion/server/internal/jev"
	"lykeion/server/internal/middleware"
	"lykeion/server/internal/service"
	"lykeion/server/internal/store"
	"lykeion/server/pkg/agent"
	db "lykeion/server/pkg/db/generated"
)

var errEnded = errors.New("the card has ended; start a new study to continue")

// steer applies one person's action to a card under its lock, cancels the
// card's in-flight agent runs when asked, and kicks the engine afterwards. A typed
// input error from act answers 400; other errors retain their server handling. Its routes sit outside
// Scope, like agent-run cancel: Scope's transaction holds the workspace row,
// which the cancellations' own transactions need after this one commits.
func (h *H) steer(w http.ResponseWriter, r *http.Request, cancelAgentRuns bool, act func(tx *store.Tx, c db.Card, s *board.State, set flow.Settings) ([]board.Item, error)) {
	h.steerCard(w, r, cancelAgentRuns, false, act)
}

// steerCard is steer; ended lets act reach a card that has ended.
func (h *H) steerCard(w http.ResponseWriter, r *http.Request, cancelAgentRuns, ended bool, act func(tx *store.Tx, c db.Card, s *board.State, set flow.Settings) ([]board.Item, error)) {
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
		if c.Ending != "" && !ended {
			return errEnded
		}
		wsRow, err := tx.Q.GetWorkspace(r.Context(), ws)
		if err != nil {
			return err
		}
		s := board.FromCard(c)
		items, err := act(tx, c, &s, flow.ParseSettings(wsRow.Settings))
		if err != nil {
			return err
		}
		if cancelAgentRuns {
			ts, err := tx.Q.CardInflightAgentRuns(r.Context(), db.CardInflightAgentRunsParams{WorkspaceID: ws, CardID: &id})
			if err != nil {
				return err
			}
			for _, t := range ts {
				cancel = append(cancel, t)
			}
		}
		if err := saveBoard(r.Context(), tx, c, &s, items); err != nil {
			return err
		}
		if err := tx.Emit(r.Context(), "card.steered", &id, store.User(who), map[string]any{"path": r.URL.Path, "column": s.Column, "state": s.State, "flag": s.Flag, "ending": s.Ending}); err != nil {
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
		var cf conflict
		switch {
		case errors.As(err, &inputErr):
			writeErr(w, 400, inputErr.Error())
		case errors.As(err, &cf):
			writeErr(w, 409, cf.Error())
		default:
			fail(w, r, err)
		}
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
	h.steer(w, r, true, func(tx *store.Tx, c db.Card, s *board.State, _ flow.Settings) ([]board.Item, error) {
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
		return nil, service.EndStudyItems(r.Context(), tx, c.ID, store.User(user(r)))
	})
}

// saveBoard saves the card's board state and delivers its inbox items to the owner.
func saveBoard(ctx context.Context, tx *store.Tx, c db.Card, s *board.State, items []board.Item) error {
	if err := tx.Q.SaveCardBoard(ctx, s.Params(c.ID, tx.WS)); err != nil {
		return err
	}
	for _, it := range items {
		if err := tx.Q.InsertInboxItem(ctx, db.InsertInboxItemParams{WorkspaceID: tx.WS, CardID: c.ID, UserID: c.OwnerID, Kind: it.Kind, Text: it.Text}); err != nil {
			return err
		}
	}
	return nil
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
		// id= finds one live record of the study in any column, as a list of
		// none or one.
		if raw, ok := r.URL.Query()["id"]; ok {
			out = []db.Record{}
			rid, perr := uuid.Parse(raw[0])
			if perr != nil {
				return nil
			}
			rec, err := tx.Q.LiveCardRecord(r.Context(), db.LiveCardRecordParams{WorkspaceID: ws, CardID: id, ID: rid})
			if errors.Is(err, pgx.ErrNoRows) {
				return nil
			}
			out = append(out, rec)
			return err
		}
		// kind= lists that kind's live records from every column.
		if kind := r.URL.Query().Get("kind"); kind != "" {
			out, err = tx.Q.CardKindRecords(r.Context(), db.CardKindRecordsParams{WorkspaceID: ws, CardID: id, Kind: kind})
			return err
		}
		col := r.URL.Query().Get("column")
		if col == "" {
			col = c.BoardColumn
		}
		// all=1 adds the superseded records, each with its superseded_at.
		if r.URL.Query().Get("all") == "1" {
			out, err = tx.Q.ListAllRecords(r.Context(), db.ListAllRecordsParams{WorkspaceID: ws, CardID: id, BoardColumn: col})
			return err
		}
		out, err = tx.Q.ListRecords(r.Context(), db.ListRecordsParams{WorkspaceID: ws, CardID: id, BoardColumn: col})
		return err
	})
	if err != nil {
		fail(w, r, err)
		return
	}
	if out == nil {
		out = []db.Record{}
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
	// A harness no daemon can run would queue agent runs nobody ever claims.
	set := flow.ParseSettings(in.Settings)
	for _, h := range append(slices.Collect(maps.Values(set.Harness.Roles)), set.Harness.Default) {
		if _, ok := agent.Harnesses[h]; !ok {
			writeErr(w, 400, fmt.Sprintf("unknown harness %q", h))
			return
		}
	}

	var pin struct {
		Jev struct {
			Model   *string         `json:"model"`
			Enabled json.RawMessage `json:"enabled"`
		} `json:"jev"`
	}
	json.Unmarshal(in.Settings, &pin)
	if pin.Jev.Model != nil && *pin.Jev.Model != jev.Model {
		writeErr(w, 400, "the Jev model is pinned to "+jev.Model)
		return
	}
	if pin.Jev.Enabled != nil {
		writeErr(w, 400, "Jev reviews a study when its owner has saved a TypeSafe key in Settings; there is no workspace setting for it")
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
