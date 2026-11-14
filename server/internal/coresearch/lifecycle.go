package coresearch

import (
	"context"
	"lykeion/server/internal/store"
)

func Expire(ctx context.Context, tx *store.Tx) error {
	rows, err := tx.Q.ExpireCoresearchWork(ctx)
	if err != nil {
		return err
	}
	for _, row := range rows {
		if err = tx.Emit(ctx, "coresearch."+row.Kind+".changed", nil, store.System, map[string]any{"project_id": row.ProjectID, "chat_id": row.ChatID, row.Kind + "_id": row.WorkID}); err != nil {
			return err
		}
	}
	return nil
}
func Sweep(ctx context.Context, st *store.Store) error {
	return st.Maintenance(ctx, func(tx *store.Tx) error {
		workspaces, err := tx.Q.CoresearchExpiredWorkspaces(ctx)
		if err != nil {
			return err
		}
		for _, ws := range workspaces {
			if err = tx.Workspace(ctx, ws); err != nil {
				return err
			}
			if _, err = tx.Q.LockWorkspaceForMutation(ctx, ws); err != nil {
				return err
			}
		}
		for _, ws := range workspaces {
			if err = tx.Workspace(ctx, ws); err != nil {
				return err
			}
			if err = Expire(ctx, tx); err != nil {
				return err
			}
		}
		return nil
	})
}
