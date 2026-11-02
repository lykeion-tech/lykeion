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

func excerpt(s string, n int) string {
	r := []rune(s)
	if len(r) <= n {
		return s
	}
	return string(r[:n-1]) + "…"
}

// Report posts a run_only agent run's one report on its run, and gives the
// Autoresearch's owner and subscribers an Inbox item with its opening.
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
	if err := s.notify(ctx, tx, ap, nil, r.ID, "autoresearch_report", ap.Title+": "+excerpt(text, 2000)); err != nil {
		return err
	}
	return tx.Emit(ctx, "autoresearch_run.reported", nil, store.Actor{Type: "role", ID: ar},
		map[string]any{"run_id": r.ID, "autoresearch_id": ap.ID})
}

// OpenCardForAgentRun opens a study for a run_only agent run, owned by its run's
// acting user and under the same duplicate guard as create_card. The agent the
// run works as works the study; a run with no assigned agent leaves the study
// to the built-in Teams.
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
		if err := tx.CompleteStudyDraft(ctx, ap.ID, card.ID, *draft); err != nil {
			return db.Card{}, err
		}
		card.ResearchID = draft.ResearchID
		card.Deadline = draft.Deadline
		if draft.ResearchID != nil {
			if err := tx.Emit(ctx, "card.properties_updated", &card.ID, store.System, map[string]any{"research_id": *draft.ResearchID}); err != nil {
				return db.Card{}, err
			}
		}
	}
	return card, nil
}
