package handler

import (
	"encoding/json"
	"errors"
	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"io"
	"lykeion/server/internal/coresearch"
	"lykeion/server/internal/coresearch/doc"
	"lykeion/server/internal/store"
	db "lykeion/server/pkg/db/generated"
	"net/http"
	"strconv"
	"strings"
	"unicode/utf8"
)

func paperAnswer(w http.ResponseWriter, r *http.Request, e error) bool {
	if errors.Is(e, doc.ErrEmptyFind) {
		writeErr(w, 422, e.Error())
		return false
	}
	if errors.Is(e, doc.ErrDecided) {
		writeErr(w, 409, e.Error())
		return false
	}
	return docAnswer(w, r, e)
}
func (h *H) coresearchChanges(w http.ResponseWriter, r *http.Request) {
	var out any
	e := h.inWS(r, func(tx *store.Tx, ws, who uuid.UUID) error {
		p, e := coresearchProject(r, tx, ws, who, r.Method == "POST", false)
		if e != nil {
			return e
		}
		if p.Mode != "cowrite" {
			return pgx.ErrNoRows
		}
		if r.Method == "GET" {
			f, e := coresearchFile(r, tx, ws, p.ID)
			if e != nil {
				return e
			}
			status := r.URL.Query().Get("status")
			if status != "" && status != "pending" && status != "stale" && status != "kept" && status != "undone" {
				return doc.ErrInvalid
			}
			rows, e := tx.Q.ListCoresearchChanges(r.Context(), db.ListCoresearchChangesParams{WorkspaceID: ws, FileID: f.ID, Status: status})
			if rows == nil {
				rows = []db.CoresearchChange{}
			}
			out = rows
			return e
		}
		id, e := coresearchID(r, "change")
		if e != nil {
			return e
		}
		row, e := tx.Q.GetCoresearchChange(r.Context(), db.GetCoresearchChangeParams{WorkspaceID: ws, ID: id, ProjectID: p.ID})
		if e != nil {
			return e
		}
		f, e := tx.Q.GetCoresearchRetainedFile(r.Context(), db.GetCoresearchRetainedFileParams{WorkspaceID: ws, ProjectID: p.ID, ID: row.FileID})
		if e != nil {
			return e
		}
		if f.DeletedAt != nil {
			return pgx.ErrNoRows
		}
		if e = doc.Decide(r.Context(), tx, doc.File{Record: f}, id, who, strings.HasSuffix(r.URL.Path, "/keep")); e != nil {
			return e
		}
		out, e = tx.Q.GetCoresearchChange(r.Context(), db.GetCoresearchChangeParams{WorkspaceID: ws, ID: id, ProjectID: p.ID})
		return e
	})
	if paperAnswer(w, r, e) {
		writeJSON(w, 200, out)
	}
}
func (h *H) coresearchPaperVersions(w http.ResponseWriter, r *http.Request) {
	var out []db.CoresearchVersion
	e := h.inWS(r, func(tx *store.Tx, ws, who uuid.UUID) error {
		p, e := coresearchProject(r, tx, ws, who, false, false)
		if e != nil {
			return e
		}
		if p.Mode != "cowrite" {
			return pgx.ErrNoRows
		}
		out, e = tx.Q.ListCoresearchVersions(r.Context(), db.ListCoresearchVersionsParams{WorkspaceID: ws, ProjectID: p.ID})
		return e
	})
	if out == nil {
		out = []db.CoresearchVersion{}
	}
	if paperAnswer(w, r, e) {
		writeJSON(w, 200, out)
	}
}

