package round

import (
	"context"
	"errors"

	"github.com/jackc/pgx/v5"
	"lykeion/server/internal/board"
	"lykeion/server/internal/fence"
	"lykeion/server/internal/ground"
	"lykeion/server/internal/store"
	db "lykeion/server/pkg/db/generated"
)

// triggerPayload gives only the Grounding scoper the card's stored webhook data.
func triggerPayload(ctx context.Context, tx *store.Tx, c db.Card, role string) (string, error) {
	if c.BoardColumn != board.Grounding || role != ground.Scoper {
		return "", nil
	}
	body, err := tx.Q.CardTriggerPayload(ctx, db.CardTriggerPayloadParams{WorkspaceID: tx.WS, CardID: c.ID})
	if errors.Is(err, pgx.ErrNoRows) {
		return "", nil
	}
	if err != nil {
		return "", err
	}
	return fence.Untrusted("The webhook payload that opened this card", string(body)), nil
}
