package handler

import (
	"encoding/json"
	"errors"
	"io"
	"io/fs"
	"net/http"
	"os"
	"path"
	"slices"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"lykeion/server/internal/middleware"
	"lykeion/server/internal/skill"
	"lykeion/server/internal/store"
	db "lykeion/server/pkg/db/generated"
)

var (
	errRuntimeSkillOffline   = errors.New("runtime is offline")
	errRuntimeSkillStale     = errors.New("runtime skill request is no longer current")
	errRuntimeSkillDuplicate = errors.New("runtime skill result already recorded")
)

type runtimeSkillInventory struct {
	Key         string `json:"key"`
	Name        string `json:"name"`
	Description string `json:"description"`
	SourcePath  string `json:"source_path"`
	Provider    string `json:"provider"`
	Root        string `json:"root"`
	FileCount   int    `json:"file_count"`
}
type runtimeSkillOutcome struct {
	Key     string `json:"key"`
	Status  string `json:"status"`
	SkillID string `json:"skill_id,omitempty"`
	Error   string `json:"error,omitempty"`
}
type runtimeSkillResults struct {
	Items  []runtimeSkillOutcome   `json:"items,omitempty"`
	Skills []runtimeSkillInventory `json:"skills,omitempty"`
}
type runtimeSkillIdentity struct {
	workspace, runtime, request, acquisition uuid.UUID
	daemon, key, operation                   string
}

func runtimeSkillAnswer(w http.ResponseWriter, r *http.Request, err error) bool {
	switch {
	case err == nil:
		return true
	case errors.Is(err, errRuntimeSkillOffline):
		writeErr(w, 503, err.Error())
	case errors.Is(err, errRuntimeSkillStale):
		writeErr(w, 409, err.Error())
	default:
		skillAnswer(w, r, err)
	}
	return false
}
func runtimeSkillIDs(w http.ResponseWriter, r *http.Request, operation string) (runtimeSkillIdentity, bool) {
	id := runtimeSkillIdentity{operation: operation}
	var err error
	id.workspace, err = uuid.Parse(middleware.FromHeader(r))
	if err != nil {
		writeErr(w, 400, "X-Workspace-ID required")
		return id, false
	}
	id.runtime, err = uuid.Parse(chi.URLParam(r, "id"))
	if err != nil {
		writeErr(w, 404, "runtime not found")
		return id, false
	}
	if raw := chi.URLParam(r, "requestId"); raw != "" {
		id.request, err = uuid.Parse(raw)
		if err != nil {
			writeErr(w, 404, "request not found")
			return id, false
		}
	}
	return id, true
}
func validRuntimeSkillKey(key string) bool {
	return key != "." && fs.ValidPath(key) && !strings.ContainsAny(key, "\\\x00:")
}

