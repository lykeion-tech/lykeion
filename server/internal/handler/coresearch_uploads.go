package handler

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"io"
	"net/http"
	"os"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"lykeion/server/internal/coresearch"
	"lykeion/server/internal/store"
	db "lykeion/server/pkg/db/generated"
)

type coresearchInputError string

func (e coresearchInputError) Error() string { return string(e) }

type coresearchAttachment struct {
	UploadID uuid.UUID `json:"upload_id"`
	Name     string    `json:"name"`
}

func (h *H) coresearchTemplates(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, 200, coresearch.Templates())
}

func (h *H) uploadCoresearchAttachment(w http.ResponseWriter, r *http.Request) {
	ws, err := uuid.Parse(r.Header.Get("X-Workspace-ID"))
	if err != nil {
		writeErr(w, 404, "workspace not found")
		return
	}
	if !coresearchAnswer(w, r, h.memberTx(r, ws, false, func(*store.Tx) error { return nil })) {
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, coresearch.MaxFileBytes+multipartSlack)
	mr, err := r.MultipartReader()
	if err != nil {
		writeErr(w, 400, "expected a multipart upload")
		return
	}
	tmp, err := os.CreateTemp("", "lyk-coresearch-upload-*")
	if err != nil {
		fail(w, r, err)
		return
	}
	defer os.Remove(tmp.Name())
	defer tmp.Close()
	hash := sha256.New()
	seen := false
	var n int64
	name := ""
	for {
		part, e := mr.NextPart()
		if errors.Is(e, io.EOF) {
			break
		}
		if e != nil {
			uploadReadErr(w, r, e, "A file can be at most 50 MiB")
			return
		}
		if seen || part.FormName() != "file" {
			writeErr(w, 400, "expected one file")
			return
		}
		seen = true
		name = part.FileName()
		n, e = io.Copy(io.MultiWriter(tmp, hash), io.LimitReader(part, coresearch.MaxFileBytes+1))
		if e == nil && n > coresearch.MaxFileBytes {
			e = &http.MaxBytesError{Limit: coresearch.MaxFileBytes}
		}
		if e != nil {
			uploadReadErr(w, r, e, "A file can be at most 50 MiB")
			return
		}
	}
	if !seen {
		writeErr(w, 400, "expected one file")
		return
	}
	if _, err = tmp.Seek(0, io.SeekStart); err != nil {
		fail(w, r, err)
		return
	}
	head, err := io.ReadAll(io.LimitReader(tmp, 8192))
	if err != nil {
		fail(w, r, err)
		return
	}
	if coresearch.IsText(head) && n > coresearch.MaxTextBytes {
		writeErr(w, 413, "Text files can be at most 1 MiB")
		return
	}
	if _, err = tmp.Seek(0, io.SeekStart); err != nil {
		fail(w, r, err)
		return
	}
	id := uuid.New()
	key := ws.String() + "/coresearch-uploads/" + id.String()
	if err = h.Blobs.Put(r.Context(), key, tmp, n); err != nil {
		_ = h.Blobs.Delete(context.WithoutCancel(r.Context()), key)
		fail(w, r, err)
		return
	}
	err = h.coresearchSaveTx(r, ws, func(tx *store.Tx) error {
		_, e := tx.Q.InsertPendingAttachment(r.Context(), db.InsertPendingAttachmentParams{ID: id, WorkspaceID: ws, UploaderID: user(r), Name: name, Kind: "file", ContentType: http.DetectContentType(head), SizeBytes: n, Sha256: hex.EncodeToString(hash.Sum(nil)), BlobKey: key})
		return e
	})
	if err != nil {
		_ = h.Blobs.Delete(context.WithoutCancel(r.Context()), key)
	}
	if coresearchAnswer(w, r, err) {
		writeJSON(w, 201, map[string]any{"upload_id": id})
	}
}

