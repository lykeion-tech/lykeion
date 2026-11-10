package report

import (
	"context"
	"errors"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"lykeion/server/internal/board"
	"lykeion/server/internal/store"
	db "lykeion/server/pkg/db/generated"
)

// Input is every recorded row a study's report is built from.
type Input struct {
	Card     db.Card
	Records  []db.Record
	Runs     []db.StudyRunsRow
	Comments []db.Comment
	Decided  []db.InboxItem
	// Agents names each agent that ran in the study, by agent id.
	Agents map[uuid.UUID]string

	// Build is the report's judging record, nil when none was raised.
	Build    *db.ReportBuild
	Concepts []db.ReportConcept
	Facts    []db.ReportFact
	Links    []db.ReportLink
	Checks   []db.ReportCheck
	// Machine names the runtime the report run waits for, while it is queued
	// or judging; "" when that runtime is gone.
	Machine string
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
	if in.Decided, err = tx.Q.StudyDecidedItems(ctx, db.StudyDecidedItemsParams{WorkspaceID: card.WorkspaceID, CardID: card.ID}); err != nil {
		return in, err
	}
	return in, loadJudged(ctx, tx, &in)
}

// loadJudged reads the report build and what the report run and Jev recorded.
func loadJudged(ctx context.Context, tx *store.Tx, in *Input) error {
	ws, card := in.Card.WorkspaceID, in.Card.ID
	b, err := tx.Q.GetReportBuild(ctx, db.GetReportBuildParams{WorkspaceID: ws, CardID: card})
	if errors.Is(err, pgx.ErrNoRows) {
		return nil
	}
	if err != nil {
		return err
	}
	in.Build = &b
	if in.Concepts, err = tx.Q.ListReportConcepts(ctx, db.ListReportConceptsParams{WorkspaceID: ws, CardID: card}); err != nil {
		return err
	}
	if in.Facts, err = tx.Q.ListReportFacts(ctx, db.ListReportFactsParams{WorkspaceID: ws, CardID: card}); err != nil {
		return err
	}
	if in.Links, err = tx.Q.ListReportLinks(ctx, db.ListReportLinksParams{WorkspaceID: ws, CardID: card}); err != nil {
		return err
	}
	if in.Checks, err = tx.Q.ListReportChecks(ctx, db.ListReportChecksParams{WorkspaceID: ws, CardID: card}); err != nil {
		return err
	}
	if (b.Status == "queued" || b.Status == "judging") && b.AgentRunID != nil {
		run, err := tx.Q.GetAgentRun(ctx, db.GetAgentRunParams{WorkspaceID: ws, ID: *b.AgentRunID})
		if err != nil && !errors.Is(err, pgx.ErrNoRows) {
			return err
		}
		if err == nil && run.PinnedRuntimeID != nil {
			rt, err := tx.Q.GetRuntime(ctx, db.GetRuntimeParams{WorkspaceID: ws, ID: *run.PinnedRuntimeID})
			if err != nil && !errors.Is(err, pgx.ErrNoRows) {
				return err
			}
			if err == nil {
				in.Machine = rt.DeviceName
				if rt.CustomName != nil && *rt.CustomName != "" {
					in.Machine = *rt.CustomName
				}
			}
		}
	}
	return nil
}

// scope is board.Stages from the card's scope_from to its scope_to.
func scope(c db.Card) []string {
	from, to := board.StageIndex(c.ScopeFrom), board.StageIndex(c.ScopeTo)
	if from < 0 || to < from {
		return nil
	}
	return append([]string(nil), board.Stages[from:to+1]...)
}
