package service

import (
	"context"
	"errors"
	"slices"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"lykeion/server/internal/store"
	db "lykeion/server/pkg/db/generated"
)

var ErrSharingConfirmation = errors.New("sharing confirmation required")
var ErrInvalidSharing = errors.New("invalid sharing")
var ErrWorkspaceSharingList = errors.New("A machine shared with everyone has no list of colleagues")

type SharingChange struct {
	Visibility string      `json:"visibility"`
	SharedWith []uuid.UUID `json:"shared_with"`
	Confirm    []uuid.UUID `json:"confirm"`
}
type AffectedRun struct {
	RunID      uuid.UUID `json:"run_id"`
	CardID     uuid.UUID `json:"card_id"`
	StudyTitle string    `json:"study_title"`
	Colleague  string    `json:"colleague"`
}

func RuntimeAllowsUser(ctx context.Context, tx *store.Tx, rt db.Runtime, user uuid.UUID) (bool, error) {
	if rt.OwnerID == user || rt.Visibility == "workspace" {
		return true, nil
	}
	ids, err := tx.Q.RuntimeShares(ctx, db.RuntimeSharesParams{WorkspaceID: rt.WorkspaceID, RuntimeID: rt.ID})
	return slices.Contains(ids, user), err
}
func (s *AgentRuns) SetSharing(ctx context.Context, owner, ws, runtimeID uuid.UUID, c SharingChange) (affected []AffectedRun, err error) {
	err = s.Store.InWorkspace(ctx, ws, owner, func(tx *store.Tx) error {
		if _, e := tx.Q.LockWorkspaceExecution(ctx, ws); e != nil {
			return e
		}
		if _, e := tx.Q.GetMember(ctx, db.GetMemberParams{WorkspaceID: ws, UserID: owner}); e != nil {
			return e
		}
		rt, e := tx.Q.LockRuntime(ctx, db.LockRuntimeParams{WorkspaceID: ws, ID: runtimeID})
		if e != nil {
			return e
		}
		if rt.OwnerID != owner {
			return pgx.ErrNoRows
		}
		if c.Visibility != "private" && c.Visibility != "workspace" {
			return ErrInvalidSharing
		}
		if c.Visibility == "workspace" && len(c.SharedWith) > 0 {
			return ErrWorkspaceSharingList
		}
		shares := []uuid.UUID{}
		for _, id := range c.SharedWith {
			if id == owner || slices.Contains(shares, id) {
				continue
			}
			if _, e := tx.Q.GetMember(ctx, db.GetMemberParams{WorkspaceID: ws, UserID: id}); e != nil {
				if errors.Is(e, pgx.ErrNoRows) {
					return ErrInvalidSharing
				}
				return e
			}
			shares = append(shares, id)
		}
		old, e := tx.Q.RuntimeShares(ctx, db.RuntimeSharesParams{WorkspaceID: ws, RuntimeID: runtimeID})
		if e != nil {
			return e
		}
		runs, e := tx.Q.RuntimeDependentRuns(ctx, db.RuntimeDependentRunsParams{WorkspaceID: ws, OwnerID: owner, RuntimeID: &runtimeID, DaemonID: &rt.DaemonID, Harness: rt.Harness})
		if e != nil {
			return e
		}
		for _, r := range runs {
			if c.Visibility != "workspace" && !slices.Contains(shares, r.ResponsibleUserID) && (rt.Visibility == "workspace" || slices.Contains(old, r.ResponsibleUserID)) {
				affected = append(affected, AffectedRun{RunID: r.ID, CardID: *r.CardID, StudyTitle: r.StudyTitle, Colleague: r.Colleague})
			}
		}
		if len(affected) > 0 {
			ids := map[uuid.UUID]bool{}
			for _, id := range c.Confirm {
				ids[id] = true
			}
			if len(ids) != len(affected) {
				return ErrSharingConfirmation
			}
			for _, r := range affected {
				if !ids[r.RunID] {
					return ErrSharingConfirmation
				}
			}
		}
		if _, e := tx.Q.SetRuntimeSharing(ctx, db.SetRuntimeSharingParams{WorkspaceID: ws, ID: runtimeID, OwnerID: owner, Visibility: c.Visibility}); e != nil {
			return e
		}
		if e := tx.Q.ReplaceRuntimeShares(ctx, db.ReplaceRuntimeSharesParams{WorkspaceID: ws, RuntimeID: runtimeID, UserIds: shares}); e != nil {
			return e
		}
		for _, r := range affected {
			if e := tx.Q.SetRuntimeRevokedReason(ctx, db.SetRuntimeRevokedReasonParams{WorkspaceID: ws, ID: r.RunID}); e != nil {
				return e
			}
			run, e := cancelLocked(ctx, tx, owner, r.RunID)
			if e != nil {
				return e
			}
			if e = saveSettlement(ctx, tx, run); e != nil {
				return e
			}
		}
		return tx.Emit(ctx, "runtime.updated", nil, store.User(owner), map[string]any{"runtime_id": runtimeID})
	})
	if err == nil {
		for _, r := range affected {
			s.deliverAfterCommit(ctx, ws, r.RunID)
		}
	}
	return
}

// RemoveMemberSharing revokes shared runs and owner grants in the caller's
// workspace transaction, which must already hold the workspace mutation lock.
func (s *AgentRuns) RemoveMemberSharing(ctx context.Context, tx *store.Tx, actor, member uuid.UUID) error {
	runs, err := tx.Q.DepartingMemberSharedRuns(ctx, db.DepartingMemberSharedRunsParams{WorkspaceID: tx.WS, UserID: member})
	if err != nil {
		return err
	}
	for _, run := range runs {
		if err := tx.Q.SetRuntimeRevokedReason(ctx, db.SetRuntimeRevokedReasonParams{WorkspaceID: tx.WS, ID: run.ID}); err != nil {
			return err
		}
		cancelled, err := cancelLocked(ctx, tx, actor, run.ID)
		if err != nil {
			return err
		}
		if err := saveSettlement(ctx, tx, cancelled); err != nil {
			return err
		}
		tx.AfterCommit(func() { s.deliverAfterCommit(ctx, tx.WS, run.ID) })
	}
	if err := tx.Q.ClearDepartingMemberRuntimeShares(ctx, db.ClearDepartingMemberRuntimeSharesParams{WorkspaceID: tx.WS, UserID: member}); err != nil {
		return err
	}
	return tx.Q.PrivatizeDepartingMemberRuntimes(ctx, db.PrivatizeDepartingMemberRuntimesParams{WorkspaceID: tx.WS, UserID: member})
}
