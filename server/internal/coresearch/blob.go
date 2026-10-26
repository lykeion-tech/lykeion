package coresearch

import (
	"context"
	"crypto/sha256"
	"encoding/binary"
	"errors"
	"io"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"lykeion/server/internal/storage"
	"lykeion/server/internal/store"
)

// WithBlob coordinates a content-addressed upload across server processes. Save
// must open and commit its own transaction before returning. Blob I/O happens
// outside that transaction. All new person and run uploads must use this wrapper;
// restores reuse an already referenced blob and need no upload lock.
// The cleanup helper currently authorizes workspace members; run-token callers
// require the equivalent active-Coresearch-run authorization before using it.
func WithBlob(ctx context.Context, st *store.Store, blobs storage.Storage, ws, user uuid.UUID, sha string, body io.Reader, size int64, save func() error) (retErr error) {
	conn, err := pgx.ConnectConfig(ctx, st.Pool.Config().ConnConfig.Copy())
	if err != nil {
		return err
	}
	defer func() {
		closeCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
		defer cancel()
		_ = conn.Close(closeCtx)
	}()
	sum := sha256.Sum256([]byte(BlobKey(ws, sha)))
	lock := int64(binary.BigEndian.Uint64(sum[:8]))
	if _, err = conn.Exec(ctx, `SELECT pg_advisory_lock($1)`, lock); err != nil {
		return err
	}
	key := BlobKey(ws, sha)
	existing, err := blobs.Get(ctx, key)
	if err == nil {
		if err = existing.Close(); err != nil {
			return err
		}
		return save()
	}
	if !errors.Is(err, storage.ErrNotFound) {
		return err
	}
	// Only a blob first written here is eligible for cleanup. Even on a failed
	// save, a hidden project's committed reference keeps the shared blob alive.
	defer func() {
		if retErr == nil {
			return
		}
		cleanup, cancel := context.WithTimeout(context.WithoutCancel(ctx), 10*time.Second)
		defer cancel()
		if _, err := conn.Exec(cleanup, `SELECT set_config('app.workspace_id',$1,false),set_config('app.user_id',$2,false)`, ws.String(), user.String()); err != nil {
			retErr = errors.Join(retErr, err)
			return
		}
		var referenced bool
		if err := conn.QueryRow(cleanup, `SELECT public.coresearch_blob_referenced($1)`, sha).Scan(&referenced); err != nil {
			retErr = errors.Join(retErr, err)
			return
		}
		if !referenced {
			retErr = errors.Join(retErr, blobs.Delete(cleanup, key))
		}
	}()
	if err = blobs.Put(ctx, key, body, size); err != nil {
		return err
	}
	return save()
}
