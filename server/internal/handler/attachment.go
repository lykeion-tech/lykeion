package handler

import (
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
	"github.com/jackc/pgx/v5"

	"lykeion/server/internal/archive"
	"lykeion/server/internal/middleware"
	"lykeion/server/internal/records"
	"lykeion/server/internal/storage"
	"lykeion/server/internal/store"
	db "lykeion/server/pkg/db/generated"
)

const (
	// MaxAttachmentBytes is the most one attached file may hold.
	MaxAttachmentBytes = 5 << 30
	// MaxOutputBytes keeps each run output within its independent file limit.
	MaxOutputBytes = 100 << 20
	// multipartSlack lets the body carry the multipart framing around a file
	// of exactly the limit; the file itself is counted separately.
	multipartSlack = 64 << 10
	// attachmentLife is how long an upload may stay unlinked to a study.
	attachmentLife = 24 * time.Hour
	// textDeadline bounds reading an upload's text; a file whose text takes
	// longer is kept as a binary.
	textDeadline = 2 * time.Minute
	// maxExtractions bounds how many uploads' text is read at once.
	maxExtractions = 3
	// A study holds at most maxStudyFiles files totalling maxStudyBytes.
	maxStudyFiles = 100
	maxStudyBytes = 10 << 30
	maxInnerFiles = 20000
)

// errFileTaken is an upload linked to another study, or swept, after this
// study read it as pending.
const errFileTaken = conflict("An attached file is no longer available")

