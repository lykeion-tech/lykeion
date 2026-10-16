package autoresearch

import (
	"context"
	"log/slog"

	"github.com/google/uuid"
	"lykeion/server/internal/store"
	db "lykeion/server/pkg/db/generated"
)

// Sync lets open runs follow their cards or agent runs. Final statuses stay final.
func (s *Service) Sync(ctx context.Context) error {
	drift, err := s.Store.Global().AutoresearchDrift(ctx)
	if err != nil {
		return err
	}
	for _, d := range drift {
		err := s.Store.InWorkspace(ctx, d.WorkspaceID, uuid.Nil, func(tx *store.Tx) error {
			if err := tx.Q.LockWorkspaceEvents(ctx, d.WorkspaceID); err != nil {
				return err
			}
			r, err := tx.Q.LockRun(ctx, db.LockRunParams{WorkspaceID: d.WorkspaceID, ID: d.RunID})
			if err != nil {
				return err
			}
			if r.Status != "running" && r.Status != "blocked" {
				return nil
			}
			// The discovery snapshot can be stale after waiting for another sync.
			status, reason := r.Status, r.Reason
			if r.CardID != nil {
				card, err := tx.Q.GetCard(ctx, db.GetCardParams{WorkspaceID: d.WorkspaceID, ID: *r.CardID})
				if err != nil {
					return err
				}
				switch {
				case card.Ending == "released":
					status, reason = "completed", ""
				case card.Ending != "":
					status, reason = "failed", "card_"+card.Ending
				case card.Flag != "":
					status, reason = "blocked", card.Flag
				default:
					status, reason = "running", ""
				}
			} else if r.AgentRunID != nil {
				agent, err := tx.Q.GetAgentRun(ctx, db.GetAgentRunParams{WorkspaceID: d.WorkspaceID, ID: *r.AgentRunID})
				if err != nil {
					return err
				}
				switch agent.Status {
				case "completed":
					status, reason = "completed", ""
				case "failed":
					status, reason = "failed", ""
					if agent.FailureReason != nil {
						reason = *agent.FailureReason
					}
				case "cancelled":
					status, reason = "failed", "cancelled"
				}
			}
			if status == r.Status && reason == r.Reason {
				return nil
			}
			n, err := tx.Q.FollowRun(ctx, db.FollowRunParams{WorkspaceID: d.WorkspaceID, ID: d.RunID, Status: status, Reason: reason})
			if err != nil || n == 0 {
				return err
			}
			r.Status, r.Reason = status, reason
			return tx.Emit(ctx, "autoresearch_run."+status, nil, store.System, runEvent(r))
		})
		if err != nil {
			slog.Error("autoresearch sync", "run", d.RunID, "err", err)
		}
	}
	return nil
}

// OnSettled wraps the engine hook after settlement commits. Repeated delivery
// is safe for autoresearch runs; other runs retain the next hook's behavior.
func (s *Service) OnSettled(next func(context.Context, db.AgentRun)) func(context.Context, db.AgentRun) {
	return func(ctx context.Context, r db.AgentRun) {
		if r.Kind == Role {
			if err := s.Sync(ctx); err != nil {
				slog.Error("autoresearch sync", "agent_run", r.ID, "err", err)
			}
			return
		}
		next(ctx, r)
	}
}
