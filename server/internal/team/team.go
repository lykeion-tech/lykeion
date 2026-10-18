// Package team builds Teams from a form and adds a Team to a researcher's
// agents.
package team

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"strconv"
	"unicode/utf8"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"lykeion/server/internal/pack"
	"lykeion/server/internal/store"
	db "lykeion/server/pkg/db/generated"
)

// ErrInvalid marks a refusal caused by the caller's input.
var ErrInvalid = errors.New("invalid team")

const maxAgentName = 64

type Slot struct {
	Name       string           `json:"name"`
	Role       string           `json:"role"`
	AgentID    uuid.UUID        `json:"agent_id"`
	Skills     []string         `json:"skills"`
	Connectors []pack.Connector `json:"connectors"`
}

type Form struct {
	Name        string   `json:"name"`
	Description string   `json:"description"`
	Domain      string   `json:"domain"`
	Stage       string   `json:"stage"`
	Leader      Slot     `json:"leader"`
	Auditor     Slot     `json:"auditor"`
	Members     []Slot   `json:"members"`
	Criteria    []string `json:"criteria"`
	Steps       int      `json:"steps"`

	// Who is asking: an agent is a candidate when it is shared, the caller's
	// own, or the caller administers the workspace.
	User  uuid.UUID `json:"-"`
	Admin bool      `json:"-"`
}

// FromForm builds a version 1 manifest, snapshotting each slot's agent so a
// later edit to the agent leaves the Team as it was.
func FromForm(ctx context.Context, tx *store.Tx, in Form) (pack.Manifest, error) {
	slot := func(s Slot) (pack.Member, error) {
		a, err := tx.Q.GetAgent(ctx, db.GetAgentParams{WorkspaceID: tx.WS, ID: s.AgentID})
		if errors.Is(err, pgx.ErrNoRows) || err == nil && (a.ArchivedAt != nil ||
			a.Visibility != "workspace" && a.OwnerID != in.User && !in.Admin) {
			return pack.Member{}, fmt.Errorf("%w: agent for %q is not available", ErrInvalid, s.Name)
		}
		if err != nil {
			return pack.Member{}, err
		}
		for _, c := range s.Connectors {
			if c.Workspace == "" {
				continue
			}
			id, err := uuid.Parse(c.Workspace)
			if err != nil {
				return pack.Member{}, fmt.Errorf("%w: invalid workspace connector", ErrInvalid)
			}
			if _, err := tx.Q.GetWorkspaceMCPServer(ctx, db.GetWorkspaceMCPServerParams{WorkspaceID: tx.WS, ID: id}); err != nil {
				return pack.Member{}, fmt.Errorf("%w: workspace connector unavailable", ErrInvalid)
			}
		}
		return pack.Member{Name: s.Name, Role: s.Role, Skills: s.Skills, Connectors: s.Connectors,
			Agent: pack.AgentDef{Harness: a.Harness, Model: a.Model, Instructions: a.Instructions}}, nil
	}
	m := pack.Manifest{Name: in.Name, Version: "1", Description: in.Description, Domain: in.Domain,
		Stage: in.Stage, Criteria: in.Criteria, Steps: in.Steps}
	names := map[string]bool{}
	for _, s := range append([]Slot{in.Leader, in.Auditor}, in.Members...) {
		if names[s.Name] {
			return m, fmt.Errorf("%w: the name %q is used by more than one slot", ErrInvalid, s.Name)
		}
		names[s.Name] = true
	}
	var err error
	if m.Leader, err = slot(in.Leader); err != nil {
		return m, err
	}
	if m.Auditor, err = slot(in.Auditor); err != nil {
		return m, err
	}
	for _, s := range in.Members {
		mem, err := slot(s)
		if err != nil {
			return m, err
		}
		m.Members = append(m.Members, mem)
	}
	return m, nil
}

// AuditorDiffers is the pack package's rule, so a form and an archive meet the
// same one.
func AuditorDiffers(m pack.Manifest) error { return pack.AuditorDiffers(m) }

// Add records user's approval of the Team version and gives them an agent for
// the leader, the auditor and each member: private, owned by them, named
// "<member> · <Team>". A repeat for the same user and version changes nothing.
func Add(ctx context.Context, tx *store.Tx, p db.FieldPack, m pack.Manifest, user uuid.UUID) ([]db.TeamAgent, error) {
	// Serialises with other creators of live names in the workspace.
	if err := tx.Q.LockWorkspaceEvents(ctx, tx.WS); err != nil {
		return nil, err
	}
	if _, err := tx.Q.ApproveFieldPack(ctx, db.ApproveFieldPackParams{WorkspaceID: tx.WS, PackID: p.ID, ApprovedBy: user}); err != nil {
		return nil, err
	}
	have, err := tx.Q.TeamAgentsFor(ctx, db.TeamAgentsForParams{WorkspaceID: tx.WS, PackID: p.ID, UserID: user})
	if err != nil || len(have) > 0 {
		return have, err
	}
	for _, mem := range slices.Concat([]pack.Member{m.Leader, m.Auditor}, m.Members) {
		name, err := freeName(ctx, tx, mem.Name+" · "+m.Name)
		if err != nil {
			return nil, err
		}
		a, err := tx.Q.CreateAgent(ctx, db.CreateAgentParams{WorkspaceID: tx.WS, Name: name, Description: mem.Role,
			Instructions: mem.Agent.Instructions, Harness: mem.Agent.Harness, Model: mem.Agent.Model,
			Visibility: "private", OwnerID: user})
		if err != nil {
			return nil, err
		}
		if err := tx.Q.InsertTeamAgent(ctx, db.InsertTeamAgentParams{WorkspaceID: tx.WS, PackID: p.ID, UserID: user,
			Member: mem.Name, AgentID: a.ID}); err != nil {
			return nil, err
		}
	}
	if err := tx.Emit(ctx, "team.added", nil, store.User(user), map[string]any{"pack_id": p.ID}); err != nil {
		return nil, err
	}
	return tx.Q.TeamAgentsFor(ctx, db.TeamAgentsForParams{WorkspaceID: tx.WS, PackID: p.ID, UserID: user})
}

// freeName is base, or base with " 2", " 3" … until no live agent holds it.
func freeName(ctx context.Context, tx *store.Tx, base string) (string, error) {
	if utf8.RuneCountInString(base) > maxAgentName-4 {
		return "", fmt.Errorf("%w: %q is too long for an agent name", ErrInvalid, base)
	}
	for i := 1; ; i++ {
		name := base
		if i > 1 {
			name += " " + strconv.Itoa(i)
		}
		taken, err := tx.Q.AgentLiveNameTaken(ctx, db.AgentLiveNameTakenParams{WorkspaceID: tx.WS, Name: name})
		if err != nil || !taken {
			return name, err
		}
	}
}

// SkillNames is the distinct library selection made by the Team's members.
func SkillNames(m pack.Manifest) []string {
	var out []string
	for _, mem := range append([]pack.Member{m.Leader, m.Auditor}, m.Members...) {
		for _, name := range mem.Skills {
			if !slices.Contains(out, name) {
				out = append(out, name)
			}
		}
	}
	return out
}

// SkillFolders embeds a selected import only for the members declaring it.
func SkillFolders(m pack.Manifest, files map[string]map[string][]byte) map[string]map[string][]byte {
	out := map[string]map[string][]byte{}
	for _, mem := range append([]pack.Member{m.Leader, m.Auditor}, m.Members...) {
		for _, name := range mem.Skills {
			if folder, ok := files[name]; ok {
				out[mem.Name+"/"+name] = folder
			}
		}
	}
	return out
}
