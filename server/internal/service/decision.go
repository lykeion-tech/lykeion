package service

import (
	"context"
	"encoding/json"
	"time"

	"github.com/google/uuid"

	"lykeion/server/internal/store"
	db "lykeion/server/pkg/db/generated"
)

// DecisionWindow is how long a person has to answer a stop before its
// default option applies.
const DecisionWindow = 7 * 24 * time.Hour

// Option is one way a person can answer a decision.
type Option struct {
	ID          string `json:"id"`
	Label       string `json:"label"`
	Consequence string `json:"consequence"`
}

var (
	optRedo     = Option{"redo", "Redo with guidance", "The column runs again with your guidance as its brief."}
	optClose    = Option{"close", "Close as a negative result", "The study ends here and is recorded as a negative result."}
	optPark     = Option{"park", "Park", "The study stops here; anyone who can open it may redo it later."}
	optTryAgain = Option{"try_again", "Try again", "The parked runs are queued again on their machine."}
)

// StopOptions are the answers to a study's stop: parked runs can be tried
// again, any other stop can be redone, closed or parked.
func StopOptions(cause string) []Option {
	if cause == "machine_failures" {
		return []Option{optTryAgain, optPark}
	}
	return []Option{optRedo, optClose, optPark}
}

// HoldForDecision writes the open decision a study's stop asks its owner:
// one per study and cause, whose default, parking the study, applies after
// DecisionWindow.
func HoldForDecision(ctx context.Context, tx *store.Tx, c db.Card, kind, cause, text string, now time.Time) error {
	options, err := json.Marshal(StopOptions(cause))
	if err != nil {
		return err
	}
	expires := now.Add(DecisionWindow)
	return tx.Q.UpsertStopDecision(ctx, db.UpsertStopDecisionParams{WorkspaceID: tx.WS, CardID: c.ID, UserID: c.OwnerID,
		Kind: kind, Text: text, Cause: cause, Options: options, DefaultOption: "park", ExpiresAt: &expires})
}

// EndStudyItems closes what a stopped study asked of people: its open
// decisions are cancelled, and its other items waiting on a person resolved.
func EndStudyItems(ctx context.Context, tx *store.Tx, card uuid.UUID, who store.Actor) error {
	cancelled, err := tx.Q.CancelCardDecisions(ctx, db.CancelCardDecisionsParams{WorkspaceID: tx.WS, CardID: card})
	if err != nil {
		return err
	}
	resolved, err := tx.Q.ResolveCardInbox(ctx, db.ResolveCardInboxParams{WorkspaceID: tx.WS, CardID: &card})
	if err != nil {
		return err
	}
	for _, id := range append(cancelled, resolved...) {
		if err := tx.Emit(ctx, "inbox.resolved", &card, who, map[string]string{"inbox_item_id": id.String()}); err != nil {
			return err
		}
	}
	return nil
}
