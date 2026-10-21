package flow

import (
	"context"
	"crypto/rand"
	"errors"
	"math/big"
	"time"

	"github.com/jackc/pgx/v5"

	"lykeion/server/internal/store"
	db "lykeion/server/pkg/db/generated"
)

// A finished Team column is offered for rating with a chance of one in
// ratingChance, and a researcher is offered at most one rating per
// ratingCooldown in a workspace.
const (
	ratingChance   = 5
	ratingCooldown = 7 * 24 * time.Hour
)

func defaultRatingDraw() bool {
	n, err := rand.Int(rand.Reader, big.NewInt(ratingChance))
	return err == nil && n.Int64() == 0
}

// drawRating draws once for the column the study is leaving, the first time
// it leaves it, and records the draw. It reads only who owns the study and
// which Team worked the column, never how the column went.
func (e *Engine) drawRating(ctx context.Context, tx *store.Tx, c db.Card) error {
	drawn, err := tx.Q.RatingDrawn(ctx, db.RatingDrawnParams{WorkspaceID: tx.WS, CardID: &c.ID, BoardColumn: c.BoardColumn})
	if err != nil || drawn {
		return err
	}
	pack, err := tx.Q.GetCardTeamPack(ctx, db.GetCardTeamPackParams{WorkspaceID: tx.WS, CardID: c.ID, BoardColumn: c.BoardColumn})
	if errors.Is(err, pgx.ErrNoRows) {
		return nil
	}
	if err != nil {
		return err
	}
	draw := e.RatingDraw
	if draw == nil {
		draw = defaultRatingDraw
	}
	now := e.now()
	offered := draw()
	if offered {
		recent, err := tx.Q.OfferedSince(ctx, db.OfferedSinceParams{WorkspaceID: tx.WS, UserID: c.OwnerID, Since: now.Add(-ratingCooldown)})
		if err != nil {
			return err
		}
		offered = !recent
	}
	return tx.Q.InsertRatingDraw(ctx, db.InsertRatingDrawParams{WorkspaceID: tx.WS, CardID: &c.ID, BoardColumn: c.BoardColumn,
		PackID: pack, UserID: c.OwnerID, Offered: offered, DrawnAt: now})
}
