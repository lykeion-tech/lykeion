package handler

import (
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"strings"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"

	"lykeion/server/internal/auth"
	"lykeion/server/internal/middleware"
	"lykeion/server/internal/service"
	"lykeion/server/pkg/agent"
)

// Daemon routes accept only personal tokens; Auth has already checked validity.
func personalOnly(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !strings.HasPrefix(r.Header.Get("Authorization"), "Bearer "+auth.PersonalPrefix) {
			writeErr(w, http.StatusUnauthorized, "personal token required")
			return
		}
		next.ServeHTTP(w, r)
	})
}

func agentRunFail(w http.ResponseWriter, r *http.Request, err error) {
	switch {
	case errors.Is(err, service.ErrConflict):
		writeErr(w, 409, err.Error())
	case errors.Is(err, service.ErrNotYours):
		writeErr(w, 404, "not found")
	case errors.Is(err, service.ErrBadReport):
		writeErr(w, 400, err.Error())
	default:
		fail(w, r, err)
	}
}

func user(r *http.Request) uuid.UUID { return middleware.IdentityFrom(r.Context()).UserID }

func (h *H) daemonRegister(w http.ResponseWriter, r *http.Request) {
	var in struct {
		WorkspaceID uuid.UUID         `json:"workspace_id"`
		DaemonID    string            `json:"daemon_id"`
		DeviceName  string            `json:"device_name"`
		Harnesses   []string          `json:"harnesses"`
		Versions    map[string]string `json:"versions"`
		Platform    agent.Platform    `json:"platform"`
	}
	if err := readJSON(r, &in); err != nil || in.WorkspaceID == uuid.Nil || strings.TrimSpace(in.DaemonID) == "" || len(in.Harnesses) == 0 || len(in.Harnesses) > 16 {
		writeErr(w, 400, "workspace_id, daemon_id and harnesses required")
		return
	}
	for _, name := range in.Harnesses {
		if _, ok := agent.Harnesses[name]; !ok {
			writeErr(w, 400, fmt.Sprintf("unknown harness %q", name))
			return
		}
	}
	rts, err := h.AgentRuns.Register(r.Context(), user(r), in.WorkspaceID, in.DaemonID, in.DeviceName, in.Harnesses, in.Versions, in.Platform)
	if err != nil {
		agentRunFail(w, r, err)
		return
	}
	writeJSON(w, 200, rts)
}

func (h *H) daemonHeartbeat(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Runtimes []service.RuntimeRef `json:"runtimes"`
		Bases    map[string]string    `json:"bases"`
	}
	if err := readJSON(r, &in); err != nil || len(in.Runtimes) == 0 || len(in.Runtimes) > 64 {
		writeErr(w, 400, "runtimes required (1-64)")
		return
	}
	for lang, state := range in.Bases {
		if !validBaseState(lang, state) {
			writeErr(w, 400, fmt.Sprintf("bad base state %q for %q", state, lang))
			return
		}
	}
	for _, ref := range in.Runtimes {
		if ref.WorkspaceID == uuid.Nil || ref.RuntimeID == uuid.Nil {
			writeErr(w, 400, "workspace_id and runtime_id required")
			return
		}
	}
	n, err := h.AgentRuns.Heartbeat(r.Context(), user(r), in.Runtimes, in.Bases)
	if err != nil {
		agentRunFail(w, r, err)
		return
	}
	writeJSON(w, 200, map[string]int{"touched": n})
}

// validBaseState accepts a base the daemon knows reported as ready, building
// or failed with a reason of at most 200 bytes, which keeps a runtime's whole
// set well inside its column's size limit. jsonb text cannot hold NUL.
func validBaseState(lang, state string) bool {
	switch lang {
	case "python", "r", "julia":
	default:
		return false
	}
	if state == "ready" || state == "building" {
		return true
	}
	reason, ok := strings.CutPrefix(state, "failed: ")
	return ok && reason != "" && len(reason) <= 200 && !strings.ContainsRune(reason, 0)
}

