// Package flow advances studies through their columns.
package flow

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"slices"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"lykeion/server/internal/board"
	"lykeion/server/internal/jev"
	"lykeion/server/internal/pack"
	"lykeion/server/internal/review"
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
	Jev       *jev.Client    // nil when the lab has no TypeSafe key
	Sources   review.Sources // resolves Grounding's papers and datasets; nil reviews no Grounding column
	Retry     func() *jev.Retry
	Now       func() time.Time
	Inline    bool // run kicks in the caller's goroutine; tests use it to stay deterministic

	// RatingDraw says whether a finished Team column is offered for rating;
	// nil draws one in ratingChance.
	RatingDraw func() bool
}

// ErrPackUnavailable wraps a failure to load a card's Team.
var ErrPackUnavailable = errors.New("the card's Team cannot be loaded")

var errNoAgent = errors.New("no agent works this column")

// OnSettlement is wired to service.AgentRuns' durable settlement: a settled
// run may let its study move on, by its lead's hand-in or failure (see
// afterRun). An error leaves the settlement pending, so it is delivered
// again. A card-less run moves nothing.
func (e *Engine) OnSettlement(ctx context.Context, n service.Settlement) error {
	t := n.AgentRun
	if t.CardID == nil {
		return nil
	}
	if err := e.afterRun(ctx, t); err != nil {
		return err
	}
	e.Kick(t.WorkspaceID, *t.CardID)
	return nil
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

// Tick marks queued runs whose owner is offline, expires and reminds due
// decisions, and kicks every working study in a column with nothing in
// flight or a settled run to act on.
func (e *Engine) Tick(ctx context.Context) error {
	if err := e.Store.Global().MarkQueueOffline(ctx); err != nil {
		slog.Error("mark queue offline", "err", err)
	}
	if err := e.sweepDecisions(ctx, e.now()); err != nil {
		slog.Error("sweep decisions", "err", err)
	}
	cands, err := e.Store.Global().BoardCandidates(ctx)
	if err != nil {
		return err
	}
	for _, c := range cands {
		e.Kick(c.WorkspaceID, c.CardID)
	}
	return nil
}

// Advance starts the study's current column when it is working there and no
// run of this attempt at the column exists yet. Once the column is declared
// done it starts the auditor, and once that settles it scores the column, with
// no transaction open. A study another pass holds is left to that pass.
func (e *Engine) Advance(ctx context.Context, ws, id uuid.UUID) error {
	score := false
	err := e.Store.InWorkspace(ctx, ws, uuid.Nil, func(tx *store.Tx) error {
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
		if !working(c) {
			return nil
		}
		runs, err := tx.Q.RoundAgentRuns(ctx, db.RoundAgentRunsParams{WorkspaceID: ws, CardID: id, BoardColumn: c.BoardColumn, Round: c.Round})
		if err != nil {
			return err
		}
		if len(runs) == 0 {
			return e.Start(ctx, tx, c)
		}
		pending, err := e.followUp(ctx, tx, c, runs)
		if err != nil || pending {
			return err
		}
		score, err = e.afterDone(ctx, tx, c, runs)
		return err
	})
	if err != nil || !score {
		return err
	}
	return e.Score(ctx, ws, id)
}

// columnTeam reads the Team working a study's column from its row, so no blob is
// read inside the transaction. A column with no Team gives pgx.ErrNoRows.
func columnTeam(ctx context.Context, tx *store.Tx, card uuid.UUID, column string) (uuid.UUID, *pack.Manifest, error) {
	packID, err := tx.Q.GetCardTeamPack(ctx, db.GetCardTeamPackParams{WorkspaceID: tx.WS, CardID: card, BoardColumn: column})
	if err != nil {
		return uuid.Nil, nil, err
	}
	row, err := tx.Q.GetFieldPack(ctx, db.GetFieldPackParams{WorkspaceID: tx.WS, ID: packID})
	if err != nil {
		return uuid.Nil, nil, err
	}
	var m pack.Manifest
	if err := json.Unmarshal(row.Manifest, &m); err != nil {
		return uuid.Nil, nil, fmt.Errorf("%w: %v", ErrPackUnavailable, err)
	}
	return packID, &m, nil
}

// teamAgent is the study owner's agent for one member of a Team: their own
// copy, or the workspace's shared laboratory agent.
func teamAgent(ctx context.Context, tx *store.Tx, packID, owner uuid.UUID, member string) (db.Agent, error) {
	a, err := tx.Q.GetTeamAgent(ctx, db.GetTeamAgentParams{WorkspaceID: tx.WS, PackID: packID, UserID: owner, Member: member})
	if errors.Is(err, pgx.ErrNoRows) {
		return a, fmt.Errorf("%w: the study's owner has no agent for %s", ErrPackUnavailable, member)
	}
	return a, err
}

// worker is who starts the study's current column: the leader of its Team,
// or, with no Team there, the study's agent.
func (e *Engine) worker(ctx context.Context, tx *store.Tx, c db.Card) (string, *pack.Manifest, db.Agent, error) {
	packID, m, err := columnTeam(ctx, tx, c.ID, c.BoardColumn)
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
	a, err := teamAgent(ctx, tx, packID, c.OwnerID, m.Leader.Name)
	return "leader", m, a, err
}

// Start enqueues the first run of the study's current column, in the
// caller's transaction: the leader's on a Team column, the study's agent's
// otherwise. A redone column's first run carries the reason for the redo.
// A Validation round that checks the study's runs also starts their rerun.
func (e *Engine) Start(ctx context.Context, tx *store.Tx, c db.Card) error {
	role, m, a, err := e.worker(ctx, tx, c)
	if err != nil {
		return err
	}
	if _, err = e.enqueue(ctx, tx, c, role, m, a, c.HandoffNote); err != nil || c.BoardColumn != board.Validation {
		return err
	}
	// A Validation round that checks the study's runs replays them while
	// its lead works.
	if mode, err := validationMode(ctx, tx, c.ID); err != nil || mode != modeRuns {
		return err
	}
	return e.startRerun(ctx, tx, c)
}

// enqueue queues one run of role on the study's current column. The run
// takes its harness, model and instructions from the owner's agent row, and
// the protocol for its role is appended to them; note, when set, follows the
// study in the prompt.
func (e *Engine) enqueue(ctx context.Context, tx *store.Tx, c db.Card, role string, m *pack.Manifest, a db.Agent, note string) (db.AgentRun, error) {
	p, comments, err := e.runPrompt(ctx, tx, c, role, note)
	if err != nil {
		return db.AgentRun{}, err
	}
	run, err := e.enqueueWithInput(ctx, tx, c, role, m, a, p, assignment(c, note))
	if err != nil {
		return run, err
	}
	_, err = service.DeliverComments(ctx, tx, run, service.DeliveredNextRun, comments)
	return run, err
}

// runPrompt is a run's prompt, with the study's comments it includes; the
// caller, which holds the card lock, gives the run those not yet delivered.
func (e *Engine) runPrompt(ctx context.Context, tx *store.Tx, c db.Card, role, note string) (string, []db.Comment, error) {
	comments, err := tx.Q.ListComments(ctx, db.ListCommentsParams{WorkspaceID: tx.WS, CardID: c.ID})
	if err != nil {
		return "", nil, err
	}
	comments, err = service.CommentsForRun(ctx, tx, comments, nil)
	if err != nil {
		return "", nil, err
	}
	superseded, err := tx.PendingBeforeUserStop(ctx, c.ID)
	if err != nil {
		return "", nil, err
	}
	comments = slices.DeleteFunc(comments, func(comment db.Comment) bool { return superseded[comment.ID] })
	// Read at start, so a change to the research context reaches the next run.
	ws, err := tx.Q.GetWorkspace(ctx, tx.WS)
	if err != nil {
		return "", nil, err
	}
	files, err := tx.Q.CardAttachments(ctx, db.CardAttachmentsParams{WorkspaceID: tx.WS, CardID: &c.ID})
	if err != nil {
		return "", nil, err
	}
	p := prompt(c, role, comments, ws.ResearchContext, files)
	earlier, err := earlierWork(ctx, tx, c)
	if err != nil {
		return "", nil, err
	}
	if earlier != "" {
		p += "\n" + earlier
	}
	if c.BoardColumn == board.Validation && role != "auditor" {
		mode, err := validationMode(ctx, tx, c.ID)
		if err != nil {
			return "", nil, err
		}
		p += "\n" + modeLines[mode] + "\n"
	}
	if note != "" {
		p += "\n" + note + "\n"
	}
	return p, comments, nil
}

// enqueueWithInput accepts only research inputs from an interrupted run. Agent
// instructions and role protocol are current; execution state is minted anew.
func (e *Engine) enqueueWithInput(ctx context.Context, tx *store.Tx, c db.Card, role string, m *pack.Manifest, a db.Agent, runPrompt, runAssignment string) (db.AgentRun, error) {
	agentID := a.ID
	harness, model := a.Harness, a.Model
	if m != nil && c.StudyCli != nil && c.AuditorCli != nil {
		member := m.Leader
		if role == "auditor" {
			member = m.Auditor
		} else if role != "leader" {
			for _, candidate := range m.Members {
				if candidate.Name == role {
					member = candidate
					break
				}
			}
		}
		harness, model = studyCLI(role, *c.StudyCli, *c.AuditorCli, member.Agent)
	}
	return e.AgentRuns.Enqueue(ctx, tx, c.OwnerID, service.NewAgentRun{Card: c.ID, Column: c.BoardColumn, Round: c.Round,
		Role: role, Harness: harness, Model: model, Prompt: runPrompt, Agent: &agentID, Assignment: runAssignment,
		ContinuityScope: ContinuityScope(c.ID, role, c.BoardColumn, c.Round),
		Skill:           strings.TrimSpace(a.Instructions + "\n\n" + Protocol(role, c.BoardColumn, c.ScopeFrom, m))})
}

func studyCLI(role, worker, auditor string, def pack.AgentDef) (string, string) {
	cli := worker
	if role == "auditor" {
		cli = auditor
	}
	if len(def.Models) > 0 {
		return cli, def.Models[cli]
	}
	if def.Harness == cli {
		return cli, def.Model
	}
	return cli, ""
}

// ContinuityScope names the CLI session a run continues on its owner's
// daemon: one per role within one attempt at a column. Every attempt (a
// study's first at a column, a redo after a low score, a redo the owner asks
// for) is a new round, so its runs start afresh, while a woken leader resumes
// its session within the attempt.
func ContinuityScope(card uuid.UUID, role, column string, round int32) string {
	return fmt.Sprintf("card:%s/column:%s/round:%d/role:%s", card, column, round, role)
}

// followUp consumes durable composer work only when the current study has no
// active work. A true result also holds scoring while pending input waits for
// active work to settle. The caller holds workspace execution and card locks.
func (e *Engine) followUp(ctx context.Context, tx *store.Tx, c db.Card, runs []db.AgentRun) (bool, error) {
	if !working(c) || (c.GatingUntil != nil && c.GatingUntil.After(e.now())) {
		return false, nil
	}
	pending, err := hasPendingComposer(ctx, tx, c)
	if err != nil || !pending {
		return false, err
	}
	// Failures and system cancellations retain their recovery policy. Only a
	// new message after a user stops a lead can resume that lead.
	seen := map[string]bool{}
	for i := len(runs) - 1; i >= 0; i-- {
		r := runs[i]
		if seen[r.Role] {
			continue
		}
		seen[r.Role] = true
		if accessInterrupted(r) || leads(r.Role) && r.Status == "failed" {
			// Keep pending input from flowing into an audit of older work too.
			return true, nil
		}
		if leads(r.Role) && r.Status == "cancelled" {
			// An explicit new message can resume a run stopped by a person, but
			// older pending input must not immediately undo that person's stop.
			fresh, err := tx.ComposerAfterUserStop(ctx, c.ID, r.ID)
			if err != nil || !fresh {
				return true, err
			}
		}
	}
	active, err := tx.Q.CardInflightAgentRuns(ctx, db.CardInflightAgentRunsParams{WorkspaceID: tx.WS, CardID: &c.ID})
	if err != nil {
		return false, err
	}
	// Pending composer work belongs to the lead's next input, not an auditor
	// started from an earlier hand-in while that lead is still settling.
	if len(active) > 0 {
		return true, nil
	}
	role, m, a, err := e.worker(ctx, tx, c)
	if errors.Is(err, errNoAgent) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	if _, err = e.enqueue(ctx, tx, c, role, m, a, c.HandoffNote); err != nil {
		return false, err
	}
	if err = tx.Q.SupersedeColumnCompletion(ctx, db.SupersedeColumnCompletionParams{WorkspaceID: tx.WS, CardID: c.ID, BoardColumn: c.BoardColumn, Round: c.Round}); err != nil {
		return false, err
	}
	// Invalidate any expired scoring pass that was reading the old completion.
	err = tx.Q.SaveCardFlow(ctx, db.SaveCardFlowParams{WorkspaceID: tx.WS, ID: c.ID, BoardColumn: c.BoardColumn, State: c.State, BlockKind: c.BlockKind, Round: c.Round, Redos: c.Redos, HandoffNote: c.HandoffNote})
	return err == nil, err
}

// hasPendingComposer excludes cancelled, delivered and actively reserved inputs.
func hasPendingComposer(ctx context.Context, tx *store.Tx, c db.Card) (bool, error) {
	return hasPendingComposerSince(ctx, tx, c, time.Time{})
}

func hasPendingComposerSince(ctx context.Context, tx *store.Tx, c db.Card, since time.Time) (bool, error) {
	pending, err := tx.Q.UndeliveredComments(ctx, db.UndeliveredCommentsParams{WorkspaceID: tx.WS, CardID: c.ID, Since: since})
	if err != nil {
		return false, err
	}
	superseded, err := tx.PendingBeforeUserStop(ctx, c.ID)
	if err != nil {
		return false, err
	}
	for _, comment := range pending {
		if comment.ReplyRunID == nil && !superseded[comment.ID] {
			return true, nil
		}
	}
	return false, nil
}

// deferScoringForComments runs under the scoring callback's card lock. Input
// accepted while scoring was outside the transaction must reach current-column
// work before that stale result can advance or finish the study.
func (e *Engine) deferScoringForComments(ctx context.Context, tx *store.Tx, c db.Card) (bool, error) {
	pending, err := hasPendingComposer(ctx, tx, c)
	if err != nil || !pending {
		return false, err
	}
	if err := tx.Q.SetGatingLease(ctx, db.SetGatingLeaseParams{WorkspaceID: tx.WS, ID: c.ID}); err != nil {
		return false, err
	}
	c.GatingUntil = nil
	runs, err := tx.Q.RoundAgentRuns(ctx, db.RoundAgentRunsParams{WorkspaceID: tx.WS, CardID: c.ID, BoardColumn: c.BoardColumn, Round: c.Round})
	if err != nil {
		return false, err
	}
	_, err = e.followUp(ctx, tx, c, runs)
	return true, err
}
