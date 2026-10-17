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
	"strconv"
	"strings"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"lykeion/server/internal/board"
	"lykeion/server/internal/jev"
	"lykeion/server/internal/middleware"
	"lykeion/server/internal/records"
	"lykeion/server/internal/round"
	"lykeion/server/internal/service"
	"lykeion/server/internal/store"
	"lykeion/server/pkg/agent"
	db "lykeion/server/pkg/db/generated"
)

var errEnded = errors.New("the card has ended; continue with a new card that follows from it")

// errBlocked refuses a plain resume of a Blocked card: its failed gate opened
// no round, so only an unblock action can move it on.
var errBlocked = errors.New("this study is blocked: use unblock")

// resolveBlocked resolves the card's open escalated and out-of-budget inbox
// items: a person has answered them by resuming, stopping or changing the budget.
func resolveBlocked(ctx context.Context, tx *store.Tx, card, who uuid.UUID) error {
	ids, err := tx.Q.ResolveCardInbox(ctx, db.ResolveCardInboxParams{WorkspaceID: tx.WS, CardID: &card})
	if err != nil {
		return err
	}
	for _, id := range ids {
		if err := tx.Emit(ctx, "inbox.resolved", &card, store.User(who), map[string]string{"inbox_item_id": id.String()}); err != nil {
			return err
		}
	}
	return nil
}

// steer applies one person's action to a card under its lock, cancels the
// card's in-flight agent runs when asked, and kicks the engine afterwards. A typed
// input error from act answers 400; other errors retain their server handling. Its routes sit outside
// Scope, like agent-run cancel: Scope's transaction holds the workspace row,
// which the cancellations' own transactions need after this one commits.
func (h *H) steer(w http.ResponseWriter, r *http.Request, cancelAgentRuns bool, act func(tx *store.Tx, c db.Card, s *board.State, set round.Settings) ([]board.Item, error)) {
	h.steerCard(w, r, cancelAgentRuns, false, act)
}

