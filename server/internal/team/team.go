// Package team builds Teams from a form and adds a Team to a researcher's
// agents.
package team

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"maps"
	"slices"
	"strconv"
	"strings"
	"unicode/utf8"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"lykeion/server/internal/availability"
	"lykeion/server/internal/board"
	"lykeion/server/internal/pack"
	"lykeion/server/internal/store"
	db "lykeion/server/pkg/db/generated"
)

// ErrInvalid marks a refusal caused by the caller's input.
var ErrInvalid = errors.New("invalid team")

// ErrNoHarness: a member names a model per harness and the user has no runtime
// for any of them.
var ErrNoHarness = errors.New("Connect Claude Code or Codex first")

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
			policy, err := availability.Get(ctx, tx, "mcp", id.String())
			if err != nil {
				return pack.Member{}, err
			}
			if !policy.Enabled {
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

// NoBuiltInError: a column needs its free stage Team and the workspace has none.
type NoBuiltInError struct{ Stage string }

func (e NoBuiltInError) Error() string {
	return "No built-in Team for " + strings.ToUpper(e.Stage[:1]) + e.Stage[1:]
}

// ColumnTeams gives the Team of every column from..to: the chosen one where
// there is one, the stage's built-in Team elsewhere.
func ColumnTeams(ctx context.Context, tx *store.Tx, from, to string, chosen map[string]uuid.UUID) (map[string]uuid.UUID, error) {
	i, j := board.StageIndex(from), board.StageIndex(to)
	if i < 0 || j < i {
		return nil, fmt.Errorf("team: %q to %q is not a range of stages", from, to)
	}
	out := map[string]uuid.UUID{}
	for _, col := range board.Stages[i : j+1] {
		if id, ok := chosen[col]; ok {
			out[col] = id
			continue
		}
		p, err := tx.Q.BuiltInStageTeam(ctx, db.BuiltInStageTeamParams{WorkspaceID: tx.WS, Stage: col})
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, NoBuiltInError{col}
		}
		if err != nil {
			return nil, err
		}
		out[col] = p.ID
	}
	return out, nil
}

// ErrNotAdded: an uploaded Team the user has no agents for.
var ErrNotAdded = errors.New("add this Team to your workspace first")

// Ensure readies a Team for user: a shipped Team works with the workspace's
// shared laboratory agents, an uploaded one must already be added.
func Ensure(ctx context.Context, tx *store.Tx, user, packID uuid.UUID) error {
	p, err := tx.Q.GetFieldPack(ctx, db.GetFieldPackParams{WorkspaceID: tx.WS, ID: packID})
	if errors.Is(err, pgx.ErrNoRows) {
		return ErrNotAdded
	}
	if err != nil {
		return err
	}
	if p.DeletedAt != nil || (p.Source != "shipped" && p.Visibility != "workspace" && (p.UploadedBy == nil || *p.UploadedBy != user)) {
		return ErrNotAdded
	}
	if p.Source == "shipped" {
		// No user id matches uuid.Nil, so only the shared rows come back.
		shared, err := tx.Q.TeamAgentsFor(ctx, db.TeamAgentsForParams{WorkspaceID: tx.WS, PackID: packID, UserID: uuid.Nil})
		if err != nil || len(shared) > 0 {
			return err
		}
		return InstallLaboratory(ctx, tx)
	}
	have, err := tx.Q.TeamAgentsFor(ctx, db.TeamAgentsForParams{WorkspaceID: tx.WS, PackID: packID, UserID: user})
	if err == nil && len(have) == 0 {
		err = ErrNotAdded
	}
	return err
}

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
	owned, err := tx.Q.OwnerHarnesses(ctx, db.OwnerHarnessesParams{WorkspaceID: tx.WS, OwnerID: user})
	if err != nil {
		return nil, err
	}
	for _, mem := range slices.Concat([]pack.Member{m.Leader, m.Auditor}, m.Members) {
		harness, model := mem.Agent.Harness, mem.Agent.Model
		if len(mem.Agent.Models) > 0 {
			// owned is sorted, so the first match is the same on every run.
			i := slices.IndexFunc(owned, func(h string) bool { _, ok := mem.Agent.Models[h]; return ok })
			if i < 0 {
				return nil, ErrNoHarness
			}
			harness = owned[i]
			model = mem.Agent.Models[harness]
		}
		name, err := freeName(ctx, tx, mem.Name+" · "+m.Name)
		if err != nil {
			return nil, err
		}
		a, err := tx.Q.CreateAgent(ctx, db.CreateAgentParams{WorkspaceID: tx.WS, Name: name, Description: mem.Role,
			Instructions: mem.Agent.Instructions, Harness: harness, Model: model,
			Visibility: "private", OwnerID: user})
		if err != nil {
			return nil, err
		}
		if err := tx.Q.InsertTeamAgent(ctx, db.InsertTeamAgentParams{WorkspaceID: tx.WS, PackID: p.ID, UserID: &user,
			Member: mem.Name, AgentID: a.ID}); err != nil {
			return nil, err
		}
	}
	if err := tx.Emit(ctx, "team.added", nil, store.User(user), map[string]any{"pack_id": p.ID}); err != nil {
		return nil, err
	}
	return tx.Q.TeamAgentsFor(ctx, db.TeamAgentsForParams{WorkspaceID: tx.WS, PackID: p.ID, UserID: user})
}

// ErrLaboratoryReadOnly: laboratory agents change only with the Teams Lykeion
// ships.
var ErrLaboratoryReadOnly = errors.New("laboratory agents can't be changed")

