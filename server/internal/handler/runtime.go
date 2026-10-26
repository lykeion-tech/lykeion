package handler

import (
	"errors"
	"net/http"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"lykeion/server/internal/middleware"
	"lykeion/server/internal/service"
	"lykeion/server/internal/store"
	db "lykeion/server/pkg/db/generated"
)

type machineUsagePeriod struct {
	Tokens  int64   `json:"tokens"`
	CostUSD float64 `json:"cost_usd"`
}

type machineColleagueTotal struct {
	Colleague  string             `json:"colleague"`
	Last7Days  machineUsagePeriod `json:"last_7_days"`
	Last30Days machineUsagePeriod `json:"last_30_days"`
}

func (h *H) machineColleagues(w http.ResponseWriter, r *http.Request) {
	var out struct {
		Runs   []db.MachineGuestRunsRow `json:"runs"`
		Totals []machineColleagueTotal  `json:"totals"`
	}
	out.Runs = []db.MachineGuestRunsRow{}
	out.Totals = []machineColleagueTotal{}
	err := h.inWS(r, func(tx *store.Tx, ws, owner uuid.UUID) error {
		daemon := chi.URLParam(r, "daemon_id")
		if _, err := tx.Q.MachineOwnedRuntime(r.Context(), db.MachineOwnedRuntimeParams{WorkspaceID: ws, DaemonID: daemon, OwnerID: owner}); err != nil {
			return err
		}
		runs, err := tx.Q.MachineGuestRuns(r.Context(), db.MachineGuestRunsParams{WorkspaceID: ws, DaemonID: daemon, OwnerID: owner})
		if err != nil {
			return err
		}
		usage, err := tx.Q.MachineGuestUsage(r.Context(), db.MachineGuestUsageParams{WorkspaceID: ws, DaemonID: daemon, OwnerID: owner, AsOf: time.Now().UTC()})
		if err != nil {
			return err
		}
		if runs != nil {
			out.Runs = runs
		}
		for _, row := range usage {
			out.Totals = append(out.Totals, machineColleagueTotal{
				Colleague:  row.Colleague,
				Last7Days:  machineUsagePeriod{Tokens: row.Tokens7Days, CostUSD: row.Cost7Days},
				Last30Days: machineUsagePeriod{Tokens: row.Tokens30Days, CostUSD: row.Cost30Days},
			})
		}
		return nil
	})
	if err != nil {
		fail(w, r, err)
		return
	}
	writeJSON(w, 200, out)
}

func (h *H) listRuntimes(w http.ResponseWriter, r *http.Request) {
	var out []db.ListRuntimesRow
	err := h.inWS(r, func(tx *store.Tx, ws, _ uuid.UUID) error {
		var err error
		out, err = tx.Q.ListRuntimes(r.Context(), ws)
		return err
	})
	if err != nil {
		fail(w, r, err)
		return
	}
	if out == nil {
		out = []db.ListRuntimesRow{}
	}
	writeJSON(w, 200, out)
}

func (h *H) patchRuntime(w http.ResponseWriter, r *http.Request) {
	id, ok := cardID(w, r)
	if !ok {
		return
	}
	var in struct {
		MaxConcurrent *int32  `json:"max_concurrent"`
		CustomName    *string `json:"custom_name"`
	}
	if err := readJSON(r, &in); err != nil || (in.MaxConcurrent == nil && in.CustomName == nil) {
		writeErr(w, 400, "max_concurrent or custom_name required")
		return
	}
	if in.MaxConcurrent != nil && (*in.MaxConcurrent < 1 || *in.MaxConcurrent > 32) {
		writeErr(w, 400, "max_concurrent between 1 and 32 required")
		return
	}
	var name *string
	if in.CustomName != nil {
		n := strings.TrimSpace(*in.CustomName)
		if utf8.RuneCountInString(n) > 64 {
			writeErr(w, 400, "custom_name at most 64 characters")
			return
		}
		if n != "" {
			name = &n
		}
	}
	err := h.inWS(r, func(tx *store.Tx, ws, u uuid.UUID) error {
		if in.MaxConcurrent != nil {
			n, err := tx.Q.SetRuntimeConcurrency(r.Context(), db.SetRuntimeConcurrencyParams{ID: id, WorkspaceID: ws, OwnerID: u, MaxConcurrent: *in.MaxConcurrent})
			if err != nil {
				return err
			}
			if n == 0 {
				return pgx.ErrNoRows
			}
		}
		if in.CustomName != nil {
			n, err := tx.Q.SetRuntimeName(r.Context(), db.SetRuntimeNameParams{ID: id, WorkspaceID: ws, OwnerID: u, CustomName: name})
			if err != nil {
				return err
			}
			if n == 0 {
				return pgx.ErrNoRows
			}
		}
		return tx.Emit(r.Context(), "runtime.updated", nil, store.User(u), map[string]any{"runtime_id": id, "custom_name": name, "max_concurrent": in.MaxConcurrent})
	})
	if err != nil {
		fail(w, r, err)
		return
	}
	w.WriteHeader(204)
}

