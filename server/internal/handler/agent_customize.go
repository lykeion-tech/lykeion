package handler

import (
	"encoding/json"
	"errors"
	"net/http"
	"slices"
	"strings"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"lykeion/server/internal/connectors"
	"lykeion/server/internal/middleware"
	"lykeion/server/internal/pack"
	"lykeion/server/internal/store"
	db "lykeion/server/pkg/db/generated"
)

type agentTeamCapability struct {
	ID         uuid.UUID        `json:"id"`
	Member     string           `json:"member"`
	Source     string           `json:"source"`
	UploadedBy *uuid.UUID       `json:"uploaded_by"`
	TeamName   string           `json:"team_name"`
	Skills     []string         `json:"skills"`
	Connectors []pack.Connector `json:"connectors"`
}

func (h *H) getAgentCapabilities(w http.ResponseWriter, r *http.Request) {
	id, ok := cardID(w, r)
	if !ok {
		return
	}
	out := struct {
		Skills     []string              `json:"skills"`
		Connectors []pack.Connector      `json:"connectors"`
		Teams      []agentTeamCapability `json:"teams"`
		TeamMember bool                  `json:"team_member"`
	}{Skills: []string{}, Connectors: []pack.Connector{}, Teams: []agentTeamCapability{}}
	err := h.inWS(r, func(tx *store.Tx, ws, user uuid.UUID) error {
		a, err := tx.Q.GetAgent(r.Context(), db.GetAgentParams{WorkspaceID: ws, ID: id})
		if err != nil {
			return err
		}
		if !agentVisible(r.Context(), a, user) {
			return pgx.ErrNoRows
		}
		links, err := tx.AgentTeams(r.Context(), id)
		if err != nil {
			return err
		}
		out.TeamMember = len(links) > 0
		for _, link := range links {
			p, err := visiblePack(r, tx, user, link.PackID)
			if errors.Is(err, pgx.ErrNoRows) {
				continue
			}
			if err != nil {
				return err
			}
			var manifest pack.Manifest
			if err := json.Unmarshal(p.Manifest, &manifest); err != nil {
				return err
			}
			var member *pack.Member
			for _, candidate := range append([]*pack.Member{&manifest.Leader, &manifest.Auditor}, memberPointers(manifest.Members)...) {
				if candidate.Name == link.Member {
					member = candidate
					break
				}
			}
			if member == nil {
				continue
			}
			refs := make([]pack.Connector, 0, len(member.Connectors))
			for _, c := range member.Connectors {
				name := c.Name
				if name == "" {
					name = c.Catalogue
				}
				refs = append(refs, pack.Connector{Name: name, Catalogue: c.Catalogue, Workspace: c.Workspace})
			}
			out.Teams = append(out.Teams, agentTeamCapability{p.ID, link.Member, p.Source, p.UploadedBy, manifest.Name, member.Skills, refs})
		}
		c, err := tx.AgentCapabilities(r.Context(), id)
		if errors.Is(err, pgx.ErrNoRows) {
			if !out.TeamMember {
				for _, entry := range connectors.Catalogue {
					out.Connectors = append(out.Connectors, pack.Connector{Name: entry.Name, Catalogue: entry.Name})
				}
				rows, err := tx.Q.ListWorkspaceMCPServers(r.Context(), ws)
				if err != nil {
					return err
				}
				for _, row := range rows {
					out.Connectors = append(out.Connectors, pack.Connector{Name: row.Name, Workspace: row.ID.String()})
				}
			}
			return nil
		}
		if err != nil {
			return err
		}
		if err := json.Unmarshal(c.Skills, &out.Skills); err != nil {
			return err
		}
		return json.Unmarshal(c.Connectors, &out.Connectors)
	})
	agentFail(w, r, err, out, 200)
}

func (h *H) saveAgentCapabilities(w http.ResponseWriter, r *http.Request) {
	id, ok := cardID(w, r)
	if !ok {
		return
	}
	ws, err := uuid.Parse(middleware.FromHeader(r))
	if err != nil {
		writeErr(w, 400, "X-Workspace-ID required")
		return
	}
	who := user(r)
	err = h.inWS(r, func(tx *store.Tx, ws, user uuid.UUID) error {
		a, err := tx.Q.GetAgent(r.Context(), db.GetAgentParams{WorkspaceID: ws, ID: id})
		if err != nil {
			return err
		}
		if !agentVisible(r.Context(), a, user) {
			return pgx.ErrNoRows
		}
		if a.OwnerID != user && !agentAdmin(r) {
			return errAgentForbidden
		}
		return refuseLaboratory(a)
	})
	if err != nil {
		agentFail(w, r, err, nil, 0)
		return
	}
	var in struct {
		Skills     []string         `json:"skills"`
		Connectors []pack.Connector `json:"connectors"`
	}
	if err := readJSON(r, &in); err != nil || in.Skills == nil || in.Connectors == nil {
		writeErr(w, 400, "skills and connectors are required")
		return
	}
	for i, name := range in.Skills {
		if name == "" || slices.Contains(in.Skills[:i], name) {
			writeErr(w, 400, "invalid skills")
			return
		}
	}
	names := map[string]bool{}
	for _, c := range in.Connectors {
		name := strings.ToLower(strings.TrimSpace(c.Name))
		if name == "" || name == "sources" || pack.ReservedServer(name) || names[name] || (c.Catalogue == "" && c.Workspace == "") || c.URL != "" || len(c.Command) != 0 || len(c.Env) != 0 || pack.ValidateConnector(c) != nil {
			writeErr(w, 400, "invalid connector")
			return
		}
		names[name] = true
	}
	selection, err := h.skillService().SelectForTeam(r.Context(), ws, who, in.Skills)
	if errors.Is(err, pgx.ErrNoRows) {
		err = cardInputError("a selected skill is unavailable")
	}
	if !answer(w, r, err) {
		return
	}
	err = h.inWS(r, func(tx *store.Tx, ws, user uuid.UUID) error {
		if _, err := tx.Q.LockWorkspaceExecution(r.Context(), ws); err != nil {
			return err
		}
		if err := selection.Fence(r.Context(), tx, user); err != nil {
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
		if a.ArchivedAt != nil {
			return errAgentArchived
		}
		if err := agentInUse(r, tx, id); err != nil {
			return err
		}
		live, err := tx.AgentHasLiveStudyRuns(r.Context(), id)
		if err != nil {
			return err
		}
		if live {
			return conflict("This agent has a run in progress. Try again once it finishes.")
		}
		links, err := tx.AgentTeams(r.Context(), id)
		if err != nil {
			return err
		}
		if len(links) != 0 {
			return conflict("Edit this agent's Team instead")
		}
		for _, c := range in.Connectors {
			if c.Workspace != "" {
				connector, err := uuid.Parse(c.Workspace)
				if err != nil {
					return cardInputError("invalid connector")
				}
				if _, err = tx.Q.GetWorkspaceMCPServer(r.Context(), db.GetWorkspaceMCPServerParams{WorkspaceID: ws, ID: connector}); err != nil {
					return err
				}
			}
		}
		skills, _ := json.Marshal(in.Skills)
		conns, _ := json.Marshal(in.Connectors)
		if err := tx.SetAgentCapabilities(r.Context(), id, store.AgentCapabilities{Skills: skills, Connectors: conns}); err != nil {
			return err
		}
		return tx.Emit(r.Context(), "agent.updated", nil, store.User(user), map[string]any{"agent_id": id})
	})
	agentFail(w, r, err, map[string]any{"id": id}, 200)
}
