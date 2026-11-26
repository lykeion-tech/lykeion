package flow

import (
	"context"
	"slices"

	"github.com/google/uuid"

	"lykeion/server/internal/board"
	"lykeion/server/internal/handin"
	"lykeion/server/internal/records"
	"lykeion/server/internal/review"
	"lykeion/server/internal/store"
	db "lykeion/server/pkg/db/generated"
)

func frozenComparisonRequiresAttestation(ctx context.Context, tx *store.Tx, card uuid.UUID) (bool, error) {
	study, err := tx.Q.ListRecords(ctx, db.ListRecordsParams{WorkspaceID: tx.WS, CardID: card, BoardColumn: board.Hypotheses})
	if err != nil {
		return false, err
	}
	return slices.ContainsFunc(handin.AcceptedHypotheses(study), func(r db.Record) bool {
		return records.Body[review.HypothesisBody](r).Comparison != nil
	}), nil
}
