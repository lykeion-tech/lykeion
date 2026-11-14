package service

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"lykeion/server/internal/store"
)

const (
	runtimeSkillSweepBatch            = 64
	runtimeSkillSweepWorkspaceBudget  = 256
	runtimeSkillSweepPassTimeout      = 10 * time.Second
	runtimeSkillSweepWorkspaceTimeout = time.Second
)

// RuntimeSkillSweeper rotates across workspaces, including after a failed pass.
type RuntimeSkillSweeper struct {
	store *store.Store
	mu    sync.Mutex
	after uuid.UUID
}

func NewRuntimeSkillSweeper(st *store.Store) *RuntimeSkillSweeper {
	return &RuntimeSkillSweeper{store: st}
}

// Sweep records expired requests without assuming their local work stopped.
func (s *RuntimeSkillSweeper) Sweep(ctx context.Context) error {
	_, err := s.SweepMore(ctx)
	return err
}

// SweepMore reports whether the pass exhausted its work budget and should continue.
func (s *RuntimeSkillSweeper) SweepMore(ctx context.Context) (bool, error) {
	if !s.mu.TryLock() {
		return false, nil
	}
	defer s.mu.Unlock()
	ctx, cancel := context.WithTimeout(ctx, runtimeSkillSweepPassTimeout)
	defer cancel()
	start := s.after
	wrapped := false
	remaining := runtimeSkillSweepBatch
	var failures []error
	var ids []store.RuntimeSkillWorkspace
	attempts := 0
	for attempts < runtimeSkillSweepWorkspaceBudget && remaining > 0 {
		if len(ids) == 0 {
			err := s.store.Maintenance(ctx, func(tx *store.Tx) error {
				var err error
				ids, err = tx.RuntimeSkillTimeoutWorkspaces(ctx, s.after)
				return err
			})
			if err != nil {
				return false, errors.Join(append(failures, err)...)
			}
		}
		if len(ids) == 0 {
			if !wrapped && s.after != uuid.Nil {
				s.after = uuid.Nil
				wrapped = true
				continue
			}
			break
		}
		candidate := ids[0]
		id := candidate.ID
		if wrapped && id.String() > start.String() {
			break
		}
		s.after = id
		attempts++
		if !candidate.Eligible {
			ids = ids[1:]
			continue
		}
		eligible := 0
		for _, candidate := range ids {
			if candidate.Eligible {
				eligible++
			}
		}
		share := max(1, remaining/eligible)
		ids = ids[1:]
		var changed int
		workCtx, workCancel := context.WithTimeout(ctx, runtimeSkillSweepWorkspaceTimeout)
		err := s.store.Maintenance(workCtx, func(tx *store.Tx) error {
			if err := tx.Workspace(workCtx, id); err != nil {
				return err
			}
			if _, err := tx.Q.LockWorkspaceExecution(workCtx, id); errors.Is(err, pgx.ErrNoRows) {
				return nil
			} else if err != nil {
				return err
			}
			if err := tx.Q.LockWorkspaceEvents(workCtx, id); err != nil {
				return err
			}
			items, err := tx.TimeoutRuntimeSkills(workCtx, share)
			if err != nil {
				return err
			}
			for _, item := range items {
				if err := tx.Emit(workCtx, "runtime_skill_request.updated", nil, store.User(item.OwnerID), map[string]any{
					"request_id": item.ID, "runtime_id": item.RuntimeID, "operation": item.Operation, "status": "timeout",
				}); err != nil {
					return err
				}
			}
			changed = len(items)
			return nil
		})
		workCancel()
		if err != nil {
			failures = append(failures, fmt.Errorf("workspace %s: %w", id, err))
			if ctx.Err() != nil {
				break
			}
			continue
		}
		remaining -= changed
	}
	return attempts >= runtimeSkillSweepWorkspaceBudget || remaining == 0, errors.Join(failures...)
}
