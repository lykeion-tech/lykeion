package handler

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"regexp"
	"strings"
	"unicode/utf8"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"lykeion/server/internal/board"
	"lykeion/server/internal/flow"
	"lykeion/server/internal/middleware"
	"lykeion/server/internal/pack"
	"lykeion/server/internal/store"
	"lykeion/server/internal/team"
	"lykeion/server/pkg/agent"
	db "lykeion/server/pkg/db/generated"
)

var agentModelPattern = regexp.MustCompile(`^([A-Za-z0-9][A-Za-z0-9._:/@-]{0,199})?$`)
var errAgentArchived = errors.New("agent is archived")
var errAgentBuiltinArchive = errors.New("built-in agents cannot be archived")
var errAgentForbidden = errors.New("only the owner or an admin can edit this agent")

func agentAdmin(r *http.Request) bool { return ctxAdmin(r.Context()) }
func ctxAdmin(ctx context.Context) bool {
	role := middleware.WorkspaceFrom(ctx).Role
	return role == "owner" || role == "admin"
}
func agentVisible(ctx context.Context, a db.Agent, user uuid.UUID) bool {
	return a.Visibility == "workspace" || a.OwnerID == user || ctxAdmin(ctx)
}
func validateAgent(a db.Agent) error {
	if utf8.RuneCountInString(a.Name) < 1 || utf8.RuneCountInString(a.Name) > 64 {
		return cardInputError("name must contain 1 to 64 characters")
	}
	if utf8.RuneCountInString(a.Description) > 1000 {
		return cardInputError("description exceeds 1000 characters")
	}
	if len(a.Instructions) > 65536 || strings.ContainsRune(a.Instructions, 0) || !utf8.ValidString(a.Instructions) {
		return cardInputError("instructions must be valid UTF-8 without NUL, at most 64 KiB")
	}
	if len(a.ReportTemplate) > 65536 || strings.ContainsRune(a.ReportTemplate, 0) || !utf8.ValidString(a.ReportTemplate) {
		return cardInputError("report template must be valid UTF-8 without NUL, at most 64 KiB")
	}
	if _, ok := agent.Harnesses[a.Harness]; !ok {
		return cardInputError(fmt.Sprintf("unknown harness %q", a.Harness))
	}
	if !agentModelPattern.MatchString(a.Model) {
		return cardInputError("invalid model")
	}
	if a.Visibility != "workspace" && a.Visibility != "private" {
		return cardInputError("visibility must be workspace or private")
	}
	if len(a.ConversationStarters) > 0 {
		return validateConversationStarters(a.ConversationStarters)
	}
	return nil
}

// refuseLaboratory answers 403 for any change to a laboratory agent.
func refuseLaboratory(a db.Agent) error {
	if a.Laboratory != "" {
		return team.ErrLaboratoryReadOnly
	}
	return nil
}

func agentFail(w http.ResponseWriter, r *http.Request, err error, out any, status int) {
	var bad cardInputError
	var busy conflict
	switch {
	case errors.As(err, &busy):
		writeErr(w, 409, busy.Error())
	case errors.Is(err, errAgentForbidden), errors.Is(err, team.ErrLaboratoryReadOnly):
		writeErr(w, 403, err.Error())
	case errors.Is(err, errAgentArchived), errors.Is(err, errAgentBuiltinArchive):
		writeErr(w, 409, err.Error())
	case isUnique(err):
		writeErr(w, 409, "a live agent already has this name")
	case errors.As(err, &bad):
		writeErr(w, 400, bad.Error())
	case err != nil:
		fail(w, r, err)
	default:
		writeJSON(w, status, out)
	}
}

// agentInUse refuses changes to an agent that studies or Autoresearches still
// work with, naming the first five.
func agentInUse(r *http.Request, tx *store.Tx, id uuid.UUID) error {
	uses, err := tx.Q.AgentUses(r.Context(), db.AgentUsesParams{WorkspaceID: tx.WS, AgentID: &id})
	if err != nil || len(uses) == 0 {
		return err
	}
	var items []string
	for _, u := range uses[:min(len(uses), 5)] {
		if u.Kind == "study" {
			items = append(items, fmt.Sprintf("study %s (%s)", u.Label, u.Owner))
		} else {
			items = append(items, fmt.Sprintf("Autoresearch %q (%s)", u.Label, u.Owner))
		}
	}
	list := items[0]
	if n := len(items); n > 1 {
		list = strings.Join(items[:n-1], ", ") + " and " + items[n-1]
	}
	if len(uses) > 5 {
		list = strings.Join(items, ", ") + fmt.Sprintf(" and %d more", len(uses)-5)
	}
	return conflict("Used by " + list + ". It can change once they finish or their owners delete them.")
}

