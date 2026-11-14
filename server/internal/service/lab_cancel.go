package service

import (
	"context"
	"errors"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"lykeion/server/internal/store"
	"lykeion/server/pkg/agent"
	db "lykeion/server/pkg/db/generated"
)

// CancelUnavailableLabInTx revokes the bearer and records cancellation and its
// durable settlement together. The caller holds the workspace execution lock.
// Even a terminal attempt of a disabled harness must not be asked to correct a hand-in.
func (s *AgentRuns) CancelUnavailableLabInTx(ctx context.Context, tx *store.Tx, t db.AgentRun) (bool, error) {
	if t.Kind != "aristotle" || agent.LabAvailability(t.Harness) == nil {
		return false, nil
	}
	if terminal(t.Status) {
		return true, nil
	}
	return true, s.CancelInTx(ctx, tx, uuid.Nil, t)
}

// CancelUnavailableLabRuns reconciles work left by older servers, including
// running attempts on healthy machines and queued attempts with no claimant.
func (s *AgentRuns) CancelUnavailableLabRuns(ctx context.Context) error {
	workspaces, err := s.Store.Global().AllWorkspaceIDs(ctx)
	if err != nil {
		return err
	}
	var errs []error
	for _, ws := range workspaces {
		if err := ctx.Err(); err != nil {
			return errors.Join(append(errs, err)...)
		}
		err = s.Store.InWorkspace(ctx, ws, uuid.Nil, func(tx *store.Tx) error {
			ids, err := tx.Q.OpenLabRunIDs(ctx, ws)
			if err != nil || len(ids) == 0 {
				return err
			}
			if _, err = tx.Q.LockWorkspaceExecution(ctx, ws); errors.Is(err, pgx.ErrNoRows) {
				return nil
			} else if err != nil {
				return err
			}
			for _, id := range ids {
				t, err := tx.Q.LockAgentRun(ctx, db.LockAgentRunParams{WorkspaceID: ws, ID: id})
				if errors.Is(err, pgx.ErrNoRows) {
					continue
				}
				if err != nil {
					return err
				}
				if _, err = s.CancelUnavailableLabInTx(ctx, tx, t); err != nil {
					return err
				}
			}
			return nil
		})
		if err != nil {
			errs = append(errs, err)
		}
	}
	return errors.Join(errs...)
}
