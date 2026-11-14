package coresearch

import (
	"context"
	"crypto/sha256"
	"encoding/binary"
	"errors"
	"fmt"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"lykeion/server/internal/storage"
	"lykeion/server/internal/store"
)

// CleanupBlobs removes queued blobs only while holding the same lock as writers.
// A failed deletion leaves the queue entry in place for a later retry.
func CleanupBlobs(ctx context.Context, st *store.Store, blobs storage.Storage, ws uuid.UUID, keys []string) error {
	var result error
	for _, key := range keys {
		if ctx.Err() != nil {
			return errors.Join(result, ctx.Err())
		}
		err := st.InWorkspace(ctx, ws, uuid.Nil, func(tx *store.Tx) error {
			sum := sha256.Sum256([]byte(key))
			if err := tx.Q.LockCoresearchBlobCleanup(ctx, int64(binary.BigEndian.Uint64(sum[:8]))); err != nil {
				return err
			}
			if _, err := tx.Q.LockCoresearchBlobCleanupKey(ctx, key); err != nil {
				if errors.Is(err, pgx.ErrNoRows) {
					return nil
				}
				return err
			}
			referenced, err := tx.Q.CoresearchCleanupBlobReferenced(ctx, key)
			if err != nil {
				return err
			}
			if !referenced {
				if err := blobs.Delete(ctx, key); err != nil {
					return err
				}
			}
			return tx.Q.RemoveCoresearchBlobCleanup(ctx, key)
		})
		if err != nil {
			result = errors.Join(result, fmt.Errorf("cleanup blob %s: %w", key, err))
		}
	}
	return result
}

// SweepBlobCleanup retries durable cleanup requests, including for workspaces
// which have since been deleted.
func SweepBlobCleanup(ctx context.Context, st *store.Store, blobs storage.Storage) error {
	var workspaces []uuid.UUID
	if err := st.InWorkspace(ctx, uuid.Nil, uuid.Nil, func(tx *store.Tx) error {
		var err error
		workspaces, err = tx.Q.CoresearchBlobCleanupWorkspaces(ctx)
		return err
	}); err != nil {
		return err
	}
	var result error
	for _, ws := range workspaces {
		var keys []string
		err := st.InWorkspace(ctx, ws, uuid.Nil, func(tx *store.Tx) error {
			var err error
			keys, err = tx.Q.PendingCoresearchBlobCleanup(ctx)
			return err
		})
		if err == nil {
			err = CleanupBlobs(ctx, st, blobs, ws, keys)
		}
		if err != nil {
			result = errors.Join(result, fmt.Errorf("workspace %s: %w", ws, err))
		}
	}
	return result
}
