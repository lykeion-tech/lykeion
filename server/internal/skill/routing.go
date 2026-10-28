package skill

import (
	"context"
	"errors"
	"slices"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"lykeion/server/internal/availability"
	"lykeion/server/internal/middleware"
	"lykeion/server/internal/science"
	"lykeion/server/internal/store"
	"lykeion/server/internal/toolroute"
	db "lykeion/server/pkg/db/generated"
)

const DomainWorkspace = "Workspace"

var ErrRoutingChanged = errors.New("skill routing changed; request tools again")

type routingKey struct{}
type authorityKey struct{}
type runAuthority struct {
	role  middleware.Role
	token string
}
type routingSnapshot struct {
	workspace uuid.UUID
	scope     Scope
	authority runAuthority
	versions  map[string]db.WorkspaceSkillVersion
	bundled   map[string]bool
}

// WithRunAuthority carries the authenticated request's authority only for the
// lifetime of the routing call. Every transaction rechecks its live token.
func WithRunAuthority(ctx context.Context, role middleware.Role, token string) context.Context {
	return context.WithValue(ctx, authorityKey{}, runAuthority{role, token})
}
func lockScope(ctx context.Context, tx *store.Tx, ws uuid.UUID, scope Scope, a runAuthority) error {
	if ws == uuid.Nil || scope.ID == uuid.Nil || a.role.WorkspaceID != ws || a.role.AgentRunID == uuid.Nil || a.token == "" {
		return middleware.ErrRoleUnauthorized
	}
	if err := middleware.LockRole(ctx, tx, a.role, a.token); err != nil {
		return err
	}
	switch scope.Kind {
	case "card":
		if scope.ID != a.role.CardID {
			return middleware.ErrRoleUnauthorized
		}
	case "autoresearch_run":
		if a.role.CardID != uuid.Nil {
			return middleware.ErrRoleUnauthorized
		}
		r, err := tx.Q.GetRun(ctx, db.GetRunParams{WorkspaceID: ws, ID: scope.ID})
		if err != nil {
			return err
		}
		if r.AgentRunID == nil || *r.AgentRunID != a.role.AgentRunID {
			return middleware.ErrRoleUnauthorized
		}
	default:
		return middleware.ErrRoleUnauthorized
	}
	return nil
}
func activeVersions(ctx context.Context, tx *store.Tx) ([]db.WorkspaceSkillVersion, error) {
	rows, err := tx.Q.ListWorkspaceSkills(ctx, tx.WS)
	if err != nil {
		return nil, err
	}
	out := make([]db.WorkspaceSkillVersion, 0, len(rows))
	for _, r := range rows {
		v, err := tx.Q.GetWorkspaceSkillVersion(ctx, db.GetWorkspaceSkillVersionParams{WorkspaceID: tx.WS, SkillID: r.ID, Revision: r.Revision})
		if err != nil {
			return nil, err
		}
		out = append(out, v)
	}
	return out, nil
}
func (s *Service) loadVersion(ctx context.Context, v db.WorkspaceSkillVersion) (Bundle, error) {
	return s.load(ctx, db.GetWorkspaceSkillRow{WorkspaceID: v.WorkspaceID, ID: v.SkillID, Name: v.Name, Revision: v.Revision, Description: v.Description, Domain: v.Domain, Sha256: v.Sha256, Manifest: v.Manifest, BlobKey: v.BlobKey})
}

