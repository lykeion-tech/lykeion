package flow

import (
	"context"
	"log/slog"
	"time"

	"github.com/google/uuid"

	"lykeion/server/internal/store"
	db "lykeion/server/pkg/db/generated"
)

type reviewActivityKey struct{}

type reviewActivity struct {
	engine    *Engine
	workspace uuid.UUID
	card      db.Card
	pass      uuid.UUID
	attempt   int
	started   bool
	invokedAt time.Time
	failed    bool
}

func (a *reviewActivity) emit(ctx context.Context, kind string, status string) error {
	return a.engine.Store.InWorkspace(ctx, a.workspace, uuid.Nil, func(tx *store.Tx) error {
		return tx.Emit(ctx, kind, &a.card.ID, store.System, map[string]any{
			"pass_id": a.pass.String(), "column": a.card.BoardColumn,
			"round": a.card.Round, "attempt": a.attempt, "status": status,
		})
	})
}

func markReviewStarted(ctx context.Context) {
	a, _ := ctx.Value(reviewActivityKey{}).(*reviewActivity)
	if a == nil || a.started {
		return
	}
	if a.invokedAt.IsZero() {
		a.invokedAt = time.Now().UTC()
	}
	if err := a.emit(ctx, "review.started", "reviewing"); err != nil {
		slog.Warn("could not record review start", "card", a.card.ID, "err", err)
		return
	}
	a.started = true
}

func markReviewFailed(ctx context.Context, err error) {
	if a, _ := ctx.Value(reviewActivityKey{}).(*reviewActivity); a != nil && err != nil {
		a.failed = true
	}
}

func (a *reviewActivity) finish(ctx context.Context, err error) {
	if !a.started {
		return
	}
	status := "completed"
	if err != nil || a.failed {
		status = "failed"
	}
	finishCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 2*time.Second)
	defer cancel()
	if err := a.emit(finishCtx, "review.finished", status); err != nil {
		slog.Warn("could not record review finish", "card", a.card.ID, "err", err)
	}
}