func (h *H) listAgents(w http.ResponseWriter, r *http.Request) {
	out := []db.ListAgentsRow{}
	err := h.inWS(r, func(tx *store.Tx, ws, user uuid.UUID) error {
		var err error
		out, err = tx.Q.ListAgents(r.Context(), db.ListAgentsParams{WorkspaceID: ws, UserID: user, IsAdmin: agentAdmin(r)})
		return err
	})
	if out == nil {
		out = []db.ListAgentsRow{}
	}
	agentFail(w, r, err, out, 200)
}

func (h *H) createAgent(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Name                 string          `json:"name"`
		Description          string          `json:"description"`
		Instructions         string          `json:"instructions"`
		ReportTemplate       string          `json:"report_template"`
		Harness              string          `json:"harness"`
		Model                string          `json:"model"`
		Visibility           string          `json:"visibility"`
		BuiltInKey           string          `json:"built_in_key"`
		Stage                string          `json:"stage"`
		ConversationStarters json.RawMessage `json:"conversation_starters"`
	}
	if err := readJSON(r, &in); err != nil {
		writeErr(w, 400, "invalid agent: "+err.Error())
		return
	}
	if in.BuiltInKey != "" {
		writeErr(w, 400, "built-in identity cannot be created")
		return
	}
	if board.StageIndex(in.Stage) < 0 {
		writeErr(w, 400, "choose a stage for this agent")
		return
	}
	if len(in.ConversationStarters) > 0 {
		if err := validateConversationStarters(in.ConversationStarters); err != nil {
			agentFail(w, r, err, nil, 201)
			return
		}
	}
	if in.Visibility == "" {
		in.Visibility = "workspace"
	}
	if len(in.ConversationStarters) == 0 {
		in.ConversationStarters = json.RawMessage(`[]`)
	}
	a := db.Agent{ConversationStarters: in.ConversationStarters, Name: strings.TrimSpace(in.Name), Description: in.Description, Instructions: in.Instructions, ReportTemplate: in.ReportTemplate, Harness: in.Harness, Model: in.Model, Visibility: in.Visibility}
	if err := validateAgent(a); err != nil {
		agentFail(w, r, err, nil, 201)
		return
	}
	var out db.Agent
	err := h.inWS(r, func(tx *store.Tx, ws, user uuid.UUID) error {
		var err error
		out, err = tx.Q.CreateAgent(r.Context(), db.CreateAgentParams{WorkspaceID: ws, OwnerID: user, Name: a.Name, Description: a.Description, Instructions: a.Instructions, ReportTemplate: a.ReportTemplate, Harness: a.Harness, Model: a.Model, Visibility: a.Visibility, Stage: in.Stage, ConversationStarters: a.ConversationStarters})
		if err != nil {
			return err
		}
		return tx.Emit(r.Context(), "agent.created", nil, store.User(user), map[string]any{"agent_id": out.ID})
	})
	agentFail(w, r, err, out, 201)
}

func (h *H) getAgent(w http.ResponseWriter, r *http.Request) {
	id, ok := cardID(w, r)
	if !ok {
		return
	}
	var out struct {
		db.Agent
		DefaultReportTemplate string `json:"default_report_template"`
	}
	err := h.inWS(r, func(tx *store.Tx, ws, user uuid.UUID) error {
		var err error
		out.Agent, err = tx.Q.GetAgent(r.Context(), db.GetAgentParams{WorkspaceID: ws, ID: id})
		if err == nil && !agentVisible(r.Context(), out.Agent, user) {
			return pgx.ErrNoRows
		}
		if err != nil {
			return err
		}
		role := "agent"
		builtIn, err := tx.Q.AgentIsBuiltIn(r.Context(), db.AgentIsBuiltInParams{WorkspaceID: ws, AgentID: id})
		if err != nil {
			return err
		}
		if builtIn && out.BuiltInKey == "" {
			links, err := tx.AgentTeams(r.Context(), id)
			if err != nil {
				return err
			}
			if len(links) > 0 {
				p, err := tx.Q.GetFieldPack(r.Context(), db.GetFieldPackParams{WorkspaceID: ws, ID: links[0].PackID})
				if err != nil {
					return err
				}
				var m pack.Manifest
				if err := json.Unmarshal(p.Manifest, &m); err != nil {
					return err
				}
				role = links[0].Member
				if role == m.Leader.Name {
					role = "leader"
				} else if role == m.Auditor.Name {
					role = "auditor"
				}
			}
		}
		if out.BuiltInKey == "" {
			out.DefaultReportTemplate = flow.DefaultReportTemplate(role, out.Stage)
		}
		return nil
	})
	agentFail(w, r, err, out, 200)
}

