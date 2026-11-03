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
	"lykeion/server/internal/handin"
	"lykeion/server/internal/service"
	"lykeion/server/internal/store"
	db "lykeion/server/pkg/db/generated"
)

// holdForDecision raises the decision a study's stop asks its owner.
func holdForDecision(ctx context.Context, tx *store.Tx, c db.Card, kind, cause, text string, now time.Time) error {
	return service.HoldForDecision(ctx, tx, c, kind, cause, text, now)
}

var (
	ErrNotAnswerer    = errors.New("only the study's owner or a workspace owner or admin may answer this decision")
	ErrDecisionClosed = errors.New("this decision is no longer open")
	ErrNotWaiting     = errors.New("the study is no longer waiting on this decision")
)

// OptionError is an answer the decision does not accept.
type OptionError string

func (e OptionError) Error() string { return string(e) }

// Decide answers an open decision with one of its options. An agent's
// question resumes its lead's session with the answer. A stop's option is
// applied to the study: redo restarts the column with guidance as its note,
// close ends the study as a negative result, park leaves it blocked until
// someone redoes it and cancels its column's open runs, and try_again
// releases the runs its machines kept failing. The item is locked and
// rechecked under the study's lock, so of two answers at once, or an answer
// and its expiry, only the first applies; the other answer gets
// ErrDecisionClosed.
func (e *Engine) Decide(ctx context.Context, ws, item, user uuid.UUID, option, guidance string) error {
	guidance = strings.TrimSpace(guidance)
	restart := false
	var card uuid.UUID
	var cancel []uuid.UUID
	err := e.Store.InWorkspace(ctx, ws, user, func(tx *store.Tx) error {
		// The workspace, then the membership, as every member write takes them.
		if _, err := tx.Q.LockWorkspaceForMutation(ctx, ws); err != nil {
			return err
		}
		m, err := tx.Q.LockMember(ctx, db.LockMemberParams{WorkspaceID: ws, UserID: user})
		if err != nil {
			return err
		}
		row, err := tx.Q.GetDecision(ctx, db.GetDecisionParams{ID: item, WorkspaceID: ws, Viewer: user, IsAdmin: m.Role == "owner" || m.Role == "admin"})
		if err != nil {
			return err
		}
		if !row.MayAnswer {
			return ErrNotAnswerer
		}
		if row.InboxItem.CardID == nil {
			return ErrNotWaiting
		}
		card = *row.InboxItem.CardID
		// The study before its item, as the engine's stops take them.
		c, err := lockCard(ctx, tx, card)
		if err != nil {
			return err
		}
		it, err := tx.Q.LockDecision(ctx, db.LockDecisionParams{ID: item, WorkspaceID: ws})
		if err != nil {
			return err
		}
		if it.Status != "open" || it.ResolvedAt != nil || it.ArchivedAt != nil {
			return ErrDecisionClosed
		}
		var options []service.Option
		if err := json.Unmarshal(it.Options, &options); err != nil {
			return err
		}
		if !slices.ContainsFunc(options, func(o service.Option) bool { return o.ID == option }) {
			return OptionError("this decision has no option " + option)
		}
		agent := it.Kind == "agent_decision"
		if !agent && option == "redo" && guidance == "" {
			return OptionError("redo needs guidance")
		}
		if c.ID == uuid.Nil || c.Ending != "" || agent && !waitsOnQuestion(c) {
			return ErrNotWaiting
		}
		decided, err := tx.Q.DecideItem(ctx, db.DecideItemParams{ID: item, WorkspaceID: ws, ChosenOption: option, Guidance: guidance, DecidedBy: user})
		if err != nil {
			return err
		}
		if err := tx.Emit(ctx, "inbox.resolved", &card, store.User(user), map[string]string{"inbox_item_id": item.String()}); err != nil {
			return err
		}
		if agent {
			err = e.resumeAfterDecision(ctx, tx, c, decided)
		} else {
			restart, cancel, err = applyOption(ctx, tx, c, user, option, guidance)
		}
		if err != nil || restart {
			// redo emits card.steered itself.
			return err
		}
		return emitSteered(ctx, tx, card, store.User(user))
	})
	if err != nil {
		return err
	}
	if restart {
		e.Kick(ws, card)
	}
	return e.cancelParked(ctx, ws, user, cancel)
}

// waitsOnQuestion is whether the study is blocked on its lead's question.
func waitsOnQuestion(c db.Card) bool {
	return c.State == "blocked" && c.BlockKind == "decision"
}