// hydratePaper reads immutable source bytes only between authorization transactions.
func (h *H) hydratePaper(r *http.Request, f *doc.File, head db.CoresearchFileVersion) error {
	if head.Size > coresearch.MaxTextBytes {
		return doc.ErrInvalid
	}
	text, e := h.paperBlob(r, head)
	if e != nil {
		return e
	}
	f.InitialText = text
	f.InitialHead = head.N
	return nil
}
func (h *H) paperBlob(r *http.Request, v db.CoresearchFileVersion) (string, error) {
	if v.Size < 0 || v.Size > coresearch.MaxTextBytes {
		return "", doc.ErrInvalid
	}
	rc, e := h.Blobs.Get(r.Context(), v.BlobKey)
	if e != nil {
		return "", e
	}
	defer rc.Close()
	b, e := io.ReadAll(io.LimitReader(rc, coresearch.MaxTextBytes+1))
	if e != nil {
		return "", e
	}
	if int64(len(b)) != v.Size || !utf8.Valid(b) || strings.ContainsRune(string(b), 0) {
		return "", doc.ErrInvalid
	}
	return string(b), nil
}
func (h *H) coresearchPaper(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Path  string     `json:"path"`
		Base  *int64     `json:"base"`
		Hunks []doc.Hunk `json:"hunks"`
	}
	if r.Method == "POST" {
		if readJSON(r, &in) != nil || in.Base == nil {
			paperAnswer(w, r, doc.ErrInvalid)
			return
		}
	} else {
		in.Path = chi.URLParam(r, "*")
	}
	path, e := coresearch.CleanPath(in.Path)
	if e != nil || path != in.Path {
		paperAnswer(w, r, pgx.ErrNoRows)
		return
	}
	var out any
	for attempt := 0; attempt < 4; attempt++ {
		var f doc.File
		var head db.CoresearchFileVersion
		exists := false
		e = h.coresearchRunTx(r, false, func(tx *store.Tx, c db.CoresearchChat, _ uuid.UUID) error {
			if c.Mode != "cowrite" {
				return pgx.ErrNoRows
			}
			row, e := tx.Q.CoresearchFileByPath(r.Context(), db.CoresearchFileByPathParams{WorkspaceID: tx.WS, ProjectID: c.ProjectID, Path: path})
			if e != nil {
				return e
			}
			f.Record = row
			if !doc.Eligible(row) {
				if _, e := tx.Q.GetCoresearchDoc(r.Context(), db.GetCoresearchDocParams{WorkspaceID: tx.WS, FileID: row.ID}); e != nil {
					return e
				}
			}
			_, e = tx.Q.GetCoresearchDoc(r.Context(), db.GetCoresearchDocParams{WorkspaceID: tx.WS, FileID: row.ID})
			if e == nil {
				exists = true
				return nil
			}
			if !errors.Is(e, pgx.ErrNoRows) {
				return e
			}
			head, e = tx.Q.CoresearchFileHead(r.Context(), db.CoresearchFileHeadParams{WorkspaceID: tx.WS, FileID: row.ID})
			return e
		})
		if e != nil {
			break
		}
		if !exists {
			e = h.hydratePaper(r, &f, head)
			if e != nil {
				break
			}
		}
		e = h.coresearchRunTx(r, r.Method == "POST", func(tx *store.Tx, c db.CoresearchChat, run uuid.UUID) error {
			fresh, e := tx.Q.CoresearchFileByPath(r.Context(), db.CoresearchFileByPathParams{WorkspaceID: tx.WS, ProjectID: c.ProjectID, Path: path})
			if e != nil {
				return e
			}
			if fresh.ID != f.Record.ID {
				return doc.ErrHeadChanged
			}
			f.Record = fresh
			d, e := doc.Open(r.Context(), tx, f)
			if e != nil {
				return e
			}
			if r.Method == "GET" {
				out = d
				return nil
			}
			p, e := tx.Q.InsertCoresearchProposal(r.Context(), db.InsertCoresearchProposalParams{WorkspaceID: tx.WS, ProjectID: c.ProjectID, ChatID: &c.ID, AgentRunID: &run})
			if e != nil {
				return e
			}
			results, e := doc.ApplyHunks(r.Context(), tx, f, *in.Base, in.Hunks, run, p.ID)
			if e != nil {
				return e
			}
			d, e = doc.Open(r.Context(), tx, f)
			out = map[string]any{"version": d.Version, "results": results}
			return e
		})
		if !errors.Is(e, doc.ErrHeadChanged) {
			break
		}
	}
	if paperAnswer(w, r, e) {
		writeJSON(w, 200, out)
	}
}
func (h *H) restoreCoresearchPaper(w http.ResponseWriter, r *http.Request) {
	ws, e := uuid.Parse(r.Header.Get("X-Workspace-ID"))
	if e != nil {
		paperAnswer(w, r, pgx.ErrNoRows)
		return
	}
	n, e := strconv.ParseInt(chi.URLParam(r, "seq"), 10, 32)
	if e != nil || n < 1 {
		paperAnswer(w, r, pgx.ErrNoRows)
		return
	}
	type target struct {
		file     doc.File
		head     db.CoresearchFileVersion
		hydrate  bool
		snapshot *db.CoresearchFileVersion
		text     string
		skip     bool
	}
	var targets []target
	var project uuid.UUID
	var out db.CoresearchProposal
	e = h.memberTx(r, ws, false, func(tx *store.Tx) error {
		p, e := coresearchProject(r, tx, ws, user(r), true, false)
		if e != nil {
			return e
		}
		if p.Mode != "cowrite" {
			return pgx.ErrNoRows
		}
		project = p.ID
		v, e := tx.Q.GetCoresearchVersion(r.Context(), db.GetCoresearchVersionParams{WorkspaceID: ws, ProjectID: p.ID, Seq: int32(n)})
		if e != nil {
			return e
		}
		var refs []coresearch.FileRef
		if e = json.Unmarshal(v.Files, &refs); e != nil {
			return e
		}
		byID := map[uuid.UUID]coresearch.FileRef{}
		for _, ref := range refs {
			byID[ref.FileID] = ref
		}
		files, e := tx.Q.ListCoresearchRetainedFiles(r.Context(), db.ListCoresearchRetainedFilesParams{WorkspaceID: ws, ProjectID: p.ID})
		if e != nil {
			return e
		}
		for _, f := range files {
			ref, has := byID[f.ID]
			if !has && f.DeletedAt != nil {
				continue
			}
			item := target{file: doc.File{Record: f}}
			if has {
				v, e := tx.Q.CoresearchFileVersion(r.Context(), db.CoresearchFileVersionParams{WorkspaceID: ws, FileID: f.ID, N: ref.N})
				if e != nil {
					return e
				}
				item.snapshot = &v
			}
			_, e = tx.Q.GetCoresearchDoc(r.Context(), db.GetCoresearchDocParams{WorkspaceID: ws, FileID: f.ID})
			if errors.Is(e, pgx.ErrNoRows) {
				item.hydrate = true
				item.head, e = tx.Q.CoresearchFileHead(r.Context(), db.CoresearchFileHeadParams{WorkspaceID: ws, FileID: f.ID})
			}
			if e != nil {
				return e
			}
			targets = append(targets, item)
		}
		return nil
	})
	if e != nil {
		paperAnswer(w, r, e)
		return
	}
	for i := range targets {
		t := &targets[i]

		if t.hydrate {
			var isText bool
			t.file.InitialText, isText, e = h.restoreTextBlob(r, t.head)
			if e != nil {
				break
			}
			t.file.InitialHead = t.head.N
			if !isText {
				t.skip = true
				continue
			}
		}
		if t.snapshot != nil {
			var isText bool
			t.text, isText, e = h.restoreTextBlob(r, *t.snapshot)
			if e != nil {
				break
			}
			if !isText {
				t.skip = true
			}
		}

	}
	if e == nil {
		e = h.coresearchSaveTx(r, ws, func(tx *store.Tx) error {
			p, e := coresearchProject(r, tx, ws, user(r), true, false)
			if e != nil {
				return e
			}
			if p.ID != project {
				return pgx.ErrNoRows
			}
			seq := int32(n)
			who := user(r)
			out, e = tx.Q.InsertCoresearchProposal(r.Context(), db.InsertCoresearchProposalParams{WorkspaceID: ws, ProjectID: p.ID, RestoreOf: &seq, CreatedBy: &who})
			if e != nil {
				return e
			}
			// The file set must still match the staged read; a newly added file needs its own removal proposal.
			fresh, e := tx.Q.ListCoresearchRetainedFiles(r.Context(), db.ListCoresearchRetainedFilesParams{WorkspaceID: ws, ProjectID: p.ID})
			if e != nil {
				return e
			}
			known := map[uuid.UUID]bool{}
			for _, t := range targets {
				known[t.file.Record.ID] = true
			}
			for _, f := range fresh {
				if f.DeletedAt == nil && !known[f.ID] {
					return conflict("paper files changed; retry restore")
				}
			}
			for _, t := range targets {
				f, e := tx.Q.GetCoresearchRetainedFile(r.Context(), db.GetCoresearchRetainedFileParams{WorkspaceID: ws, ProjectID: p.ID, ID: t.file.Record.ID})
				if e != nil {
					return e
				}
				if f.Path != t.file.Record.Path || (f.DeletedAt == nil) != (t.file.Record.DeletedAt == nil) {
					return conflict("paper files changed; retry restore")
				}

				if t.skip {
					if t.hydrate {
						head, e := tx.Q.CoresearchFileHead(r.Context(), db.CoresearchFileHeadParams{WorkspaceID: ws, FileID: f.ID})
						if e != nil {
							return e
						}
						if head.N != t.head.N {
							return conflict("file changed; retry restore")
						}
					}
					continue
				}
				t.file.Record = f
				d, e := doc.OpenText(r.Context(), tx, t.file)
				if e != nil {
					return e
				}
				action := ""
				if t.snapshot == nil {
					action = "remove"
				} else if f.DeletedAt != nil {
					action = "restore"
				}
				if d.Text == t.text && action == "" {
					continue
				}
				row, e := doc.Propose(r.Context(), tx, t.file, d, 0, doc.Length(d.Text), t.text, out.ID, doc.Author{UserID: &who})
				if e != nil {
					return e
				}
				if action != "" {
					if e = tx.Q.SetCoresearchChangeFileAction(r.Context(), db.SetCoresearchChangeFileActionParams{WorkspaceID: ws, ID: row.ID, FileAction: action}); e != nil {
						return e
					}
					if action == "restore" {
						if e = doc.SetDeleted(r.Context(), tx, t.file, false); e != nil {
							return e
						}
					}
				}
			}
			return tx.Emit(r.Context(), "coresearch.proposal.created", nil, store.User(who), map[string]any{"project_id": p.ID, "proposal_id": out.ID})
		})
	}
	if paperAnswer(w, r, e) {
		writeJSON(w, 201, out)
	}
}

func (h *H) restoreTextBlob(r *http.Request, v db.CoresearchFileVersion) (string, bool, error) {
	if v.Size < 0 || v.Size > coresearch.MaxFileBytes {
		return "", false, doc.ErrInvalid
	}
	rc, e := h.Blobs.Get(r.Context(), v.BlobKey)
	if e != nil {
		return "", false, e
	}
	defer rc.Close()
	b, e := io.ReadAll(io.LimitReader(rc, coresearch.MaxFileBytes+1))
	if e != nil {
		return "", false, e
	}
	if int64(len(b)) != v.Size {
		return "", false, doc.ErrInvalid
	}
	if !coresearch.IsText(b) || !utf8.Valid(b) || strings.ContainsRune(string(b), 0) {
		return "", false, nil
	}
	if len(b) > int(coresearch.MaxTextBytes) {
		return "", true, doc.ErrInvalid
	}
	return string(b), true, nil
}