func (h *H) lockRuntimeSkillOwner(r *http.Request, tx *store.Tx, id runtimeSkillIdentity) (db.Runtime, error) {
	if _, err := tx.Q.LockMember(r.Context(), db.LockMemberParams{WorkspaceID: id.workspace, UserID: user(r)}); err != nil {
		return db.Runtime{}, err
	}
	rt, err := tx.Q.LockRuntime(r.Context(), db.LockRuntimeParams{WorkspaceID: id.workspace, ID: id.runtime})
	if err != nil {
		return rt, err
	}
	if rt.OwnerID != user(r) || id.daemon != "" && rt.DaemonID != id.daemon {
		return rt, skill.ErrForbidden
	}
	return rt, nil
}
func (h *H) runtimeSkillTx(r *http.Request, id runtimeSkillIdentity, fn func(*store.Tx, db.Runtime) error) error {
	return h.Store.InWorkspace(r.Context(), id.workspace, user(r), func(tx *store.Tx) error {
		if _, err := tx.Q.LockWorkspaceExecution(r.Context(), id.workspace); err != nil {
			return err
		}
		if err := tx.Q.LockWorkspaceEvents(r.Context(), id.workspace); err != nil {
			return err
		}
		rt, err := h.lockRuntimeSkillOwner(r, tx, id)
		if err != nil {
			return err
		}
		return fn(tx, rt)
	})
}
func runtimeSkillEvent(r *http.Request, tx *store.Tx, j db.RuntimeSkillRequest) error {
	return tx.Emit(r.Context(), "runtime_skill_request.updated", nil, store.User(j.OwnerID), map[string]any{"request_id": j.ID, "runtime_id": j.RuntimeID, "operation": j.Operation, "status": j.Status})
}
func timeoutRuntimeSkills(r *http.Request, tx *store.Tx, id runtimeSkillIdentity) error {
	jobs, err := tx.Q.TimeoutRuntimeSkillRequests(r.Context(), db.TimeoutRuntimeSkillRequestsParams{WorkspaceID: id.workspace, RuntimeID: id.runtime, OwnerID: user(r)})
	if err != nil {
		return err
	}
	for _, j := range jobs {
		if err := runtimeSkillEvent(r, tx, j); err != nil {
			return err
		}
	}
	return nil
}
func (h *H) initiateRuntimeSkills(operation string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		id, ok := runtimeSkillIDs(w, r, operation)
		if !ok {
			return
		}
		keys := []string{}
		if operation == "import" {
			// Check access before interpreting input, then recheck at insertion.
			if err := h.runtimeSkillTx(r, id, func(_ *store.Tx, rt db.Runtime) error {
				if rt.Status != "online" {
					return errRuntimeSkillOffline
				}
				return nil
			}); !runtimeSkillAnswer(w, r, err) {
				return
			}
			var in struct {
				Keys []string `json:"keys"`
			}
			if err := readJSON(r, &in); err != nil || len(in.Keys) < 1 || len(in.Keys) > 10 {
				writeErr(w, 400, "select between 1 and 10 inventory keys")
				return
			}
			seen := map[string]bool{}
			for _, key := range in.Keys {
				if !validRuntimeSkillKey(key) || seen[key] {
					writeErr(w, 400, "invalid or duplicate inventory key")
					return
				}
				seen[key] = true
			}
			keys = in.Keys
		}
		var out db.RuntimeSkillRequest
		err := h.runtimeSkillTx(r, id, func(tx *store.Tx, rt db.Runtime) error {
			if rt.Status != "online" {
				return errRuntimeSkillOffline
			}
			var err error
			out, err = tx.Q.InsertRuntimeSkillRequest(r.Context(), db.InsertRuntimeSkillRequestParams{WorkspaceID: id.workspace, ID: uuid.New(), RuntimeID: id.runtime, OwnerID: user(r), DaemonID: rt.DaemonID, Operation: operation, Keys: keys})
			if err != nil {
				return err
			}
			return runtimeSkillEvent(r, tx, out)
		})
		if runtimeSkillAnswer(w, r, err) {
			writeJSON(w, 200, out)
		}
	}
}
func (h *H) getRuntimeSkills(operation string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		id, ok := runtimeSkillIDs(w, r, operation)
		if !ok {
			return
		}
		var out db.RuntimeSkillRequest
		err := h.runtimeSkillTx(r, id, func(tx *store.Tx, _ db.Runtime) error {
			if err := timeoutRuntimeSkills(r, tx, id); err != nil {
				return err
			}
			var err error
			out, err = tx.Q.GetRuntimeSkillRequest(r.Context(), db.GetRuntimeSkillRequestParams{WorkspaceID: id.workspace, ID: id.request, RuntimeID: id.runtime, OwnerID: user(r)})
			if err == nil && out.Operation != operation {
				return pgx.ErrNoRows
			}
			return err
		})
		if runtimeSkillAnswer(w, r, err) {
			writeJSON(w, 200, out)
		}
	}
}
func (h *H) claimRuntimeSkills(operation string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		id, ok := runtimeSkillIDs(w, r, operation)
		if !ok {
			return
		}
		var in struct {
			DaemonID string `json:"daemon_id"`
		}
		if err := readJSON(r, &in); err != nil || strings.TrimSpace(in.DaemonID) == "" {
			writeErr(w, 400, "daemon_id required")
			return
		}
		id.daemon = in.DaemonID
		type claim struct {
			db.RuntimeSkillRequest
			Harness string `json:"harness"`
		}
		out := []claim{}
		err := h.runtimeSkillTx(r, id, func(tx *store.Tx, rt db.Runtime) error {
			if rt.Status != "online" {
				return errRuntimeSkillOffline
			}
			if err := timeoutRuntimeSkills(r, tx, id); err != nil {
				return err
			}
			jobs, err := tx.Q.ListPendingRuntimeSkillRequests(r.Context(), db.ListPendingRuntimeSkillRequestsParams{WorkspaceID: id.workspace, RuntimeID: id.runtime, OwnerID: user(r), DaemonID: id.daemon})
			if err != nil {
				return err
			}
			for _, job := range jobs {
				if job.Operation != operation {
					continue
				}
				acquisition := uuid.New()
				j, err := tx.Q.ClaimRuntimeSkillRequest(r.Context(), db.ClaimRuntimeSkillRequestParams{WorkspaceID: id.workspace, ID: job.ID, RuntimeID: id.runtime, OwnerID: user(r), DaemonID: id.daemon, AcquisitionID: &acquisition})
				if err != nil {
					return err
				}
				out = append(out, claim{j, rt.Harness})
				return runtimeSkillEvent(r, tx, j)
			}
			return nil
		})
		if runtimeSkillAnswer(w, r, err) {
			writeJSON(w, 200, out)
		}
	}
}
func (h *H) fenceRuntimeSkillResult(r *http.Request, tx *store.Tx, id runtimeSkillIdentity) (db.RuntimeSkillRequest, error) {
	rt, err := h.lockRuntimeSkillOwner(r, tx, id)
	if err != nil {
		return db.RuntimeSkillRequest{}, err
	}
	j, err := tx.Q.LockRuntimeSkillRequest(r.Context(), db.LockRuntimeSkillRequestParams{WorkspaceID: id.workspace, ID: id.request, RuntimeID: id.runtime, OwnerID: user(r)})
	if err != nil {
		return j, err
	}
	if j.Operation != id.operation || j.DaemonID != rt.DaemonID || j.AcquisitionID == nil || *j.AcquisitionID != id.acquisition {
		return j, errRuntimeSkillStale
	}
	if j.CompletedAt != nil && time.Since(*j.CompletedAt) >= 5*time.Minute {
		return j, pgx.ErrNoRows
	}
	if id.operation == "import" {
		if !slices.Contains(j.Keys, id.key) {
			return j, errRuntimeSkillStale
		}
		var result runtimeSkillResults
		if err = json.Unmarshal(j.Result, &result); err != nil {
			return j, err
		}
		for _, item := range result.Items {
			if item.Key == id.key {
				return j, errRuntimeSkillDuplicate
			}
		}
	} else if j.Status == "completed" || j.Status == "failed" {
		return j, errRuntimeSkillDuplicate
	}
	if j.Status != "running" || !j.ExpiresAt.After(time.Now()) {
		return j, errRuntimeSkillStale
	}
	return j, nil
}
func saveRuntimeSkillResult(r *http.Request, tx *store.Tx, id runtimeSkillIdentity, j db.RuntimeSkillRequest, result runtimeSkillResults, status, reason string) (db.RuntimeSkillRequest, error) {
	raw, err := json.Marshal(result)
	if err != nil {
		return j, err
	}
	out, err := tx.Q.UpdateRuntimeSkillRequestResult(r.Context(), db.UpdateRuntimeSkillRequestResultParams{WorkspaceID: id.workspace, ID: j.ID, RuntimeID: id.runtime, OwnerID: user(r), DaemonID: id.daemon, AcquisitionID: &id.acquisition, Status: status, Result: raw, Error: reason})
	if errors.Is(err, pgx.ErrNoRows) {
		return out, errRuntimeSkillStale
	}
	if err != nil {
		return out, err
	}
	return out, runtimeSkillEvent(r, tx, out)
}
func saveRuntimeSkillItem(r *http.Request, tx *store.Tx, id runtimeSkillIdentity, j db.RuntimeSkillRequest, item runtimeSkillOutcome) (db.RuntimeSkillRequest, error) {
	var result runtimeSkillResults
	if err := json.Unmarshal(j.Result, &result); err != nil {
		return j, err
	}
	result.Items = append(result.Items, item)
	// Keep selected order even when deliveries arrive in a different order.
	slices.SortFunc(result.Items, func(a, b runtimeSkillOutcome) int { return slices.Index(j.Keys, a.Key) - slices.Index(j.Keys, b.Key) })
	status := "running"
	if len(result.Items) == len(j.Keys) {
		status = "completed"
	}
	return saveRuntimeSkillResult(r, tx, id, j, result, status, "")
}
func (h *H) recordRuntimeSkillItem(r *http.Request, id runtimeSkillIdentity, item runtimeSkillOutcome) (db.RuntimeSkillRequest, error) {
	var out db.RuntimeSkillRequest
	err := h.runtimeSkillTx(r, id, func(tx *store.Tx, _ db.Runtime) error {
		j, err := h.fenceRuntimeSkillResult(r, tx, id)
		out = j
		if errors.Is(err, errRuntimeSkillDuplicate) {
			return nil
		}
		if err != nil {
			return err
		}
		out, err = saveRuntimeSkillItem(r, tx, id, j, item)
		return err
	})
	return out, err
}
func (h *H) reportRuntimeSkills(operation string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		id, ok := runtimeSkillIDs(w, r, operation)
		if !ok {
			return
		}
		q := r.URL.Query()
		id.daemon = q.Get("daemon_id")
		id.key = q.Get("key")
		var err error
		id.acquisition, err = uuid.Parse(q.Get("acquisition_id"))
		if err != nil || id.daemon == "" || len(q["daemon_id"]) != 1 || len(q["acquisition_id"]) != 1 || operation == "import" && (!validRuntimeSkillKey(id.key) || len(q["key"]) != 1) {
			writeErr(w, 400, "daemon identity, acquisition and selected key required")
			return
		}
		var out db.RuntimeSkillRequest
		duplicate := false
		err = h.runtimeSkillTx(r, id, func(tx *store.Tx, _ db.Runtime) error {
			var err error
			out, err = h.fenceRuntimeSkillResult(r, tx, id)
			if errors.Is(err, errRuntimeSkillDuplicate) {
				duplicate = true
				return nil
			}
			return err
		})
		if !runtimeSkillAnswer(w, r, err) {
			return
		}
		if duplicate {
			writeJSON(w, 200, out)
			return
		}
		if operation == "list" {
			var in struct {
				Status string                  `json:"status"`
				Skills []runtimeSkillInventory `json:"skills"`
				Error  string                  `json:"error"`
			}
			if err := readJSON(r, &in); err != nil || in.Status != "completed" && in.Status != "failed" {
				writeErr(w, 400, "completed or failed inventory required")
				return
			}
			seen := map[string]bool{}
			for _, s := range in.Skills {
				if !validRuntimeSkillKey(s.Key) || seen[s.Key] {
					writeErr(w, 400, "invalid inventory keys")
					return
				}
				seen[s.Key] = true
			}
			err = h.runtimeSkillTx(r, id, func(tx *store.Tx, _ db.Runtime) error {
				j, err := h.fenceRuntimeSkillResult(r, tx, id)
				out = j
				if errors.Is(err, errRuntimeSkillDuplicate) {
					return nil
				}
				if err != nil {
					return err
				}
				out, err = saveRuntimeSkillResult(r, tx, id, j, runtimeSkillResults{Skills: in.Skills}, in.Status, in.Error)
				return err
			})
		} else if middleware.RuntimeSkillZIPUpload(r) {
			out, err = h.importRuntimeSkillZIP(w, r, id)
			if err == errRuntimeSkillBodyAnswered {
				return
			}
		} else {
			var in struct {
				Error string `json:"error"`
			}
			if err := readJSON(r, &in); err != nil || strings.TrimSpace(in.Error) == "" {
				writeErr(w, 400, "item error required")
				return
			}
			out, err = h.recordRuntimeSkillItem(r, id, runtimeSkillOutcome{Key: id.key, Status: "failed", Error: in.Error})
		}
		if runtimeSkillAnswer(w, r, err) {
			writeJSON(w, 200, out)
		}
	}
}

