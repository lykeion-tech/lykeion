package handler

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"io"
	"mime"
	"net/http"
	"os"
	"path"
	"strconv"
	"strings"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"lykeion/server/internal/coresearch"
	"lykeion/server/internal/middleware"
	"lykeion/server/internal/store"
	db "lykeion/server/pkg/db/generated"
)

type coresearchFileView struct {
	HasDoc bool                     `json:"has_doc"`
	ID     uuid.UUID                `json:"id"`
	Path   string                   `json:"path"`
	Head   db.CoresearchFileVersion `json:"head"`
}

func (h *H) listCoresearchFiles(w http.ResponseWriter, r *http.Request) {
	out := []coresearchFileView{}
	err := h.inWS(r, func(tx *store.Tx, ws, who uuid.UUID) error {
		p, err := coresearchProject(r, tx, ws, who, false, false)
		if err != nil {
			return err
		}
		files, err := tx.Q.ListCoresearchFiles(r.Context(), db.ListCoresearchFilesParams{WorkspaceID: ws, ProjectID: p.ID})
		if err != nil {
			return err
		}
		journalIDs, err := tx.Q.ListCoresearchDocumentFiles(r.Context(), db.ListCoresearchDocumentFilesParams{WorkspaceID: ws, ProjectID: p.ID})
		if err != nil {
			return err
		}
		journals := make(map[uuid.UUID]bool, len(journalIDs))
		for _, id := range journalIDs {
			journals[id] = true
		}
		for _, f := range files {
			head, err := tx.Q.CoresearchFileHead(r.Context(), db.CoresearchFileHeadParams{WorkspaceID: ws, FileID: f.ID})
			if err != nil {
				return err
			}
			out = append(out, coresearchFileView{ID: f.ID, Path: f.Path, Head: head, HasDoc: journals[f.ID]})
		}
		return nil
	})
	if coresearchAnswer(w, r, err) {
		writeJSON(w, 200, out)
	}
}
func coresearchFile(r *http.Request, tx *store.Tx, ws, project uuid.UUID) (db.CoresearchFile, error) {
	id, err := coresearchID(r, "file")
	if err != nil {
		return db.CoresearchFile{}, err
	}
	return tx.Q.GetCoresearchFile(r.Context(), db.GetCoresearchFileParams{WorkspaceID: ws, ProjectID: project, ID: id})
}
func (h *H) coresearchVersions(w http.ResponseWriter, r *http.Request) {
	out := []db.CoresearchFileVersion{}
	err := h.inWS(r, func(tx *store.Tx, ws, who uuid.UUID) error {
		p, err := coresearchProject(r, tx, ws, who, false, false)
		if err != nil {
			return err
		}
		f, err := coresearchFile(r, tx, ws, p.ID)
		if err != nil {
			return err
		}
		out, err = tx.Q.ListCoresearchFileVersions(r.Context(), db.ListCoresearchFileVersionsParams{WorkspaceID: ws, FileID: f.ID})
		return err
	})
	if coresearchAnswer(w, r, err) {
		writeJSON(w, 200, out)
	}
}
func (h *H) restoreCoresearchFile(w http.ResponseWriter, r *http.Request) {
	var out db.CoresearchFileVersion
	err := h.inWS(r, func(tx *store.Tx, ws, who uuid.UUID) error {
		p, err := coresearchProject(r, tx, ws, who, true, false)
		if err != nil {
			return err
		}
		f, err := coresearchFile(r, tx, ws, p.ID)
		if err != nil {
			return err
		}
		var in struct {
			N    int32 `json:"n"`
			Base *int  `json:"base"`
		}
		if err = readJSON(r, &in); err != nil || in.N <= 0 || in.Base == nil || *in.Base < 0 {
			return cardInputError("a version and nonnegative base are required")
		}
		v, err := tx.Q.CoresearchFileVersion(r.Context(), db.CoresearchFileVersionParams{WorkspaceID: ws, FileID: f.ID, N: in.N})
		if err != nil {
			return err
		}
		if _, e := coresearch.CleanPath(f.Path); e != nil {
			return cardInputError(e.Error())
		}
		out, err = coresearch.SaveVersion(r.Context(), tx, coresearch.SaveIn{Workspace: ws, Project: p.ID, Path: f.Path, Sha256: v.Sha256, Size: v.Size, Base: *in.Base, Author: &who, RestoredFrom: &in.N})
		return err
	})
	if coresearchAnswer(w, r, err) {
		writeJSON(w, 201, out)
	}
}

