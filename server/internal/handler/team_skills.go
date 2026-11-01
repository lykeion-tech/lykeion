package handler

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"reflect"
	"slices"
	"strings"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"lykeion/server/internal/board"
	"lykeion/server/internal/middleware"
	"lykeion/server/internal/pack"
	"lykeion/server/internal/skill"
	"lykeion/server/internal/store"
	"lykeion/server/internal/team"
	db "lykeion/server/pkg/db/generated"
)

const errBuiltInTeam = forbidden("Built-in Teams can't be changed; duplicate it instead")

// editable refuses changes to a built-in Team and to a Team that studies or
// Autoresearches still need as it is.
func editable(r *http.Request, tx *store.Tx, p db.FieldPack) error {
	if p.Source == "shipped" {
		return errBuiltInTeam
	}
	if p.UploadedBy == nil || *p.UploadedBy != user(r) {
		return forbidden("Only the Team owner can change it")
	}
	uses, err := tx.Q.TeamUses(r.Context(), db.TeamUsesParams{WorkspaceID: tx.WS, PackID: p.ID, Stages: board.Stages})
	if err != nil {
		return err
	}
	var by []string
	if uses.Studies > 0 {
		by = append(by, plural(uses.Studies, "study", "studies"))
	}
	if uses.Autoresearches > 0 {
		by = append(by, plural(uses.Autoresearches, "Autoresearch", "Autoresearches"))
	}
	if len(by) > 0 {
		return conflict("Used by " + strings.Join(by, " and ") + ". Swap it off the columns not yet reached, or wait for them to finish.")
	}
	return nil
}

func plural(n int32, one, many string) string {
	if n == 1 {
		return "1 " + one
	}
	return fmt.Sprintf("%d %s", n, many)
}

func (h *H) saveTeamSkills(w http.ResponseWriter, r *http.Request) {
	h.saveTeamCapabilities(w, r, false)
}

func (h *H) saveTeamConnectors(w http.ResponseWriter, r *http.Request) {
	h.saveTeamCapabilities(w, r, true)
}

func (h *H) saveTeamCapabilities(w http.ResponseWriter, r *http.Request, connectors bool) {
	ws, scopeErr := uuid.Parse(middleware.FromHeader(r))
	if scopeErr != nil {
		writeErr(w, 400, "X-Workspace-ID required")
		return
	}
	user := user(r)
	id, ok := cardID(w, r)
	if !ok {
		return
	}
	var in struct {
		Member     string           `json:"member"`
		Skills     []string         `json:"skills"`
		Connectors []pack.Connector `json:"connectors"`
	}
	var source db.FieldPack
	err := h.memberTx(r, ws, false, func(tx *store.Tx) (err error) {
		if source, err = visiblePack(r, tx, user, id); err != nil {
			return err
		}
		return editable(r, tx, source)
	})
	if !answer(w, r, err) {
		return
	}
	if err := readJSON(r, &in); err != nil || in.Member == "" || (!connectors && in.Skills == nil) || (connectors && in.Connectors == nil) {
		writeErr(w, 400, "member and capabilities are required")
		return
	}
	base, err := pack.Load(r.Context(), h.Blobs, source)
	if !answer(w, r, err) {
		return
	}
	var m pack.Manifest
	err = json.Unmarshal(source.Manifest, &m)
	if !answer(w, r, err) {
		return
	}
	var target *pack.Member
	for _, mem := range append([]*pack.Member{&m.Leader, &m.Auditor}, memberPointers(m.Members)...) {
		if mem.Name == in.Member {
			target = mem
			break
		}
	}
	if target == nil {
		writeErr(w, 400, "unknown Team member")
		return
	}
	if connectors {
		for i, c := range in.Connectors {
			if c.URL == "" && len(c.Command) == 0 && len(c.Env) == 0 {
				at := slices.IndexFunc(target.Connectors, func(old pack.Connector) bool {
					name := old.Name
					if name == "" && old.Catalogue != "" {
						name = old.Catalogue
					}
					return name == c.Name && old.Catalogue == c.Catalogue && old.Workspace == c.Workspace
				})
				if at >= 0 {
					in.Connectors[i] = target.Connectors[at]
					continue
				}
			}
			if c.Catalogue == "" && c.Workspace == "" && c.URL == "" && len(c.Command) == 0 && len(c.Env) == 0 {
				writeErr(w, 400, "select a connector from the library")
				return
			}
			if c.URL == "" && len(c.Command) == 0 && len(c.Env) == 0 {
				continue
			}
			if !slices.ContainsFunc(target.Connectors, func(old pack.Connector) bool { return reflect.DeepEqual(old, c) }) {
				writeErr(w, 400, "select a connector from the library")
				return
			}
		}
	}
	var additions []string
	for _, name := range in.Skills {
		if !slices.Contains(target.Skills, name) {
			additions = append(additions, name)
		}
	}
	selection, err := h.skillService().SelectForTeam(r.Context(), ws, user, additions)
	if errors.Is(err, pgx.ErrNoRows) {
		err = cardInputError("a selected skill is unavailable")
	}
	if errors.Is(err, skill.ErrRoutingChanged) {
		err = conflict("A selected skill is no longer enabled. Reload the skills list.")
	}
	if !answer(w, r, err) {
		return
	}
	if connectors {
		target.Connectors = in.Connectors
	} else {
		target.Skills = in.Skills
	}
	folders := map[string]map[string][]byte{}
	for _, mem := range append([]pack.Member{m.Leader, m.Auditor}, m.Members...) {
		for _, name := range mem.Skills {
			key := mem.Name + "/" + name
			if files, ok := base.Skills[key]; ok {
				folders[key] = files
			}
			if mem.Name == in.Member && slices.Contains(additions, name) {
				if files, ok := selection.Files[name]; ok {
					folders[key] = files
				}
			}
		}
	}
	z, err := pack.Zip(m, folders)
	var parsed *pack.Pack
	if err == nil {
		parsed, err = pack.Parse(z)
	}
	if err != nil {
		writeErr(w, 400, err.Error())
		return
	}
	key, err := pack.PutBlob(r.Context(), h.Blobs, ws, z, parsed.SHA256)
	if !answer(w, r, err) {
		return
	}
	err = h.Store.InWorkspace(r.Context(), ws, user, func(tx *store.Tx) error {
		if _, err := tx.Q.LockWorkspaceExecution(r.Context(), ws); err != nil {
			return err
		}
		if err := selection.Fence(r.Context(), tx, user); err != nil {
			return err
		}
		if err := tx.Q.LockWorkspaceEvents(r.Context(), ws); err != nil {
			return err
		}
		current, err := visiblePack(r, tx, user, id)
		if err != nil {
			return err
		}
		if current.Sha256 != source.Sha256 {
			return conflict("This Team changed while saving. Reload and try again.")
		}
		if err := editable(r, tx, current); err != nil {
			return err
		}
		row, err := pack.Replace(r.Context(), tx, current, key, parsed)
		if err != nil {
			return err
		}
		if _, err = team.Add(r.Context(), tx, row, m, user); err != nil {
			return teamInput(err)
		}
		return tx.Emit(r.Context(), "team.updated", nil, store.User(user), map[string]any{"pack_id": row.ID})
	})
	if errors.Is(err, skill.ErrRoutingChanged) {
		err = conflict("Skills changed while saving. Reload and try again.")
	}
	if answer(w, r, err) {
		writeJSON(w, 200, map[string]any{"id": id})
	}
}

