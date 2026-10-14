// Package records reads a card column's live records and the blobs they point
// to; gates read the board through it.
package records

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"io"
	"slices"

	"github.com/google/uuid"

	"lykeion/server/internal/storage"
	"lykeion/server/internal/store"
	db "lykeion/server/pkg/db/generated"
)

type View struct {
	ctx   context.Context
	recs  []db.Record
	blobs storage.Storage
}

// Load reads a column's live records into memory. The view keeps no
// transaction: the engine runs gates on it, and reads blobs through it, after
// the transaction that loaded it has committed.
func Load(ctx context.Context, tx *store.Tx, blobs storage.Storage, card uuid.UUID, column string) (*View, error) {
	recs, err := tx.Q.ListRecords(ctx, db.ListRecordsParams{WorkspaceID: tx.WS, CardID: card, BoardColumn: column})
	return &View{ctx: ctx, recs: recs, blobs: blobs}, err
}

func (v *View) Kind(kind string) []db.Record {
	var out []db.Record
	for _, r := range v.recs {
		if r.Kind == kind {
			out = append(out, r)
		}
	}
	return out
}

func (v *View) Latest(kind string) (db.Record, bool) {
	ks := v.Kind(kind)
	if len(ks) == 0 {
		return db.Record{}, false
	}
	return ks[len(ks)-1], true
}

func (v *View) ByKey(kind, key string) (db.Record, bool) {
	ks := v.Kind(kind)
	for i := len(ks) - 1; i >= 0; i-- {
		if ks[i].Key == key {
			return ks[i], true
		}
	}
	return db.Record{}, false
}

func (v *View) Text(r db.Record) (string, error) {
	if r.BlobKey == "" {
		return "", nil
	}
	rc, err := v.blobs.Get(v.ctx, r.BlobKey)
	if err != nil {
		return "", err
	}
	defer rc.Close()
	b, err := io.ReadAll(rc)
	return string(b), err
}

// Digest identifies the records a gate read, so a gate finished later can
// tell whether its inputs changed underneath it.
func (v *View) Digest(skip ...string) string {
	h := sha256.New()
	for _, r := range v.recs {
		if !slices.Contains(skip, r.Kind) {
			h.Write([]byte(r.ID.String()))
		}
	}
	return hex.EncodeToString(h.Sum(nil))
}

// Body decodes a record's body; malformed bodies decode to the zero value,
// which the gate's checks then fail.
func Body[T any](r db.Record) T {
	var v T
	if err := json.Unmarshal(r.Body, &v); err != nil {
		var zero T
		return zero
	}
	return v
}
