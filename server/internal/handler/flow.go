package handler

import (
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"strings"

	"lykeion/server/internal/flow"
	"lykeion/server/internal/middleware"
	"lykeion/server/internal/records"
	"lykeion/server/internal/service"
	"lykeion/server/internal/store"
	db "lykeion/server/pkg/db/generated"
)

func writer(role middleware.Role) service.Writer {
	return service.Writer{Card: role.CardID, AgentRun: role.AgentRunID, Column: role.Column, Role: role.Role, Round: role.Round}
}

// flowError answers the engine's refusals of a role command.
func flowError(w http.ResponseWriter, r *http.Request, err error) {
	var inv service.Invalid
	switch {
	case errors.As(err, &inv), errors.Is(err, flow.ErrUndeclaredMember):
		writeErr(w, 400, err.Error())
	case errors.Is(err, flow.ErrStepCap), errors.Is(err, flow.ErrInFlight), errors.Is(err, flow.ErrMovedOn):
		writeErr(w, 409, err.Error())
	case errors.Is(err, flow.ErrPackUnavailable):
		slog.Error("load Team for a step", "card", middleware.RoleFrom(r.Context()).CardID, "err", err)
		writeErr(w, 503, "this study's Team can't be loaded")
	default:
		roleError(w, r, err)
	}
}

// postStep is the leader naming a member for a step, which starts the
// member's run, or announcing a step it does itself.
func (h *H) postStep(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Member  string `json:"member"`
		Content string `json:"content"`
		Self    bool   `json:"self"`
	}
	if err := readJSON(r, &in); err != nil || strings.TrimSpace(in.Content) == "" {
		writeErr(w, 400, "content required")
		return
	}
	if middleware.RoleFrom(r.Context()).Role != "leader" {
		writeErr(w, 403, "only the leader names members")
		return
	}
	var rec db.Record
	err := h.roleTx(r, func(tx *store.Tx, role middleware.Role) (err error) {
		if in.Self {
			rec, err = records.WriteAs(r.Context(), tx, writer(role), "self_step", map[string]string{"step": in.Content})
		} else {
			rec, err = h.Engine.Delegate(r.Context(), tx, writer(role), in.Member, in.Content)
		}
		return err
	})
	if err != nil {
		flowError(w, r, err)
		return
	}
	out := map[string]any{"key": rec.Key}
	if !in.Self {
		var body struct {
			AgentRunID string `json:"agent_run_id"`
		}
		json.Unmarshal(rec.Body, &body)
		out["agent_run_id"] = body.AgentRunID
	}
	writeJSON(w, 201, out)
}

// postDone is the leader, or a study's single agent, declaring its column
// done; the column then goes to the auditor.
func (h *H) postDone(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Summary string   `json:"summary"`
		Cites   []string `json:"cites"`
	}
	if err := readJSON(r, &in); err != nil || strings.TrimSpace(in.Summary) == "" {
		writeErr(w, 400, "summary required")
		return
	}
	if role := middleware.RoleFrom(r.Context()).Role; role != "leader" && role != "agent" {
		writeErr(w, 403, "only the leader or the study's agent declares a column done")
		return
	}
	var rec db.Record
	err := h.roleTx(r, func(tx *store.Tx, role middleware.Role) (err error) {
		rec, err = h.Engine.Finish(r.Context(), tx, writer(role), in.Summary, in.Cites)
		return err
	})
	if err != nil {
		flowError(w, r, err)
		return
	}
	role := middleware.RoleFrom(r.Context())
	if err := h.Engine.Audit(r.Context(), role.WorkspaceID, role.CardID); err != nil {
		slog.Error("audit", "card", role.CardID, "err", err)
	}
	writeJSON(w, 201, map[string]string{"key": rec.Key})
}

// listCalls lists the study's recorded database calls in the run's column,
// each answer cut to an excerpt.
func (h *H) listCalls(w http.ResponseWriter, r *http.Request) {
	var recs []db.Record
	err := h.roleTx(r, func(tx *store.Tx, role middleware.Role) (err error) {
		recs, err = tx.Q.ListRecords(r.Context(), db.ListRecordsParams{WorkspaceID: role.WorkspaceID, CardID: role.CardID, BoardColumn: role.Column})
		return err
	})
	if err != nil {
		roleError(w, r, err)
		return
	}
	out := []map[string]any{}
	for _, rec := range recs {
		if rec.Kind != "call" {
			continue
		}
		var body map[string]any
		if json.Unmarshal(rec.Body, &body) != nil {
			continue
		}
		if a, ok := body["answer"].(string); ok {
			if cut, more := flow.Excerpt(a, 2000); more {
				body["answer"], body["truncated"] = cut, true
			}
		}
		body["key"] = rec.Key
		out = append(out, body)
	}
	writeJSON(w, 200, out)
}