// coresearchSaveTx follows Scope's workspace -> member -> project -> file lock
// order without requiring a workspace admin. Only the final database save runs
// here; upload spooling and blob storage stay outside the transaction.
func (h *H) coresearchSaveTx(r *http.Request, ws uuid.UUID, save func(*store.Tx) error) error {
	who := user(r)
	return h.Store.InWorkspace(r.Context(), ws, who, func(tx *store.Tx) error {
		if _, err := tx.Q.LockWorkspaceForMutation(r.Context(), ws); err != nil {
			return err
		}
		if _, err := tx.Q.LockMember(r.Context(), db.LockMemberParams{WorkspaceID: ws, UserID: who}); err != nil {
			return err
		}
		return save(tx)
	})
}

func (h *H) uploadCoresearchFile(w http.ResponseWriter, r *http.Request) {
	role := middleware.RoleFrom(r.Context())
	ws, err := uuid.Parse(r.Header.Get("X-Workspace-ID"))
	who := user(r)
	var agent *uuid.UUID
	var project db.CoresearchProject
	if role.AgentRunID != uuid.Nil {
		ws = role.WorkspaceID
		who = role.AgentRunID
		agent = &role.AgentRunID
		err = h.coresearchRunTx(r, true, func(tx *store.Tx, c db.CoresearchChat, _ uuid.UUID) error {
			var e error
			project, e = tx.Q.GetCoresearchProject(r.Context(), db.GetCoresearchProjectParams{WorkspaceID: ws, ID: c.ProjectID})
			return e
		})
	} else if err == nil {
		err = h.memberTx(r, ws, false, func(tx *store.Tx) error {
			var e error
			project, e = coresearchProject(r, tx, ws, who, false, false)
			if e == nil && project.ArchivedAt != nil {
				return coresearch.ErrArchived
			}
			return e
		})
	} else {
		err = pgx.ErrNoRows
	}
	if !coresearchAnswer(w, r, err) {
		return
	}
	w.Header().Set("Connection", "close")
	r.Body = http.MaxBytesReader(w, r.Body, coresearch.MaxFileBytes+multipartSlack)
	mr, err := r.MultipartReader()
	if err != nil {
		writeErr(w, 400, "expected a multipart upload")
		return
	}
	tmp, err := os.CreateTemp("", "lyk-coresearch-*")
	if err != nil {
		fail(w, r, err)
		return
	}
	defer os.Remove(tmp.Name())
	defer tmp.Close()
	fields := map[string]string{}
	seenFile := false
	var size int64
	hash := sha256.New()
	for {
		part, err := mr.NextPart()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			uploadReadErr(w, r, err, "A file can be at most 50 MiB")
			return
		}
		if part.FormName() == "file" {
			if seenFile {
				writeErr(w, 400, "expected one file")
				return
			}
			seenFile = true
			size, err = io.Copy(io.MultiWriter(tmp, hash), io.LimitReader(part, coresearch.MaxFileBytes+1))
			if err == nil && size > coresearch.MaxFileBytes {
				err = &http.MaxBytesError{Limit: coresearch.MaxFileBytes}
			}
			if err != nil {
				uploadReadErr(w, r, err, "A file can be at most 50 MiB")
				return
			}
			continue
		}
		name := part.FormName()
		if name != "path" && name != "base" && name != "force" {
			writeErr(w, 400, "unknown upload field")
			return
		}
		if _, ok := fields[name]; ok {
			writeErr(w, 400, "repeated upload field")
			return
		}
		b, err := io.ReadAll(io.LimitReader(part, 1025))
		if err != nil {
			uploadReadErr(w, r, err, "A file can be at most 50 MiB")
			return
		}
		if len(b) > 1024 {
			writeErr(w, 400, "upload field exceeds 1024 bytes")
			return
		}
		fields[name] = string(b)
	}
	clean, err := coresearch.CleanPath(fields["path"])
	if err != nil {
		writeErr(w, 400, err.Error())
		return
	}
	base, err := strconv.Atoi(fields["base"])
	if err != nil || base < 0 || !seenFile {
		writeErr(w, 400, "a file and nonnegative base are required")
		return
	}
	force := false
	if text, ok := fields["force"]; ok {
		force, err = strconv.ParseBool(text)
		if err != nil {
			writeErr(w, 400, "force must be a boolean")
			return
		}
	}
	head := make([]byte, 8192)
	n, _ := tmp.ReadAt(head, 0)
	if coresearch.IsText(head[:n]) && size > coresearch.MaxTextBytes {
		writeErr(w, 413, "A text file can be at most 1 MiB")
		return
	}
	if _, err = tmp.Seek(0, io.SeekStart); err != nil {
		fail(w, r, err)
		return
	}
	sum := hex.EncodeToString(hash.Sum(nil))
	var out db.CoresearchFileVersion
	err = coresearch.WithBlob(r.Context(), h.Store, h.Blobs, ws, who, sum, tmp, size, func() error {
		save := func(tx *store.Tx) error {
			var author *uuid.UUID
			if agent == nil {
				author = &who
			}
			var e error
			out, e = coresearch.SaveVersion(r.Context(), tx, coresearch.SaveIn{Workspace: ws, Project: project.ID, Path: clean, Sha256: sum, Size: size, Base: base, Force: force, Author: author, AgentRun: agent})
			return e
		}
		if agent != nil {
			return h.coresearchRunTx(r, true, func(tx *store.Tx, _ db.CoresearchChat, _ uuid.UUID) error { return save(tx) })
		}
		return h.coresearchSaveTx(r, ws, save)
	})
	if coresearchAnswer(w, r, err) {
		writeJSON(w, 201, out)
	}
}
func (h *H) downloadCoresearchFile(w http.ResponseWriter, r *http.Request) {
	ws, err := uuid.Parse(r.Header.Get("X-Workspace-ID"))
	if err != nil {
		coresearchAnswer(w, r, pgx.ErrNoRows)
		return
	}
	var f db.CoresearchFile
	var v db.CoresearchFileVersion
	err = h.memberTx(r, ws, false, func(tx *store.Tx) error {
		p, err := coresearchProject(r, tx, ws, user(r), false, false)
		if err != nil {
			return err
		}
		f, err = coresearchFile(r, tx, ws, p.ID)
		if err != nil {
			return err
		}
		n, err := strconv.ParseInt(chi.URLParam(r, "n"), 10, 32)
		if err != nil || n <= 0 {
			return pgx.ErrNoRows
		}
		v, err = tx.Q.CoresearchFileVersion(r.Context(), db.CoresearchFileVersionParams{WorkspaceID: ws, FileID: f.ID, N: int32(n)})
		return err
	})
	if !coresearchAnswer(w, r, err) {
		return
	}
	rc, err := h.Blobs.Get(r.Context(), v.BlobKey)
	if err != nil {
		logoFail(w, r, err)
		return
	}
	defer rc.Close()
	typ := mime.TypeByExtension(strings.ToLower(path.Ext(f.Path)))
	if typ == "" {
		typ = "application/octet-stream"
	}
	w.Header().Set("Content-Type", typ)
	w.Header().Set("X-Content-Type-Options", "nosniff")
	media, _, _ := mime.ParseMediaType(typ)
	if media != "image/png" && media != "text/plain" {
		w.Header().Set("Content-Disposition", mime.FormatMediaType("attachment", map[string]string{"filename": path.Base(f.Path)}))
	}
	w.Header().Set("Content-Length", strconv.FormatInt(v.Size, 10))
	w.WriteHeader(200)
	_, _ = io.Copy(w, rc)
}
