package handler

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"lykeion/server/internal/autoresearch"
	"lykeion/server/internal/fence"
	"lykeion/server/internal/middleware"
	"lykeion/server/internal/store"
	db "lykeion/server/pkg/db/generated"
)

// StudyDraftIn is a request for an agent to draft one study.
type StudyDraftIn struct {
	AgentID    uuid.UUID  `json:"agent_id"`
	Prompt     string     `json:"prompt"`
	ResearchID *uuid.UUID `json:"research_id"`
	StartDate  *time.Time `json:"start_date"`
	Deadline   *time.Time `json:"deadline"`
	Priority   string     `json:"priority"`
}

// createStudyDraft queues a single, agent-authored study without making a recurring rule.
func (h *H) createStudyDraft(w http.ResponseWriter, r *http.Request) {
	var in StudyDraftIn
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
	ws, user := middleware.WorkspaceFrom(r.Context()).ID, middleware.IdentityFrom(r.Context()).UserID
	var run db.AutoresearchRun
	err := middleware.InWorkspace(r.Context(), h.Store, "", func(tx *store.Tx) error {
		var err error
		_, run, err = h.draftStudy(r.Context(), tx, ws, user, in)
		return err
	})
	if !answer(w, r, err) {
		return
	}
	writeJSON(w, http.StatusAccepted, map[string]any{"run_id": run.ID, "status": run.Status})
}

// DraftStudy queues a study draft for user without an HTTP request, in its own workspace transaction.
func (h *H) DraftStudy(ctx context.Context, ws, user uuid.UUID, in StudyDraftIn) (uuid.UUID, error) {
	if h.Autoresearch == nil {
		return uuid.Nil, cardInputError("agent drafting is unavailable")
	}
	var id uuid.UUID
	err := h.Store.InWorkspace(ctx, ws, user, func(tx *store.Tx) error {
		// Same locks as middleware.InWorkspace: a removed member must not queue drafts.
		if _, err := tx.Q.LockWorkspaceForMutation(ctx, ws); err != nil {
			return err
		}
		if _, err := tx.Q.LockMember(ctx, db.LockMemberParams{WorkspaceID: ws, UserID: user}); err != nil {
			return err
		}
		var err error
		id, _, err = h.draftStudy(ctx, tx, ws, user, in)
		return err
	})
	return id, err
}

// DraftingAgentUsable refuses a drafting agent that is not visible to the whole workspace.
func DraftingAgentUsable(ctx context.Context, tx *store.Tx, ws, agentID uuid.UUID) error {
	refused := cardInputError("choose an agent every member can use")
	a, err := tx.Q.GetAgentForShare(ctx, db.GetAgentForShareParams{WorkspaceID: ws, ID: agentID})
	if errors.Is(err, pgx.ErrNoRows) {
		return refused
	} else if err != nil {
		return err
	}
	if a.Visibility == "private" || a.ArchivedAt != nil || a.BuiltInKey == "aristotle" || a.Stage == "" {
		return refused
	}
	return nil
}

// draftStudy runs the checks and queues the draft inside tx.
func (h *H) draftStudy(ctx context.Context, tx *store.Tx, ws, user uuid.UUID, in StudyDraftIn) (uuid.UUID, db.AutoresearchRun, error) {
	var run db.AutoresearchRun
	var apID uuid.UUID
	err := func() error {
		if _, err := tx.Q.LockWorkspaceExecution(ctx, ws); err != nil {
			return err
		}
		if err := tx.Q.LockWorkspaceEvents(ctx, ws); err != nil {
			return err
		}
		if err := checkStudyAgent(ctx, tx, ws, user, in.AgentID, "", ""); err != nil {
			return err
		}
		agent, err := tx.Q.GetAgent(ctx, db.GetAgentParams{WorkspaceID: ws, ID: in.AgentID})
		if err != nil {
			return err
		}
		if agent.Stage == "" {
			return cardInputError("this agent has no stage")
		}
		online, err := tx.Q.HasOnlineRuntime(ctx, db.HasOnlineRuntimeParams{WorkspaceID: ws, OwnerID: user, Harness: agent.Harness})
		if err != nil {
			return err
		}
		if !online {
			return cardInputError("The agent's CLI is offline. Connect a machine and try again.")
		}
		if in.ResearchID != nil {
			if _, err := tx.Q.GetResearch(ctx, db.GetResearchParams{WorkspaceID: ws, ID: *in.ResearchID}); err != nil {
				return cardInputError("project not found")
			}
		}
		typ := "agent"
		description := "Create exactly one study from the request below. Draft a clear title and a faithful research question without inventing facts. Hand in the study as the only entry of `new_studies`, with its title and question, and say in your report that it was created. Do not perform the research yourself.\n\n" + fence.Untrusted("Researcher's request", in.Prompt)
		ap, err := tx.Q.CreateAutoresearch(ctx, db.CreateAutoresearchParams{WorkspaceID: ws, OwnerID: user, Title: "Study draft", Description: description, ExecutionMode: "run_only", AssigneeType: &typ, AssigneeID: &in.AgentID, ScopeFrom: agent.Stage, ScopeTo: agent.Stage})
		if err != nil {
			return err
		}
		if err := tx.CreateStudyDraft(ctx, ap.ID, store.StudyDraft{ResearchID: in.ResearchID, StartDate: in.StartDate, Deadline: in.Deadline, Priority: in.Priority}); err != nil {
			return err
		}
		apID = ap.ID
		run, _, err = h.Autoresearch.Admit(ctx, tx, autoresearch.Admission{Autoresearch: ap.ID, Source: "manual", Actor: user, PlannedAt: time.Now(), Key: "study-draft:" + uuid.NewString()})
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
	}()
	return apID, run, err
}