// applyOption applies a stop's option to its locked study, for user, or for
// no one when the stop expired. It reports whether the column restarts and
// which runs to cancel once the caller commits.
func applyOption(ctx context.Context, tx *store.Tx, c db.Card, user uuid.UUID, option, guidance string) (bool, []uuid.UUID, error) {
	actor := store.System
	if user != uuid.Nil {
		actor = store.User(user)
	}
	switch option {
	case "redo":
		// redo emits card.steered itself.
		restart, err := redo(ctx, tx, c, user, guidance)
		if err == nil && !restart {
			err = ErrNotWaiting
		}
		return restart, nil, err
	case "close":
		why := guidance
		if why == "" {
			why = c.Title
		}
		s := board.FromCard(c)
		board.Stop(&s, "closed as a negative result: "+why)
		if err := tx.Q.SaveCardBoard(ctx, s.Params(c.ID, tx.WS)); err != nil {
			return false, nil, err
		}
		return false, nil, service.EndStudyItems(ctx, tx, c.ID, actor)
	case "park":
		s := board.FromCard(c)
		board.Park(&s)
		if err := tx.Q.SaveCardBoard(ctx, s.Params(c.ID, tx.WS)); err != nil {
			return false, nil, err
		}
		// A parked study stops: its column's open runs, held ones
		// included, are cancelled once this commits.
		runs, err := tx.Q.RoundAgentRuns(ctx, db.RoundAgentRunsParams{WorkspaceID: tx.WS, CardID: c.ID, BoardColumn: c.BoardColumn, Round: c.Round})
		if err != nil {
			return false, nil, err
		}
		var cancel []uuid.UUID
		for _, r := range runs {
			if slices.Contains([]string{"queued", "dispatched", "running"}, r.Status) {
				cancel = append(cancel, r.ID)
			}
		}
		return false, cancel, nil
	case "try_again":
		return false, nil, service.ReleaseParkedRuns(ctx, tx, c.ID)
	}
	return false, nil, OptionError("option " + option + " cannot be applied")
}

// emitSteered tells the board where the study now stands.
func emitSteered(ctx context.Context, tx *store.Tx, card uuid.UUID, actor store.Actor) error {
	next, err := tx.Q.GetCard(ctx, db.GetCardParams{WorkspaceID: tx.WS, ID: card})
	if err != nil {
		return err
	}
	return tx.Emit(ctx, "card.steered", &card, actor, map[string]any{"column": next.BoardColumn, "state": next.State, "ending": next.Ending})
}

// cancelParked cancels a parked study's runs after the park commits, as
// steering does, each through the agent-run cancel path so its settlement is
// recorded. user is who the cancellations are recorded for.
func (e *Engine) cancelParked(ctx context.Context, ws, user uuid.UUID, runs []uuid.UUID) error {
	for _, run := range runs {
		if err := e.AgentRuns.Cancel(ctx, ws, user, run); err != nil && !errors.Is(err, service.ErrConflict) {
			return fmt.Errorf("the study was parked; cancelling its run %s failed: %w", run, err)
		}
	}
	return nil
}

// resumeAfterDecision wakes the lead that asked item, once it is answered or
// expired: the study works again in the same round, so the lead's next run
// has the asking run's continuity scope and resumes its session, with the
// outcome as its note.
func (e *Engine) resumeAfterDecision(ctx context.Context, tx *store.Tx, c db.Card, item db.InboxItem) error {
	title, _, _ := strings.Cut(item.Text, "\n")
	note := "Your question \"" + title + "\" expired without an answer. Continue without it."
	if item.Status == "decided" {
		var options []service.Option
		if err := json.Unmarshal(item.Options, &options); err != nil {
			return err
		}
		label := item.ChosenOption
		if i := slices.IndexFunc(options, func(o service.Option) bool { return o.ID == item.ChosenOption }); i >= 0 {
			label = options[i].Label
		}
		note = "The researcher answered your question \"" + title + "\": " + label
		if item.Guidance != "" {
			note += " — " + item.Guidance
		}
	}
	if err := tx.Q.SaveCardFlow(ctx, db.SaveCardFlowParams{ID: c.ID, WorkspaceID: tx.WS, BoardColumn: c.BoardColumn, State: "working",
		BlockKind: "", Round: c.Round, Redos: c.Redos, HandoffNote: c.HandoffNote}); err != nil {
		return err
	}
	c.State, c.BlockKind = "working", ""
	role, m, a, err := e.worker(ctx, tx, c)
	if err != nil {
		return err
	}
	_, err = e.enqueue(ctx, tx, c, role, m, a, note)
	return err
}

