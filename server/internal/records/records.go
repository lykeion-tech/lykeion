// Package records reads a card column's live records and the blobs they point
// to; gates read the board through it.
package records

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"slices"

	"github.com/google/uuid"

	"lykeion/server/internal/board"
	"lykeion/server/internal/storage"
	"lykeion/server/internal/store"
	db "lykeion/server/pkg/db/generated"
)

type View struct {
	ctx     context.Context
	recs    []db.Record
	blobs   storage.Storage
	earlier map[string]*View
}

// Load reads a column's live records into memory, with those of every stage
// column before it. The view keeps no transaction: the engine runs gates and
// server steps on it, and reads blobs through it, after the transaction that
// loaded it has committed.
func Load(ctx context.Context, tx *store.Tx, blobs storage.Storage, card uuid.UUID, column string) (*View, error) {
	v, err := load(ctx, tx, blobs, card, column)
	if err != nil {
		return nil, err
	}
	v.earlier = map[string]*View{}
	for _, col := range board.Stages[:max(board.StageIndex(column), 0)] {
		if v.earlier[col], err = load(ctx, tx, blobs, card, col); err != nil {
			return nil, err
		}
	}
	return v, nil
}

func load(ctx context.Context, tx *store.Tx, blobs storage.Storage, card uuid.UUID, column string) (*View, error) {
	recs, err := tx.Q.ListRecords(ctx, db.ListRecordsParams{WorkspaceID: tx.WS, CardID: card, BoardColumn: column})
	return &View{ctx: ctx, recs: recs, blobs: blobs}, err
}

// Column is an earlier stage column of the same card, as it was when the view
// was loaded: a later column checks its inputs against an earlier column's
// records. While the card stays in its column and round, earlier columns
// receive no records, so the engine's re-check of this column covers them.
func (v *View) Column(column string) (*View, error) {
	ev, ok := v.earlier[column]
	if !ok {
		return nil, fmt.Errorf("records: column %s was not loaded with this view", column)
	}
	return ev, nil
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

// Put is one record a server step asks for.
type Put struct {
	Kind, Key string
	Body      any
}

// Batch is what a server step decided with no transaction open; the engine
// writes it in the transaction that applies the step.
type Batch struct {
	Retire []string // server kinds whose live records are superseded first
	Put    []Put
}

// Write supersedes the server's live records of each Retire kind, then
// inserts each Put with role server and no agent run, on the card's column and
// round. Superseded records stay in the table and the timeline.
func (b Batch) Write(ctx context.Context, tx *store.Tx, c db.Card) error {
	if err := tx.Q.SupersedeRoleKinds(ctx, db.SupersedeRoleKindsParams{WorkspaceID: tx.WS, CardID: c.ID,
		BoardColumn: c.BoardColumn, Role: "server", Kinds: b.Retire}); err != nil {
		return err
	}
	for _, p := range b.Put {
		body, err := json.Marshal(p.Body)
		if err != nil {
			return err
		}
		rec, err := tx.Q.InsertRecord(ctx, db.InsertRecordParams{WorkspaceID: tx.WS, CardID: c.ID, BoardColumn: c.BoardColumn,
			Round: c.Round, Kind: p.Kind, Role: "server", Key: p.Key, Body: body})
		if err != nil {
			return err
		}
		if err := tx.Emit(ctx, "record.created", &c.ID, store.System, map[string]any{"record_id": rec.ID, "kind": p.Kind, "key": p.Key, "role": "server"}); err != nil {
			return err
		}
	}
	return nil
}
