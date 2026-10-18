package handler

import (
	"encoding/json"
	"errors"
	"net/http"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"lykeion/server/internal/pack"
	"lykeion/server/internal/skill"
	"lykeion/server/internal/store"
	"lykeion/server/internal/team"
	db "lykeion/server/pkg/db/generated"
)

type storeTeam struct {
	db.FieldPack
	Manifest json.RawMessage `json:"manifest"`
	Added    bool            `json:"added"`
}

// listTeams is the store: shipped Teams and the caller's own uploads, the
// newest version of each name.
func (h *H) listTeams(w http.ResponseWriter, r *http.Request) {
	out := []storeTeam{}
	err := h.inWS(r, func(tx *store.Tx, ws, user uuid.UUID) error {
		rows, err := tx.Q.StoreTeams(r.Context(), db.StoreTeamsParams{WorkspaceID: ws, UserID: user,
			Domain: r.URL.Query().Get("domain"), Stage: r.URL.Query().Get("stage")})
		if err != nil {
			return err
		}
		for _, p := range rows {
			out = append(out, storeTeam{FieldPack: db.FieldPack{ID: p.ID, WorkspaceID: p.WorkspaceID, Name: p.Name,
				Version: p.Version, Sha256: p.Sha256, Source: p.Source, UploadedBy: p.UploadedBy, Domain: p.Domain,
				Stage: p.Stage, CreatedAt: p.CreatedAt}, Manifest: p.Manifest, Added: p.Added})
		}
		return nil
	})
	if err != nil {
		fail(w, r, err)
		return
	}
	writeJSON(w, 200, out)
}

func (h *H) getTeam(w http.ResponseWriter, r *http.Request) {
	id, ok := cardID(w, r)
	if !ok {
		return
	}
	var out struct {
		ID       uuid.UUID                `json:"id"`
		Manifest json.RawMessage          `json:"manifest"`
		Versions []db.ListPackVersionsRow `json:"versions"`
	}
	err := h.inWS(r, func(tx *store.Tx, ws, user uuid.UUID) error {
		p, err := visiblePack(r, tx, user, id)
		if err != nil {
			return err
		}
		out.ID, out.Manifest = p.ID, p.Manifest
		out.Versions, err = tx.Q.ListPackVersions(r.Context(), db.ListPackVersionsParams{WorkspaceID: ws, Name: p.Name, UserID: user})
		return err
	})
	if err != nil {
		fail(w, r, err)
		return
	}
	writeJSON(w, 200, out)
}

// teamAgents answers with the agents Add created or already had.
func teamAgents(r *http.Request, tx *store.Tx, rows []db.TeamAgent) ([]db.Agent, error) {
	out := []db.Agent{}
	for _, row := range rows {
		a, err := tx.Q.GetAgent(r.Context(), db.GetAgentParams{WorkspaceID: tx.WS, ID: row.AgentID})
		if err != nil {
			return nil, err
		}
		out = append(out, a)
	}
	return out, nil
}

func (h *H) addTeam(w http.ResponseWriter, r *http.Request) {
	id, ok := cardID(w, r)
	if !ok {
		return
	}
	var out []db.Agent
	err := h.inWS(r, func(tx *store.Tx, _, user uuid.UUID) error {
		p, err := visiblePack(r, tx, user, id)
		if err != nil {
			return err
		}
		var m pack.Manifest
		if err := json.Unmarshal(p.Manifest, &m); err != nil {
			return err
		}
		rows, err := team.Add(r.Context(), tx, p, m, user)
		if err != nil {
			return teamInput(err)
		}
		out, err = teamAgents(r, tx, rows)
		return err
	})
	if answer(w, r, err) {
		writeJSON(w, 200, out)
	}
}

// createTeam builds a private Team from the form and adds it for its creator.
func (h *H) createTeam(w http.ResponseWriter, r *http.Request) {
	ws, who, ok := h.skillScope(w, r)
	if !ok {
		return
	}
	var in team.Form
	if err := readJSON(r, &in); err != nil {
		writeErr(w, 400, "invalid team: "+err.Error())
		return
	}
	var m pack.Manifest
	err := h.memberTx(r, ws, false, func(tx *store.Tx) (err error) {
		member, err := tx.Q.LockMember(r.Context(), db.LockMemberParams{WorkspaceID: ws, UserID: who})
		if err != nil {
			return err
		}
		in.Admin = member.Role == "owner" || member.Role == "admin"
		in.User = who
		m, err = team.FromForm(r.Context(), tx, in)
		return teamInput(err)
	})
	if !answer(w, r, err) {
		return
	}
	selection, err := h.skillService().SelectForTeam(r.Context(), ws, who, team.SkillNames(m))
	if errors.Is(err, pgx.ErrNoRows) {
		err = cardInputError("a selected skill is unavailable")
	}
	if !answer(w, r, err) {
		return
	}
	z, err := pack.Zip(m, team.SkillFolders(m, selection.Files))
	var p *pack.Pack
	if err == nil {
		if p, err = pack.Parse(z); err != nil {
			err = cardInputError(err.Error())
		}
	}
	var key string
	if err == nil {
		key, err = pack.PutBlob(r.Context(), h.Blobs, ws, z, p.SHA256)
	}
	var row db.FieldPack
	var agents []db.Agent
	if err == nil {
		err = h.Store.InWorkspace(r.Context(), ws, who, func(tx *store.Tx) error {
			user := who
			if _, err := tx.Q.LockWorkspaceExecution(r.Context(), ws); err != nil {
				return err
			}
			if err := selection.Fence(r.Context(), tx, user); err != nil {
				return err
			}
			member, err := tx.Q.LockMember(r.Context(), db.LockMemberParams{WorkspaceID: ws, UserID: user})
			if err != nil {
				return err
			}
			in.Admin = member.Role == "owner" || member.Role == "admin"
			if _, err := team.FromForm(r.Context(), tx, in); err != nil {
				return teamInput(err)
			}
			if row, err = h.insertUpload(r, tx, user, key, p); err != nil {
				return err
			}
			if err := tx.Emit(r.Context(), "team.created", nil, store.User(user), map[string]any{"pack_id": row.ID}); err != nil {
				return err
			}
			rows, err := team.Add(r.Context(), tx, row, p.Manifest, user)
			if err != nil {
				return teamInput(err)
			}
			agents, err = teamAgents(r, tx, rows)
			return err
		})
	}
	if errors.Is(err, skill.ErrRoutingChanged) {
		err = conflict(err.Error())
	}
	if answer(w, r, err) {
		writeJSON(w, 201, map[string]any{"id": row.ID, "name": row.Name, "version": row.Version, "sha256": row.Sha256, "agents": agents})
	}
}

// teamInput turns the team package's input refusals into 400s.
func teamInput(err error) error {
	if errors.Is(err, team.ErrInvalid) {
		return cardInputError(err.Error())
	}
	return err
}
