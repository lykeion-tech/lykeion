package flow

import (
	"context"
	"errors"
	"log/slog"
	"slices"
	"strings"
	"unicode/utf8"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"lykeion/server/internal/board"
	"lykeion/server/internal/handin"
	"lykeion/server/internal/pack"
	"lykeion/server/internal/records"
	"lykeion/server/internal/service"
	"lykeion/server/internal/store"
	db "lykeion/server/pkg/db/generated"
)

var (
	ErrUndeclaredMember = errors.New("the Team's manifest declares no such member")
	ErrStepCap          = errors.New("this Team has used all its steps for this column: finish the column with what you have, or do the remaining work yourself")
	ErrMovedOn          = errors.New("the study is no longer working this run's column")
	ErrDeclaredDone     = errors.New("this column was already declared done")
	ErrBusy             = errors.New("a member is still working on this column: wait for its result first")
)

// Excerpt cuts s to at most n bytes on a character boundary and says whether
// it cut anything.
func Excerpt(s string, n int) (string, bool) {
	if len(s) <= n {
		return s, false
	}
	for n > 0 && !utf8.RuneStart(s[n]) {
		n--
	}
	return s[:n], true
}

// working says whether the engine acts on the study: it works a stage column
// and nothing ended, flagged or blocked it.
func working(c db.Card) bool {
	return c.Ending == "" && c.Flag == "" && c.State == board.Working && board.StageIndex(c.BoardColumn) >= 0
}

func current(c db.Card, column string, round int32) bool {
	return working(c) && c.BoardColumn == column && c.Round == round
}

// leads says whether a run's role works a column through to done: a Team's
// leader, or a study's single agent on a column no Team works.
func leads(role string) bool { return role == "leader" || role == "agent" }

// IsMember says whether a run's role is a Team member's name rather than the
// leader, the auditor, a study's single agent or one of Validation's system
// runs, the objector and the rerun.
func IsMember(role string) bool {
	return !slices.Contains([]string{"", "leader", "auditor", "agent", "objector", "rerun"}, role)
}

func inFlight(status string) bool {
	return status == "queued" || status == "dispatched" || status == "running"
}

// openColumn reads the study a run's role command acts on and refuses it
// when the study has left the run's column and attempt, or when that column
// was already declared done.
func openColumn(ctx context.Context, tx *store.Tx, w service.Writer) (db.Card, error) {
	c, err := tx.Q.GetCard(ctx, db.GetCardParams{WorkspaceID: tx.WS, ID: w.Card})
	if err != nil {
		return c, err
	}
	if !current(c, w.Column, w.Round) {
		return c, ErrMovedOn
	}
	_, done, err := roundSummary(ctx, tx, c)
	if err == nil && done {
		err = ErrDeclaredDone
	}
	return c, err
}

// Delegate starts the run of the column Team's member for one step the
// leader names and records the delegation, and returns the member's run. w
// is the leader's run, whose card lock the caller holds. One member works a
// column at a time: while one has not ended, Delegate returns ErrBusy with
// that member's run.
func (e *Engine) Delegate(ctx context.Context, tx *store.Tx, w service.Writer, member, step string) (db.Record, uuid.UUID, error) {
	c, err := openColumn(ctx, tx, w)
	if err != nil {
		return db.Record{}, uuid.Nil, err
	}
	packID, m, err := columnTeam(ctx, tx, c.ID, c.BoardColumn)
	if errors.Is(err, pgx.ErrNoRows) {
		return db.Record{}, uuid.Nil, ErrUndeclaredMember
	}
	if err != nil {
		return db.Record{}, uuid.Nil, err
	}
	if !slices.ContainsFunc(m.Members, func(x pack.Member) bool { return x.Name == member }) {
		return db.Record{}, uuid.Nil, ErrUndeclaredMember
	}
	n, err := tx.Q.CountRoundKindRecords(ctx, db.CountRoundKindRecordsParams{WorkspaceID: tx.WS, CardID: c.ID,
		BoardColumn: c.BoardColumn, Round: c.Round, Kind: "delegation"})
	if err != nil {
		return db.Record{}, uuid.Nil, err
	}
	steps := m.Steps
	if steps == 0 {
		steps = pack.DefaultSteps
	}
	if n >= int64(steps) {
		return db.Record{}, uuid.Nil, ErrStepCap
	}
	working, err := tx.Q.InFlightMemberRun(ctx, db.InFlightMemberRunParams{WorkspaceID: tx.WS, CardID: c.ID, BoardColumn: c.BoardColumn, Round: c.Round})
	if err == nil {
		return db.Record{}, working, ErrBusy
	}
	if !errors.Is(err, pgx.ErrNoRows) {
		return db.Record{}, uuid.Nil, err
	}
	a, err := teamAgent(ctx, tx, packID, c.OwnerID, member)
	if err != nil {
		return db.Record{}, uuid.Nil, err
	}
	run, err := e.enqueue(ctx, tx, c, member, m, a, leaderStep+step)
	if err != nil {
		return db.Record{}, uuid.Nil, err
	}
	leader, err := tx.Q.LockAgentRun(ctx, db.LockAgentRunParams{WorkspaceID: tx.WS, ID: w.AgentRun})
	if err != nil {
		return db.Record{}, uuid.Nil, err
	}
	rec, err := records.WriteAs(ctx, tx, w, "delegation", map[string]any{"member": member, "step": step, "agent_run_id": run.ID, "leader_attempt": leader.Attempt})
	return rec, run.ID, err
}

