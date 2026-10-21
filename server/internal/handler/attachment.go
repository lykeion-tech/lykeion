package handler

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"io"
	"log/slog"
	"net"
	"net/http"
	"os"
	"path"
	"slices"
	"strings"
	"time"

	"github.com/google/uuid"

	"lykeion/server/internal/records"
	"lykeion/server/internal/storage"
	"lykeion/server/internal/store"
	db "lykeion/server/pkg/db/generated"
)

const (
	// MaxAttachmentBytes is the most one attached file may hold.
	MaxAttachmentBytes = 100 << 20
	// multipartSlack lets the body carry the multipart framing around a file
	// of exactly the limit; the file itself is counted separately.
	multipartSlack = 64 << 10
	// attachmentLife is how long an upload may stay unlinked to a study.
	attachmentLife = 24 * time.Hour
	// A study holds at most maxStudyFiles files totalling maxStudyBytes.
	maxStudyFiles = 100
	maxStudyBytes = 500 << 20
)

// errFileTaken is an upload linked to another study, or swept, after this
// study read it as pending.
const errFileTaken = conflict("An attached file is no longer available")

// studyFile is a pending upload a new study takes, with its text read.
type studyFile struct {
	db.Attachment
	text    string
	hasText bool
}

// studyFiles checks a new study's attachment ids against the caller's pending
// uploads and the study's limits, then reads each file's text. Nothing is
// written, so a refusal here leaves the uploads pending.
// ponytail: every file's text is held in memory at once (at most the 500 MB a
// study may total); stream through temp files if that proves too much.
func (h *H) studyFiles(r *http.Request, ids []uuid.UUID) ([]studyFile, error) {
	ids = slices.Compact(slices.SortedFunc(slices.Values(ids), func(a, b uuid.UUID) int { return bytes.Compare(a[:], b[:]) }))
	if len(ids) == 0 {
		return nil, nil
	}
	if len(ids) > maxStudyFiles {
		return nil, cardInputError("A study can have at most 100 files")
	}
	var rows []db.Attachment
	err := h.inWS(r, func(tx *store.Tx, ws, user uuid.UUID) error {
		var err error
		rows, err = tx.Q.PendingAttachments(r.Context(), db.PendingAttachmentsParams{WorkspaceID: ws, UploaderID: user, Ids: ids})
		return err
	})
	if err != nil {
		return nil, err
	}
	if len(rows) != len(ids) {
		return nil, cardInputError("Unknown attachment")
	}
	var total int64
	for _, a := range rows {
		total += a.SizeBytes
	}
	if total > maxStudyBytes {
		return nil, cardInputError("A study's files can total at most 500 MB")
	}
	files := make([]studyFile, len(rows))
	for i, a := range rows {
		rc, err := h.Blobs.Get(r.Context(), a.BlobKey)
		if errors.Is(err, storage.ErrNotFound) {
			return nil, errFileTaken
		}
		if err != nil {
			return nil, err
		}
		text, ok, err := records.ExtractText(a.Name, a.ContentType, rc)
		rc.Close()
		if err != nil {
			return nil, err
		}
		files[i] = studyFile{a, text, ok}
	}
	return files, nil
}

// linkStudyFiles records each file as a source of the new study and links its
// upload to it, under a name unique within the study. An upload another study
// linked first refuses this one. Text blobs written are added to textBlobs.
func linkStudyFiles(r *http.Request, tx *store.Tx, blobs storage.Storage, card db.Card, files []studyFile, textBlobs *[]string) error {
	names := make([]string, len(files))
	for i, f := range files {
		names[i] = f.Name
	}
	disk := records.DiskNames(names)
	for i, f := range files {
		if f.hasText {
			*textBlobs = append(*textBlobs, records.TextBlobKey(card, f.text))
		}
		key, err := records.RecordAttachment(r.Context(), tx, blobs, card, f.Attachment, f.text, f.hasText)
		if err != nil {
			return err
		}
		n, err := tx.Q.LinkAttachment(r.Context(), db.LinkAttachmentParams{ID: f.ID, CardID: &card.ID, DiskName: disk[i], RecordKey: key})
		if err != nil {
			return err
		}
		if n == 0 {
			return errFileTaken
		}
	}
	return nil
}

var textTypes = map[string]string{
	".csv": "text/csv", ".json": "application/json", ".md": "text/markdown", ".txt": "text/plain",
	".py": "text/x-python", ".r": "text/x-r", ".ipynb": "application/x-ipynb+json", ".tsv": "text/tab-separated-values",
}

func attachmentType(name string, head []byte) string {
	if t, ok := textTypes[strings.ToLower(path.Ext(name))]; ok {
		return t
	}
	return http.DetectContentType(head)
}

