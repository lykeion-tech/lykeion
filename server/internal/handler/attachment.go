package handler

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"io"
	"log/slog"
	"mime"
	"net"
	"net/http"
	"os"
	"path"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
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
	// textDeadline bounds reading an upload's text; a file whose text takes
	// longer is kept as a binary.
	textDeadline = 2 * time.Minute
	// A study holds at most maxStudyFiles files totalling maxStudyBytes.
	maxStudyFiles = 100
	maxStudyBytes = 500 << 20
)

// errFileTaken is an upload linked to another study, or swept, after this
// study read it as pending.
const errFileTaken = conflict("An attached file is no longer available")

// studyFiles checks a new study's attachment ids against the caller's pending
// uploads and the study's limits. Nothing is written, so a refusal here leaves
// the uploads pending.
func (h *H) studyFiles(r *http.Request, ids []uuid.UUID) ([]db.Attachment, error) {
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
	return rows, nil
}

// linkStudyFiles records each file as a source of the new study and links its
// upload to it, under a name unique within the study. An upload another study
// linked first, or one swept since it was read, refuses this one.
func linkStudyFiles(r *http.Request, tx *store.Tx, blobs storage.Storage, card db.Card, files []db.Attachment) error {
	names := make([]string, len(files))
	for i, f := range files {
		names[i] = f.Name
	}
	disk := records.DiskNames(names)
	for i, f := range files {
		key, err := records.RecordAttachment(r.Context(), tx, blobs, card, f)
		if errors.Is(err, storage.ErrNotFound) {
			return errFileTaken
		}
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
	textSum := ""
	if text, ok := h.uploadText(name, typ, io.NewSectionReader(tmp, 0, n)); ok {
		ts := sha256.Sum256([]byte(text))
		textSum = hex.EncodeToString(ts[:])
		if err := h.Blobs.Put(r.Context(), records.TextKey(key), strings.NewReader(text), int64(len(text))); err != nil {
			_ = h.Blobs.Delete(context.WithoutCancel(r.Context()), key)
			fail(w, r, err)
			return
		}
	}
	var row db.Attachment
	err = h.Store.InWorkspace(r.Context(), ws, user(r), func(tx *store.Tx) error {
		var err error
		row, err = tx.Q.InsertPendingAttachment(r.Context(), db.InsertPendingAttachmentParams{
			ID: id, WorkspaceID: ws, UploaderID: user(r), Name: name, ContentType: typ, SizeBytes: n, Sha256: sum, BlobKey: key, TextSha256: textSum})
		return err
	})
	if err != nil {
		_ = h.Blobs.Delete(context.WithoutCancel(r.Context()), key)
		_ = h.Blobs.Delete(context.WithoutCancel(r.Context()), records.TextKey(key))
		fail(w, r, err)
		return
	}
	writeJSON(w, 201, map[string]any{"id": row.ID, "name": row.Name, "size_bytes": row.SizeBytes, "content_type": row.ContentType, "sha256": row.Sha256})
}

// uploadText reads a stored upload's text within the deadline. A file whose
// text cannot be read, or not in time, has none.
func (h *H) uploadText(name, typ string, r io.Reader) (string, bool) {
	extract, limit := h.ExtractText, h.TextDeadline
	if extract == nil {
		extract = records.ExtractText
	}
	if limit == 0 {
		limit = textDeadline
	}
	type result struct {
		text string
		ok   bool
	}
	done := make(chan result, 1)
	// ponytail: a parse past the deadline is abandoned, not stopped; its
	// goroutine and the file's bytes (up to 100 MB) live until the library
	// returns. A hard stop needs the parse in a subprocess.
	go func() {
		text, ok, err := extract(name, typ, r)
		done <- result{text, ok && err == nil}
	}()
	timer := time.NewTimer(limit)
	defer timer.Stop()
	select {
	case res := <-done:
		return res.text, res.ok
	case <-timer.C:
		return "", false
	}
}

// downloadAttachment streams one of a study's files. It runs outside the
// buffering workspace scope so a 100 MB file never sits in memory, and it
// checks membership and the file's study before reading the blob.
func (h *H) downloadAttachment(w http.ResponseWriter, r *http.Request) {
	ws, err := uuid.Parse(r.Header.Get("X-Workspace-ID"))
	card, err2 := uuid.Parse(chi.URLParam(r, "id"))
	aid, err3 := uuid.Parse(chi.URLParam(r, "aid"))
	if err != nil || err2 != nil || err3 != nil {
		writeErr(w, 404, "not found")
		return
	}
	var row db.Attachment
	err = h.memberTx(r, ws, false, func(tx *store.Tx) error {
		var err error
		row, err = tx.Q.CardAttachment(r.Context(), db.CardAttachmentParams{WorkspaceID: ws, CardID: &card, ID: aid})
		return err
	})
	if err != nil {
		logoFail(w, r, err)
		return
	}
	rc, err := h.Blobs.Get(r.Context(), row.BlobKey)
	if err != nil {
		logoFail(w, r, err)
		return
	}
	defer rc.Close()
	disposition := mime.FormatMediaType("attachment", map[string]string{"filename": row.Name})
	if disposition == "" {
		disposition = "attachment"
	}
	hd := w.Header()
	hd.Set("Content-Disposition", disposition)
	hd.Set("Content-Type", row.ContentType)
	hd.Set("X-Content-Type-Options", "nosniff")
	hd.Set("Content-Length", strconv.FormatInt(row.SizeBytes, 10))
	// A stalled client is cut at the request deadline, not left holding the
	// file open.
	if deadline, ok := r.Context().Deadline(); ok {
		rcw := http.NewResponseController(w)
		_ = rcw.SetWriteDeadline(deadline)
		defer rcw.SetWriteDeadline(time.Time{})
	}
	if _, err := io.Copy(w, rc); err != nil {
		slog.Error("attachment download failed", "attachment", aid, "err", err)
		panic(http.ErrAbortHandler)
	}
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
	return errors.Join(blobs.Delete(ctx, key), blobs.Delete(ctx, records.TextKey(key)))
}
