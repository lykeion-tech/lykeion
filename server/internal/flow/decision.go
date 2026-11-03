package flow

import (
	"context"
	"time"

	"lykeion/server/internal/service"
	"lykeion/server/internal/store"
	db "lykeion/server/pkg/db/generated"
)

// holdForDecision raises the decision a study's stop asks its owner.
func holdForDecision(ctx context.Context, tx *store.Tx, c db.Card, kind, cause, text string, now time.Time) error {
	return service.HoldForDecision(ctx, tx, c, kind, cause, text, now)
}
