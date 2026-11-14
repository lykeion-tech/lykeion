package handler

import (
	"errors"
	"io"
	"net/http"
	"slices"
	"strings"
	"unicode/utf8"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"

	"lykeion/server/internal/coresearch"
	"lykeion/server/internal/coresearch/doc"
	"lykeion/server/internal/store"
	db "lykeion/server/pkg/db/generated"
)

func lifecycleFile(r *http.Request, tx *store.Tx, ws, who uuid.UUID, write bool) (db.CoresearchFile, error) {
	p, e := coresearchProject(r, tx, ws, who, write, false)
	if e != nil {
		return db.CoresearchFile{}, e
	}
	if p.Mode != "cowrite" {
		return db.CoresearchFile{}, pgx.ErrNoRows
	}
	return coresearchFile(r, tx, ws, p.ID)
}

func (h *H) renameCoresearchFile(w http.ResponseWriter, r *http.Request) {
	var out coresearchFileView
	e := h.inWS(r, func(tx *store.Tx, ws, who uuid.UUID) error {
		f, e := lifecycleFile(r, tx, ws, who, true)
		if e != nil {
			return e
		}
		var in struct {
			Path string `json:"path"`
		}
		if e = readJSON(r, &in); e != nil {
			return cardInputError("a file path is required")
		}
		dest, e := coresearch.CleanPath(in.Path)
		if e != nil {
			return cardInputError(e.Error())
		}
		locked, e := tx.Q.LockCoresearchFile(r.Context(), db.LockCoresearchFileParams{WorkspaceID: ws, ProjectID: f.ProjectID, Path: f.Path})
		if e != nil {
			return e
		}
		if locked.ID != f.ID {
			return pgx.ErrNoRows
		}
		other, e := tx.Q.LockCoresearchFile(r.Context(), db.LockCoresearchFileParams{WorkspaceID: ws, ProjectID: f.ProjectID, Path: dest})
		if e == nil && other.ID != f.ID {
			return conflict("a file already uses that path")
		}
		if e != nil && !errors.Is(e, pgx.ErrNoRows) {
			return e
		}
		if f.Path != dest {
			f, e = tx.Q.RenameCoresearchFile(r.Context(), db.RenameCoresearchFileParams{WorkspaceID: ws, ProjectID: f.ProjectID, ID: f.ID, Path: dest})
			if e != nil {
				return e
			}
			if e = tx.Q.TouchCoresearchProject(r.Context(), db.TouchCoresearchProjectParams{WorkspaceID: ws, ID: f.ProjectID}); e != nil {
				return e
			}
			if e = tx.Emit(r.Context(), "coresearch.file.lifecycle", nil, store.User(who), map[string]any{"project_id": f.ProjectID, "file_id": f.ID}); e != nil {
				return e
			}
		}
		head, e := tx.Q.CoresearchFileHead(r.Context(), db.CoresearchFileHeadParams{WorkspaceID: ws, FileID: f.ID})
		if e != nil {
			return e
		}
		_, docErr := tx.Q.GetCoresearchDoc(r.Context(), db.GetCoresearchDocParams{WorkspaceID: ws, FileID: f.ID})
		if docErr != nil && !errors.Is(docErr, pgx.ErrNoRows) {
			return docErr
		}
		out = coresearchFileView{ID: f.ID, Path: f.Path, Head: head, HasDoc: docErr == nil}
		return nil
	})
	var unique *pgconn.PgError
	if errors.As(e, &unique) && unique.Code == "23505" {
		e = conflict("a file already uses that path")
	}
	if coresearchAnswer(w, r, e) {
		writeJSON(w, 200, out)
	}
}