// CandidatesForScope snapshots the exact versions offered to Jev. The returned
// context must be used when resolving Jev's selected names; it cannot be reused
// for another run or scope. Existing pins hide newer versions and reused names.
func (s *Service) CandidatesForScope(ctx context.Context, ws uuid.UUID, scope Scope) (context.Context, []toolroute.Skill, error) {
	a, _ := ctx.Value(authorityKey{}).(runAuthority)
	snap := routingSnapshot{workspace: ws, scope: scope, authority: a, versions: map[string]db.WorkspaceSkillVersion{}, bundled: map[string]bool{}}
	err := s.Store.InWorkspace(ctx, ws, a.role.AgentRunID, func(tx *store.Tx) error {
		if err := lockScope(ctx, tx, ws, scope, a); err != nil {
			return err
		}
		pins, err := tx.Q.ListWorkspaceSkillPins(ctx, db.ListWorkspaceSkillPinsParams{WorkspaceID: ws, ScopeKind: scope.Kind, ScopeID: scope.ID})
		if err != nil {
			return err
		}
		ids := map[uuid.UUID]bool{}
		pinnedNames := map[string]bool{}
		for _, v := range pins {
			p, err := availability.Get(ctx, tx, "skill", v.SkillID.String())
			if err != nil {
				return err
			}
			ids[v.SkillID] = true
			pinnedNames[v.Name] = true
			if p.Enabled {
				snap.versions[v.Name] = v
			}
		}
		active, err := activeVersions(ctx, tx)
		if err != nil {
			return err
		}
		for _, v := range active {
			if !pinnedNames[v.Name] && !ids[v.SkillID] {
				p, err := availability.Get(ctx, tx, "skill", v.SkillID.String())
				if err != nil {
					return err
				}
				if !p.Enabled {
					continue
				}
				snap.versions[v.Name] = v
			}
		}
		for _, b := range science.Skills() {
			p, err := availability.Get(ctx, tx, "skill", "bundle:"+b.Name)
			if err != nil {
				return err
			}
			snap.bundled[b.Name] = p.Enabled
		}
		return nil
	})
	if err != nil {
		return ctx, nil, err
	}
	out := []toolroute.Skill{}
	for _, b := range science.Skills() {
		if !snap.bundled[b.Name] {
			continue
		}
		out = append(out, toolroute.Skill{Name: b.Name, Description: b.Description, Domain: b.Domain})
	}
	names := make([]string, 0, len(snap.versions))
	for name := range snap.versions {
		names = append(names, name)
	}
	slices.Sort(names)
	for _, name := range names {
		v := snap.versions[name]
		out = append(out, toolroute.Skill{Name: v.Name, Description: v.Description, Domain: DomainWorkspace, LoadFiles: func(ctx context.Context) (map[string][]byte, error) {
			b, err := s.loadVersion(ctx, v)
			return b.Files, err
		}})
	}
	return context.WithValue(ctx, routingKey{}, snap), out, nil
}