var errRuntimeSkillBodyAnswered = errors.New("runtime skill body error answered")

func (h *H) importRuntimeSkillZIP(w http.ResponseWriter, r *http.Request, id runtimeSkillIdentity) (db.RuntimeSkillRequest, error) {
	var out db.RuntimeSkillRequest
	tmp, err := os.CreateTemp("", "lyk-runtime-skill-*")
	if err != nil {
		return out, err
	}
	defer os.Remove(tmp.Name())
	defer tmp.Close()
	if _, err = io.Copy(tmp, http.MaxBytesReader(w, r.Body, skill.ArchiveBytes)); err != nil {
		var tooLarge *http.MaxBytesError
		if errors.As(err, &tooLarge) {
			writeErr(w, 413, "archive exceeds 16 MiB")
		} else {
			writeErr(w, 400, "unreadable skill archive")
		}
		return out, errRuntimeSkillBodyAnswered
	}
	if _, err = tmp.Seek(0, io.SeekStart); err != nil {
		return out, err
	}
	data, err := io.ReadAll(tmp)
	if err != nil {
		return out, err
	}
	b, err := skill.ParseArchive(data)
	if err == nil {
		if b.Name == "" {
			b.Name = path.Base(id.key)
		}
		err = skill.Validate(b)
	}
	if err != nil {
		return h.recordRuntimeSkillItem(r, id, runtimeSkillOutcome{Key: id.key, Status: "failed", Error: err.Error()})
	}
	var locked db.RuntimeSkillRequest
	_, err = h.skillService().CreateFenced(r.Context(), id.workspace, user(r), b, skill.Source{Kind: "runtime", Location: id.key, RuntimeID: id.runtime.String()}, func(tx *store.Tx) error {
		var err error
		locked, err = h.fenceRuntimeSkillResult(r, tx, id)
		out = locked
		return err
	}, func(tx *store.Tx, d skill.Detail) error {
		var err error
		out, err = saveRuntimeSkillItem(r, tx, id, locked, runtimeSkillOutcome{Key: id.key, Status: "imported", SkillID: d.ID})
		return err
	})
	if errors.Is(err, errRuntimeSkillDuplicate) {
		return out, nil
	}
	if errors.Is(err, skill.ErrConflict) {
		return h.recordRuntimeSkillItem(r, id, runtimeSkillOutcome{Key: id.key, Status: "conflict", Error: err.Error()})
	}
	if errors.Is(err, skill.ErrInvalid) {
		return h.recordRuntimeSkillItem(r, id, runtimeSkillOutcome{Key: id.key, Status: "failed", Error: err.Error()})
	}
	return out, err
}
