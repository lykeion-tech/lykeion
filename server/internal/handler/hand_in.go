package handler

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgconn"

	"lykeion/server/internal/autoresearch"
	"lykeion/server/internal/handin"
	"lykeion/server/internal/labmemory"
	"lykeion/server/internal/service"
	"lykeion/server/internal/store"
	"lykeion/server/internal/team"
	db "lykeion/server/pkg/db/generated"
)

// autoresearchOps gives the hand-in the Autoresearch service's operations,
// naming their outcomes the way handin does.
type autoresearchOps struct{ s *autoresearch.Service }

func (o autoresearchOps) Report(ctx context.Context, tx *store.Tx, run uuid.UUID, text string) error {
	if err := o.s.Report(ctx, tx, run, text); errors.Is(err, autoresearch.ErrReported) {
		return handin.ErrReported
	} else {
		return err
	}
}

func (o autoresearchOps) OpenCardForAgentRun(ctx context.Context, tx *store.Tx, run uuid.UUID, title, question string) (db.Card, error) {
	c, err := o.s.OpenCardForAgentRun(ctx, tx, run, title, question)
	var nb team.NoBuiltInError
	switch {
	case errors.Is(err, autoresearch.ErrAlreadyActive):
		return c, handin.ErrDuplicate
	case errors.Is(err, autoresearch.ErrStartFailed), errors.As(err, &nb):
		// A database or cancelled-context cause is a failure of the server,
		// not a reason this study cannot start.
		var pg *pgconn.PgError
		if !errors.As(err, &pg) && ctx.Err() == nil {
			return c, handin.CannotStart{Reason: err.Error()}
		}
	}
	return c, err
}

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
	s := handin.HandIns{Runs: h.AgentRuns, Autoresearch: autoresearchOps{h.Autoresearch}, Aristotle: labmemory.Answers{Store: h.Store}}
	if h.Engine != nil {
		s.SentBack = h.Engine.SentBack
	}
	probs, err := s.Submit(r.Context(), user(r), in.RuntimeRef, id, in.HandIn)
	if err != nil {
		agentRunFail(w, r, err)
		return
	}
	if len(probs) > 0 {
		reply := map[string]any{"accepted": false, "problems": probs}
		if len(probs) == 1 && probs[0] == labmemory.FreshTurnProblem {
			reply["fresh_attempt_required"] = true
		}
		writeJSON(w, 200, reply)
		return
	}
	writeJSON(w, 200, map[string]any{"accepted": true})
}
