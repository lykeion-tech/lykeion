package autoresearch

import (
	"context"
	"log/slog"

	"github.com/google/uuid"
	"lykeion/server/internal/service"
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
			if err := lockWorkspace(ctx, tx); err != nil {
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
				case card.Ending != "":
					status, reason = "failed", "card_"+card.Ending
				case card.State == "done":
					status, reason = "completed", ""
				case card.State == "blocked":
					status, reason = "blocked", card.BlockKind
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
			if status == "completed" || status == "failed" {
				draft, err := tx.LockStudyDraft(ctx, r.AutoresearchID)
				if err != nil {
					return err
				}
				if draft != nil && !draft.Completed && status == "completed" {
					status, reason = "failed", "study_not_created"
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
			if status == "failed" {
				draft, err := tx.LockStudyDraft(ctx, r.AutoresearchID)
				if err != nil {
					return err
				}
				if draft != nil && !draft.Completed {
					ap, err := tx.Q.GetAutoresearch(ctx, db.GetAutoresearchParams{WorkspaceID: d.WorkspaceID, ID: r.AutoresearchID})
					if err != nil {
						return err
					}
					if err := s.notify(ctx, tx, ap, nil, r.ID, "autoresearch_report", "The agent could not create the study. Check its run for details."); err != nil {
						return err
					}
				}
			}
			return tx.Emit(ctx, "autoresearch_run."+status, nil, store.System, runEvent(r))
		})
		if err != nil {
			slog.Error("autoresearch sync", "run", d.RunID, "err", err)
		}
	}
	return nil
}

// OnSettlement wraps the engine hook after settlement commits. Repeated
// delivery is safe for autoresearch runs; other runs retain the next hook's
// behavior. An error leaves the settlement to be delivered again.
func (s *Service) OnSettlement(next func(context.Context, service.Settlement) error) func(context.Context, service.Settlement) error {
	return func(ctx context.Context, n service.Settlement) error {
		if n.AgentRun.Kind == Role {
			return s.Sync(ctx)
		}
		return next(ctx, n)
	}
}
