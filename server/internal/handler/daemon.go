package handler

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"

	"github.com/jackc/pgx/v5"
	"lykeion/server/internal/auth"
	"lykeion/server/internal/compute"
	"lykeion/server/internal/handin"
	"lykeion/server/internal/labmemory"
	"lykeion/server/internal/middleware"
	"lykeion/server/internal/service"
	"lykeion/server/internal/store"
	"lykeion/server/pkg/agent"
	db "lykeion/server/pkg/db/generated"
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
	case errors.Is(err, agent.ErrLabClaude), errors.Is(err, agent.ErrLabHarness):
		writeErr(w, http.StatusServiceUnavailable, err.Error())
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
		Bases    json.RawMessage      `json:"bases"`
		Metrics  json.RawMessage      `json:"metrics"`
		Models   json.RawMessage      `json:"models"`
	}
	if err := readJSON(r, &in); err != nil || len(in.Runtimes) == 0 || len(in.Runtimes) > 64 {
		writeErr(w, 400, "runtimes required (1-64)")
		return
	}
	// A bad base report must not stop the heartbeat that keeps the runtimes
	// online: entries the server cannot read are dropped, and a value that is
	// not an object leaves the stored states as they are.
	var reported map[string]json.RawMessage
	var bases map[string]string
	if json.Unmarshal(in.Bases, &reported) == nil && reported != nil {
		bases = map[string]string{}
		for lang, v := range reported {
			var state string
			if json.Unmarshal(v, &state) == nil && validBaseState(lang, state) {
				bases[lang] = state
			}
		}
	}
	for _, ref := range in.Runtimes {
		if ref.WorkspaceID == uuid.Nil || ref.RuntimeID == uuid.Nil {
			writeErr(w, 400, "workspace_id and runtime_id required")
			return
		}
	}
	telemetry := service.RuntimeTelemetry{Models: map[string]json.RawMessage{}}
	var metrics compute.MachineSample
	if json.Unmarshal(in.Metrics, &metrics) == nil && metrics.Valid() {
		telemetry.Metrics, _ = json.Marshal(struct {
			compute.MachineSample
			T time.Time `json:"t"`
		}{metrics, time.Now().UTC()})
	}
	var catalogs map[string]json.RawMessage
	_ = json.Unmarshal(in.Models, &catalogs)
	for harness, raw := range catalogs {
		if _, ok := agent.Harnesses[harness]; !ok {
			continue
		}
		var models []agent.Model
		if json.Unmarshal(raw, &models) == nil && agent.ValidModels(models) {
			if models == nil {
				models = []agent.Model{}
			}
			telemetry.Models[harness], _ = json.Marshal(models)
		}
	}
	n, err := h.AgentRuns.Heartbeat(r.Context(), user(r), in.Runtimes, bases, telemetry)
	if err != nil {
		agentRunFail(w, r, err)
		return
	}
	writeJSON(w, 200, map[string]int{"touched": n})
}

// validBaseState accepts a base the daemon knows reported as ready, building
// or failed with a reason of at most 200 bytes, which keeps a runtime's whole
// set well inside its column's size limit. jsonb text cannot hold NUL. The
// sandbox entry is checked once at start, so it is never building.
func validBaseState(lang, state string) bool {
	switch lang {
	case "python", "r", "julia":
		if state == "building" {
			return true
		}
	case "sandbox":
	default:
		return false
	}
	if state == "ready" {
		return true
	}
	reason, ok := strings.CutPrefix(state, "failed: ")
	return ok && reason != "" && len(reason) <= 200 && !strings.ContainsRune(reason, 0)
}