// lockCard takes the locks Advance takes, waiting for the card's, and reads
// the card; a deleted card reads as not working.
func lockCard(ctx context.Context, tx *store.Tx, id uuid.UUID) (db.Card, error) {
	if _, err := tx.Q.LockWorkspaceExecution(ctx, tx.WS); err != nil {
		return db.Card{}, err
	}
	if err := tx.Q.WaitLockCard(ctx, id); err != nil {
		return db.Card{}, err
	}
	c, err := tx.Q.LockCard(ctx, db.LockCardParams{ID: id, WorkspaceID: tx.WS})
	if errors.Is(err, pgx.ErrNoRows) {
		return db.Card{}, nil
	}
	return c, err
}

// afterRun acts on a lead's settled run. A column finishes on its lead's
// hand-in: a done hand-in already wrote the summary that hands the column to
// its auditor, a could_not_do hand-in holds the study for its owner, an ask
// hand-in puts its question to the researcher, and a run that failed, or
// ended without a hand-in, redoes the column once and then blocks it. A member's, the auditor's or the objector's run wakes no
// one; the engine's next pass reads what it left.
func (e *Engine) afterRun(ctx context.Context, t db.AgentRun) error {
	if t.Kind == "rerun" {
		return e.afterRerun(ctx, t)
	}
	// A report run belongs to a finished study and never moves it.
	if t.Kind == "report" {
		return nil
	}
	if accessInterrupted(t) || !leads(t.Role) || t.Status == "cancelled" {
		return nil
	}
	// Jev is asked before the study is locked: an answer can take a minute.
	fit := fitUnavailable
	if t.Status == "completed" {
		var h *handInBody
		var owner uuid.UUID
		var brief string
		err := e.Store.InWorkspace(ctx, t.WorkspaceID, uuid.Nil, func(tx *store.Tx) (err error) {
			if h, _, err = runHandIn(ctx, tx, t); err != nil {
				return err
			}
			c, err := tx.Q.GetCard(ctx, db.GetCardParams{WorkspaceID: tx.WS, ID: *t.CardID})
			owner = c.OwnerID
			brief = c.Question
			return err
		})
		if err != nil {
			return err
		}
		if h != nil && h.Status == "ask" && h.Decision != nil {
			if j := e.JevFor(ctx, owner); j != nil {
				fit = e.fitCheck(ctx, j, *t.CardID, brief, *h.Decision)
			}
		}
	}
	return e.Store.InWorkspace(ctx, t.WorkspaceID, uuid.Nil, func(tx *store.Tx) error {
		c, err := lockCard(ctx, tx, *t.CardID)
		if err != nil || !current(c, t.BoardColumn, t.Round) {
			return err
		}
		runs, err := tx.Q.RoundAgentRuns(ctx, db.RoundAgentRunsParams{WorkspaceID: tx.WS, CardID: c.ID, BoardColumn: c.BoardColumn, Round: c.Round})
		if err != nil {
			return err
		}
		latest := currentLeadRuns(runs)
		if len(latest) > 0 && leads(latest[0].Role) && latest[0].ID != t.ID {
			return nil
		}
		h, summary, err := runHandIn(ctx, tx, t)
		if err != nil || summary {
			return err
		}
		if t.Status == "completed" && h != nil && h.Status == "could_not_do" {
			if h.BlockedByEnvironment != nil && slices.Contains(h.EnvironmentFailures, *h.BlockedByEnvironment) {
				return e.holdEnvironment(ctx, tx, c, ptrOr(h.Reason))
			}
			return e.askOwner(ctx, tx, c, ptrOr(h.Reason))
		}
		if t.Status == "completed" && h != nil && h.Status == "ask" && h.Decision != nil {
			return e.raiseAgentDecision(ctx, tx, c, t, *h.Decision, fit, e.now())
		}
		return e.runFailed(ctx, tx, c, t)
	})
}

