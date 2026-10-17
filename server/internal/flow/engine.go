// Package flow advances studies through their columns.
package flow

import (
	"context"
	"errors"
	"time"

	"github.com/google/uuid"

	"lykeion/server/internal/service"
	"lykeion/server/internal/storage"
	"lykeion/server/internal/store"
	db "lykeion/server/pkg/db/generated"
)

// Engine moves studies along. Until the flow task fills it, it moves nothing.
type Engine struct {
	Store     *store.Store
	AgentRuns *service.AgentRuns
	Blobs     storage.Storage
	JevKey    string // the lab's TypeSafe key; it never leaves the server
	JevURL    string
	Now       func() time.Time
	Inline    bool // run kicks in the caller's goroutine; tests use it to stay deterministic
}

func (e *Engine) Kick(ws, card uuid.UUID) {}

func (e *Engine) Tick(ctx context.Context) error { return nil }

func (e *Engine) OnSettled(_ context.Context, t db.AgentRun) {}

// ErrPackUnavailable wraps a failure to load a card's Team.
var ErrPackUnavailable = errors.New("the card's Team cannot be loaded")

// KindsFor is what a card's roles may write in its column. No role writes a
// record kind yet.
func KindsFor(context.Context, *store.Tx, storage.Storage, db.Card) (map[string]service.Kind, error) {
	return map[string]service.Kind{}, nil
}
