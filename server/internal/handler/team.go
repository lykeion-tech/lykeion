package handler

import (
	"encoding/json"
	"errors"
	"net/http"

	"github.com/google/uuid"

	"lykeion/server/internal/pack"
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
		// Shipped Teams reach the workspace's table the first time its store
		// is read; the lock keeps two readers from inserting the same version.
		if err := tx.Q.LockWorkspaceEvents(r.Context(), ws); err != nil {
			return err
		}
		for _, name := range pack.ShippedNames(h.Shipped) {
			if _, err := pack.EnsureShipped(r.Context(), tx, h.Blobs, h.Shipped, name); err != nil {
				return err
			}
		}
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
	var in team.Form
	if err := readJSON(r, &in); err != nil {
		writeErr(w, 400, "invalid team: "+err.Error())
		return
	}
	in.Admin = agentAdmin(r)
	var row db.FieldPack
	var agents []db.Agent
	err := h.inWS(r, func(tx *store.Tx, _, user uuid.UUID) error {
		in.User = user
		m, err := team.FromForm(r.Context(), tx, in)
		if err != nil {
			return teamInput(err)
		}
		z, err := pack.Zip(m, nil)
		if err != nil {
			return err
		}
		p, err := pack.Parse(z)
		if err != nil {
			return cardInputError(err.Error())
		}
		if row, err = h.insertUpload(r, tx, user, z, p); err != nil {
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
