package flow

import (
	"context"
	"fmt"

	"lykeion/server/internal/store"
	db "lykeion/server/pkg/db/generated"
)

// finishStudy tells people a study ended well. A Workflow study tells only its
// owner it is done; any other study gives each participant, the owner and
// whoever commented or decided on it, a report to read.
func finishStudy(ctx context.Context, tx *store.Tx, c db.Card) error {
	if c.Workflow != nil {
		return tx.Q.InsertInboxItem(ctx, db.InsertInboxItemParams{WorkspaceID: tx.WS, CardID: c.ID, UserID: c.OwnerID, Kind: "done",
			Text: doneText(c.Number, c.BoardColumn)})
	}
	users, err := tx.Q.StudyParticipants(ctx, db.StudyParticipantsParams{WorkspaceID: tx.WS, CardID: c.ID})
	if err != nil {
		return err
	}
	for _, u := range users {
		if err := tx.Q.InsertInboxItem(ctx, db.InsertInboxItemParams{WorkspaceID: tx.WS, CardID: c.ID, UserID: u, Kind: "study_report",
			Text: fmt.Sprintf("Study #%d's report is ready.", c.Number)}); err != nil {
			return err
		}
	}
	return nil
}
