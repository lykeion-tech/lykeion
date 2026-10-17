package pack

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"sync"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"lykeion/server/internal/storage"
	"lykeion/server/internal/store"
	db "lykeion/server/pkg/db/generated"
)

func blobKey(ws uuid.UUID, sha string) string { return ws.String() + "/packs/" + sha }

// Insert stores a parsed archive's blob and its row in tx's workspace. A nil
// uploadedBy marks a shipped pack.
func Insert(ctx context.Context, tx *store.Tx, blobs storage.Storage, zipBytes []byte, p *Pack, uploadedBy *uuid.UUID) (db.FieldPack, error) {
	manifest, err := json.Marshal(p.Manifest)
	if err != nil {
		return db.FieldPack{}, err
	}
	key := blobKey(tx.WS, p.SHA256)
	if err := blobs.Put(ctx, key, bytes.NewReader(zipBytes), int64(len(zipBytes))); err != nil {
		return db.FieldPack{}, err
	}
	source := "shipped"
	if uploadedBy != nil {
		source = "uploaded"
	}
	return tx.Q.InsertFieldPack(ctx, db.InsertFieldPackParams{WorkspaceID: tx.WS, Name: p.Manifest.Name,
		Version: p.Manifest.Version, Sha256: p.SHA256, Manifest: manifest, BlobKey: key, Source: source, UploadedBy: uploadedBy,
		Domain: p.Manifest.Domain, Stage: p.Manifest.Stage})
}

// EnsureShipped inserts the shipped pack's current version into tx's
// workspace unless that version is already there, and returns its row.
func EnsureShipped(ctx context.Context, tx *store.Tx, blobs storage.Storage, shipped fs.FS, name string) (db.FieldPack, error) {
	z, p, err := ReadShipped(shipped, name)
	if err != nil {
		return db.FieldPack{}, err
	}
	row, err := tx.Q.GetFieldPackVersion(ctx, db.GetFieldPackVersionParams{WorkspaceID: tx.WS, Name: name, Version: p.Manifest.Version})
	if !errors.Is(err, pgx.ErrNoRows) {
		return row, err
	}
	return Insert(ctx, tx, blobs, z, p, nil)
}

// loaded holds parsed packs by archive sha256; the same bytes always parse
// to the same pack.
// ponytail: unbounded, keyed by content hash; bound it if packs churn.
var loaded sync.Map

// LoadPack returns the parsed pack a field_pack row points at.
func LoadPack(ctx context.Context, tx *store.Tx, blobs storage.Storage, ws, id uuid.UUID) (*Pack, error) {
	row, err := tx.Q.GetFieldPack(ctx, db.GetFieldPackParams{WorkspaceID: ws, ID: id})
	if err != nil {
		return nil, err
	}
	if p, ok := loaded.Load(row.Sha256); ok {
		return p.(*Pack), nil
	}
	rc, err := blobs.Get(ctx, row.BlobKey)
	if err != nil {
		return nil, err
	}
	defer rc.Close()
	z, err := io.ReadAll(io.LimitReader(rc, MaxArchive+1))
	if err != nil {
		return nil, err
	}
	p, err := Parse(z)
	if err != nil {
		return nil, err
	}
	if p.SHA256 != row.Sha256 {
		return nil, fmt.Errorf("pack: blob for %s does not match its sha256", row.ID)
	}
	loaded.Store(row.Sha256, p)
	return p, nil
}
