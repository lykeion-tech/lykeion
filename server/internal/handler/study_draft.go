package handler

import (
	"context"
	"log/slog"
	"net/http"
	"strings"
	"time"

	"github.com/google/uuid"
	"lykeion/server/internal/autoresearch"
	"lykeion/server/internal/fence"
	"lykeion/server/internal/store"
	db "lykeion/server/pkg/db/generated"
)

// createStudyDraft queues a single, agent-authored study without making a recurring rule.
func (h *H) createStudyDraft(w http.ResponseWriter, r *http.Request) {
	var in struct {
		AgentID    uuid.UUID  `json:"agent_id"`
		Prompt     string     `json:"prompt"`
		ResearchID *uuid.UUID `json:"research_id"`
		StartDate  *time.Time `json:"start_date"`
		Deadline   *time.Time `json:"deadline"`
		Priority   string     `json:"priority"`
	}
	if err := readJSON(r, &in); err != nil {
		writeErr(w, 400, "invalid request")
		return
	}
	in.Prompt = strings.TrimSpace(in.Prompt)
	if in.AgentID == uuid.Nil || in.Prompt == "" || len(in.Prompt) > 12000 {
		writeErr(w, 400, "choose an agent and enter a request of at most 12000 bytes")
		return
	}
	if !validPlanningPriority(in.Priority) {
		writeErr(w, 400, "invalid priority")
		return
	}
	if in.Priority == "" {
		in.Priority = "none"
	}
	if in.StartDate != nil && in.Deadline != nil && in.StartDate.After(*in.Deadline) {
		writeErr(w, 400, "start date must be on or before the due date")
		return
	}
	if h.Autoresearch == nil {
		writeErr(w, 503, "agent drafting is unavailable")
		return
	}
	var ws, user uuid.UUID
	var run db.AutoresearchRun
	err := h.inWS(r, func(tx *store.Tx, workspace, actor uuid.UUID) error {
		ws, user = workspace, actor
		if _, err := tx.Q.LockWorkspaceExecution(r.Context(), ws); err != nil {
			return err
		}
		if err := tx.Q.LockWorkspaceEvents(r.Context(), ws); err != nil {
			return err
		}
		if err := checkStudyAgent(r, tx, ws, user, in.AgentID, "", ""); err != nil {
			return err
		}
		agent, err := tx.Q.GetAgent(r.Context(), db.GetAgentParams{WorkspaceID: ws, ID: in.AgentID})
		if err != nil {
			return err
		}
		if agent.Stage == "" {
			return cardInputError("this agent has no stage")
		}
		online, err := tx.Q.HasOnlineRuntime(r.Context(), db.HasOnlineRuntimeParams{WorkspaceID: ws, OwnerID: user, Harness: agent.Harness})
		if err != nil {
			return err
		}
		if !online {
			return cardInputError("The agent's CLI is offline. Connect a machine and try again.")
		}
		if in.ResearchID != nil {
			if _, err := tx.Q.GetResearch(r.Context(), db.GetResearchParams{WorkspaceID: ws, ID: *in.ResearchID}); err != nil {
				return cardInputError("project not found")
			}
		}
		typ := "agent"
		description := "Create exactly one study from the request below. Draft a clear title and a faithful research question without inventing facts. Hand in the study as the only entry of `new_studies`, with its title and question, and say in your report that it was created. Do not perform the research yourself.\n\n" + fence.Untrusted("Researcher's request", in.Prompt)
		ap, err := tx.Q.CreateAutoresearch(r.Context(), db.CreateAutoresearchParams{WorkspaceID: ws, OwnerID: user, Title: "Study draft", Description: description, ExecutionMode: "run_only", AssigneeType: &typ, AssigneeID: &in.AgentID, ScopeFrom: agent.Stage, ScopeTo: agent.Stage})
		if err != nil {
			return err
		}
		if err := tx.CreateStudyDraft(r.Context(), ap.ID, store.StudyDraft{ResearchID: in.ResearchID, StartDate: in.StartDate, Deadline: in.Deadline, Priority: in.Priority}); err != nil {
			return err
		}
		run, _, err = h.Autoresearch.Admit(r.Context(), tx, autoresearch.Admission{Autoresearch: ap.ID, Source: "manual", Actor: user, PlannedAt: time.Now(), Key: "study-draft:" + uuid.NewString()})
		if err != nil {
			return err
		}
		tx.AfterPublish(func() {
			ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
			defer cancel()
			if err := h.Autoresearch.Dispatch(ctx, ws, run.ID); err != nil {
				slog.Error("dispatch study draft", "run_id", run.ID, "error", err)
				failureCtx, stop := context.WithTimeout(context.Background(), 10*time.Second)
				defer stop()
				if failureErr := h.Autoresearch.FailStudyDraft(failureCtx, ws, run.ID); failureErr != nil {
					slog.Error("record study draft failure", "run_id", run.ID, "error", failureErr)
				}
			}
		})
		return nil
	})
	if !answer(w, r, err) {
		return
	}
	writeJSON(w, http.StatusAccepted, map[string]any{"run_id": run.ID, "status": run.Status})
}