func (h *H) getAgentWork(w http.ResponseWriter, r *http.Request) {
	id, ok := cardID(w, r)
	if !ok {
		return
	}
	var work []store.AgentStudyWork
	err := h.inWS(r, func(tx *store.Tx, ws, user uuid.UUID) error {
		a, err := tx.Q.GetAgent(r.Context(), db.GetAgentParams{WorkspaceID: ws, ID: id})
		if err != nil {
			return err
		}
		if !agentVisible(r.Context(), a, user) {
			return pgx.ErrNoRows
		}
		work, err = tx.AgentStudyWork(r.Context(), id)
		return err
	})
	agentFail(w, r, err, work, 200)
}

func (h *H) updateAgent(w http.ResponseWriter, r *http.Request) {
	id, ok := cardID(w, r)
	if !ok {
		return
	}
	var in struct {
		Name                 *string         `json:"name"`
		Description          *string         `json:"description"`
		Instructions         *string         `json:"instructions"`
		ReportTemplate       *string         `json:"report_template"`
		Harness              *string         `json:"harness"`
		Model                *string         `json:"model"`
		Visibility           *string         `json:"visibility"`
		BuiltInKey           *string         `json:"built_in_key"`
		Stage                *string         `json:"stage"`
		AvatarURL            *string         `json:"avatar_url"`
		ConversationStarters json.RawMessage `json:"conversation_starters"`
	}
	if err := readJSON(r, &in); err != nil {
		writeErr(w, 400, "invalid agent: "+err.Error())
		return
	}
	if in.BuiltInKey != nil {
		writeErr(w, 400, "built-in identity is immutable")
		return
	}
	if in.Name == nil && in.Description == nil && in.Instructions == nil && in.ReportTemplate == nil && in.Harness == nil && in.Model == nil && in.Visibility == nil && in.AvatarURL == nil && in.Stage == nil && len(in.ConversationStarters) == 0 {
		writeErr(w, 400, "nothing to change")
		return
	}
	var out db.Agent
	err := h.inWS(r, func(tx *store.Tx, ws, user uuid.UUID) error {
		if _, err := tx.Q.LockWorkspaceExecution(r.Context(), ws); err != nil {
			return err
		}
		if err := tx.Q.LockWorkspaceEvents(r.Context(), ws); err != nil {
			return err
		}
		a, err := tx.Q.GetAgentForUpdate(r.Context(), db.GetAgentForUpdateParams{WorkspaceID: ws, ID: id})
		if err != nil {
			return err
		}
		if !agentVisible(r.Context(), a, user) {
			return pgx.ErrNoRows
		}
		if a.OwnerID != user && !agentAdmin(r) {
			return errAgentForbidden
		}
		if err := refuseLaboratory(a); err != nil {
			return err
		}
		if in.ReportTemplate != nil {
			builtIn, err := tx.Q.AgentIsBuiltIn(r.Context(), db.AgentIsBuiltInParams{WorkspaceID: ws, AgentID: id})
			if err != nil {
				return err
			}
			if builtIn {
				return cardInputError("built-in agents cannot have a report template")
			}
			a.ReportTemplate = *in.ReportTemplate
		}
		if a.ArchivedAt != nil {
			return errAgentArchived
		}
		if err := agentInUse(r, tx, id); err != nil {
			return err
		}
		if in.Harness != nil || in.Model != nil {
			live, err := tx.Q.TeamAgentHasLiveRuns(r.Context(), db.TeamAgentHasLiveRunsParams{WorkspaceID: ws, AgentID: &id})
			if err != nil {
				return err
			}
			if live {
				return conflict("This agent has a queued or running task. Change its CLI after the task finishes.")
			}
		}
		if a.BuiltInKey != "" && in.Visibility != nil && *in.Visibility != "private" {
			return cardInputError("built-in agents remain private")
		}
		if len(in.ConversationStarters) > 0 {
			a.ConversationStarters = in.ConversationStarters
		}
		if in.Name != nil {
			a.Name = strings.TrimSpace(*in.Name)
		}
		if in.Description != nil {
			a.Description = *in.Description
		}
		if in.Instructions != nil {
			a.Instructions = *in.Instructions
		}
		if in.Harness != nil {
			a.Harness = *in.Harness
		}
		if in.Model != nil {
			a.Model = *in.Model
		}
		if in.Visibility != nil {
			a.Visibility = *in.Visibility
		}
		if in.AvatarURL != nil {
			if !validEmojiAvatar(*in.AvatarURL) {
				return cardInputError("invalid emoji avatar")
			}
			a.AvatarUrl = *in.AvatarURL
		}
		if in.Stage != nil {
			if a.BuiltInKey == "aristotle" {
				return cardInputError("Aristotle has no stage")
			}
			if board.StageIndex(*in.Stage) < 0 {
				return cardInputError("choose a stage for this agent")
			}
			a.Stage = *in.Stage
		}
		if err := validateAgent(a); err != nil {
			return err
		}
		out, err = tx.Q.UpdateAgent(r.Context(), db.UpdateAgentParams{WorkspaceID: ws, ID: id, Name: a.Name, Description: a.Description, Instructions: a.Instructions, ReportTemplate: a.ReportTemplate, Harness: a.Harness, Model: a.Model, Visibility: a.Visibility, ConversationStarters: a.ConversationStarters, AvatarUrl: a.AvatarUrl, Stage: a.Stage})
		if err != nil {
			return err
		}
		return tx.Emit(r.Context(), "agent.updated", nil, store.User(user), map[string]any{"agent_id": id})
	})
	agentFail(w, r, err, out, 200)
}

