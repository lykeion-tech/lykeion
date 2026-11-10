package report

import (
	"context"

	"github.com/google/uuid"

	"lykeion/server/internal/board"
	"lykeion/server/internal/store"
	db "lykeion/server/pkg/db/generated"
)

// Input is every recorded row a study's report is built from.
type Input struct {
	Card     db.Card
	Records  []db.Record
	Runs     []db.AgentRun
	Comments []db.Comment
	Decided  []db.InboxItem
	// Agents names each agent that ran in the study, by agent id.
	Agents map[uuid.UUID]string
}

// Load reads the live records of every stage in the card's scope, its runs
// and their agents' names, its comments and its decided Inbox items.
func Load(ctx context.Context, tx *store.Tx, card db.Card) (Input, error) {
	in := Input{Card: card, Agents: map[uuid.UUID]string{}}
	for _, stage := range scope(card) {
		recs, err := tx.Q.ListRecords(ctx, db.ListRecordsParams{WorkspaceID: card.WorkspaceID, CardID: card.ID, BoardColumn: stage})
		if err != nil {
			return in, err
		}
		in.Records = append(in.Records, recs...)
	}
	var err error
	if in.Runs, err = tx.Q.StudyRuns(ctx, db.StudyRunsParams{WorkspaceID: card.WorkspaceID, CardID: card.ID}); err != nil {
		return in, err
	}
	agents, err := tx.Q.StudyRunAgents(ctx, db.StudyRunAgentsParams{WorkspaceID: card.WorkspaceID, CardID: card.ID})
	if err != nil {
		return in, err
	}
	for _, a := range agents {
		in.Agents[a.ID] = a.Name
	}
	if in.Comments, err = tx.Q.ListComments(ctx, db.ListCommentsParams{WorkspaceID: card.WorkspaceID, CardID: card.ID}); err != nil {
		return in, err
	}
	in.Decided, err = tx.Q.StudyDecidedItems(ctx, db.StudyDecidedItemsParams{WorkspaceID: card.WorkspaceID, CardID: card.ID})
	return in, err
}

// scope is board.Stages from the card's scope_from to its scope_to.
func scope(c db.Card) []string {
	from, to := board.StageIndex(c.ScopeFrom), board.StageIndex(c.ScopeTo)
	if from < 0 || to < from {
		return nil
	}
	return append([]string(nil), board.Stages[from:to+1]...)
}