// Hydration and blob staging surround the atomic clear/snapshot/hide publication.
func (h *H) deleteCoresearchFile(w http.ResponseWriter, r *http.Request) {
	ws, e := uuid.Parse(r.Header.Get("X-Workspace-ID"))
	if e != nil {
		coresearchAnswer(w, r, pgx.ErrNoRows)
		return
	}
	who := user(r)
	var f db.CoresearchFile
	var head db.CoresearchFileVersion
	journal := false
	e = h.memberTx(r, ws, false, func(tx *store.Tx) error {
		var e error
		f, e = lifecycleFile(r, tx, ws, who, false)
		if e != nil {
			return e
		}
		p, e := tx.Q.GetCoresearchProject(r.Context(), db.GetCoresearchProjectParams{WorkspaceID: ws, ID: f.ProjectID})
		if e != nil {
			return e
		}
		if p.ArchivedAt != nil {
			return coresearch.ErrArchived
		}
		_, e = tx.Q.GetCoresearchDoc(r.Context(), db.GetCoresearchDocParams{WorkspaceID: ws, FileID: f.ID})
		journal = e == nil
		if e != nil && !errors.Is(e, pgx.ErrNoRows) {
			return e
		}
		head, e = tx.Q.CoresearchFileHead(r.Context(), db.CoresearchFileHeadParams{WorkspaceID: ws, FileID: f.ID})
		return e
	})
	if e != nil {
		docAnswer(w, r, e)
		return
	}
	hydrated := doc.File{Record: f}
	textFile := journal || doc.Eligible(f)
	if textFile && !journal {
		if head.Size > coresearch.MaxTextBytes {
			docAnswer(w, r, doc.ErrInvalid)
			return
		}
		var rc io.ReadCloser
		rc, e = h.Blobs.Get(r.Context(), head.BlobKey)
		if e == nil {
			var b []byte
			b, e = io.ReadAll(io.LimitReader(rc, coresearch.MaxTextBytes+1))
			e = errors.Join(e, rc.Close())
			if e == nil && (int64(len(b)) != head.Size || !utf8.Valid(b) || strings.ContainsRune(string(b), 0)) {
				e = doc.ErrInvalid
			}
			hydrated.InitialText, hydrated.InitialHead = string(b), head.N
		}
		if e != nil {
			docAnswer(w, r, e)
			return
		}
	}
	publish := func() error {
		return h.coresearchSaveTx(r, ws, func(tx *store.Tx) error {
			fresh, e := lifecycleFile(r, tx, ws, who, true)
			if e != nil {
				return e
			}
			if fresh.ID != f.ID || fresh.Path != f.Path {
				return doc.ErrHeadChanged
			}
			locked, e := tx.Q.LockCoresearchFile(r.Context(), db.LockCoresearchFileParams{WorkspaceID: ws, ProjectID: f.ProjectID, Path: f.Path})
			if e != nil {
				return e
			}
			if locked.ID != f.ID || locked.DeletedAt != nil {
				return pgx.ErrNoRows
			}
			pending, e := tx.Q.ListCoresearchPendingRanges(r.Context(), db.ListCoresearchPendingRangesParams{WorkspaceID: ws, FileID: f.ID})
			if e != nil {
				return e
			}
			// A stale change is settled by the typing inside it and blocks nothing.
			if slices.ContainsFunc(pending, func(c db.CoresearchChange) bool { return c.Status == "pending" }) {
				return conflict("Keep or Undo the pending changes before removing this file")
			}
			// Restore can initialize a retained text journal even at a non-TeX path.
			// Refuse a changed classification rather than hiding nonempty live text.
			if !textFile {
				_, journalErr := tx.Q.GetCoresearchDoc(r.Context(), db.GetCoresearchDocParams{WorkspaceID: ws, FileID: f.ID})
				if journalErr == nil {
					return doc.ErrHeadChanged
				}
				if !errors.Is(journalErr, pgx.ErrNoRows) {
					return journalErr
				}
			}
			if textFile {
				hydrated.Record = fresh
				d, e := doc.Open(r.Context(), tx, hydrated)
				if e != nil {
					return e
				}
				if d.Text != "" {
					c := doc.Replace(0, doc.Length(d.Text), "", doc.Length(d.Text))
					raw, e := c.MarshalJSON()
					if e != nil {
						return e
					}
					d.Version, e = doc.Push(r.Context(), tx, hydrated, d.Version, []doc.Step{{ClientID: "delete:" + who.String(), Changes: raw}}, doc.Author{UserID: &who})
					if e != nil {
						return e
					}
				}
				d.Text = ""
				if _, e = doc.Snapshot(r.Context(), tx, hydrated, doc.PrepareSnapshot(d), doc.Author{UserID: &who}); e != nil {
					return e
				}
			}
			if e = doc.SetDeleted(r.Context(), tx, doc.File{Record: fresh}, true); e != nil {
				return e
			}
			return tx.Q.TouchCoresearchProject(r.Context(), db.TouchCoresearchProjectParams{WorkspaceID: ws, ID: f.ProjectID})
		})
	}
	if textFile {
		empty := doc.PrepareSnapshot(doc.Doc{})
		e = coresearch.WithBlob(r.Context(), h.Store, h.Blobs, ws, who, empty.SHA256, strings.NewReader(""), 0, publish)
	} else {
		e = publish()
	}
	if docAnswer(w, r, e) {
		w.WriteHeader(204)
	}
}