// studyFiles checks a new study's attachment ids against the caller's pending
// uploads and the study's limits, and answers the uploads in the order the ids
// name them, a repeated id once. Nothing is written, so a refusal here leaves
// the uploads pending.
func (h *H) studyFiles(r *http.Request, ids []uuid.UUID) ([]db.Attachment, error) {
	at := map[uuid.UUID]int{}
	ids = slices.DeleteFunc(slices.Clone(ids), func(id uuid.UUID) bool {
		_, seen := at[id]
		if !seen {
			at[id] = len(at)
		}
		return seen
	})
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
	// The files are linked and keyed in the order they were attached.
	slices.SortFunc(rows, func(a, b db.Attachment) int { return at[a.ID] - at[b.ID] })
	var total int64
	var inner int64
	for _, a := range rows {
		if a.SizeBytes > maxStudyBytes-total || a.UnpackedBytes > maxStudyBytes-total-a.SizeBytes {
			return nil, cardInputError("A study's files can total at most 10 GB")
		}
		total += a.SizeBytes + a.UnpackedBytes
		inner += int64(a.InnerFiles)
	}
	if inner > maxInnerFiles {
		return nil, cardInputError("A study can have at most 20,000 files and directories inside archives")
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
	format := archive.Kind(head[:hn], name)
	var listing archive.Listing
	if format != archive.None {
		listing, err = archive.List(tmp.Name(), format)
		if err != nil {
			if errors.Is(err, archive.ErrTooBig) {
				writeErr(w, 400, "An archive can contain at most 20,000 files and directories and 10 GB unpacked")
			} else if errors.Is(err, archive.ErrUnsafe) {
				writeErr(w, 400, "Unsafe or unreadable archive")
			} else {
				fail(w, r, err)
			}
			return
		}
	}
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
	textSum, textSize := "", int64(0)
	var text io.Reader
	if format != archive.None {
		manifest := records.ArchiveManifest(name, sum, listing)
		text, textSize = strings.NewReader(manifest), int64(len(manifest))
	} else if extracted, ok := h.uploadTextFile(format, name, typ, tmp, n); ok {
		text, textSize = extracted, extracted.Size()
	}
	if text != nil {
		th := sha256.New()
		if err := h.Blobs.Put(r.Context(), records.TextKey(key), io.TeeReader(text, th), textSize); err != nil {
			_ = h.Blobs.Delete(context.WithoutCancel(r.Context()), key)
			_ = h.Blobs.Delete(context.WithoutCancel(r.Context()), records.TextKey(key))
			fail(w, r, err)
			return
		}
		textSum = hex.EncodeToString(th.Sum(nil))
	}
	// Nothing reads the temp file from here on; a parse abandoned at the
	// deadline fails its next read instead of holding the file open.
	tmp.Close()
	var row db.Attachment
	err = h.Store.InWorkspace(r.Context(), ws, user(r), func(tx *store.Tx) error {
		var err error
		row, err = tx.Q.InsertPendingAttachment(r.Context(), db.InsertPendingAttachmentParams{
			ID: id, WorkspaceID: ws, UploaderID: user(r), Name: name, Kind: "file", ContentType: typ, SizeBytes: n, Sha256: sum, BlobKey: key, TextSha256: textSum, TextSizeBytes: textSize, InnerFiles: int32(listing.Files), UnpackedBytes: listing.Bytes})
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

// extractions holds a slot for each text read under way, abandoned ones
// included, so at most maxExtractions run at once.
var extractions = make(chan struct{}, maxExtractions)

func (h *H) uploadTextFile(format archive.Format, name, typ string, f io.ReaderAt, size int64) (*io.SectionReader, bool) {
	if format != archive.None || size > 100<<20 {
		return nil, false
	}
	return h.uploadText(name, typ, f, size)
}

// uploadText reads a stored upload's text within the deadline, waiting for a
// free slot included. A file whose text cannot be read, or not in time, has
// none.
func (h *H) uploadText(name, typ string, f io.ReaderAt, size int64) (*io.SectionReader, bool) {
	extract, limit := h.ExtractText, h.TextDeadline
	if extract == nil {
		extract = records.ExtractText
	}
	if limit == 0 {
		limit = textDeadline
	}
	timer := time.NewTimer(limit)
	defer timer.Stop()
	select {
	case extractions <- struct{}{}:
	case <-timer.C:
		return nil, false
	}
	type result struct {
		text *io.SectionReader
		ok   bool
	}
	done := make(chan result, 1)
	// A parse past the deadline is abandoned, not stopped: it keeps its slot
	// until it returns, which it does once the caller closes f under it.
	go func() {
		defer func() { <-extractions }()
		text, ok, err := extract(name, typ, f, size)
		done <- result{text, ok && err == nil}
	}()
	select {
	case res := <-done:
		return res.text, res.ok
	case <-timer.C:
		return nil, false
	}
}

// downloadAttachment streams one of a study's files. It runs outside the
// buffering workspace scope so a 5 GB file never sits in memory, and it
// checks membership and the file's study before reading the blob.
func (h *H) downloadAttachment(w http.ResponseWriter, r *http.Request) {
	ws, wsErr := uuid.Parse(r.Header.Get("X-Workspace-ID"))
	card, cardErr := uuid.Parse(chi.URLParam(r, "id"))
	aid, aidErr := uuid.Parse(chi.URLParam(r, "aid"))
	if err := errors.Join(wsErr, cardErr, aidErr); err != nil {
		writeErr(w, 404, "not found")
		return
	}
	var row db.Attachment
	err := h.memberTx(r, ws, false, func(tx *store.Tx) error {
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
	serveFile(w, r, row.Name, row.ContentType, row.SizeBytes, rc)
}

// serveFile streams a file, always as a download, never shown inline.
func serveFile(w http.ResponseWriter, r *http.Request, name, contentType string, size int64, rc io.Reader) {
	disposition := mime.FormatMediaType("attachment", map[string]string{"filename": name})
	if disposition == "" {
		disposition = "attachment"
	}
	hd := w.Header()
	hd.Set("Content-Disposition", disposition)
	hd.Set("Content-Type", contentType)
	hd.Set("X-Content-Type-Options", "nosniff")
	hd.Set("Content-Length", strconv.FormatInt(size, 10))
	// A stalled client is cut at the request deadline, not left holding the
	// file open.
	if deadline, ok := r.Context().Deadline(); ok {
		rcw := http.NewResponseController(w)
		_ = rcw.SetWriteDeadline(deadline)
		defer rcw.SetWriteDeadline(time.Time{})
	}
	if _, err := io.Copy(w, rc); err != nil {
		slog.Error("download failed", "name", name, "err", err)
		panic(http.ErrAbortHandler)
	}
}

// roleAttachment streams one of the run's own study's files to its daemon.
// Any other id, another study's included, is not found.
func (h *H) roleAttachment(w http.ResponseWriter, r *http.Request) {
	id, err := uuid.Parse(chi.URLParam(r, "id"))
	if err != nil {
		writeErr(w, 404, "not found")
		return
	}
	var row db.Attachment
	err = h.roleTx(r, func(tx *store.Tx, role middleware.Role) error {
		var err error
		row, err = tx.Q.CardAttachment(r.Context(), db.CardAttachmentParams{WorkspaceID: role.WorkspaceID, CardID: &role.CardID, ID: id})
		if errors.Is(err, pgx.ErrNoRows) {
			return storage.ErrNotFound
		}
		return err
	})
	if err != nil {
		roleError(w, r, err)
		return
	}
	rc, err := h.Blobs.Get(r.Context(), row.BlobKey)
	if err != nil {
		roleError(w, r, err)
		return
	}
	defer rc.Close()
	serveFile(w, r, row.Name, row.ContentType, row.SizeBytes, roleBlobReader{r.Context(), rc})
}

func attachmentReadErr(w http.ResponseWriter, r *http.Request, err error) {
	uploadReadErr(w, r, err, "A file can be at most 5 GB")
}

func uploadReadErr(w http.ResponseWriter, r *http.Request, err error, largeMessage string) {
	_ = http.NewResponseController(w).SetReadDeadline(time.Now())
	var large *http.MaxBytesError
	var network net.Error
	var disk *os.PathError
	switch {
	case errors.As(err, &large):
		writeErr(w, 413, largeMessage)
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

// fileDeleteTimeout bounds deleting the blobs of a removed study's or
// workspace's files.
const fileDeleteTimeout = time.Minute

// deleteFilesAfterCommit deletes each file's blob and its text once tx has
// committed.
func (h *H) deleteFilesAfterCommit(r *http.Request, tx *store.Tx, files []db.Attachment) {
	var keys []string
	for _, f := range files {
		keys = append(keys, f.BlobKey, records.TextKey(f.BlobKey))
	}
	h.deleteBlobsAfterCommit(r, tx, keys)
}

// deleteBlobsAfterCommit deletes the blobs at keys once tx has committed; a
// blob that fails to go is wasted space, not a reason to keep what was
// deleted.
func (h *H) deleteBlobsAfterCommit(r *http.Request, tx *store.Tx, keys []string) {
	tx.AfterCommit(func() {
		ctx, cancel := context.WithTimeout(context.WithoutCancel(r.Context()), fileDeleteTimeout)
		defer cancel()
		for _, key := range keys {
			if err := h.Blobs.Delete(ctx, key); err != nil {
				slog.Error("blob delete", "key", key, "err", err)
			}
		}
	})
}