// runHandIn is the hand-in a run recorded, and whether the run already
// declared its column done.
func runHandIn(ctx context.Context, tx *store.Tx, run db.AgentRun) (*handInBody, bool, error) {
	recs, err := tx.Q.AgentRunRecords(ctx, db.AgentRunRecordsParams{WorkspaceID: tx.WS, AgentRunID: &run.ID})
	if err != nil {
		return nil, false, err
	}
	var h *handInBody
	var summary, hasAttempt bool
	for _, r := range recs {
		if r.Kind == "hand_in" && records.Body[handInRecord](r).Attempt > 0 {
			hasAttempt = true
		}
		if r.SupersededAt != nil {
			continue
		}
		switch r.Kind {
		case "summary":
			summary = true
		case "hand_in":
			rec := records.Body[handInRecord](r)
			if rec.HandIn.BlockedByEnvironment != nil && (rec.Attempt < 1 || rec.Attempt != run.Attempt) {
				continue
			}
			if rec.HandIn.BlockedByEnvironment == nil && run.Attempt != 0 && rec.Attempt != 0 && rec.Attempt != run.Attempt {
				continue
			}
			b := rec.HandIn
			b.EnvironmentFailures = rec.EnvironmentFailures
			h = &b
		}
	}
	// A non-done current hand-in cannot have produced a summary. Older
	// summaries have no attempt field, so never let one mask that hand-in.
	return h, summary && ((h != nil && h.Status == "done") || (h == nil && !hasAttempt)), nil
}

// handInRecord is the part of a hand_in record the engine reads.
type handInRecord struct {
	HandIn              handInBody `json:"hand_in"`
	Attempt             int32      `json:"attempt"`
	EnvironmentFailures []string   `json:"environment_failures"`
}

type handInBody struct {
	Status               string             `json:"status"`
	Reason               *string            `json:"reason"`
	Decision             *handin.DecisionIn `json:"decision"`
	BlockedByEnvironment *string            `json:"blocked_by_environment"`
	EnvironmentFailures  []string           `json:"-"`
}

func (e *Engine) holdEnvironment(ctx context.Context, tx *store.Tx, c db.Card, reason string) error {
	if err := tx.Q.SaveCardFlow(ctx, db.SaveCardFlowParams{ID: c.ID, WorkspaceID: tx.WS, BoardColumn: c.BoardColumn, State: "blocked",
		Round: c.Round, Redos: c.Redos, HandoffNote: c.HandoffNote}); err != nil {
		return err
	}
	if err := tx.Q.InsertEnvironmentHold(ctx, db.InsertEnvironmentHoldParams{WorkspaceID: tx.WS, CardID: c.ID, UserID: c.OwnerID, Text: couldNotDoText(c.Number, reason)}); err != nil {
		return err
	}
	return tx.Emit(ctx, "card.steered", &c.ID, store.System, map[string]any{"column": c.BoardColumn, "state": "blocked"})
}

// askOwner holds a study whose lead could not do its column, as a block
// holds it but with the redo count unchanged, and asks its owner once; the
// owner's comment restarts the column through unblock.
func (e *Engine) askOwner(ctx context.Context, tx *store.Tx, c db.Card, reason string) error {
	if err := tx.Q.SaveCardFlow(ctx, db.SaveCardFlowParams{ID: c.ID, WorkspaceID: tx.WS, BoardColumn: c.BoardColumn, State: "blocked",
		Round: c.Round, Redos: c.Redos, HandoffNote: c.HandoffNote}); err != nil {
		return err
	}
	if err := holdForDecision(ctx, tx, c, "needs_decision", "could_not_do", couldNotDoText(c.Number, reason), e.now()); err != nil {
		return err
	}
	if err := tx.Emit(ctx, "inbox.created", &c.ID, store.System, map[string]any{"card_id": c.ID}); err != nil {
		return err
	}
	return tx.Emit(ctx, "card.steered", &c.ID, store.System, map[string]any{"column": c.BoardColumn, "state": "blocked"})
}

