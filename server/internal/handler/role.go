package handler

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net"
	"net/http"
	"os"
	"slices"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/jackc/pgx/v5"
	"lykeion/server/internal/auth"
	"lykeion/server/internal/middleware"
	"lykeion/server/internal/storage"
	"lykeion/server/internal/store"
	db "lykeion/server/pkg/db/generated"
)

const maxBlob = 50 << 20

var errRoleUnauthorized = errors.New("invalid or finished agent run")
var errBlobReference = errors.New("blob is not available on this card")

func blobKey(role middleware.Role, sum string) string {
	return role.WorkspaceID.String() + "/" + role.CardID.String() + "/" + sum
}
func validSHA(sum string) bool {
	if len(sum) != 64 || sum != strings.ToLower(sum) {
		return false
	}
	_, err := hex.DecodeString(sum)
	return err == nil
}

// roleTx follows settlement's run-then-token lock order. The middleware lookup
// alone is insufficient: the run can settle while a body is read or validated.
func (h *H) roleTx(r *http.Request, fn func(*store.Tx, middleware.Role) error) error {
	role := middleware.RoleFrom(r.Context())
	return h.Store.InWorkspace(r.Context(), role.WorkspaceID, role.AgentRunID, func(tx *store.Tx) error {
		run, err := tx.Q.LockAgentRun(r.Context(), db.LockAgentRunParams{ID: role.AgentRunID, WorkspaceID: role.WorkspaceID})
		if errors.Is(err, pgx.ErrNoRows) {
			return errRoleUnauthorized
		}
		if err != nil {
			return err
		}
		if run.Status != "running" || run.CardID != role.CardID || run.Role != role.Role || run.BoardColumn != role.Column || run.Round != role.Round {
			return errRoleUnauthorized
		}
		token, err := tx.Q.LockAgentRunToken(r.Context(), db.LockAgentRunTokenParams{TokenHash: auth.Hash(strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer ")), WorkspaceID: role.WorkspaceID, AgentRunID: role.AgentRunID})
		if errors.Is(err, pgx.ErrNoRows) {
			return errRoleUnauthorized
		}
		if err != nil {
			return err
		}
		if token.Attempt != run.Attempt || token.CardID != run.CardID || token.Role != run.Role {
			return errRoleUnauthorized
		}
		return fn(tx, role)
	})
}
func roleError(w http.ResponseWriter, r *http.Request, err error) {
	switch {
	case errors.Is(err, errRoleUnauthorized):
		writeErr(w, 401, err.Error())
	case errors.Is(err, errBlobReference):
		writeErr(w, 400, err.Error())
	case errors.Is(err, storage.ErrNotFound):
		writeErr(w, 404, "not found")
	default:
		fail(w, r, err)
	}
}
func (h *H) createRecord(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Kind    string          `json:"kind"`
		Key     string          `json:"key"`
		Body    json.RawMessage `json:"body"`
		BlobKey string          `json:"blob_key"`
	}
	if err := readJSON(r, &in); err != nil || in.Kind == "" {
		writeErr(w, 400, "kind and body required")
		return
	}
	role := middleware.RoleFrom(r.Context())
	kind, ok := h.Kinds[in.Kind]
	if !ok {
		writeErr(w, 400, "unknown record kind")
		return
	}
	if !slices.Contains(kind.Roles, role.Role) {
		writeErr(w, 403, "role may not write this kind")
		return
	}
	in.Body = bytes.TrimSpace(in.Body)
	if len(in.Body) == 0 || in.Body[0] != '{' || !json.Valid(in.Body) {
		writeErr(w, 400, "body must be a JSON object")
		return
	}
	if kind.Validate != nil {
		if err := kind.Validate(in.Body); err != nil {
			writeErr(w, 400, err.Error())
			return
		}
	}
	if in.BlobKey != "" {
		_, sum, _ := strings.Cut(in.BlobKey, role.WorkspaceID.String()+"/"+role.CardID.String()+"/")
		if !validSHA(sum) || in.BlobKey != blobKey(role, sum) {
			writeErr(w, 400, "invalid blob_key for this card")
			return
		}
	}
	var rec db.Record
	err := h.roleTx(r, func(tx *store.Tx, role middleware.Role) error {
		if in.BlobKey != "" {
			exists, err := tx.Q.RoleBlobExists(r.Context(), db.RoleBlobExistsParams{WorkspaceID: role.WorkspaceID, CardID: &role.CardID, BlobKey: in.BlobKey})
			if err != nil {
				return err
			}
			if !exists {
				return errBlobReference
			}
			rc, err := h.Blobs.Get(r.Context(), in.BlobKey)
			if errors.Is(err, storage.ErrNotFound) {
				return errBlobReference
			}
			if err != nil {
				return err
			}
			if err := rc.Close(); err != nil {
				return err
			}
		}
		var err error
		rec, err = tx.Q.InsertRecord(r.Context(), db.InsertRecordParams{WorkspaceID: role.WorkspaceID, CardID: role.CardID, BoardColumn: role.Column, Round: role.Round, Kind: in.Kind, Role: role.Role, Key: in.Key, Body: in.Body, BlobKey: in.BlobKey, AgentRunID: &role.AgentRunID})
		if err != nil {
			return err
		}
		return tx.Emit(r.Context(), "record.created", &role.CardID, store.Actor{Type: "role", ID: role.AgentRunID}, map[string]any{"record_id": rec.ID, "kind": in.Kind, "key": in.Key, "role": role.Role})
	})
	if err != nil {
		roleError(w, r, err)
		return
	}
	writeJSON(w, 201, rec)
}
func (h *H) listRecords(w http.ResponseWriter, r *http.Request) {
	var records []db.Record
	err := h.roleTx(r, func(tx *store.Tx, role middleware.Role) error {
		col := r.URL.Query().Get("column")
		if col == "" {
			col = role.Column
		}
		var err error
		records, err = tx.Q.ListRecords(r.Context(), db.ListRecordsParams{WorkspaceID: role.WorkspaceID, CardID: role.CardID, BoardColumn: col})
		return err
	})
	if err != nil {
		roleError(w, r, err)
		return
	}
	if records == nil {
		records = []db.Record{}
	}
	writeJSON(w, 200, records)
}

// putBlob spools and hashes before acquiring tenant locks. Only a committed
// blob.created event makes the stored object available to readers or records.
func (h *H) putBlob(w http.ResponseWriter, r *http.Request) {
	tmp, err := os.CreateTemp("", "lyk-blob-*")
	if err != nil {
		fail(w, r, err)
		return
	}
	defer os.Remove(tmp.Name())
	defer tmp.Close()
	hash := sha256.New()
	n, err := io.Copy(tmp, io.TeeReader(http.MaxBytesReader(w, r.Body, maxBlob), hash))
	controller := http.NewResponseController(w)
	if err != nil {
		w.Header().Set("Connection", "close")
		_ = controller.SetReadDeadline(time.Now())
		var large *http.MaxBytesError
		var network net.Error
		var disk *os.PathError
		switch {
		case errors.As(err, &large):
			writeErr(w, 413, "blob exceeds 50 MiB")
		case errors.As(err, &disk):
			fail(w, r, err)
		case errors.As(err, &network) && network.Timeout(), r.Context().Err() != nil:
			writeErr(w, 408, "blob upload timed out")
		default:
			writeErr(w, 400, "unreadable blob")
		}
		return
	}
	_ = controller.SetReadDeadline(time.Time{})
	if err := tmp.Close(); err != nil {
		fail(w, r, err)
		return
	}
	input, err := os.Open(tmp.Name())
	if err != nil {
		fail(w, r, err)
		return
	}
	defer input.Close()
	sum := hex.EncodeToString(hash.Sum(nil))
	key := blobKey(middleware.RoleFrom(r.Context()), sum)
	err = h.roleTx(r, func(tx *store.Tx, role middleware.Role) error {
		if err := h.Blobs.Put(r.Context(), key, input, n); err != nil {
			return err
		}
		return tx.Emit(r.Context(), "blob.created", &role.CardID, store.Actor{Type: "role", ID: role.AgentRunID}, map[string]any{"blob_key": key, "sha256": sum, "size": n})
	})
	if err != nil {
		roleError(w, r, err)
		return
	}
	writeJSON(w, 201, map[string]any{"blob_key": key, "sha256": sum, "size": n})
}
func (h *H) getBlob(w http.ResponseWriter, r *http.Request) {
	sum := chi.URLParam(r, "sha")
	if !validSHA(sum) {
		writeErr(w, 400, "invalid sha256")
		return
	}
	var rc io.ReadCloser
	err := h.roleTx(r, func(tx *store.Tx, role middleware.Role) error {
		key := blobKey(role, sum)
		exists, err := tx.Q.RoleBlobExists(r.Context(), db.RoleBlobExistsParams{WorkspaceID: role.WorkspaceID, CardID: &role.CardID, BlobKey: key})
		if err != nil {
			return err
		}
		if !exists {
			return storage.ErrNotFound
		}
		rc, err = h.Blobs.Get(r.Context(), key)
		return err
	})
	if rc != nil {
		defer rc.Close()
	}
	if err != nil {
		roleError(w, r, err)
		return
	}
	w.Header().Set("Content-Type", "application/octet-stream")
	// Interrupt network writes at the request deadline, including a stalled client.
	if deadline, ok := r.Context().Deadline(); ok {
		_ = http.NewResponseController(w).SetWriteDeadline(deadline)
		defer http.NewResponseController(w).SetWriteDeadline(time.Time{})
	}
	if _, err := io.Copy(w, roleBlobReader{r.Context(), rc}); err != nil {
		slog.Error("blob download failed", "err", err)
		panic(http.ErrAbortHandler)
	}
}

type roleBlobReader struct {
	ctx context.Context
	io.Reader
}

func (r roleBlobReader) Read(p []byte) (int, error) {
	if err := r.ctx.Err(); err != nil {
		return 0, err
	}
	return r.Reader.Read(p)
}
