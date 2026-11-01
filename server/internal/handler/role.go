package handler

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/http"
	"os"
	"regexp"
	"slices"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"lykeion/server/internal/auth"
	"lykeion/server/internal/board"
	"lykeion/server/internal/flow"
	"lykeion/server/internal/middleware"
	"lykeion/server/internal/records"
	"lykeion/server/internal/review"
	"lykeion/server/internal/service"
	"lykeion/server/internal/storage"
	"lykeion/server/internal/store"
	db "lykeion/server/pkg/db/generated"
)

const maxBlob = 50 << 20

var errRoleUnauthorized = middleware.ErrRoleUnauthorized
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

// roleTx re-checks the run under row locks: the middleware lookup alone is
// insufficient, because the run can settle while a body is read or validated.
func (h *H) roleTx(r *http.Request, fn func(*store.Tx, middleware.Role) error) error {
	role := middleware.RoleFrom(r.Context())
	token := strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer ")
	return h.Store.InWorkspace(r.Context(), role.WorkspaceID, role.AgentRunID, func(tx *store.Tx) error {
		if err := middleware.LockRole(r.Context(), tx, role, token); err != nil {
			return err
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
	if err := service.MayWrite(role.Role, in.Kind); err != nil {
		writeErr(w, 400, err.Error())
		return
	}
	if err := review.Validate(in.Kind, role.Column, in.Body); err != nil {
		writeErr(w, 400, err.Error())
		return
	}
	if in.Key != "" {
		writeErr(w, 400, "the server gives a record its key")
		return
	}
	in.Body = bytes.TrimSpace(in.Body)
	if len(in.Body) == 0 || in.Body[0] != '{' || !json.Valid(in.Body) {
		writeErr(w, 400, "body must be a JSON object")
		return
	}
	cites, err := recordCites(in.Body)
	if err != nil {
		writeErr(w, 400, err.Error())
		return
	}
	if in.BlobKey != "" {
		_, sum, _ := strings.Cut(in.BlobKey, role.WorkspaceID.String()+"/"+role.CardID.String()+"/")
		if !validSHA(sum) || in.BlobKey != blobKey(role, sum) {
			writeErr(w, 400, "invalid blob_key for this card")
			return
		}
		// Probe availability without holding lifecycle locks. The final transaction
		// still requires current authority and committed metadata before insertion.
		rc, err := h.Blobs.Get(r.Context(), in.BlobKey)
		if errors.Is(err, storage.ErrNotFound) {
			err = errBlobReference
		}
		if err != nil {
			roleError(w, r, err)
			return
		}
		if err := rc.Close(); err != nil {
			fail(w, r, err)
			return
		}
	}
	var rec db.Record
	err = h.roleTx(r, func(tx *store.Tx, role middleware.Role) error {
		if in.BlobKey != "" {
			exists, err := tx.Q.RoleBlobExists(r.Context(), db.RoleBlobExistsParams{WorkspaceID: role.WorkspaceID, CardID: &role.CardID, BlobKey: in.BlobKey})
			if err != nil {
				return err
			}
			if !exists {
				return errBlobReference
			}
		}
		if len(cites) > 0 {
			found, err := tx.Q.LiveRecordKeys(r.Context(), db.LiveRecordKeysParams{WorkspaceID: role.WorkspaceID, CardID: role.CardID, Keys: cites})
			if err != nil {
				return err
			}
			for _, k := range cites {
				if !slices.Contains(found, k) {
					return service.Invalid("no recorded call, run or record has the key " + k)
				}
			}
			if in.Kind == review.KindHypothesis {
				if err := groundingCites(r.Context(), tx, role.CardID, cites); err != nil {
					return err
				}
			}
		}
		if in.Kind == review.KindWithdrawn {
			if err := withdrawable(r.Context(), tx, role.CardID, in.Body); err != nil {
				return err
			}
		}
		key, err := records.NextRecordKey(r.Context(), tx, role.CardID)
		if err != nil {
			return err
		}
		rec, err = tx.Q.InsertRecord(r.Context(), db.InsertRecordParams{WorkspaceID: role.WorkspaceID, CardID: role.CardID, BoardColumn: role.Column, Round: role.Round, Kind: in.Kind, Role: role.Role, Key: key, Body: in.Body, BlobKey: in.BlobKey, AgentRunID: &role.AgentRunID})
		if err != nil {
			return err
		}
		return tx.Emit(r.Context(), "record.created", &role.CardID, store.Actor{Type: "role", ID: role.AgentRunID}, map[string]any{"record_id": rec.ID, "kind": in.Kind, "key": key, "role": role.Role})
	})
	var inv service.Invalid
	if errors.As(err, &inv) {
		writeErr(w, 400, string(inv))
		return
	}
	if err != nil {
		roleError(w, r, err)
		return
	}
	writeJSON(w, 201, rec)
}

// withdrawable refuses a withdrawal that names no live record of the study,
// or a record that is neither an item nor reports values.
func withdrawable(ctx context.Context, tx *store.Tx, card uuid.UUID, body json.RawMessage) error {
	var b review.WithdrawnBody
	_ = json.Unmarshal(body, &b)
	recs, err := tx.Q.LiveRecordsByKeys(ctx, db.LiveRecordsByKeysParams{WorkspaceID: tx.WS, CardID: card, Keys: []string{b.Item}})
	if err != nil {
		return err
	}
	if len(recs) == 0 {
		return service.Invalid("no record has the key " + b.Item)
	}
	rec := recs[0]
	if _, item := review.ColumnOf(rec.Kind); item || len(flow.MatchValues([]db.Record{rec}, nil)) > 0 {
		return nil
	}
	return service.Invalid("withdrawn names " + b.Item + ", of kind " + rec.Kind + ": name an item record or a record that reports values")
}

var (
	callKey   = regexp.MustCompile(`^c[0-9]+$`)
	runKey    = regexp.MustCompile(`^x[0-9]+$`)
	recordKey = regexp.MustCompile(`^r[0-9]+$`)
)

// recordCites reads what a record body cites, `call:<key>`, `run:<key>` or a
// bare record key such as `r3` (an earlier column's item), and returns the
// keys.
func recordCites(body json.RawMessage) ([]string, error) {
	var v struct {
		Cites json.RawMessage `json:"cites"`
	}
	json.Unmarshal(body, &v)
	if v.Cites == nil {
		return nil, nil
	}
	var refs []string
	if err := json.Unmarshal(v.Cites, &refs); err != nil {
		return nil, errors.New("cites must be a list of call:KEY, run:KEY or record key references")
	}
	keys := make([]string, 0, len(refs))
	for _, ref := range refs {
		k, ok := strings.CutPrefix(ref, "call:")
		if ok && !callKey.MatchString(k) {
			ok = false
		} else if !ok {
			k, ok = strings.CutPrefix(ref, "run:")
			ok = ok && runKey.MatchString(k)
		}
		if !ok && recordKey.MatchString(ref) {
			k, ok = ref, true
		}
		if !ok {
			return nil, fmt.Errorf("cite %q is not a call:KEY, run:KEY or record key reference", ref)
		}
		keys = append(keys, k)
	}
	return keys, nil
}

// groundingCites refuses a hypothesis whose bare record keys name anything
// but a Grounding paper or dataset: the agents' own prose is no evidence.
func groundingCites(ctx context.Context, tx *store.Tx, card uuid.UUID, cites []string) error {
	bare := slices.DeleteFunc(slices.Clone(cites), func(k string) bool { return !recordKey.MatchString(k) })
	if len(bare) == 0 {
		return nil
	}
	recs, err := tx.Q.LiveRecordsByKeys(ctx, db.LiveRecordsByKeysParams{WorkspaceID: tx.WS, CardID: card, Keys: bare})
	if err != nil {
		return err
	}
	for _, k := range bare {
		if !slices.ContainsFunc(recs, func(r db.Record) bool { return r.Key == k && review.GroundingSource(r) }) {
			return service.Invalid("a hypothesis cites " + k + ", which is not a Grounding paper or dataset; cite those, or a call:KEY")
		}
	}
	return nil
}

func (h *H) listRecords(w http.ResponseWriter, r *http.Request) {
	var records []db.Record
	if c := r.URL.Query().Get("column"); c != "" && board.StageIndex(c) < 0 {
		writeErr(w, 400, fmt.Sprintf("unknown column %q; the columns are %s", c, strings.Join(board.Stages, ", ")))
		return
	}
	err := h.roleTx(r, func(tx *store.Tx, role middleware.Role) error {
		col := r.URL.Query().Get("column")
		if col == "" {
			col = role.Column
		}
		var err error
		records, err = tx.Q.ListRecords(r.Context(), db.ListRecordsParams{WorkspaceID: role.WorkspaceID, CardID: role.CardID, BoardColumn: col})
		return err
	})
	// A trigger's payload is untrusted text kept for people; no role reads it.
	kind := r.URL.Query().Get("kind")
	records = slices.DeleteFunc(records, func(rec db.Record) bool {
		return rec.Kind == "trigger_payload" || (kind != "" && rec.Kind != kind)
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

// putBlob spools, hashes and provisionally stores bytes without lifecycle locks,
// allowing cancellation during slow storage. Only the final authorized commit
// of blob.created makes the object available to readers or records.
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
	// Avoid provisional storage for a token that expired while spooling. This
	// precheck is only an optimization; roleTx revalidates after external I/O.
	if _, err := h.Store.Global().LookupAgentRunToken(r.Context(), auth.Hash(strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer "))); err != nil {
		roleError(w, r, errRoleUnauthorized)
		return
	}
	if err := h.Blobs.Put(r.Context(), key, input, n); err != nil {
		fail(w, r, err)
		return
	}
	err = h.roleTx(r, func(tx *store.Tx, role middleware.Role) error {
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
	// Opening storage is provisional: no bytes are sent until current token
	// authority and committed blob metadata have been checked under locks.
	key := blobKey(middleware.RoleFrom(r.Context()), sum)
	rc, err := h.Blobs.Get(r.Context(), key)
	if rc != nil {
		defer rc.Close()
	}
	if err != nil {
		roleError(w, r, err)
		return
	}
	err = h.roleTx(r, func(tx *store.Tx, role middleware.Role) error {
		exists, err := tx.Q.RoleBlobExists(r.Context(), db.RoleBlobExistsParams{WorkspaceID: role.WorkspaceID, CardID: &role.CardID, BlobKey: key})
		if err != nil {
			return err
		}
		if !exists {
			return storage.ErrNotFound
		}
		return nil
	})
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