// ResolveForScope loads selected immutable folders, then fences the candidate
// snapshot and live authority before publishing pins in a single transaction.
func (s *Service) ResolveForScope(ctx context.Context, ws uuid.UUID, scope Scope, names []string) ([]toolroute.Skill, error) {
	snap, ok := ctx.Value(routingKey{}).(routingSnapshot)
	a, _ := ctx.Value(authorityKey{}).(runAuthority)
	if !ok || snap.workspace != ws || snap.scope != scope || snap.authority.role.AgentRunID != a.role.AgentRunID || snap.authority.token != a.token {
		return nil, middleware.ErrRoleUnauthorized
	}
	versions := []db.WorkspaceSkillVersion{}
	out := []toolroute.Skill{}
	seen := map[string]bool{}
	for _, name := range names {
		if seen[name] {
			continue
		}
		seen[name] = true
		if v, ok := snap.versions[name]; ok {
			b, err := s.loadVersion(ctx, v)
			if err != nil {
				return nil, err
			}
			versions = append(versions, v)
			out = append(out, toolroute.Skill{Name: v.Name, Description: v.Description, Domain: DomainWorkspace, Files: b.Files})
			continue
		}
		found := false
		for _, b := range science.Skills() {
			if b.Name == name && snap.bundled[name] {
				out = append(out, toolroute.Skill{Name: b.Name, Description: b.Description, Domain: b.Domain})
				found = true
				break
			}
		}
		if !found {
			return nil, ErrRoutingChanged
		}
	}
	err := s.Store.InWorkspace(ctx, ws, a.role.AgentRunID, func(tx *store.Tx) error {
		if err := lockScope(ctx, tx, ws, scope, a); err != nil {
			return err
		}
		if err := tx.Q.LockWorkspaceEvents(ctx, ws); err != nil {
			return err
		}
		pins, err := tx.Q.ListWorkspaceSkillPins(ctx, db.ListWorkspaceSkillPinsParams{WorkspaceID: ws, ScopeKind: scope.Kind, ScopeID: scope.ID})
		if err != nil {
			return err
		}
		for _, name := range names {
			if snap.bundled[name] {
				p, err := availability.Get(ctx, tx, "skill", "bundle:"+name)
				if err != nil {
					return err
				}
				if !p.Enabled {
					return ErrRoutingChanged
				}
			}
		}
		for _, v := range versions {
			p, err := availability.Get(ctx, tx, "skill", v.SkillID.String())
			if err != nil {
				return err
			}
			if !p.Enabled {
				return ErrRoutingChanged
			}
			pinned := false
			for _, p := range pins {
				if p.SkillID == v.SkillID || p.Name == v.Name {
					if p.SkillID != v.SkillID || p.Revision != v.Revision {
						return ErrRoutingChanged
					}
					pinned = true
				}
			}
			if pinned {
				continue
			}
			current, err := tx.Q.GetWorkspaceSkill(ctx, db.GetWorkspaceSkillParams{WorkspaceID: ws, ID: v.SkillID})
			if errors.Is(err, pgx.ErrNoRows) {
				return ErrRoutingChanged
			}
			if err != nil {
				return err
			}
			if current.Revision != v.Revision || current.Name != v.Name {
				return ErrRoutingChanged
			}
			if err := tx.Q.PinWorkspaceSkill(ctx, db.PinWorkspaceSkillParams{WorkspaceID: ws, ScopeKind: scope.Kind, ScopeID: scope.ID, SkillID: v.SkillID, Revision: v.Revision}); err != nil {
				return err
			}
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	return out, nil
}

// TeamSelection is a frozen workspace-library selection prepared before any
// Team write transaction. Its folders are embedded into the new pack.
type TeamSelection struct {
	workspace, user uuid.UUID
	versions        []db.WorkspaceSkillVersion
	policyIDs       []string
	Files           map[string]map[string][]byte
}

func (s *Service) SelectForTeam(ctx context.Context, ws, user uuid.UUID, names []string) (TeamSelection, error) {
	sel := TeamSelection{workspace: ws, user: user, Files: map[string]map[string][]byte{}}
	err := s.Store.InWorkspace(ctx, ws, user, func(tx *store.Tx) error {
		if _, err := member(ctx, tx, user, false); err != nil {
			return err
		}
		active, err := activeVersions(ctx, tx)
		if err != nil {
			return err
		}
		for _, name := range names {
			if science.Has(name) {
				p, err := availability.Get(ctx, tx, "skill", "bundle:"+name)
				if err != nil {
					return err
				}
				if !p.Enabled {
					return ErrRoutingChanged
				}
				sel.policyIDs = append(sel.policyIDs, "bundle:"+name)
				continue
			}
			at := slices.IndexFunc(active, func(v db.WorkspaceSkillVersion) bool { return v.Name == name })
			if at < 0 {
				return pgx.ErrNoRows
			}
			p, err := availability.Get(ctx, tx, "skill", active[at].SkillID.String())
			if err != nil {
				return err
			}
			if !p.Enabled {
				return ErrRoutingChanged
			}
			sel.policyIDs = append(sel.policyIDs, active[at].SkillID.String())
			sel.versions = append(sel.versions, active[at])
		}
		return nil
	})
	if err != nil {
		return sel, err
	}
	for _, v := range sel.versions {
		b, err := s.loadVersion(ctx, v)
		if err != nil {
			return sel, err
		}
		sel.Files[v.Name] = b.Files
	}
	return sel, nil
}

// Fence requires the caller's workspace lock before checking current membership
// and selected versions in the transaction that publishes the Team.
func (s TeamSelection) Fence(ctx context.Context, tx *store.Tx, user uuid.UUID) error {
	if tx.WS != s.workspace || user != s.user {
		return ErrForbidden
	}
	if _, err := member(ctx, tx, user, false); err != nil {
		return err
	}
	for _, id := range s.policyIDs {
		p, err := availability.Get(ctx, tx, "skill", id)
		if err != nil {
			return err
		}
		if !p.Enabled {
			return ErrRoutingChanged
		}
	}
	for _, v := range s.versions {
		current, err := tx.Q.GetWorkspaceSkill(ctx, db.GetWorkspaceSkillParams{WorkspaceID: tx.WS, ID: v.SkillID})
		if errors.Is(err, pgx.ErrNoRows) {
			return ErrRoutingChanged
		}
		if err != nil {
			return err
		}
		if current.Revision != v.Revision || current.Name != v.Name {
			return ErrRoutingChanged
		}
	}
	return nil
}

// EnabledFolders snapshots current workspace availability for a chat turn.
// Immutable folder blobs are loaded only after the metadata transaction closes.
func (s *Service) EnabledFolders(ctx context.Context, ws, user uuid.UUID) ([]toolroute.RoutedSkill, error) {
	var versions []db.WorkspaceSkillVersion
	candidates := toolroute.Candidates{}
	err := s.Store.InWorkspace(ctx, ws, user, func(tx *store.Tx) error {
		if _, err := member(ctx, tx, user, false); err != nil {
			return err
		}
		for _, b := range science.Skills() {
			p, err := availability.Get(ctx, tx, "skill", "bundle:"+b.Name)
			if err != nil {
				return err
			}
			if p.Enabled {
				candidates.Skills = append(candidates.Skills, toolroute.Skill{Name: b.Name})
			}
		}
		active, err := activeVersions(ctx, tx)
		if err != nil {
			return err
		}
		for _, v := range active {
			p, err := availability.Get(ctx, tx, "skill", v.SkillID.String())
			if err != nil {
				return err
			}
			if p.Enabled {
				versions = append(versions, v)
			}
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	for _, v := range versions {
		b, err := s.loadVersion(ctx, v)
		if err != nil {
			return nil, err
		}
		candidates.Skills = append(candidates.Skills, toolroute.Skill{Name: v.Name, Files: b.Files})
	}
	result := toolroute.Result{}
	for _, s := range candidates.Skills {
		result.Skills = append(result.Skills, toolroute.RoutedSkill{Name: s.Name})
	}
	if err = candidates.AttachFolders(&result); err != nil {
		return nil, err
	}
	return result.Skills, nil
}
