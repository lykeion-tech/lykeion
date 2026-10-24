package handler

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"os"
	"path"
	"slices"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"lykeion/server/internal/auth"
	"lykeion/server/internal/middleware"
	"lykeion/server/internal/records"
	"lykeion/server/internal/service"
	"lykeion/server/internal/storage"
	"lykeion/server/internal/store"
	db "lykeion/server/pkg/db/generated"
)

// maxOutputName is the longest name an output may have, in bytes.
const maxOutputName = 512

// A run saves at most maxRunOutputs outputs totalling maxRunOutputBytes.
var (
	maxRunOutputs     int32 = 100
	maxRunOutputBytes int64 = 500 << 20
)

var errOutputName = errors.New("an output's path must be relative to the run folder, with no .. segment, backslash or NUL")

// cleanOutputName is the name an output is kept under: its path relative to
// the run folder, cleaned, with `/` separators.
func cleanOutputName(p string) (string, error) {
	if p == "" || strings.HasPrefix(p, "/") || strings.ContainsAny(p, "\\\x00") || slices.Contains(strings.Split(p, "/"), "..") {
		return "", errOutputName
	}
	p = path.Clean(p)
	if p == "." {
		return "", errOutputName
	}
	if len(p) > maxOutputName {
		return "", errors.New("an output's path can be at most 512 bytes")
	}
	return p, nil
}