func (h *H) archiveAgent(w http.ResponseWriter, r *http.Request) {
	id, ok := cardID(w, r)
	if !ok {
		return
	}
	var out db.Agent
	err := h.inWS(r, func(tx *store.Tx, ws, user uuid.UUID) error {
		if _, err := tx.Q.LockWorkspaceExecution(r.Context(), ws); err != nil {
			return err
		}
		if err := tx.Q.LockWorkspaceEvents(r.Context(), ws); err != nil {
			return err
		}
		a, err := tx.Q.GetAgentForUpdate(r.Context(), db.GetAgentForUpdateParams{WorkspaceID: ws, ID: id})
		if err != nil {
			return err
		}
		if !agentVisible(r.Context(), a, user) {
			return pgx.ErrNoRows
		}
		if a.OwnerID != user && !agentAdmin(r) {
			return errAgentForbidden
		}
		if err := refuseLaboratory(a); err != nil {
			return err
		}
		if a.BuiltInKey != "" {
			return errAgentBuiltinArchive
		}
		if a.ArchivedAt != nil {
			return errAgentArchived
		}
		if err := agentInUse(r, tx, id); err != nil {
			return err
		}
		out, err = tx.Q.ArchiveAgent(r.Context(), db.ArchiveAgentParams{WorkspaceID: ws, ID: id})
		if err != nil {
			return err
		}
		return tx.Emit(r.Context(), "agent.archived", nil, store.User(user), map[string]any{"agent_id": id})
	})
	agentFail(w, r, err, map[string]any{"agent": out, "cancelled": 0}, 200)
}

func (h *H) deleteAgent(w http.ResponseWriter, r *http.Request) {
	id, ok := cardID(w, r)
	if !ok {
		return
	}
	err := h.inWS(r, func(tx *store.Tx, ws, user uuid.UUID) error {
		if _, err := tx.Q.LockWorkspaceExecution(r.Context(), ws); err != nil {
			return err
		}
		if err := tx.Q.LockWorkspaceEvents(r.Context(), ws); err != nil {
			return err
		}
		a, err := tx.Q.GetAgentForUpdate(r.Context(), db.GetAgentForUpdateParams{WorkspaceID: ws, ID: id})
		if err != nil {
			return err
		}
		if !agentVisible(r.Context(), a, user) {
			return pgx.ErrNoRows
		}
		if a.OwnerID != user && !agentAdmin(r) {
			return errAgentForbidden
		}
		if err := refuseLaboratory(a); err != nil {
			return err
		}
		if a.BuiltInKey != "" {
			return conflict("built-in agents cannot be deleted")
		}
		if err := agentInUse(r, tx, id); err != nil {
			return err
		}
		member, err := tx.Q.AgentIsTeamCopy(r.Context(), db.AgentIsTeamCopyParams{WorkspaceID: ws, AgentID: id})
		if err != nil {
			return err
		}
		if member {
			return conflict("agents belonging to a Team cannot be deleted")
		}
		live, err := tx.Q.AgentHasLiveRuns(r.Context(), db.AgentHasLiveRunsParams{WorkspaceID: ws, AgentID: &id})
		if err != nil {
			return err
		}
		if live {
			return conflict("agents with active runs cannot be deleted")
		}
		if err := tx.Q.DeleteAgent(r.Context(), db.DeleteAgentParams{WorkspaceID: ws, ID: id}); err != nil {
			return err
		}
		return tx.Emit(r.Context(), "agent.deleted", nil, store.User(user), map[string]any{"agent_id": id})
	})
	agentFail(w, r, err, map[string]any{"deleted": true}, 200)
}
