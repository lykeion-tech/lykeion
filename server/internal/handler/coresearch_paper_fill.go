package handler

import (
	"errors"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"lykeion/server/internal/coresearch"
	"lykeion/server/internal/coresearch/doc"
	"lykeion/server/internal/store"
	db "lykeion/server/pkg/db/generated"
	"net/http"
	"strings"
)

// daemonPaperFiles publishes staged live snapshots after rechecking daemon ownership
// and the complete active file set. Storage never runs inside the transactions.
func (h *H) daemonPaperFiles(r *http.Request) ([]coresearch.FileRef, error) {
	var out []coresearch.FileRef
	var last error
	for attempt := 0; attempt < 4; attempt++ {
		type item struct {
			file     doc.File
			head     db.CoresearchFileVersion
			hydrate  bool
			prepared doc.PreparedSnapshot
		}
		var items []item
		var project, ws uuid.UUID
		var mode string
		authorize := func(tx *store.Tx, daemon string) (db.CoresearchChat, error) {
			id, e := coresearchID(r, "chat")
			if e != nil {
				return db.CoresearchChat{}, e
			}
			return daemonCoresearchChat(r, tx, id, daemon)
		}
		e := h.coresearchDaemonTx(r, func(tx *store.Tx, daemon string) error {
			c, e := authorize(tx, daemon)
			if e != nil {
				return e
			}
			project, ws, mode = c.ProjectID, tx.WS, c.Mode
			if mode != "cowrite" {
				out, e = coresearch.FileRefs(r.Context(), tx, c.ProjectID)
				return e
			}
			files, e := tx.Q.ListCoresearchFiles(r.Context(), db.ListCoresearchFilesParams{WorkspaceID: tx.WS, ProjectID: project})
			if e != nil {
				return e
			}
			for _, f := range files {
				if !doc.Eligible(f) {
					if _, e := tx.Q.GetCoresearchDoc(r.Context(), db.GetCoresearchDocParams{WorkspaceID: tx.WS, FileID: f.ID}); errors.Is(e, pgx.ErrNoRows) {
						continue
					} else if e != nil {
						return e
					}
				}
				it := item{file: doc.File{Record: f}}
				_, e = tx.Q.GetCoresearchDoc(r.Context(), db.GetCoresearchDocParams{WorkspaceID: tx.WS, FileID: f.ID})
				if errors.Is(e, pgx.ErrNoRows) {
					it.hydrate = true
					it.head, e = tx.Q.CoresearchFileHead(r.Context(), db.CoresearchFileHeadParams{WorkspaceID: tx.WS, FileID: f.ID})
				}
				if e != nil {
					return e
				}
				items = append(items, it)
			}
			return nil
		})
		if e != nil || mode != "cowrite" {
			return out, e
		}
		for i := range items {
			if items[i].hydrate {
				e = h.hydratePaper(r, &items[i].file, items[i].head)
				if e != nil {
					return nil, e
				}
			}
		}
		e = h.coresearchDaemonTx(r, func(tx *store.Tx, daemon string) error {
			if _, e := authorize(tx, daemon); e != nil {
				return e
			}
			for i := range items {
				it := &items[i]
				f, e := tx.Q.GetCoresearchFile(r.Context(), db.GetCoresearchFileParams{WorkspaceID: ws, ProjectID: project, ID: it.file.Record.ID})
				if e != nil {
					return e
				}
				it.file.Record = f
				current, e := doc.Open(r.Context(), tx, it.file)
				if e != nil {
					return e
				}
				it.prepared = doc.PrepareSnapshot(current)
				it.prepared.MaxProjectBytes = coresearch.MaxProjectBytes
			}
			return nil
		})
		if errors.Is(e, doc.ErrHeadChanged) {
			last = e
			continue
		}
		if e != nil {
			return nil, e
		}
		uploads := make([]coresearch.UploadBlob, 0, len(items))
		for _, it := range items {
			uploads = append(uploads, coresearch.UploadBlob{SHA256: it.prepared.SHA256, Body: strings.NewReader(it.prepared.Text), Size: int64(len(it.prepared.Text))})
		}
		e = coresearch.WithBlobs(r.Context(), h.Store, h.Blobs, ws, user(r), uploads, func() error {
			return h.coresearchDaemonTx(r, func(tx *store.Tx, daemon string) error {
				if _, e := authorize(tx, daemon); e != nil {
					return e
				}
				files, e := tx.Q.ListCoresearchFiles(r.Context(), db.ListCoresearchFilesParams{WorkspaceID: ws, ProjectID: project})
				if e != nil {
					return e
				}
				byID := map[uuid.UUID]item{}
				for _, it := range items {
					byID[it.file.Record.ID] = it
				}
				for _, f := range files {
					if !doc.Eligible(f) {
						if _, e := tx.Q.GetCoresearchDoc(r.Context(), db.GetCoresearchDocParams{WorkspaceID: tx.WS, FileID: f.ID}); errors.Is(e, pgx.ErrNoRows) {
							continue
						} else if e != nil {
							return e
						}
					}
					it, ok := byID[f.ID]
					if !ok || it.file.Record.Path != f.Path {
						return doc.ErrHeadChanged
					}
					delete(byID, f.ID)
					d, e := doc.Open(r.Context(), tx, it.file)
					if e != nil {
						return e
					}
					if d.Version != it.prepared.Version {
						return doc.ErrBehind{Current: d.Version}
					}
					if d.Text != it.prepared.Text {
						return doc.ErrInvalid
					}
					head, e := tx.Q.CoresearchFileHead(r.Context(), db.CoresearchFileHeadParams{WorkspaceID: ws, FileID: f.ID})
					if e != nil {
						return e
					}
					if head.Sha256 != it.prepared.SHA256 {
						who := user(r)
						if _, e = doc.Snapshot(r.Context(), tx, it.file, it.prepared, doc.Author{UserID: &who}); e != nil {
							return e
						}
					}
				}
				if len(byID) != 0 {
					return doc.ErrHeadChanged
				}
				out, e = coresearch.FileRefs(r.Context(), tx, project)
				if e != nil {
					return e
				}
				for i := range out {
					for _, it := range items {
						if out[i].FileID == it.file.Record.ID {
							v := it.prepared.Version
							out[i].DocVersion = &v
						}
					}
				}
				return nil
			})
		})
		var behind doc.ErrBehind
		if errors.As(e, &behind) || errors.Is(e, doc.ErrHeadChanged) {
			last = e
			continue
		}
		return out, e
	}
	return nil, last
}