// steerCard is steer; ended lets act reach a card that has ended.
func (h *H) steerCard(w http.ResponseWriter, r *http.Request, cancelAgentRuns, ended bool, act func(tx *store.Tx, c db.Card, s *board.State, set round.Settings) ([]board.Item, error)) {
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
		if err := tx.Emit(r.Context(), "card.steered", &id, store.User(who), map[string]any{"path": r.URL.Path, "column": s.Column, "flag": s.Flag, "ending": s.Ending}); err != nil {
			return err
		}
		out, err = tx.Q.GetCard(r.Context(), db.GetCardParams{WorkspaceID: ws, ID: id})
		return err
	})
	switch {
	case errors.Is(err, errEnded), errors.Is(err, errBlocked):
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
	h.steer(w, r, true, func(_ *store.Tx, _ db.Card, s *board.State, _ round.Settings) ([]board.Item, error) {
		items, err := board.SendBack(s, in.To, in.Step, in.Reason)
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
		return nil, resolveBlocked(r.Context(), tx, c.ID, user(r))
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
	h.steer(w, r, false, func(tx *store.Tx, c db.Card, s *board.State, _ round.Settings) ([]board.Item, error) {
		s.BudgetUSD = in.USD
		if s.Flag == board.OutOfBudget {
			s.Flag = ""
		}
		return nil, resolveBlocked(r.Context(), tx, c.ID, user(r))
	})
}

func (h *H) resumeCard(w http.ResponseWriter, r *http.Request) {
	h.steer(w, r, false, func(tx *store.Tx, c db.Card, s *board.State, _ round.Settings) ([]board.Item, error) {
		if s.BlockKind != "" {
			return nil, errBlocked
		}
		board.Resume(s)
		return nil, resolveBlocked(r.Context(), tx, c.ID, user(r))
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

// serverRecord writes one server record on the card, in column and round.
func serverRecord(ctx context.Context, tx *store.Tx, c db.Card, column string, round int32, kind, key string, body any) error {
	c.BoardColumn, c.Round = column, round
	return records.Batch{Put: []records.Put{{Kind: kind, Key: key, Body: body}}}.Write(ctx, tx, c)
}

func (h *H) unblockCard(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Action string `json:"action"`
		Cap    int32  `json:"cap"`
	}
	if err := readJSON(r, &in); err != nil || in.Action == "" {
		writeErr(w, 400, "action required")
		return
	}
	h.steer(w, r, false, func(tx *store.Tx, c db.Card, s *board.State, _ round.Settings) ([]board.Item, error) {
		if err := board.Unblock(s, in.Action, in.Cap); err != nil {
			return nil, conflict(err.Error())
		}
		return nil, resolveBlocked(r.Context(), tx, c.ID, user(r))
	})
}

// closeCard stops a Blocked study and writes its stop record in the column it
// was blocked in: why, the block, the reasons of the gates failed since the
// last reopen in order, and the last gate.
func (h *H) closeCard(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Reason string `json:"reason"`
	}
	if err := readJSON(r, &in); err != nil || strings.TrimSpace(in.Reason) == "" {
		writeErr(w, 400, "reason required")
		return
	}
	h.steer(w, r, true, func(tx *store.Tx, c db.Card, s *board.State, _ round.Settings) ([]board.Item, error) {
		gates, err := tx.Q.CardGates(r.Context(), db.CardGatesParams{WorkspaceID: tx.WS, CardID: c.ID})
		if err != nil {
			return nil, err
		}
		since, err := reopenedRound(r.Context(), tx, c)
		if err != nil {
			return nil, err
		}
		failures := []string{}
		var last json.RawMessage
		for _, g := range gates {
			if b := records.Body[board.Gate](g); b.Verdict == board.VFail && g.Round >= since {
				failures = append(failures, b.Reason)
			}
			last = g.Body
		}
		stop := map[string]any{"reason": in.Reason, "block_kind": s.BlockKind, "failures": failures, "last_gate": last}
		if err := serverRecord(r.Context(), tx, c, c.BoardColumn, c.Round, "stop", "", stop); err != nil {
			return nil, err
		}
		board.Close(s, in.Reason)
		return nil, resolveBlocked(r.Context(), tx, c.ID, user(r))
	})
}

// reopenedRound is the round the study's last reopen began, or 0 for a study
// never reopened (or whose reopen left no evidence record). Rounds only grow, so later gates have rounds at or above it.
func reopenedRound(ctx context.Context, tx *store.Tx, c db.Card) (int32, error) {
	if c.Reopens == 0 {
		return 0, nil
	}
	evidence, err := tx.Q.CardKindRecords(ctx, db.CardKindRecordsParams{WorkspaceID: tx.WS, CardID: c.ID, Kind: "reopen_evidence"})
	if err != nil {
		return 0, err
	}
	for _, e := range evidence {
		if e.Key == strconv.Itoa(int(c.Reopens)) {
			return e.Round, nil
		}
	}
	// Without its evidence record the reopen's round is unknown; list every failure.
	return 0, nil
}

// revertRevision puts the study back on its original starting point.
func (h *H) revertRevision(w http.ResponseWriter, r *http.Request) {
	h.steer(w, r, true, func(tx *store.Tx, c db.Card, s *board.State, _ round.Settings) ([]board.Item, error) {
		if s.BlockKind != "" {
			return nil, conflict("unblock or close this study first")
		}
		items, err := round.RevertQuestion(r.Context(), tx, c, s)
		if errors.Is(err, round.ErrNoRevision) {
			return nil, conflict(err.Error())
		}
		return items, err
	})
}

type reopenInput struct {
	Reason   string `json:"reason"`
	Evidence string `json:"evidence"`
}

// reopenCard takes a person, or an agent run whose token may reopen cards.
func (h *H) reopenCard(w http.ResponseWriter, r *http.Request) {
	if tok, _ := strings.CutPrefix(r.Header.Get("Authorization"), "Bearer "); strings.HasPrefix(tok, service.AgentRunPrefix) {
		middleware.AgentRunAuth(h.Store)(middleware.RequireScope(service.ScopeCardReopen)(http.HandlerFunc(h.reopenByRun))).ServeHTTP(w, r)
		return
	}
	middleware.Auth(h.Store, h.JWT)(http.HandlerFunc(h.reopenByPerson)).ServeHTTP(w, r)
}

func readReopen(w http.ResponseWriter, r *http.Request) (reopenInput, bool) {
	var in reopenInput
	if err := readJSON(r, &in); err != nil || strings.TrimSpace(in.Reason) == "" || strings.TrimSpace(in.Evidence) == "" {
		writeErr(w, 400, "reason and evidence required")
		return in, false
	}
	return in, true
}

func (h *H) reopenByPerson(w http.ResponseWriter, r *http.Request) {
	in, ok := readReopen(w, r)
	if !ok {
		return
	}
	h.steerCard(w, r, false, true, func(tx *store.Tx, c db.Card, s *board.State, _ round.Settings) ([]board.Item, error) {
		return reopen(r.Context(), tx, c, s, in, store.User(user(r)))
	})
}

func (h *H) reopenByRun(w http.ResponseWriter, r *http.Request) {
	id, ok := cardID(w, r)
	if !ok {
		return
	}
	in, ok := readReopen(w, r)
	if !ok {
		return
	}
	var out db.Card
	err := h.roleTx(r, func(tx *store.Tx, role middleware.Role) error {
		c, err := tx.Q.LockCard(r.Context(), db.LockCardParams{ID: id, WorkspaceID: tx.WS})
		if err != nil {
			return err
		}
		run, err := tx.Q.GetAgentRun(r.Context(), db.GetAgentRunParams{ID: role.AgentRunID, WorkspaceID: tx.WS})
		if err != nil {
			return err
		}
		if run.RequesterID == nil || *run.RequesterID != c.OwnerID {
			return forbidden("this Autoresearch can only reopen its owner's studies")
		}
		s := board.FromCard(c)
		items, err := reopen(r.Context(), tx, c, &s, in, store.Actor{Type: "role", ID: role.AgentRunID})
		if err != nil {
			return err
		}
		if err := saveBoard(r.Context(), tx, c, &s, items); err != nil {
			return err
		}
		out, err = tx.Q.GetCard(r.Context(), db.GetCardParams{WorkspaceID: tx.WS, ID: id})
		return err
	})
	if errors.Is(err, middleware.ErrRoleUnauthorized) {
		roleError(w, r, err)
		return
	}
	if !answer(w, r, err) {
		return
	}
	if h.Engine != nil {
		h.Engine.Kick(out.WorkspaceID, id)
	}
	writeJSON(w, 200, out)
}

// reopen sends a released study back to Grounding on new evidence, with a
// fresh cap. The release it reopens is marked under review in Reporting,
// keyed by its version, and the evidence goes to Grounding for that stage.
func reopen(ctx context.Context, tx *store.Tx, c db.Card, s *board.State, in reopenInput, actor store.Actor) ([]board.Item, error) {
	release := s.Reopens
	items, err := board.Reopen(s, in.Reason)
	if err != nil {
		return nil, conflict(err.Error())
	}
	if err := serverRecord(ctx, tx, c, board.Reporting, c.Round, "report_status", strconv.Itoa(int(release)),
		map[string]any{"state": "under_review", "release": release}); err != nil {
		return nil, err
	}
	if err := serverRecord(ctx, tx, c, board.Grounding, s.Round, "reopen_evidence", strconv.Itoa(int(s.Reopens)),
		map[string]any{"reopen": s.Reopens, "reason": in.Reason, "evidence": in.Evidence}); err != nil {
		return nil, err
	}
	return items, tx.Emit(ctx, "card.reopened", &c.ID, actor, map[string]any{"reopens": s.Reopens, "reason": in.Reason})
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
	// A harness no daemon can run would queue agent runs nobody ever claims.
	set := round.ParseSettings(in.Settings)
	for _, h := range append(slices.Collect(maps.Values(set.Harness.Roles)), set.Harness.Default) {
		if _, ok := agent.Harnesses[h]; !ok {
			writeErr(w, 400, fmt.Sprintf("unknown harness %q", h))
			return
		}
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
