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
	ErrAgentDecision  = errors.New("agent decisions are answered once resumption exists")
	ErrNotWaiting     = errors.New("the study is no longer waiting on this decision")
)

// OptionError is an answer the decision does not accept.
type OptionError string

func (e OptionError) Error() string { return string(e) }

// Decide answers an open decision with one of its options and applies it to
// the study: redo restarts the column with guidance as its note, close ends
// the study as a negative result, park leaves it blocked until someone redoes
// it and cancels its column's open runs, and try_again releases the runs its
// machines kept failing. The item is locked and rechecked under the study's
// lock, so of two answers at once only the first applies; the other gets
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
		if row.InboxItem.Kind == "agent_decision" {
			return ErrAgentDecision
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
		if option == "redo" && guidance == "" {
			return OptionError("redo needs guidance")
		}
		if c.ID == uuid.Nil || c.Ending != "" {
			return ErrNotWaiting
		}
		if _, err := tx.Q.DecideItem(ctx, db.DecideItemParams{ID: item, WorkspaceID: ws, ChosenOption: option, Guidance: guidance, DecidedBy: user}); err != nil {
			return err
		}
		if err := tx.Emit(ctx, "inbox.resolved", &card, store.User(user), map[string]string{"inbox_item_id": item.String()}); err != nil {
			return err
		}
		switch option {
		case "redo":
			// redo emits card.steered itself.
			restart, err = redo(ctx, tx, c, user, guidance)
			if err == nil && !restart {
				err = ErrNotWaiting
			}
			return err
		case "close":
			why := guidance
			if why == "" {
				why = c.Title
			}
			s := board.FromCard(c)
			board.Stop(&s, "closed as a negative result: "+why)
			if err := tx.Q.SaveCardBoard(ctx, s.Params(c.ID, ws)); err != nil {
				return err
			}
			if err := service.EndStudyItems(ctx, tx, card, store.User(user)); err != nil {
				return err
			}
		case "park":
			s := board.FromCard(c)
			board.Park(&s)
			if err := tx.Q.SaveCardBoard(ctx, s.Params(c.ID, ws)); err != nil {
				return err
			}
			// A parked study stops: its column's open runs, held ones
			// included, are cancelled once this commits.
			runs, err := tx.Q.RoundAgentRuns(ctx, db.RoundAgentRunsParams{WorkspaceID: ws, CardID: card, BoardColumn: c.BoardColumn, Round: c.Round})
			if err != nil {
				return err
			}
			for _, r := range runs {
				if slices.Contains([]string{"queued", "dispatched", "running"}, r.Status) {
					cancel = append(cancel, r.ID)
				}
			}
		case "try_again":
			if err := service.ReleaseParkedRuns(ctx, tx, card); err != nil {
				return err
			}
		default:
			return OptionError("option " + option + " cannot be applied")
		}
		next, err := tx.Q.GetCard(ctx, db.GetCardParams{WorkspaceID: ws, ID: card})
		if err != nil {
			return err
		}
		return tx.Emit(ctx, "card.steered", &card, store.User(user), map[string]any{"column": next.BoardColumn, "state": next.State, "ending": next.Ending})
	})
	if err != nil {
		return err
	}
	if restart {
		e.Kick(ws, card)
	}
	// As steering does, the cancellations run after the answer commits, each
	// through the agent-run cancel path so its settlement is recorded.
	for _, run := range cancel {
		if err := e.AgentRuns.Cancel(ctx, ws, user, run); err != nil && !errors.Is(err, service.ErrConflict) {
			return fmt.Errorf("the study was parked; cancelling its run %s failed: %w", run, err)
		}
	}
	return nil
}

// errDecisionOpen is the failed hand-in of a lead that asks while its study
// already waits on one of its questions.
const errDecisionOpen = "one decision is already open on this study"

// fitQuestion is what Jev is asked of an agent's question before it reaches
// the researcher.
const fitQuestion = "Does this question belong to the kind it is tagged with, and is it one only the researcher can answer?"

var fitOptions = map[string]string{
	"fits":            "It is of its tagged kind (scope, change_question, ethics, cost or access), and only the researcher can settle it.",
	"decide_yourself": "It is not of its tagged kind, or it is a choice of method, tools, analysis, evidence or a retry that the agent can make itself.",
}

// raiseAgentDecision puts a lead's question to the study's owner: the study
// is blocked on it in the same round, so the lead resumes its session once
// it is answered. A study waits on one question at a time, so a second ask
// fails the hand-in. When Jev judges the question not the researcher's, the
// lead runs again in the same round and decides it itself.
func (e *Engine) raiseAgentDecision(ctx context.Context, tx *store.Tx, c db.Card, run db.AgentRun, d handin.DecisionIn, now time.Time) error {
	open, err := tx.Q.HasOpenAgentDecision(ctx, db.HasOpenAgentDecisionParams{WorkspaceID: tx.WS, CardID: c.ID})
	if err != nil {
		return err
	}
	if open {
		run.Error = errDecisionOpen
		return e.runFailed(ctx, tx, c, run)
	}
	if e.Jev != nil {
		a, err := e.Jev.Choice(ctx, decisionState(d), fitQuestion, fitOptions)
		switch {
		case err != nil:
			// As with no Jev at all, the server's checks alone apply.
			slog.Warn("jev fit check failed; raising the decision on the server's checks", "card", c.ID, "err", err)
		case a.Choice == "decide_yourself":
			role, m, ag, err := e.worker(ctx, tx, c)
			if err != nil {
				return err
			}
			_, err = e.enqueue(ctx, tx, c, role, m, ag, "Decide this yourself: "+d.Title)
			return err
		}
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
