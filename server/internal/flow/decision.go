package flow

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"strings"
	"time"

	"github.com/google/uuid"

	"lykeion/server/internal/board"
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