// sweepDecisions expires the decisions due by now, reminds once the day
// before a decision expires, and archives history untouched for 90 days. An
// expiring decision is locked and rechecked under its study's lock, as
// Decide takes them, so an answer and its expiry close it once between them.
func (e *Engine) sweepDecisions(ctx context.Context, now time.Time) error {
	var errs []error
	due, err := e.Store.Global().DueDecisions(ctx, now)
	if err != nil {
		return err
	}
	for _, d := range due {
		if err := e.expire(ctx, d, now); err != nil {
			errs = append(errs, fmt.Errorf("expire decision %s: %w", d.ID, err))
		}
	}
	remind, err := e.Store.Global().DueReminders(ctx, now)
	if err != nil {
		return errors.Join(append(errs, err)...)
	}
	for _, r := range remind {
		err := e.Store.InWorkspace(ctx, r.WorkspaceID, uuid.Nil, func(tx *store.Tx) error {
			card, err := tx.Q.MarkReminded(ctx, db.MarkRemindedParams{At: now, ID: r.ID, WorkspaceID: r.WorkspaceID})
			if errors.Is(err, pgx.ErrNoRows) {
				return nil
			}
			if err != nil {
				return err
			}
			return tx.Emit(ctx, "decision.reminder", card, store.System, map[string]string{"inbox_item_id": r.ID.String()})
		})
		if err != nil {
			errs = append(errs, fmt.Errorf("remind decision %s: %w", r.ID, err))
		}
	}
	if err := e.Store.Global().ArchiveStale(ctx, now); err != nil {
		errs = append(errs, err)
	}
	return errors.Join(errs...)
}

// expire closes one due decision: a stop applies its default option for no
// one, and an agent's question wakes its lead without an answer.
func (e *Engine) expire(ctx context.Context, d db.DueDecisionsRow, now time.Time) error {
	var cancel []uuid.UUID
	var owner uuid.UUID
	err := e.Store.InWorkspace(ctx, d.WorkspaceID, uuid.Nil, func(tx *store.Tx) error {
		if _, err := tx.Q.LockWorkspaceForMutation(ctx, d.WorkspaceID); err != nil {
			return err
		}
		var c db.Card
		if d.CardID != uuid.Nil {
			var err error
			if c, err = lockCard(ctx, tx, d.CardID); err != nil {
				return err
			}
		}
		it, err := tx.Q.LockDecision(ctx, db.LockDecisionParams{ID: d.ID, WorkspaceID: d.WorkspaceID})
		if err != nil {
			return err
		}
		if it.Status != "open" || it.ResolvedAt != nil || it.ExpiresAt == nil || it.ExpiresAt.After(now) {
			return nil
		}
		expired, err := tx.Q.ExpireDecision(ctx, db.ExpireDecisionParams{ID: d.ID, WorkspaceID: d.WorkspaceID})
		if err != nil {
			return err
		}
		if err := tx.Emit(ctx, "inbox.resolved", it.CardID, store.System, map[string]string{"inbox_item_id": d.ID.String()}); err != nil {
			return err
		}
		// A study already stopped, or no longer waiting on the question,
		// only has its item closed.
		if c.ID == uuid.Nil || c.Ending != "" {
			return nil
		}
		switch {
		case it.Kind == "agent_decision":
			if !waitsOnQuestion(c) {
				return nil
			}
			err = e.resumeAfterDecision(ctx, tx, c, expired)
		case it.DefaultOption != "":
			owner = c.OwnerID
			_, cancel, err = applyOption(ctx, tx, c, uuid.Nil, it.DefaultOption, "")
		default:
			return nil
		}
		if err != nil {
			return err
		}
		return emitSteered(ctx, tx, c.ID, store.System)
	})
	if err != nil {
		return err
	}
	// Cancelling needs a member; the study's owner, who the decision was
	// for, stands for the expired default.
	return e.cancelParked(ctx, d.WorkspaceID, owner, cancel)
}

// errDecisionOpen is the failed hand-in of a lead that asks while its study
// already waits on one of its questions.
const errDecisionOpen = "one decision is already open on this study"

// errDecideTwice is the failed hand-in of a lead whose question Jev sent
// back once already in this round.
const errDecideTwice = "Jev judged two questions in a row as the agent's own to decide"

// decideYourself introduces the note that sends a question back to its lead.
const decideYourself = "Decide this yourself: "

// fitQuestion is what Jev is asked of an agent's question before it reaches
// the researcher.
const fitQuestion = "Does this question belong to the kind it is tagged with, and is it one only the researcher can answer?"

