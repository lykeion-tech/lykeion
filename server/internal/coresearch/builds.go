package coresearch

import (
	"context"
	"encoding/json"
	"errors"
	"strings"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"lykeion/server/internal/coresearch/doc"
	"lykeion/server/internal/storage"
	"lykeion/server/internal/store"
	db "lykeion/server/pkg/db/generated"
)

type DocText struct {
	Path    string    `json:"path"`
	FileID  uuid.UUID `json:"file_id"`
	Version int64     `json:"version"`
	Text    string    `json:"text"`
}
type ClaimedBuild struct {
	WorkContext
	ID      uuid.UUID `json:"id"`
	Seq     int32     `json:"seq"`
	Sources []FileRef `json:"sources"`
	Docs    []DocText `json:"docs"`
}
type BuildTransaction func(func(*store.Tx, uuid.UUID, uuid.UUID) error) error

// ErrNoMachine refuses a requested build when none of the requester's own
// machines is online: builds never wait for a machine that is away.
var ErrNoMachine = errors.New("no machine online")

// QueueBuild stages current live bytes outside transactions and then atomically
// verifies the complete source set, snapshots journals and freezes references.
func QueueBuild(ctx context.Context, st *store.Store, blobs storage.Storage, ws, identity uuid.UUID, transaction BuildTransaction, origin ...uuid.UUID) (db.CoresearchBuild, error) {
	var out db.CoresearchBuild
	for attempt := 0; attempt < 4; attempt++ {
		var refs []FileRef
		prepared := map[uuid.UUID]doc.PreparedSnapshot{}
		var project, who uuid.UUID
		err := transaction(func(tx *store.Tx, p, u uuid.UUID) error {
			project, who = p, u
			if len(origin) > 0 {
				existing, e := tx.Q.CoresearchRunBuild(ctx, db.CoresearchRunBuildParams{WorkspaceID: ws, ID: origin[0]})
				if e != nil {
					return e
				}
				if existing != nil {
					out, e = tx.Q.GetCoresearchBuild(ctx, db.GetCoresearchBuildParams{WorkspaceID: ws, ID: *existing})
					return e
				}
			} else {
				online, e := tx.Q.CoresearchOwnMachineOnline(ctx, db.CoresearchOwnMachineOnlineParams{WorkspaceID: ws, OwnerID: u})
				if e != nil {
					return e
				}
				if !online {
					return ErrNoMachine
				}
			}

			var e error
			refs, e = FileRefs(ctx, tx, p)
			if e != nil {
				return e
			}
			for _, ref := range refs {
				_, e := tx.Q.GetCoresearchDoc(ctx, db.GetCoresearchDocParams{WorkspaceID: ws, FileID: ref.FileID})
				if errors.Is(e, pgx.ErrNoRows) {
					continue
				}
				if e != nil {
					return e
				}
				f, e := tx.Q.GetCoresearchFile(ctx, db.GetCoresearchFileParams{WorkspaceID: ws, ProjectID: p, ID: ref.FileID})
				if e != nil {
					return e
				}
				current, e := doc.Open(ctx, tx, doc.File{Record: f})
				if e != nil {
					return e
				}
				prepared[f.ID] = doc.PrepareSnapshot(current)
			}
			return nil
		})
		if err != nil {
			return out, err
		}
		if out.ID != uuid.Nil {
			return out, nil
		}
		var uploads []UploadBlob
		for _, p := range prepared {
			uploads = append(uploads, UploadBlob{SHA256: p.SHA256, Body: strings.NewReader(p.Text), Size: int64(len(p.Text))})
		}
		err = WithBlobs(ctx, st, blobs, ws, identity, uploads, func() error {
			return transaction(func(tx *store.Tx, p, u uuid.UUID) error {
				if len(origin) > 0 {
					existing, e := tx.Q.CoresearchRunBuild(ctx, db.CoresearchRunBuildParams{WorkspaceID: ws, ID: origin[0]})
					if e != nil {
						return e
					}
					if existing != nil {
						out, e = tx.Q.GetCoresearchBuild(ctx, db.GetCoresearchBuildParams{WorkspaceID: ws, ID: *existing})
						return e
					}
				}
				if p != project || u != who {
					return pgx.ErrNoRows
				}
				current, e := FileRefs(ctx, tx, p)
				if e != nil {
					return e
				}
				if len(current) != len(refs) {
					return doc.ErrHeadChanged
				}
				for i, f := range current {
					if f.FileID != refs[i].FileID || f.N != refs[i].N || f.Path != refs[i].Path {
						return doc.ErrHeadChanged
					}
				}
				for i := range refs {
					ref := &refs[i]
					prep, ok := prepared[ref.FileID]
					if !ok {
						if _, e := tx.Q.GetCoresearchDoc(ctx, db.GetCoresearchDocParams{WorkspaceID: ws, FileID: ref.FileID}); e == nil {
							return doc.ErrHeadChanged
						} else if !errors.Is(e, pgx.ErrNoRows) {
							return e
						}
						continue
					}
					prep.MaxProjectBytes = MaxProjectBytes
					f, e := tx.Q.GetCoresearchFile(ctx, db.GetCoresearchFileParams{WorkspaceID: ws, ProjectID: p, ID: ref.FileID})
					if e != nil {
						return e
					}
					n, e := doc.Snapshot(ctx, tx, doc.File{Record: f}, prep, doc.Author{UserID: &who})
					if e != nil {
						return e
					}
					ref.N = int32(n)
					ref.SHA256 = prep.SHA256
					ref.Size = int64(len(prep.Text))
					v := prep.Version
					ref.DocVersion = &v
				}
				raw, _ := json.Marshal(refs)
				old, e := tx.Q.QueuedCoresearchBuild(ctx, db.QueuedCoresearchBuildParams{WorkspaceID: ws, ProjectID: p, RequestedBy: who})
				if e == nil {
					old, e = tx.Q.SupersedeCoresearchBuild(ctx, db.SupersedeCoresearchBuildParams{WorkspaceID: ws, ID: old.ID})
					if e != nil {
						return e
					}
					if e = BuildChanged(ctx, tx, old); e != nil {
						return e
					}
				} else if !errors.Is(e, pgx.ErrNoRows) {
					return e
				}
				out, e = tx.Q.EnqueueCoresearchBuild(ctx, db.EnqueueCoresearchBuildParams{WorkspaceID: ws, ProjectID: p, RequestedBy: who, Sources: raw})

				if e != nil {
					return e
				}
				if len(origin) > 0 {
					if e = tx.Q.SetCoresearchRunBuild(ctx, db.SetCoresearchRunBuildParams{WorkspaceID: ws, ID: origin[0], BuildID: &out.ID}); e != nil {
						return e
					}
				}
				if e = BuildChanged(ctx, tx, out); e != nil {
					return e
				}
				return tx.Emit(ctx, "coresearch.work.queued", nil, store.System, map[string]any{"owner_id": who})
			})
		})
		var behind doc.ErrBehind
		if errors.Is(err, doc.ErrHeadChanged) || errors.As(err, &behind) {
			continue
		}
		return out, err
	}
	return out, doc.ErrHeadChanged
}
func BuildChanged(ctx context.Context, tx *store.Tx, b db.CoresearchBuild) error {
	return tx.Emit(ctx, "coresearch.build.changed", nil, store.System, map[string]any{"project_id": b.ProjectID, "build_id": b.ID})
}
