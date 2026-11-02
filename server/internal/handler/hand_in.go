package handler

import (
	"encoding/json"
	"net/http"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"

	"lykeion/server/internal/handin"
	"lykeion/server/internal/service"
)

// daemonHandIn takes a run's hand-in from the daemon running it: accepted,
// or the problems to ask the agent to correct.
func (h *H) daemonHandIn(w http.ResponseWriter, r *http.Request) {
	id, err := uuid.Parse(chi.URLParam(r, "id"))
	if err != nil {
		writeErr(w, 404, "not found")
		return
	}
	var in struct {
		service.RuntimeRef
		HandIn json.RawMessage `json:"hand_in"`
	}
	if err := readJSON(r, &in); err != nil || in.WorkspaceID == uuid.Nil || in.RuntimeID == uuid.Nil || in.Attempt <= 0 || len(in.HandIn) == 0 {
		writeErr(w, 400, "workspace_id, runtime_id, positive attempt and hand_in required")
		return
	}
	s := handin.HandIns{Runs: h.AgentRuns}
	if h.Engine != nil {
		s.SentBack = h.Engine.SentBack
	}
	probs, err := s.Submit(r.Context(), user(r), in.RuntimeRef, id, in.HandIn)
	if err != nil {
		agentRunFail(w, r, err)
		return
	}
	if len(probs) > 0 {
		writeJSON(w, 200, map[string]any{"accepted": false, "problems": probs})
		return
	}
	writeJSON(w, 200, map[string]any{"accepted": true})
}
