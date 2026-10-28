package coresearch

import (
	"context"
	"crypto/sha256"
	"encoding/binary"
	"errors"
	"io"
	"sort"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"lykeion/server/internal/storage"
	"lykeion/server/internal/store"
)

type UploadBlob struct {
	SHA256 string
	Body   io.Reader
	Size   int64
	Open   func() (io.ReadCloser, error)
}

// WithBlob coordinates content-addressed storage outside the save transaction.
func WithBlob(ctx context.Context, st *store.Store, blobs storage.Storage, ws, user uuid.UUID, sha string, body io.Reader, size int64, save func() error) error {
	return WithBlobs(ctx, st, blobs, ws, user, []UploadBlob{{SHA256: sha, Body: body, Size: size}}, save)
}

// WithBlobs holds sorted workspace/blob locks on one dedicated connection until
// save commits or rolls back. Save acquires workspace, authorization, project,
// and file locks in that order. Only newly written, unreferenced blobs are deleted.
func WithBlobs(ctx context.Context, st *store.Store, blobs storage.Storage, ws, user uuid.UUID, uploads []UploadBlob, save func() error) (retErr error) {
	conn, err := pgx.ConnectConfig(ctx, st.Pool.Config().ConnConfig.Copy())
	if err != nil {
		return err
	}
	defer func() {
		c, cancel := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
		defer cancel()
		_ = conn.Close(c)
	}()
	ordered := append([]UploadBlob(nil), uploads...)
	sort.Slice(ordered, func(i, j int) bool { return ordered[i].SHA256 < ordered[j].SHA256 })
	fresh := []string{}
	defer func() {
		if retErr == nil || len(fresh) == 0 {
			return
		}
		cleanup, cancel := context.WithTimeout(context.WithoutCancel(ctx), 10*time.Second)
		defer cancel()
		if _, e := conn.Exec(cleanup, `SELECT set_config('app.workspace_id',$1,false),set_config('app.user_id',$2,false)`, ws.String(), user.String()); e != nil {
			retErr = errors.Join(retErr, e)
			return
		}
		for _, sha := range fresh {
			var referenced bool
			if e := conn.QueryRow(cleanup, `SELECT public.coresearch_blob_referenced($1)`, sha).Scan(&referenced); e != nil {
				retErr = errors.Join(retErr, e)
				continue
			}
			if !referenced {
				retErr = errors.Join(retErr, blobs.Delete(cleanup, BlobKey(ws, sha)))
			}
		}
	}()
	seen := map[string]bool{}
	for _, up := range ordered {
		if seen[up.SHA256] {
			continue
		}
		seen[up.SHA256] = true
		key := BlobKey(ws, up.SHA256)
		sum := sha256.Sum256([]byte(key))
		lock := int64(binary.BigEndian.Uint64(sum[:8]))
		if _, err = conn.Exec(ctx, `SELECT pg_advisory_lock($1)`, lock); err != nil {
			return err
		}
		existing, e := blobs.Get(ctx, key)
		if e == nil {
			if e = existing.Close(); e != nil {
				return e
			}
			continue
		}
		if !errors.Is(e, storage.ErrNotFound) {
			return e
		}
		fresh = append(fresh, up.SHA256)
		body := up.Body
		var closer io.ReadCloser
		if up.Open != nil {
			closer, e = up.Open()
			if e != nil {
				return e
			}
			body = closer
		}
		e = blobs.Put(ctx, key, body, up.Size)
		if closer != nil {
			e = errors.Join(e, closer.Close())
		}
		if e != nil {
			return e
		}
	}
	return save()
}

// WithCellOutput serializes a cell's fixed output key across finish retries.
// The callback checks authority before storage and again in its commit transaction.
func WithCellOutput(ctx context.Context, st *store.Store, ws, id uuid.UUID, finish func() error) error {
	conn, e := pgx.ConnectConfig(ctx, st.Pool.Config().ConnConfig.Copy())
	if e != nil {
		return e
	}
	defer func() {
		c, cancel := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
		defer cancel()
		_ = conn.Close(c)
	}()
	sum := sha256.Sum256([]byte(ws.String() + "/coresearch/cells/" + id.String() + ".json"))
	if _, e = conn.Exec(ctx, `SELECT pg_advisory_lock($1)`, int64(binary.BigEndian.Uint64(sum[:8]))); e != nil {
		return e
	}
	return finish()
}