func (h *H) daemonClaim(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Runtimes       []service.RuntimeRef `json:"runtimes"`
		Slots          int                  `json:"slots"`
		GuestIsolation string               `json:"guest_isolation"`
		CoresearchOnly bool                 `json:"coresearch_only"`
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
	got, err := h.AgentRuns.Claim(r.Context(), user(r), in.Runtimes, in.Slots, service.ClaimOptions{GuestIsolation: in.GuestIsolation, CoresearchOnly: in.CoresearchOnly})
	if err != nil {
		agentRunFail(w, r, err)
		return
	}
	if got == nil {
		got = []service.Claimed{}
	}
	for i, c := range got {
		if k, ok := handin.KindFor(c.AgentRun.Kind, c.AgentRun.Role, c.AgentRun.BoardColumn); ok {
			got[i].OutputSchema, got[i].Lead = handin.Schema(k), handin.Leads(c.AgentRun.Role)
			got[i].Leader = c.AgentRun.Role == "leader"
		}
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
	regular := h.daemonStep(func(r *http.Request, id uuid.UUID, in agentRunCall) error {
		return h.AgentRuns.Start(r.Context(), user(r), in.RuntimeRef, id)
	})
	return func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Query().Get("lab_history") != "1" {
			regular(w, r)
			return
		}
		id, in, ok := h.agentRunCall(w, r)
		if !ok {
			return
		}
		prompt, err := h.AgentRuns.StartLab(r.Context(), user(r), in.RuntimeRef, id, labmemory.LaunchPrompt)
		if err != nil {
			agentRunFail(w, r, err)
			return
		}
		writeJSON(w, 200, map[string]string{"prompt": prompt})
	}
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
		if t.Kind == "aristotle" {
			// An old daemon acknowledges settlement with no need for private turn data.
			writeJSON(w, http.StatusOK, map[string]any{"id": t.ID, "status": t.Status, "attempt": t.Attempt})
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

// daemonComments hands a running turn the comments posted on its study
// since it started, each once.
func (h *H) daemonComments(w http.ResponseWriter, r *http.Request) {
	id, in, ok := h.agentRunCall(w, r)
	if !ok {
		return
	}
	out, err := h.AgentRuns.TakeComments(r.Context(), user(r), in.RuntimeRef, id)
	if err != nil {
		agentRunFail(w, r, err)
		return
	}
	writeJSON(w, 200, out)
}

// daemonService streams hidden code only to the runtime running this study.
func (h *H) daemonService(w http.ResponseWriter, r *http.Request) {
	ws, e1 := uuid.Parse(r.Header.Get("X-Workspace-ID"))
	run, e2 := uuid.Parse(chi.URLParam(r, "run"))
	id, e3 := uuid.Parse(chi.URLParam(r, "id"))
	if errors.Join(e1, e2, e3) != nil {
		writeErr(w, 404, "not found")
		return
	}
	var row db.Attachment
	err := h.memberTx(r, ws, false, func(tx *store.Tx) error {
		a, err := tx.Q.GetAgentRun(r.Context(), db.GetAgentRunParams{WorkspaceID: ws, ID: run})
		if err != nil {
			return err
		}
		// A rerun replays Experimentation's scripts, with its services.
		if a.Status != "running" || (a.BoardColumn != "experimentation" && a.Kind != "rerun") || a.RuntimeID == nil || a.CardID == nil {
			return pgx.ErrNoRows
		}
		rt, err := tx.Q.GetRuntime(r.Context(), db.GetRuntimeParams{WorkspaceID: ws, ID: *a.RuntimeID})
		if err != nil {
			return err
		}
		if rt.OwnerID != user(r) {
			return pgx.ErrNoRows
		}
		card, err := tx.Q.GetCard(r.Context(), db.GetCardParams{WorkspaceID: ws, ID: *a.CardID})
		if err != nil {
			return err
		}
		allowed, err := service.RuntimeAllowsUser(r.Context(), tx, rt, card.OwnerID)
		if err != nil {
			return err
		}
		if !allowed {
			return pgx.ErrNoRows
		}
		row, err = tx.Q.CardAttachment(r.Context(), db.CardAttachmentParams{WorkspaceID: ws, CardID: a.CardID, ID: id})
		if err != nil {
			return err
		}
		if row.Kind != "service" {
			return pgx.ErrNoRows
		}
		return nil
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

// Hidden downloads conceal their existence from agent and browser credentials.
type hiddenServiceResponse struct{ http.ResponseWriter }

func (w hiddenServiceResponse) Unwrap() http.ResponseWriter { return w.ResponseWriter }
func (w hiddenServiceResponse) WriteHeader(status int) {
	if status == http.StatusUnauthorized {
		status = http.StatusNotFound
	}
	w.ResponseWriter.WriteHeader(status)
}

func hiddenServiceCredential(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !strings.HasPrefix(r.Header.Get("Authorization"), "Bearer "+auth.PersonalPrefix) {
			writeErr(w, 404, "not found")
			return
		}
		next.ServeHTTP(hiddenServiceResponse{w}, r)
	})
}