// runFailed redoes a column whose lead's run failed, or ended without a
// hand-in, telling the redo what went wrong; a column already redone once is
// blocked instead, and its owner told why.
func (e *Engine) runFailed(ctx context.Context, tx *store.Tx, c db.Card, t db.AgentRun) error {
	detail := strings.TrimSpace(t.Error)
	if detail == "" {
		detail = "the run ended without a hand-in"
	}
	next := db.SaveCardFlowParams{ID: c.ID, WorkspaceID: tx.WS, BoardColumn: c.BoardColumn, State: c.State, Round: c.Round, Redos: c.Redos}
	if c.Redos == 0 {
		next.Redos, next.Round = 1, c.Round+1
		next.HandoffNote = "The last attempt at this column failed. Fix this first:\n" + detail
	} else {
		next.State = "blocked"
	}
	if err := tx.Q.SaveCardFlow(ctx, next); err != nil {
		return err
	}
	if next.State == "blocked" {
		if err := holdForDecision(ctx, tx, c, "blocked", "run_failed", runFailedText(c.Number, c.BoardColumn, ptrOr(t.FailureReason), detail), e.now()); err != nil {
			return err
		}
	}
	return tx.Emit(ctx, "card.steered", &c.ID, store.System, map[string]any{"column": next.BoardColumn, "state": next.State})
}

// OnComment restarts the column of a study blocked or held for its owner when
// the owner comments. Working studies also check for pending composer work.
// Like Kick it runs off the caller's request, after the comment commits.
func (e *Engine) OnComment(ws, card, author uuid.UUID, body string) {
	run := func() {
		if err := e.wakeOnComment(context.Background(), ws, card, author, body); err != nil {
			slog.Error("wake on comment", "card", card, "err", err)
		}
	}
	if e.Inline {
		run()
		return
	}
	go run()
}

func (e *Engine) wakeOnComment(ctx context.Context, ws, card, author uuid.UUID, body string) error {
	restart := false
	err := e.Store.InWorkspace(ctx, ws, uuid.Nil, func(tx *store.Tx) error {
		c, err := lockCard(ctx, tx, card)
		if err != nil {
			return err
		}
		if working(c) {
			restart = true
			return nil
		}
		restart, err = e.unblock(ctx, tx, c, author, body)
		return err
	})
	if err == nil && restart {
		e.Kick(ws, card)
	}
	return err
}

func ptrOr(s *string) string {
	if s == nil {
		return ""
	}
	return *s
}

func accessInterrupted(r db.AgentRun) bool {
	return r.Status == "cancelled" && ptrOr(r.CancelReason) == "runtime_access_revoked" ||
		r.Status == "failed" && ptrOr(r.FailureReason) == "runtime_access_denied"
}

// Resume restores the latest interrupted work for each role after its owner
// chooses a machine. Later work for that role already supersedes the interruption.
func (e *Engine) Resume(ctx context.Context, tx *store.Tx, c db.Card) error {
	if !working(c) {
		return nil
	}
	runs, err := tx.Q.RoundAgentRuns(ctx, db.RoundAgentRunsParams{WorkspaceID: tx.WS, CardID: c.ID, BoardColumn: c.BoardColumn, Round: c.Round})
	if err != nil {
		return err
	}
	seen := map[string]bool{}
	for i := len(runs) - 1; i >= 0; i-- {
		r := runs[i]
		// The objector and the rerun are the system's runs, with no agent
		// to restore.
		if seen[r.Role] || r.Role == "objector" || r.Role == "rerun" {
			continue
		}
		seen[r.Role] = true
		if !accessInterrupted(r) {
			continue
		}
		var m *pack.Manifest
		var a db.Agent
		if leads(r.Role) {
			_, m, a, err = e.worker(ctx, tx, c)
		} else {
			var packID uuid.UUID
			packID, m, err = columnTeam(ctx, tx, c.ID, c.BoardColumn)
			if err == nil {
				member := r.Role
				if r.Role == "auditor" {
					member = m.Auditor.Name
				}
				a, err = teamAgent(ctx, tx, packID, c.OwnerID, member)
			}
		}
		if err != nil {
			return err
		}
		p := r.Prompt
		var comments []db.Comment
		if p == "" {
			// Legacy runs may lack a prompt snapshot. Rebuild card context and
			// include any saved assignment without parsing human-facing text.
			p, comments, err = e.runPrompt(ctx, tx, c, r.Role, r.Assignment)
			if err != nil {
				return err
			}
		}
		runAssignment := r.Assignment
		if runAssignment == "" {
			runAssignment = assignment(c, "")
		}
		p += "\nResumed on another machine.\n"
		run, err := e.enqueueWithInput(ctx, tx, c, r.Role, m, a, p, runAssignment)
		if err != nil {
			return err
		}
		if r.Prompt != "" {
			if err := tx.CopyRunAttachmentSnapshot(ctx, r.ID, run.ID); err != nil {
				return err
			}
			if err := service.CopyRunCommentSnapshot(ctx, tx, r.ID, run); err != nil {
				return err
			}
		}
		if _, err := service.DeliverComments(ctx, tx, run, service.DeliveredNextRun, comments); err != nil {
			return err
		}
	}
	return nil
}