func (h *H) deleteRuntime(w http.ResponseWriter, r *http.Request) {
	id, ok := cardID(w, r)
	if !ok {
		return
	}
	err := h.inWS(r, func(tx *store.Tx, ws, u uuid.UUID) error {
		n, err := tx.Q.DeleteRuntime(r.Context(), db.DeleteRuntimeParams{ID: id, WorkspaceID: ws, OwnerID: u})
		if err != nil {
			return err
		}
		if n == 0 {
			return pgx.ErrNoRows
		}
		return tx.Emit(r.Context(), "runtime.deleted", nil, store.User(u), map[string]any{"runtime_id": id})
	})
	if err != nil {
		fail(w, r, err)
		return
	}
	w.WriteHeader(204)
}

func (h *H) getAgentRun(w http.ResponseWriter, r *http.Request) {
	id, ok := cardID(w, r)
	if !ok {
		return
	}
	var t db.AgentRun
	err := h.inWS(r, func(tx *store.Tx, ws, _ uuid.UUID) error {
		var err error
		t, err = tx.Q.GetAgentRun(r.Context(), db.GetAgentRunParams{ID: id, WorkspaceID: ws})
		return err
	})
	if err != nil {
		fail(w, r, err)
		return
	}
	writeJSON(w, 200, t)
}

func (h *H) agentRunMessages(w http.ResponseWriter, r *http.Request) {
	id, ok := cardID(w, r)
	if !ok {
		return
	}
	var out []db.AgentRunMessage
	var badQuery bool
	err := h.inWS(r, func(tx *store.Tx, ws, _ uuid.UUID) error {
		run, err := tx.Q.GetAgentRun(r.Context(), db.GetAgentRunParams{ID: id, WorkspaceID: ws})
		if err != nil {
			return err
		}
		q := r.URL.Query()
		after := int64(0)
		if raw := q.Get("after"); raw != "" {
			after, err = strconv.ParseInt(raw, 10, 32)
			if err != nil || after < 0 {
				badQuery = true
				return nil
			}
		}
		attempt := run.Attempt
		if raw := q.Get("attempt"); raw != "" {
			n, e := strconv.ParseInt(raw, 10, 32)
			if e != nil || n < 1 || n > int64(run.Attempt) {
				badQuery = true
				return nil
			}
			attempt = int32(n)
		}
		out, err = tx.Q.AgentRunMessagesAfter(r.Context(), db.AgentRunMessagesAfterParams{WorkspaceID: ws, AgentRunID: id, Attempt: attempt, After: int32(after)})
		return err
	})
	if err != nil {
		fail(w, r, err)
		return
	}
	if badQuery {
		writeErr(w, 400, "valid after and attempt required")
		return
	}
	if out == nil {
		out = []db.AgentRunMessage{}
	}
	writeJSON(w, 200, out)
}

func (h *H) cancelAgentRun(w http.ResponseWriter, r *http.Request) {
	id, ok := cardID(w, r)
	if !ok {
		return
	}
	raw := middleware.FromHeader(r)
	if raw == "" {
		writeErr(w, 400, "X-Workspace-ID required")
		return
	}
	ws, err := uuid.Parse(raw)
	if err != nil {
		writeErr(w, 404, "workspace not found")
		return
	}
	if err := h.AgentRuns.Cancel(r.Context(), ws, user(r), id); err != nil {
		agentRunFail(w, r, err)
		return
	}
	w.WriteHeader(204)
}

func (h *H) setRuntimeSharing(w http.ResponseWriter, r *http.Request) {
	id, ok := cardID(w, r)
	if !ok {
		return
	}
	raw := middleware.FromHeader(r)
	if raw == "" {
		writeErr(w, 400, "X-Workspace-ID required")
		return
	}
	wsID, e := uuid.Parse(raw)
	if e != nil {
		writeErr(w, 404, "workspace not found")
		return
	}
	err := h.Store.InWorkspace(r.Context(), wsID, user(r), func(tx *store.Tx) error {
		ws, u := wsID, user(r)
		if _, e := tx.Q.GetMember(r.Context(), db.GetMemberParams{WorkspaceID: ws, UserID: u}); e != nil {
			return e
		}
		rt, e := tx.Q.GetRuntime(r.Context(), db.GetRuntimeParams{WorkspaceID: ws, ID: id})
		if e != nil {
			return e
		}
		if rt.OwnerID != u {
			return pgx.ErrNoRows
		}
		return nil
	})
	if err != nil {
		fail(w, r, err)
		return
	}
	var in service.SharingChange
	if err := readJSON(r, &in); err != nil {
		writeErr(w, 400, "invalid sharing")
		return
	}
	affected, err := h.AgentRuns.SetSharing(r.Context(), user(r), wsID, id, in)
	if errors.Is(err, service.ErrSharingConfirmation) {
		writeJSON(w, 409, map[string]any{"affected": affected})
		return
	}
	if errors.Is(err, service.ErrInvalidSharing) {
		writeErr(w, 400, err.Error())
		return
	}
	if errors.Is(err, service.ErrWorkspaceSharingList) || errors.Is(err, service.ErrSharedRunsRequireLinux) {
		writeErr(w, 400, err.Error())
		return
	}
	if err != nil {
		fail(w, r, err)
		return
	}
	var rt db.Runtime
	err = h.Store.InWorkspace(r.Context(), wsID, user(r), func(tx *store.Tx) error {
		ws := wsID
		var e error
		rt, e = tx.Q.GetRuntime(r.Context(), db.GetRuntimeParams{WorkspaceID: ws, ID: id})
		return e
	})
	if err != nil {
		fail(w, r, err)
		return
	}
	writeJSON(w, 200, rt)
}
