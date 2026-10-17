// Package flow advances studies through their columns.
package flow

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"lykeion/server/internal/board"
	"lykeion/server/internal/jev"
	"lykeion/server/internal/pack"
	"lykeion/server/internal/service"
	"lykeion/server/internal/storage"
	"lykeion/server/internal/store"
	db "lykeion/server/pkg/db/generated"
)

// Engine starts each column of a study: the leader of the column's Team, or
// the study's agent where no Team covers the column.
type Engine struct {
	Store     *store.Store
	AgentRuns *service.AgentRuns
	Blobs     storage.Storage
	Jev       *jev.Client // nil when the lab has no TypeSafe key
	Now       func() time.Time
	Inline    bool // run kicks in the caller's goroutine; tests use it to stay deterministic
}

// ErrPackUnavailable wraps a failure to load a card's Team.
var ErrPackUnavailable = errors.New("the card's Team cannot be loaded")

var errNoAgent = errors.New("no agent works this column")

// KindsFor is what a card's roles may write in its column. No role writes a
// record kind yet.
func KindsFor(context.Context, *store.Tx, storage.Storage, db.Card) (map[string]service.Kind, error) {
	return map[string]service.Kind{}, nil
}

// OnSettled is wired to service.AgentRuns: a settled run may let its study
// move on. A card-less run moves nothing.
func (e *Engine) OnSettled(_ context.Context, t db.AgentRun) {
	if t.CardID == nil {
		return
	}
	e.Kick(t.WorkspaceID, *t.CardID)
}

// Kick advances a study soon, off the caller's request; Tick catches anything
// a crash dropped.
func (e *Engine) Kick(ws, card uuid.UUID) {
	run := func() {
		if err := e.Advance(context.Background(), ws, card); err != nil {
			slog.Error("advance", "card", card, "err", err)
		}
	}
	if e.Inline {
		run()
		return
	}
	go run()
}

// Tick marks queued runs whose owner is offline and kicks every study in a
// column with nothing in flight. Backlog studies run nothing.
func (e *Engine) Tick(ctx context.Context) error {
	if err := e.Store.Global().MarkQueueOffline(ctx); err != nil {
		slog.Error("mark queue offline", "err", err)
	}
	cands, err := e.Store.Global().BoardCandidates(ctx)
	if err != nil {
		return err
	}
	for _, c := range cands {
		if c.BoardColumn != board.Backlog {
			e.Kick(c.WorkspaceID, c.CardID)
		}
	}
	return nil
}

// Advance starts the study's current column when it is working there and no
// run of this attempt at the column exists yet. A study another pass holds is
// left to that pass.
func (e *Engine) Advance(ctx context.Context, ws, id uuid.UUID) error {
	return e.Store.InWorkspace(ctx, ws, uuid.Nil, func(tx *store.Tx) error {
		if _, err := tx.Q.LockWorkspaceExecution(ctx, ws); err != nil {
			return err
		}
		ok, err := tx.Q.TryLockCard(ctx, id)
		if err != nil || !ok {
			return err
		}
		c, err := tx.Q.LockCard(ctx, db.LockCardParams{ID: id, WorkspaceID: ws})
		if errors.Is(err, pgx.ErrNoRows) {
			return nil
		}
		if err != nil {
			return err
		}
		if c.Ending != "" || c.Flag != "" || c.State != board.Working || board.StageIndex(c.BoardColumn) < 0 {
			return nil
		}
		runs, err := tx.Q.RoundAgentRuns(ctx, db.RoundAgentRunsParams{WorkspaceID: ws, CardID: id, BoardColumn: c.BoardColumn, Round: c.Round})
		if err != nil || len(runs) > 0 {
			return err
		}
		return e.Start(ctx, tx, c)
	})
}

// worker is who starts the study's current column: the leader of its Team,
// or, with no Team there, the study's agent. The Team's manifest is read from
// its row, so no blob is read inside the transaction.
func (e *Engine) worker(ctx context.Context, tx *store.Tx, c db.Card) (string, *pack.Manifest, db.Agent, error) {
	packID, err := tx.Q.GetCardTeamPack(ctx, db.GetCardTeamPackParams{WorkspaceID: tx.WS, CardID: c.ID, BoardColumn: c.BoardColumn})
	if errors.Is(err, pgx.ErrNoRows) {
		if c.AssigneeID == nil || c.AssigneeType == nil || *c.AssigneeType == "member" {
			return "", nil, db.Agent{}, errNoAgent
		}
		a, err := tx.Q.GetAgent(ctx, db.GetAgentParams{WorkspaceID: tx.WS, ID: *c.AssigneeID})
		return "agent", nil, a, err
	}
	if err != nil {
		return "", nil, db.Agent{}, err
	}
	row, err := tx.Q.GetFieldPack(ctx, db.GetFieldPackParams{WorkspaceID: tx.WS, ID: packID})
	if err != nil {
		return "", nil, db.Agent{}, err
	}
	var m pack.Manifest
	if err := json.Unmarshal(row.Manifest, &m); err != nil {
		return "", nil, db.Agent{}, fmt.Errorf("%w: %v", ErrPackUnavailable, err)
	}
	a, err := tx.Q.GetTeamAgent(ctx, db.GetTeamAgentParams{WorkspaceID: tx.WS, PackID: packID, UserID: c.OwnerID, Member: m.Leader.Name})
	if errors.Is(err, pgx.ErrNoRows) {
		return "", nil, db.Agent{}, fmt.Errorf("%w: the study's owner has no agent for its leader", ErrPackUnavailable)
	}
	return "leader", &m, a, err
}

// Start enqueues the first run of the study's current column, in the
// caller's transaction: the leader's on a Team column, the study's agent's
// otherwise. The run takes its harness, model and instructions from the
// owner's agent row, and the protocol for its role is appended to them.
func (e *Engine) Start(ctx context.Context, tx *store.Tx, c db.Card) error {
	role, m, a, err := e.worker(ctx, tx, c)
	if err != nil {
		return err
	}
	comments, err := tx.Q.ListComments(ctx, db.ListCommentsParams{WorkspaceID: tx.WS, CardID: c.ID})
	if err != nil {
		return err
	}
	// Read at start, so a change to the research context reaches the next run.
	ws, err := tx.Q.GetWorkspace(ctx, tx.WS)
	if err != nil {
		return err
	}
	t, err := e.AgentRuns.Enqueue(ctx, tx, c.OwnerID, service.NewAgentRun{Card: c.ID, Column: c.BoardColumn, Round: c.Round,
		Role: role, Harness: a.Harness, Model: a.Model, Prompt: prompt(c, role, comments, ws.ResearchContext),
		Skill: strings.TrimSpace(a.Instructions + "\n\n" + Protocol(role, c.BoardColumn, m))})
	if err != nil {
		return err
	}
	return tx.Q.SetAgentRunContinuity(ctx, db.SetAgentRunContinuityParams{ContinuityScope: ContinuityScope(c.ID, role, c.BoardColumn, c.Redos),
		ID: t.ID, WorkspaceID: tx.WS})
}

// ContinuityScope names the CLI session a run continues on its owner's
// daemon: one per role within one attempt at a column, so a resumed leader
// keeps its session and a redo starts afresh.
func ContinuityScope(card uuid.UUID, role, column string, redos int32) string {
	return fmt.Sprintf("card:%s/column:%s/attempt:%d/role:%s", card, column, redos, role)
}