// uploadOutput keeps a file the run wrote as an output record of its study.
// The multipart body names the file in a `path` field, then carries it in a
// `file` part. The file streams into a temp file while being hashed and goes
// to storage before the run's authority and caps are checked under its locks;
// a refused upload leaves no record and no blob.
func (h *H) uploadOutput(w http.ResponseWriter, r *http.Request) {
	if middleware.RoleFrom(r.Context()).CardID == uuid.Nil {
		writeErr(w, 400, "a run with no study saves no outputs")
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
	name := ""
	for part == nil {
		p, err := mr.NextPart()
		if err != nil {
			attachmentReadErr(w, r, err)
			return
		}
		switch p.FormName() {
		case "path":
			b, err := io.ReadAll(io.LimitReader(p, 4<<10))
			if err != nil {
				attachmentReadErr(w, r, err)
				return
			}
			if name, err = cleanOutputName(string(b)); err != nil {
				writeErr(w, 400, err.Error())
				return
			}
		case "file":
			if name == "" {
				writeErr(w, 400, "the path field must come before the file")
				return
			}
			part = p
		}
	}
	tmp, err := os.CreateTemp("", "lyk-output-*")
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
	if _, err := tmp.Seek(0, io.SeekStart); err != nil {
		fail(w, r, err)
		return
	}
	// Avoid storing a file for a token that expired while spooling; roleTx
	// rechecks the run after storage.
	if _, err := h.Store.Global().LookupAgentRunToken(r.Context(), auth.Hash(strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer "))); err != nil {
		roleError(w, r, errRoleUnauthorized)
		return
	}
	role := middleware.RoleFrom(r.Context())
	key := role.WorkspaceID.String() + "/outputs/" + uuid.NewString()
	if err := h.Blobs.Put(r.Context(), key, tmp, n); err != nil {
		fail(w, r, err)
		return
	}
	sum, typ := hex.EncodeToString(hash.Sum(nil)), attachmentType(name, head[:hn])
	body, err := json.Marshal(map[string]any{"name": name, "size_bytes": n, "sha256": sum, "content_type": typ,
		"at": time.Now().UTC().Format(time.RFC3339), "agent_run_id": role.AgentRunID})
	var rec db.Record
	if err == nil {
		err = h.roleTx(r, func(tx *store.Tx, role middleware.Role) error {
			saved, err := tx.Q.RunOutputTotals(r.Context(), db.RunOutputTotalsParams{WorkspaceID: role.WorkspaceID, CardID: role.CardID, AgentRunID: &role.AgentRunID})
			if err != nil {
				return err
			}
			if saved.Outputs >= maxRunOutputs {
				return service.Invalid("A run can save at most 100 outputs")
			}
			if saved.Bytes+n > maxRunOutputBytes {
				return service.Invalid("A run's outputs can total at most 500 MB")
			}
			// the card lock roleTx took keeps two uploads from taking one number
			k, err := records.NextOutputKey(r.Context(), tx, role.CardID)
			if err != nil {
				return err
			}
			actor := store.Actor{Type: "role", ID: role.AgentRunID}
			if err := tx.Emit(r.Context(), "blob.created", &role.CardID, actor, map[string]any{"blob_key": key, "sha256": sum, "size": n}); err != nil {
				return err
			}
			rec, err = tx.Q.InsertRecord(r.Context(), db.InsertRecordParams{WorkspaceID: role.WorkspaceID, CardID: role.CardID,
				BoardColumn: role.Column, Round: role.Round, Kind: "output", Role: role.Role, Key: k, Body: body,
				BlobKey: key, AgentRunID: &role.AgentRunID})
			if err != nil {
				return err
			}
			return tx.Emit(r.Context(), "record.created", &role.CardID, actor,
				map[string]any{"record_id": rec.ID, "kind": "output", "key": k, "role": role.Role})
		})
	}
	if err != nil {
		_ = h.Blobs.Delete(context.WithoutCancel(r.Context()), key)
		var inv service.Invalid
		if errors.As(err, &inv) {
			writeErr(w, 400, string(inv))
			return
		}
		roleError(w, r, err)
		return
	}
	writeJSON(w, 201, map[string]any{"key": rec.Key, "name": name, "size_bytes": n, "sha256": sum, "content_type": typ})
}

// outputFile is the live output record a study holds under key. Any other
// kind of record, or no record, is not found.
func outputFile(ctx context.Context, tx *store.Tx, ws, card uuid.UUID, key string) (db.Record, error) {
	rec, err := tx.Q.LiveCardRecordByKey(ctx, db.LiveCardRecordByKeyParams{WorkspaceID: ws, CardID: card, Key: key})
	if err == nil && rec.Kind != "output" {
		err = pgx.ErrNoRows
	}
	return rec, err
}

// serveOutput streams an output record's blob once the record check has passed.
func (h *H) serveOutput(w http.ResponseWriter, r *http.Request, rec db.Record, wrap func(io.Reader) io.Reader) error {
	var meta struct {
		Name        string `json:"name"`
		SizeBytes   int64  `json:"size_bytes"`
		ContentType string `json:"content_type"`
	}
	if err := json.Unmarshal(rec.Body, &meta); err != nil {
		return err
	}
	rc, err := h.Blobs.Get(r.Context(), rec.BlobKey)
	if err != nil {
		return err
	}
	defer rc.Close()
	serveFile(w, r, meta.Name, meta.ContentType, meta.SizeBytes, wrap(rc))
	return nil
}

// downloadOutput streams one of a study's outputs to a member. Like an
// attachment download it runs outside the buffering workspace scope.
func (h *H) downloadOutput(w http.ResponseWriter, r *http.Request) {
	ws, wsErr := uuid.Parse(r.Header.Get("X-Workspace-ID"))
	card, cardErr := uuid.Parse(chi.URLParam(r, "id"))
	if err := errors.Join(wsErr, cardErr); err != nil {
		writeErr(w, 404, "not found")
		return
	}
	var rec db.Record
	err := h.memberTx(r, ws, false, func(tx *store.Tx) error {
		var err error
		rec, err = outputFile(r.Context(), tx, ws, card, chi.URLParam(r, "key"))
		return err
	})
	if err == nil {
		err = h.serveOutput(w, r, rec, func(rd io.Reader) io.Reader { return rd })
	}
	if err != nil {
		logoFail(w, r, err)
	}
}

// roleOutput streams one of the run's own study's outputs to its daemon.
func (h *H) roleOutput(w http.ResponseWriter, r *http.Request) {
	var rec db.Record
	err := h.roleTx(r, func(tx *store.Tx, role middleware.Role) error {
		var err error
		rec, err = outputFile(r.Context(), tx, role.WorkspaceID, role.CardID, chi.URLParam(r, "key"))
		if errors.Is(err, pgx.ErrNoRows) {
			return storage.ErrNotFound
		}
		return err
	})
	if err == nil {
		err = h.serveOutput(w, r, rec, func(rd io.Reader) io.Reader { return roleBlobReader{r.Context(), rd} })
	}
	if err != nil {
		roleError(w, r, err)
	}
}