// Jev's verdicts on an agent's question; unavailable is no Jev, or a Jev
// that failed, and leaves the server's checks alone.
const (
	fitUnavailable = ""
	fitFits        = "fits"
	fitDecide      = "decide_yourself"
)

var fitOptions = map[string]string{
	fitFits:   "It is of its tagged kind (scope, change_question, ethics, cost or access), and only the researcher can settle it.",
	fitDecide: "It is not of its tagged kind, or it is a choice of method, tools, analysis, evidence or a retry that the agent can make itself.",
}

// fitCheck asks Jev whether an agent's question is the researcher's to
// answer. It runs with no transaction open.
func (e *Engine) fitCheck(ctx context.Context, card uuid.UUID, d handin.DecisionIn) string {
	a, err := e.Jev.Choice(ctx, decisionState(d), fitQuestion, fitOptions)
	if err != nil {
		slog.Warn("jev fit check failed; raising the decision on the server's checks", "card", card, "err", err)
		return fitUnavailable
	}
	return a.Choice
}

// raiseAgentDecision puts a lead's question to the study's owner: the study
// is blocked on it in the same round, so the lead resumes its session once
// it is answered. A study waits on one question at a time, so a second ask
// fails the hand-in. When Jev judged the question not the researcher's, the
// lead runs again in the same round and decides it itself; a second such
// question in the round fails the hand-in.
func (e *Engine) raiseAgentDecision(ctx context.Context, tx *store.Tx, c db.Card, run db.AgentRun, d handin.DecisionIn, fit string, now time.Time) error {
	open, err := tx.Q.HasOpenAgentDecision(ctx, db.HasOpenAgentDecisionParams{WorkspaceID: tx.WS, CardID: c.ID})
	if err != nil {
		return err
	}
	if open {
		run.Error = errDecisionOpen
		return e.runFailed(ctx, tx, c, run)
	}
	if fit == fitDecide {
		runs, err := tx.Q.RoundAgentRuns(ctx, db.RoundAgentRunsParams{WorkspaceID: tx.WS, CardID: c.ID, BoardColumn: c.BoardColumn, Round: c.Round})
		if err != nil {
			return err
		}
		// The runs a question was sent back by carry the note as their assignment.
		if slices.ContainsFunc(runs, func(r db.AgentRun) bool { return leads(r.Role) && strings.HasPrefix(r.Assignment, decideYourself) }) {
			run.Error = errDecideTwice
			return e.runFailed(ctx, tx, c, run)
		}
		role, m, ag, err := e.worker(ctx, tx, c)
		if err != nil {
			return err
		}
		_, err = e.enqueue(ctx, tx, c, role, m, ag, decideYourself+d.Title)
		return err
	}
	options := make([]service.Option, len(d.Options))
	for i, o := range d.Options {
		options[i] = service.Option{ID: o.ID, Label: o.Label, Consequence: o.Consequence}
	}
	raw, err := json.Marshal(options)
	if err != nil {
		return err
	}
	expires := now.Add(service.DecisionWindow)
	if d.ExpiresAt != nil {
		expires = *d.ExpiresAt
	}
	if err := tx.Q.InsertAgentDecision(ctx, db.InsertAgentDecisionParams{WorkspaceID: tx.WS, CardID: c.ID, UserID: c.OwnerID,
		Text: d.Title + "\n\n" + d.Body, Options: raw, ExpiresAt: &expires, RaisedRunID: run.ID, FromMember: ptrOr(d.FromMember), DecisionKind: d.Kind}); err != nil {
		return err
	}
	if err := tx.Q.SaveCardFlow(ctx, db.SaveCardFlowParams{ID: c.ID, WorkspaceID: tx.WS, BoardColumn: c.BoardColumn, State: "blocked",
		BlockKind: "decision", Round: c.Round, Redos: c.Redos}); err != nil {
		return err
	}
	if err := tx.Emit(ctx, "inbox.created", &c.ID, store.System, map[string]any{"card_id": c.ID}); err != nil {
		return err
	}
	return tx.Emit(ctx, "card.steered", &c.ID, store.System, map[string]any{"column": c.BoardColumn, "state": "blocked"})
}

// decisionState is an agent's question as Jev reads it.
func decisionState(d handin.DecisionIn) string {
	var b strings.Builder
	fmt.Fprintf(&b, "Kind: %s\nTitle: %s\n\n%s\n\nOptions:\n", d.Kind, d.Title, d.Body)
	for _, o := range d.Options {
		fmt.Fprintf(&b, "- %s: %s\n", o.Label, o.Consequence)
	}
	return b.String()
}