// InstallLaboratory gives the transaction's workspace one shared agent, owned
// by its oldest owner, for each member of every built-in stage Team. A
// person's private copy of a member is turned into it: the oldest live copy is
// kept and reset to the shipped manifest, and every other copy's runs,
// studies and history move onto it before the copy is retired. Members that
// already have their shared agent are left as they are.
func InstallLaboratory(ctx context.Context, tx *store.Tx) error {
	// Serialises with other installs and creators of live names.
	if err := tx.Q.LockWorkspaceEvents(ctx, tx.WS); err != nil {
		return err
	}
	var refs []store.AgentRef
	var owner uuid.UUID
	installed, reset := false, []string{}
	for _, stage := range board.Stages {
		p, err := tx.Q.BuiltInStageTeam(ctx, db.BuiltInStageTeamParams{WorkspaceID: tx.WS, Stage: stage})
		if errors.Is(err, pgx.ErrNoRows) {
			continue
		}
		if err != nil {
			return err
		}
		var m pack.Manifest
		if err := json.Unmarshal(p.Manifest, &m); err != nil {
			return err
		}
		shared, err := tx.Q.TeamAgentsFor(ctx, db.TeamAgentsForParams{WorkspaceID: tx.WS, PackID: p.ID, UserID: uuid.Nil})
		if err != nil {
			return err
		}
		for _, mem := range slices.Concat([]pack.Member{m.Leader, m.Auditor}, m.Members) {
			if slices.ContainsFunc(shared, func(r db.TeamAgent) bool { return r.Member == mem.Name }) {
				continue
			}
			if refs == nil {
				if refs, err = tx.AgentRefs(ctx); err != nil {
					return err
				}
				if owner, err = tx.Q.OldestOwner(ctx, tx.WS); err != nil {
					return fmt.Errorf("team: laboratory owner: %w", err)
				}
			}
			customised, err := installMember(ctx, tx, refs, owner, p, m, mem)
			if err != nil {
				return err
			}
			if customised {
				reset = append(reset, mem.Name+" · "+m.Name)
			}
			installed = true
		}
	}
	if !installed {
		return nil
	}
	return tx.Emit(ctx, "laboratory.installed", nil, store.System, map[string]any{"reset": reset})
}

// installMember makes the shared agent for one Team member and reports whether
// the copy it kept had customisations the reset dropped.
func installMember(ctx context.Context, tx *store.Tx, refs []store.AgentRef, owner uuid.UUID, p db.FieldPack, m pack.Manifest, mem pack.Member) (bool, error) {
	harness, model := mem.Agent.Harness, mem.Agent.Model
	if len(mem.Agent.Models) > 0 {
		harness = slices.Sorted(maps.Keys(mem.Agent.Models))[0]
		model = mem.Agent.Models[harness]
	}
	base := mem.Name + " · " + m.Name
	copies, err := tx.Q.TeamMemberCopies(ctx, db.TeamMemberCopiesParams{WorkspaceID: tx.WS, PackID: p.ID, Member: mem.Name})
	if err != nil {
		return false, err
	}
	var keep db.Agent
	if i := slices.IndexFunc(copies, func(a db.Agent) bool { return a.DeletedAt == nil && a.TeamDeletedAt == nil }); i >= 0 {
		keep = copies[i]
	} else {
		name, err := freeName(ctx, tx, base)
		if err != nil {
			return false, err
		}
		if keep, err = tx.Q.CreateAgent(ctx, db.CreateAgentParams{WorkspaceID: tx.WS, Name: name, Description: mem.Role,
			Instructions: mem.Agent.Instructions, Harness: harness, Model: model, Visibility: "workspace", OwnerID: owner}); err != nil {
			return false, err
		}
	}
	var drop []uuid.UUID
	for _, a := range copies {
		if a.ID != keep.ID {
			drop = append(drop, a.ID)
		}
	}
	customised := keep.Instructions != mem.Agent.Instructions || keep.Model != model || keep.AvatarUrl != "" ||
		len(keep.ConversationStarters) > 0 && string(keep.ConversationStarters) != "[]"
	for _, ref := range refs {
		if len(drop) > 0 {
			if _, err := tx.MoveAgentRefs(ctx, ref, drop, keep.ID); err != nil {
				return false, err
			}
		}
		// The kept agent's own rows are its private Team membership and any
		// customisation of its skills and connectors.
		if ref.Owned {
			n, err := tx.MoveAgentRefs(ctx, ref, []uuid.UUID{keep.ID}, keep.ID)
			if err != nil {
				return false, err
			}
			customised = customised || n > 0 && ref.Table != "team_agent"
		}
	}
	for _, id := range drop {
		if err := tx.Q.DeleteAgent(ctx, db.DeleteAgentParams{WorkspaceID: tx.WS, ID: id}); err != nil {
			return false, err
		}
	}
	// Retiring the other copies frees the plain name for the kept one.
	name := keep.Name
	if !strings.EqualFold(name, base) {
		if name, err = freeName(ctx, tx, base); err != nil {
			return false, err
		}
	}
	if _, err := tx.Q.MakeLaboratoryAgent(ctx, db.MakeLaboratoryAgentParams{WorkspaceID: tx.WS, ID: keep.ID, Name: name,
		Description: mem.Role, Instructions: mem.Agent.Instructions, Harness: harness, Model: model, OwnerID: owner, Stage: p.Stage}); err != nil {
		return false, err
	}
	return customised, tx.Q.InsertTeamAgent(ctx, db.InsertTeamAgentParams{WorkspaceID: tx.WS, PackID: p.ID, Member: mem.Name, AgentID: keep.ID})
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
