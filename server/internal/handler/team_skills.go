package handler

import (
	"encoding/json"
	"errors"
	"net/http"
	"slices"
	"strconv"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"lykeion/server/internal/middleware"
	"lykeion/server/internal/pack"
	"lykeion/server/internal/skill"
	"lykeion/server/internal/store"
	"lykeion/server/internal/team"
	db "lykeion/server/pkg/db/generated"
)

// nextTeamVersion refuses stale edits and reserves no state while archives are read.
func nextTeamVersion(r *http.Request, tx *store.Tx, user uuid.UUID, source db.FieldPack) (string, error) {
	versions, err := tx.Q.ListPackVersions(r.Context(), db.ListPackVersionsParams{WorkspaceID: tx.WS, Name: source.Name, UserID: user})
	if err != nil {
		return "", err
	}
	if len(versions) == 0 || versions[len(versions)-1].ID != source.ID {
		return "", conflict("This Team has a newer version. Open it before editing skills.")
	}
	rows, err := tx.Q.ListFieldPacks(r.Context(), tx.WS)
	if err != nil {
		return "", err
	}
	next := 1
	for _, row := range rows {
		if row.Name == source.Name {
			if n, e := strconv.Atoi(row.Version); e == nil && n >= next {
				next = n + 1
			}
		}
	}
	// Non-numeric version names are valid too; the next integer must still be free.
	return strconv.Itoa(next), nil
}

func (h *H) saveTeamSkills(w http.ResponseWriter, r *http.Request) {
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
		Member string   `json:"member"`
		Skills []string `json:"skills"`
	}
	var source db.FieldPack
	var version string
	err := h.memberTx(r, ws, false, func(tx *store.Tx) (err error) {
		source, err = visiblePack(r, tx, user, id)
		if err != nil {
			return err
		}
		version, err = nextTeamVersion(r, tx, user, source)
		return err
	})
	if !answer(w, r, err) {
		return
	}
	if err := readJSON(r, &in); err != nil || in.Member == "" || in.Skills == nil {
		writeErr(w, 400, "member and skills are required")
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
	target.Skills = in.Skills
	m.Version = version
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
	var row db.FieldPack
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
		next, err := nextTeamVersion(r, tx, user, current)
		if err != nil {
			return err
		}
		if next != version {
			return conflict("Team versions changed while saving. Reload and try again.")
		}
		row, err = pack.Insert(r.Context(), tx, key, parsed, &user)
		if err != nil {
			return err
		}
		if _, err = team.Add(r.Context(), tx, row, m, user); err != nil {
			return err
		}
		return tx.Emit(r.Context(), "team.created", nil, store.User(user), map[string]any{"pack_id": row.ID})
	})
	if errors.Is(err, skill.ErrRoutingChanged) {
		err = conflict("Skills changed while saving. Reload and try again.")
	}
	if answer(w, r, err) {
		writeJSON(w, 201, map[string]any{"id": row.ID, "version": row.Version})
	}
}

func memberPointers(members []pack.Member) []*pack.Member {
	out := make([]*pack.Member, len(members))
	for i := range members {
		out[i] = &members[i]
	}
	return out
}