// uploadAttachment streams one multipart file into a temp file while hashing
// it, stores the blob, and records an unlinked row for the uploader. The
// request is authorized before any of the body is read, and a refused upload
// leaves no row, no blob and no temp file.
func (h *H) uploadAttachment(w http.ResponseWriter, r *http.Request) {
	ws, err := uuid.Parse(r.Header.Get("X-Workspace-ID"))
	if err != nil {
		writeErr(w, 404, "workspace not found")
		return
	}
	if err := h.memberTx(r, ws, false, func(*store.Tx) error { return nil }); err != nil {
		logoFail(w, r, err)
		return
	}
	// The body may be unread on a refusal; close so net/http never drains it.
	w.Header().Set("Connection", "close")
	r.Body = http.MaxBytesReader(w, r.Body, MaxAttachmentBytes+multipartSlack)
	mr, err := r.MultipartReader()
	if err != nil {
		writeErr(w, 400, "expected a multipart upload")
		return
	}
	var part io.Reader
	var name string
	for {
		p, err := mr.NextPart()
		if err != nil {
			attachmentReadErr(w, r, err)
			return
		}
		if p.FormName() == "file" && p.FileName() != "" {
			part, name = p, path.Base(strings.ReplaceAll(p.FileName(), `\`, "/"))
			break
		}
	}
	tmp, err := os.CreateTemp("", "lyk-attachment-*")
	if err != nil {
		fail(w, r, err)
		return
	}
	defer os.Remove(tmp.Name())
	defer tmp.Close()
	hash := sha256.New()
	n, err := io.Copy(io.MultiWriter(tmp, hash), io.LimitReader(part, MaxAttachmentBytes+1))
	if err == nil && n > MaxAttachmentBytes {
		err = &http.MaxBytesError{Limit: MaxAttachmentBytes}
	}
	if err != nil {
		attachmentReadErr(w, r, err)
		return
	}
	head := make([]byte, 512)
	hn, _ := tmp.ReadAt(head, 0)
	id := uuid.New()
	key := ws.String() + "/attachments/" + id.String()
	if _, err := tmp.Seek(0, io.SeekStart); err != nil {
		fail(w, r, err)
		return
	}
	if err := h.Blobs.Put(r.Context(), key, tmp, n); err != nil {
		fail(w, r, err)
		return
	}
	sum, typ := hex.EncodeToString(hash.Sum(nil)), attachmentType(name, head[:hn])
	var row db.Attachment
	err = h.Store.InWorkspace(r.Context(), ws, user(r), func(tx *store.Tx) error {
		var err error
		row, err = tx.Q.InsertPendingAttachment(r.Context(), db.InsertPendingAttachmentParams{
			ID: id, WorkspaceID: ws, UploaderID: user(r), Name: name, ContentType: typ, SizeBytes: n, Sha256: sum, BlobKey: key})
		return err
	})
	if err != nil {
		_ = h.Blobs.Delete(context.WithoutCancel(r.Context()), key)
		fail(w, r, err)
		return
	}
	writeJSON(w, 201, map[string]any{"id": row.ID, "name": row.Name, "size_bytes": row.SizeBytes, "content_type": row.ContentType, "sha256": row.Sha256})
}

func attachmentReadErr(w http.ResponseWriter, r *http.Request, err error) {
	_ = http.NewResponseController(w).SetReadDeadline(time.Now())
	var large *http.MaxBytesError
	var network net.Error
	var disk *os.PathError
	switch {
	case errors.As(err, &large):
		writeErr(w, 413, "A file can be at most 100 MB")
	case errors.As(err, &disk):
		fail(w, r, err)
	case errors.As(err, &network) && network.Timeout(), r.Context().Err() != nil:
		writeErr(w, 408, "upload timed out")
	case errors.Is(err, io.EOF):
		writeErr(w, 400, "no file in the upload")
	default:
		writeErr(w, 400, "unreadable upload")
	}
}

// SweepAttachments deletes uploads never linked to a study within a day. The
// row goes first, so no row a study could link ever points at a missing blob;
// a blob left behind by a failed delete is only wasted space.
func SweepAttachments(ctx context.Context, st *store.Store, blobs storage.Storage, now func() time.Time) error {
	stale, err := st.Global().StalePendingAttachments(ctx, now().Add(-attachmentLife))
	if err != nil {
		return err
	}
	var errs []error
	for _, a := range stale {
		if err := removePendingAttachment(ctx, st, blobs, a.WorkspaceID, a.ID, a.BlobKey); err != nil {
			slog.Error("attachment sweep", "id", a.ID, "err", err)
			errs = append(errs, err)
		}
	}
	return errors.Join(errs...)
}

// removePendingAttachment deletes an upload still unlinked, then its blob. An
// upload a study linked since it was listed is left alone, blob included.
func removePendingAttachment(ctx context.Context, st *store.Store, blobs storage.Storage, ws, id uuid.UUID, key string) error {
	var n int64
	err := st.InWorkspace(ctx, ws, uuid.Nil, func(tx *store.Tx) error {
		var err error
		n, err = tx.Q.DeletePendingAttachment(ctx, db.DeletePendingAttachmentParams{WorkspaceID: ws, ID: id})
		return err
	})
	if err != nil || n == 0 {
		return err
	}
	return blobs.Delete(ctx, key)
}