// stageCoresearchAttachments reads only authorized pending metadata in the short
// transaction. Blob readers and head classification run after it closes.
func (h *H) stageCoresearchAttachments(r *http.Request, ws uuid.UUID, target, mode string, attachments []coresearchAttachment) ([]db.Attachment, []coresearch.UploadBlob, map[string]bool, error) {
	rows := []db.Attachment{}
	heads := []db.CoresearchFileVersion{}
	textHeads := map[string]bool{}
	ids := []uuid.UUID{}
	names := map[string]bool{}
	seen := map[uuid.UUID]bool{}
	if len(attachments) > 100 {
		return nil, nil, nil, coresearchInputError("at most 100 attachments")
	}
	for _, a := range attachments {
		path, e := coresearch.AttachPath(mode, a.Name)
		if e != nil {
			return nil, nil, nil, coresearchInputError(e.Error())
		}
		if a.UploadID == uuid.Nil || names[path] || seen[a.UploadID] {
			return nil, nil, nil, coresearchInputError("unique attachments and names required")
		}
		names[path] = true
		seen[a.UploadID] = true
		ids = append(ids, a.UploadID)
	}
	err := h.memberTx(r, ws, false, func(tx *store.Tx) error {
		var err error
		if len(ids) > 0 {
			rows, err = tx.Q.PendingAttachments(r.Context(), db.PendingAttachmentsParams{WorkspaceID: ws, UploaderID: user(r), Ids: ids})
			if err != nil {
				return err
			}
			if len(rows) != len(ids) {
				return coresearchInputError("unknown or claimed upload")
			}
		}
		for _, row := range rows {
			if row.Kind != "file" || row.SizeBytes > coresearch.MaxFileBytes || !strings.HasPrefix(row.BlobKey, ws.String()+"/coresearch-uploads/") || time.Since(row.CreatedAt) > attachmentLife {
				return coresearchInputError("unknown or expired upload")
			}
		}
		if target != "" && len(ids) > 0 {
			id, e := uuid.Parse(target)
			if e != nil {
				return pgx.ErrNoRows
			}
			c, e := tx.Q.GetCoresearchConversation(r.Context(), id)
			if e != nil {
				return e
			}
			files, e := tx.Q.ListCoresearchFiles(r.Context(), db.ListCoresearchFilesParams{WorkspaceID: ws, ProjectID: c.ProjectID})
			if e != nil {
				return e
			}
			for _, f := range files {
				if names[f.Path] {
					head, e := tx.Q.CoresearchFileHead(r.Context(), db.CoresearchFileHeadParams{WorkspaceID: ws, FileID: f.ID})
					if e != nil {
						return e
					}
					heads = append(heads, head)
				}
			}
		}
		return nil
	})
	if err != nil {
		return nil, nil, nil, err
	}
	for _, head := range heads {
		rc, e := h.Blobs.Get(r.Context(), head.BlobKey)
		if e != nil {
			return nil, nil, nil, e
		}
		b, e := io.ReadAll(io.LimitReader(rc, 8192))
		e = errors.Join(e, rc.Close())
		if e != nil {
			return nil, nil, nil, e
		}
		textHeads[head.Sha256] = coresearch.IsText(b)
	}
	uploads := []coresearch.UploadBlob{}
	for _, row := range rows {
		row := row
		rc, e := h.Blobs.Get(r.Context(), row.BlobKey)
		if e != nil {
			return nil, nil, nil, e
		}
		head, e := io.ReadAll(io.LimitReader(rc, 8192))
		e = errors.Join(e, rc.Close())
		if e != nil {
			return nil, nil, nil, e
		}
		textHeads[row.Sha256] = coresearch.IsText(head)
		if textHeads[row.Sha256] && row.SizeBytes > coresearch.MaxTextBytes {
			return nil, nil, nil, coresearchInputError("text files can be at most 1 MiB")
		}
		uploads = append(uploads, coresearch.UploadBlob{SHA256: row.Sha256, Size: row.SizeBytes, Open: func() (io.ReadCloser, error) { return h.Blobs.Get(r.Context(), row.BlobKey) }})
	}
	return rows, uploads, textHeads, nil
}
