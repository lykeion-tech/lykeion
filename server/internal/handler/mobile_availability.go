package handler

import (
	"net/http"

	"lykeion/server/internal/middleware"
	"lykeion/server/pkg/agent"
	db "lykeion/server/pkg/db/generated"
)

func (h *H) mobileAvailability(w http.ResponseWriter, r *http.Request) {
	tx := middleware.TxFrom(r.Context())
	harnesses := []string{}
	for _, name := range []string{"codex", "claude"} {
		if agent.LabAvailability(name) != nil {
			continue
		}
		ok, err := tx.Q.AristotleRuntimeAvailable(r.Context(), db.AristotleRuntimeAvailableParams{Harness: name, RequireHealthy: true})
		if err != nil {
			fail(w, r, err)
			return
		}
		if ok {
			harnesses = append(harnesses, name)
		}
	}
	writeJSON(w, 200, map[string]any{"harnesses": harnesses, "capabilities": []string{"study-runs", "study-proposals", "study-suggestions", "study-proposal-properties", "aristotle-coresearch"}})
}