// duplicateTeam copies a Team the caller can see into a new Team of their
// own under the name they chose, and adds it for them.
func (h *H) duplicateTeam(w http.ResponseWriter, r *http.Request) {
	ws, scopeErr := uuid.Parse(middleware.FromHeader(r))
	if scopeErr != nil {
		writeErr(w, 400, "X-Workspace-ID required")
		return
	}
	user := user(r)
	id, ok := cardID(w, r)
	if !ok {
		return
	}
	var in struct {
		Name string `json:"name"`
	}
	var source db.FieldPack
	err := h.memberTx(r, ws, false, func(tx *store.Tx) (err error) {
		source, err = visiblePack(r, tx, user, id)
		return err
	})
	if !answer(w, r, err) {
		return
	}
	if err := readJSON(r, &in); err != nil || strings.TrimSpace(in.Name) == "" {
		writeErr(w, 400, "a name is required")
		return
	}
	base, err := pack.Load(r.Context(), h.Blobs, source)
	if !answer(w, r, err) {
		return
	}
	// The row's manifest is the Team as it stands: a rename at install
	// changes the row, not the archive.
	var m pack.Manifest
	err = json.Unmarshal(source.Manifest, &m)
	if !answer(w, r, err) {
		return
	}
	m.Name = strings.TrimSpace(in.Name)
	z, err := pack.Zip(m, base.Skills)
	var parsed *pack.Pack
	if err == nil {
		parsed, err = pack.Parse(z)
	}
	if err != nil {
		writeErr(w, 400, err.Error())
		return
	}
	key, err := pack.PutBlob(r.Context(), h.Blobs, ws, z, parsed.SHA256)
	if !answer(w, r, err) {
		return
	}
	var row db.FieldPack
	var agents []db.Agent
	err = h.Store.InWorkspace(r.Context(), ws, user, func(tx *store.Tx) error {
		if err := tx.Q.LockWorkspaceEvents(r.Context(), ws); err != nil {
			return err
		}
		if _, err := visiblePack(r, tx, user, id); err != nil {
			return err
		}
		if row, err = h.insertUpload(r, tx, user, key, parsed); err != nil {
			return err
		}
		if err := tx.Emit(r.Context(), "team.created", nil, store.User(user), map[string]any{"pack_id": row.ID}); err != nil {
			return err
		}
		rows, err := team.Add(r.Context(), tx, row, parsed.Manifest, user)
		if err != nil {
			return teamInput(err)
		}
		agents, err = teamAgents(r, tx, rows)
		return err
	})
	if answer(w, r, err) {
		writeJSON(w, 201, map[string]any{"id": row.ID, "name": row.Name, "agents": agents})
	}
}

func memberPointers(members []pack.Member) []*pack.Member {
	out := make([]*pack.Member, len(members))
	for i := range members {
		out[i] = &members[i]
	}
	return out
}