func (h *H) daemonClaim(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Runtimes []service.RuntimeRef `json:"runtimes"`
		Slots    int                  `json:"slots"`
	}
	if err := readJSON(r, &in); err != nil || len(in.Runtimes) == 0 || len(in.Runtimes) > 64 || in.Slots < 1 || in.Slots > 64 {
		writeErr(w, 400, "runtimes and slots (1-64) required")
		return
	}
	for _, ref := range in.Runtimes {
		if ref.WorkspaceID == uuid.Nil || ref.RuntimeID == uuid.Nil {
			writeErr(w, 400, "workspace_id and runtime_id required")
			return
		}
	}
	got, err := h.AgentRuns.Claim(r.Context(), user(r), in.Runtimes, in.Slots)
	if err != nil {
		agentRunFail(w, r, err)
		return
	}
	if got == nil {
		got = []service.Claimed{}
	}
	writeJSON(w, 200, got)
}

type agentRunCall struct {
	service.RuntimeRef
	service.Report
	Messages []service.Message `json:"messages"`
}

func (h *H) agentRunCall(w http.ResponseWriter, r *http.Request) (uuid.UUID, agentRunCall, bool) {
	id, err := uuid.Parse(chi.URLParam(r, "id"))
	if err != nil {
		writeErr(w, 404, "not found")
		return uuid.Nil, agentRunCall{}, false
	}
	var in agentRunCall
	if r.Method == http.MethodGet {
		q := r.URL.Query()
		in.WorkspaceID, err = uuid.Parse(q.Get("workspace_id"))
		if err != nil {
			writeErr(w, 400, "workspace_id required")
			return id, in, false
		}
		in.RuntimeID, err = uuid.Parse(q.Get("runtime_id"))
		if err != nil {
			writeErr(w, 400, "runtime_id required")
			return id, in, false
		}
		n, parseErr := strconv.ParseInt(q.Get("attempt"), 10, 32)
		if parseErr != nil {
			writeErr(w, 400, "positive attempt required")
			return id, in, false
		}
		in.Attempt = int32(n)
	} else if err := readJSON(r, &in); err != nil {
		writeErr(w, 400, "invalid agent run request: "+err.Error())
		return id, in, false
	}
	if in.WorkspaceID == uuid.Nil || in.RuntimeID == uuid.Nil || in.Attempt <= 0 {
		writeErr(w, 400, "workspace_id, runtime_id and positive attempt required")
		return id, in, false
	}
	return id, in, true
}

func (h *H) daemonStep(step func(*http.Request, uuid.UUID, agentRunCall) error) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		id, in, ok := h.agentRunCall(w, r)
		if !ok {
			return
		}
		if err := step(r, id, in); err != nil {
			agentRunFail(w, r, err)
			return
		}
		w.WriteHeader(204)
	}
}
func (h *H) daemonPrepare() http.HandlerFunc {
	return h.daemonStep(func(r *http.Request, id uuid.UUID, in agentRunCall) error {
		return h.AgentRuns.PrepareLease(r.Context(), user(r), in.RuntimeRef, id)
	})
}
func (h *H) daemonStart() http.HandlerFunc {
	return h.daemonStep(func(r *http.Request, id uuid.UUID, in agentRunCall) error {
		return h.AgentRuns.Start(r.Context(), user(r), in.RuntimeRef, id)
	})
}
func (h *H) daemonMessages() http.HandlerFunc {
	return h.daemonStep(func(r *http.Request, id uuid.UUID, in agentRunCall) error {
		return h.AgentRuns.Messages(r.Context(), user(r), in.RuntimeRef, id, in.Messages)
	})
}
func (h *H) daemonCancelAck() http.HandlerFunc {
	return h.daemonStep(func(r *http.Request, id uuid.UUID, in agentRunCall) error {
		_, err := h.AgentRuns.Status(r.Context(), user(r), in.RuntimeRef, id)
		return err
	})
}
func (h *H) daemonSettle(ok bool) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		id, in, valid := h.agentRunCall(w, r)
		if !valid {
			return
		}
		settle := h.AgentRuns.Fail
		if ok {
			settle = h.AgentRuns.Complete
		}
		t, err := settle(r.Context(), user(r), in.RuntimeRef, id, in.Report)
		if err != nil {
			agentRunFail(w, r, err)
			return
		}
		writeJSON(w, 200, t)
	}
}
func (h *H) daemonStatus(w http.ResponseWriter, r *http.Request) {
	id, in, ok := h.agentRunCall(w, r)
	if !ok {
		return
	}
	st, err := h.AgentRuns.Status(r.Context(), user(r), in.RuntimeRef, id)
	if err != nil {
		agentRunFail(w, r, err)
		return
	}
	writeJSON(w, 200, map[string]string{"status": st})
}
