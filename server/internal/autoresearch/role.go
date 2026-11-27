package autoresearch

import (
	"context"
	"errors"
	"fmt"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"lykeion/server/internal/store"
	db "lykeion/server/pkg/db/generated"
)

var ErrReported = errors.New("this run already posted its report")

// Report posts a run_only agent run's one report on its run.
func (s *Service) Report(ctx context.Context, tx *store.Tx, ar uuid.UUID, text string) error {
	r, err := tx.Q.RunByAgentRun(ctx, db.RunByAgentRunParams{WorkspaceID: tx.WS, AgentRunID: ar})
	if err != nil {
		return err
	}
	if r.Report != "" {
		return ErrReported
	}
	ap, err := tx.Q.GetAutoresearch(ctx, db.GetAutoresearchParams{WorkspaceID: tx.WS, ID: r.AutoresearchID})
	if err != nil {
		return err
	}
	if err := tx.Q.SetRunReport(ctx, db.SetRunReportParams{Report: text, WorkspaceID: tx.WS, ID: r.ID}); err != nil {
		return err
	}
	return tx.Emit(ctx, "autoresearch_run.reported", nil, store.Actor{Type: "role", ID: ar},
		map[string]any{"run_id": r.ID, "autoresearch_id": ap.ID})
}

// OpenCardForAgentRun opens a study for the run's acting user. Slack drafts
// instead save a proposal for that user to start later.
func (s *Service) OpenCardForAgentRun(ctx context.Context, tx *store.Tx, ar uuid.UUID, title, question string) (db.Card, error) {
	if err := lockWorkspace(ctx, tx); err != nil {
		return db.Card{}, err
	}
	r, err := tx.Q.RunByAgentRun(ctx, db.RunByAgentRunParams{WorkspaceID: tx.WS, AgentRunID: ar})
	if err != nil {
		return db.Card{}, err
	}
	ap, err := tx.Q.GetAutoresearch(ctx, db.GetAutoresearchParams{WorkspaceID: tx.WS, ID: r.AutoresearchID})
	if err != nil {
		return db.Card{}, err
	}
	draft, err := tx.LockStudyDraft(ctx, ap.ID)
	if err != nil {
		return db.Card{}, err
	}
	if draft != nil && draft.Completed {
		if draft.CardID != nil {
			return db.Card{ID: *draft.CardID}, ErrAlreadyActive
		}
		return db.Card{}, ErrAlreadyActive
	}
	if draft != nil && draft.SaveToTodo {
		pack, err := tx.Q.BuiltInStageTeam(ctx, db.BuiltInStageTeamParams{WorkspaceID: tx.WS, Stage: ap.ScopeFrom})
		if err != nil && !errors.Is(err, pgx.ErrNoRows) {
			return db.Card{}, err
		}
		assignee := store.ProposalAssignee{Type: "team"}
		if err == nil {
			assignee.PackID = &pack.ID
		}
		// Use the request ID as the proposal ID, and complete both in the same transaction.
		p, err := tx.WriteStudyProposal(ctx, ap.ID, r.ActingUserID, 0, store.ProposalContent{
			Title: title, Description: question, Stage: ap.ScopeFrom, Priority: draft.Priority, ProjectID: draft.ResearchID,
			Assignee: assignee,
		})
		if err != nil {
			return db.Card{}, err
		}
		if err := tx.CompleteStudyDraft(ctx, ap.ID, nil, *draft); err != nil {
			return db.Card{}, err
		}
		return db.Card{}, tx.Emit(ctx, "study_proposal.saved", nil, store.User(r.ActingUserID), map[string]any{"proposal_id": p.ID, "revision": p.Revision})
	}
	var agent *uuid.UUID
	if ap.AssigneeType != nil {
		a, err := s.assignedAgent(ctx, tx, ap, r.ActingUserID)
		if errors.Is(err, errNoAssignee) || errors.Is(err, pgx.ErrNoRows) {
			return db.Card{}, fmt.Errorf("%w: %w", ErrStartFailed, err)
		}
		if err != nil {
			return db.Card{}, err
		}
		agent = &a.ID
	}
	card, err := s.openCard(ctx, tx, ap, r, title, question, nil, agent, nil)
	if err != nil {
		return card, err
	}
	if draft != nil {
		if err := tx.CompleteStudyDraft(ctx, ap.ID, &card.ID, *draft); err != nil {
			return db.Card{}, err
		}
		card.ResearchID = draft.ResearchID
		card.Deadline = draft.Deadline
		card.Priority = draft.Priority
		if draft.ResearchID != nil {
			if err := tx.Emit(ctx, "card.properties_updated", &card.ID, store.System, map[string]any{"research_id": *draft.ResearchID}); err != nil {
				return db.Card{}, err
			}
		}
	}
	return card, nil
}
