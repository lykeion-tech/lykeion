package handler

import (
	"encoding/json"
	"errors"
	"net/http"
	"strings"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"lykeion/server/internal/pack"
	"lykeion/server/internal/science"
	"lykeion/server/internal/store"
	db "lykeion/server/pkg/db/generated"
)

func (h *H) duplicateAgent(w http.ResponseWriter, r *http.Request) {
	id, ok := cardID(w, r)
	if !ok {
		return
	}
	var source db.Agent
	var sourceTeam *db.FieldPack
	var memberName string
	err := h.inWS(r, func(tx *store.Tx, ws, user uuid.UUID) error {
		var err error
		source, err = tx.Q.GetAgent(r.Context(), db.GetAgentParams{WorkspaceID: ws, ID: id})
		if err != nil {
			return err
		}
		if !agentVisible(r, source, user) {
			return pgx.ErrNoRows
		}
		links, err := tx.AgentTeams(r.Context(), id)
		if err != nil {
			return err
		}
		if len(links) > 1 {
			return conflict("This agent belongs to more than one Team and cannot be duplicated")
		}
		if len(links) == 1 {
			p, err := visiblePack(r, tx, user, links[0].PackID)
			if err != nil {
				return err
			}
			sourceTeam, memberName = &p, links[0].Member
		}
		return nil
	})
	if err != nil {
		agentFail(w, r, err, nil, 0)
		return
	}
	var input struct {
		Name string `json:"name"`
	}
	if err := readJSON(r, &input); err != nil {
		writeErr(w, 400, "invalid agent")
		return
	}
	input.Name = strings.TrimSpace(input.Name)
	copySource := source
	copySource.Name, copySource.Visibility = input.Name, "private"
	if err := validateAgent(copySource); err != nil {
		agentFail(w, r, err, nil, 0)
		return
	}
	var teamCaps *store.AgentCapabilities
	if sourceTeam != nil {
		p, err := pack.Load(r.Context(), h.Blobs, *sourceTeam)
		if !answer(w, r, err) {
			return
		}
		var member *pack.Member
		for _, item := range append([]*pack.Member{&p.Manifest.Leader, &p.Manifest.Auditor}, memberPointers(p.Manifest.Members)...) {
			if item.Name == memberName {
				member = item
				break
			}
		}
		if member == nil {
			writeErr(w, 409, "Team member no longer exists")
			return
		}
		skills := []string{}
		for _, name := range member.Skills {
			if _, embedded := p.Skills[member.Name+"/"+name]; !embedded && science.Has(name) {
				skills = append(skills, name)
			}
		}
		conns := []pack.Connector{}
		for _, c := range member.Connectors {
			if (c.Catalogue != "" || c.Workspace != "") && c.URL == "" && len(c.Command) == 0 && len(c.Env) == 0 {
				conns = append(conns, c)
			}
		}
		skillJSON, _ := json.Marshal(skills)
		connectorJSON, _ := json.Marshal(conns)
		teamCaps = &store.AgentCapabilities{Skills: skillJSON, Connectors: connectorJSON}
	}
	var out db.Agent
	err = h.inWS(r, func(tx *store.Tx, ws, user uuid.UUID) error {
		if _, err := tx.Q.LockWorkspaceExecution(r.Context(), ws); err != nil {
			return err
		}
		if err := tx.Q.LockWorkspaceEvents(r.Context(), ws); err != nil {
			return err
		}
		current, err := tx.Q.GetAgentForShare(r.Context(), db.GetAgentForShareParams{WorkspaceID: ws, ID: id})
		if err != nil {
			return err
		}
		if !agentVisible(r, current, user) {
			return pgx.ErrNoRows
		}
		if !current.UpdatedAt.Equal(source.UpdatedAt) {
			return conflict("The agent changed. Reload and try again.")
		}
		links, err := tx.AgentTeams(r.Context(), id)
		if err != nil {
			return err
		}
		if sourceTeam == nil && len(links) != 0 || sourceTeam != nil && (len(links) != 1 || links[0].PackID != sourceTeam.ID || links[0].Member != memberName) {
			return conflict("The agent's Team changed. Reload and try again.")
		}
		if sourceTeam != nil {
			p, err := visiblePack(r, tx, user, sourceTeam.ID)
			if err != nil {
				return err
			}
			if p.Sha256 != sourceTeam.Sha256 {
				return conflict("The Team changed. Reload and try again.")
			}
		}
		out, err = tx.Q.CreateAgent(r.Context(), db.CreateAgentParams{WorkspaceID: ws, OwnerID: user, Name: input.Name, Description: current.Description, Instructions: current.Instructions, Harness: current.Harness, Model: current.Model, Visibility: "private", Stage: current.Stage, ConversationStarters: current.ConversationStarters})
		if err != nil {
			return err
		}
		if current.AvatarUrl != "" {
			out, err = tx.Q.UpdateAgent(r.Context(), db.UpdateAgentParams{WorkspaceID: ws, ID: out.ID, Name: out.Name, Description: out.Description, Instructions: out.Instructions, Harness: out.Harness, Model: out.Model, Visibility: out.Visibility, ConversationStarters: out.ConversationStarters, AvatarUrl: current.AvatarUrl, Stage: out.Stage})
			if err != nil {
				return err
			}
		}
		caps := teamCaps
		if caps == nil {
			c, err := tx.AgentCapabilities(r.Context(), id)
			if err != nil && !errors.Is(err, pgx.ErrNoRows) {
				return err
			}
			if err == nil {
				caps = &c
			}
		}
		if caps != nil {
			if err := tx.SetAgentCapabilities(r.Context(), out.ID, *caps); err != nil {
				return err
			}
		}
		return tx.Emit(r.Context(), "agent.created", nil, store.User(user), map[string]any{"agent_id": out.ID})
	})
	agentFail(w, r, err, out, 201)
}
