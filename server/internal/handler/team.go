package handler

import (
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"lykeion/server/internal/middleware"
	"lykeion/server/internal/pack"
	"lykeion/server/internal/skill"
	"lykeion/server/internal/store"
	"lykeion/server/internal/team"
	db "lykeion/server/pkg/db/generated"
)

// storeTeam is a Team as the API shows it: the stored version stays inside.
type storeTeam struct {
	ID          uuid.UUID       `json:"id"`
	WorkspaceID uuid.UUID       `json:"workspace_id"`
	Name        string          `json:"name"`
	Sha256      string          `json:"sha256"`
	Manifest    json.RawMessage `json:"manifest"`
	Source      string          `json:"source"`
	UploadedBy  *uuid.UUID      `json:"uploaded_by"`
	CreatedAt   time.Time       `json:"created_at"`
	Domain      string          `json:"domain"`
	Stage       string          `json:"stage"`
	Added       bool            `json:"added"`
	AvatarURL   string          `json:"avatar_url"`
	Visibility  string          `json:"visibility"`
}

// shownManifest is a stored manifest without its version.
func shownManifest(raw json.RawMessage) (json.RawMessage, error) {
	var m map[string]json.RawMessage
	if err := json.Unmarshal(raw, &m); err != nil {
		return nil, err
	}
	delete(m, "version")
	return json.Marshal(m)
}

// listTeams shows shipped Teams, the caller's own uploads, and workspace-shared Teams.
func (h *H) listTeams(w http.ResponseWriter, r *http.Request) {
	out := []storeTeam{}
	err := h.inWS(r, func(tx *store.Tx, ws, user uuid.UUID) error {
		rows, err := tx.Q.StoreTeams(r.Context(), db.StoreTeamsParams{WorkspaceID: ws, UserID: user,
			Domain: r.URL.Query().Get("domain"), Stage: r.URL.Query().Get("stage")})
		if err != nil {
			return err
		}
		for _, p := range rows {
			manifest, err := shownManifest(p.Manifest)
			if err != nil {
				return err
			}
			out = append(out, storeTeam{ID: p.ID, WorkspaceID: p.WorkspaceID, Name: p.Name, Sha256: p.Sha256,
				Manifest: manifest, Source: p.Source, UploadedBy: p.UploadedBy, CreatedAt: p.CreatedAt,
				Domain: p.Domain, Stage: p.Stage, Added: p.Added, AvatarURL: p.AvatarUrl, Visibility: p.Visibility})
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
		ID         uuid.UUID       `json:"id"`
		Manifest   json.RawMessage `json:"manifest"`
		AvatarURL  string          `json:"avatar_url"`
		Visibility string          `json:"visibility"`
		UploadedBy *uuid.UUID      `json:"uploaded_by"`
	}
	err := h.inWS(r, func(tx *store.Tx, _, user uuid.UUID) error {
		p, err := visiblePack(r, tx, user, id)
		if err != nil {
			return err
		}
		out.ID = p.ID
		out.AvatarURL = p.AvatarUrl
		out.Visibility, out.UploadedBy = p.Visibility, p.UploadedBy
		if p.Source == "shipped" {
			out.Visibility = "workspace"
		}
		out.Manifest, err = shownManifest(p.Manifest)
		return err
	})
	if err != nil {
		fail(w, r, err)
		return
	}
	writeJSON(w, 200, out)
}

func (h *H) updateTeamSettings(w http.ResponseWriter, r *http.Request) {
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
	var in struct {
		AvatarURL   *string `json:"avatar_url"`
		Name        *string `json:"name"`
		Description *string `json:"description"`
		Visibility  *string `json:"visibility"`
	}
	if err := readJSON(r, &in); err != nil || (in.AvatarURL == nil && in.Name == nil && in.Description == nil && in.Visibility == nil) {
		writeErr(w, 400, "invalid Team settings")
		return
	}
	if in.AvatarURL != nil && !validEmojiAvatar(*in.AvatarURL) {
		writeErr(w, 400, "invalid emoji avatar")
		return
	}
	if in.Visibility != nil && *in.Visibility != "private" && *in.Visibility != "workspace" {
		writeErr(w, 400, "invalid visibility")
		return
	}
	if in.Name != nil {
		n := strings.TrimSpace(*in.Name)
		in.Name = &n
		if n == "" || utf8.RuneCountInString(n) > 64 {
			writeErr(w, 400, "invalid Team name")
			return
		}
	}
	if in.Description != nil && utf8.RuneCountInString(*in.Description) > 1000 {
		writeErr(w, 400, "description is too long")
		return
	}
	var source db.FieldPack
	err = h.memberTx(r, ws, false, func(tx *store.Tx) error {
		var e error
		source, e = visiblePack(r, tx, who, id)
		if e != nil {
			return e
		}
		return editable(r, tx, source)
	})
	if !answer(w, r, err) {
		return
	}
	m := pack.Manifest{}
	if err = json.Unmarshal(source.Manifest, &m); !answer(w, r, err) {
		return
	}
	key, sha, manifest := source.BlobKey, source.Sha256, source.Manifest
	if in.Name != nil || in.Description != nil {
		base, e := pack.Load(r.Context(), h.Blobs, source)
		if !answer(w, r, e) {
			return
		}
		if in.Name != nil {
			m.Name = *in.Name
		}
		if in.Description != nil {
			m.Description = *in.Description
		}
		z, e := pack.Zip(m, base.Skills)
		if e != nil {
			writeErr(w, 400, e.Error())
			return
		}
		parsed, e := pack.Parse(z)
		if e != nil {
			writeErr(w, 400, e.Error())
			return
		}
		key, e = pack.PutBlob(r.Context(), h.Blobs, ws, z, parsed.SHA256)
		if !answer(w, r, e) {
			return
		}
		sha = parsed.SHA256
		manifest, e = json.Marshal(parsed.Manifest)
		if !answer(w, r, e) {
			return
		}
	}
	var out db.FieldPack
	err = h.Store.InWorkspace(r.Context(), ws, who, func(tx *store.Tx) error {
		if _, e := tx.Q.LockWorkspaceExecution(r.Context(), ws); e != nil {
			return e
		}
		if _, e := tx.Q.LockMember(r.Context(), db.LockMemberParams{WorkspaceID: ws, UserID: who}); e != nil {
			return e
		}
		if e := tx.Q.LockWorkspaceEvents(r.Context(), ws); e != nil {
			return e
		}
		current, e := visiblePack(r, tx, who, id)
		if e != nil {
			return e
		}
		if current.Sha256 != source.Sha256 || current.Visibility != source.Visibility {
			return conflict("This Team changed while saving. Reload and try again.")
		}
		if e = editable(r, tx, current); e != nil {
			return e
		}
		if m.Name != current.Name {
			taken, e := tx.Q.PackNameInUse(r.Context(), db.PackNameInUseParams{WorkspaceID: ws, Name: m.Name})
			if e != nil {
				return e
			}
			if taken {
				return conflict("another Team already has this name")
			}
		}
		visibility := current.Visibility
		if in.Visibility != nil {
			visibility = *in.Visibility
		}
		out, e = tx.Q.UpdateTeamSettings(r.Context(), db.UpdateTeamSettingsParams{WorkspaceID: ws, ID: id, Name: m.Name, Manifest: manifest, Sha256: sha, BlobKey: key, Visibility: visibility})
		if isUnique(e) {
			return conflict("another Team already has this name")
		}
		if e != nil {
			return e
		}
		if in.AvatarURL != nil {
			out, e = tx.Q.SetFieldPackAvatar(r.Context(), db.SetFieldPackAvatarParams{WorkspaceID: ws, ID: id, AvatarUrl: *in.AvatarURL})
			if e != nil {
				return e
			}
		}
		return tx.Emit(r.Context(), "team.updated", nil, store.User(who), map[string]any{})
	})
	if answer(w, r, err) {
		writeJSON(w, 200, map[string]any{"id": out.ID, "avatar_url": out.AvatarUrl, "visibility": out.Visibility})
	}
}

func (h *H) deleteTeam(w http.ResponseWriter, r *http.Request) {
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
		p, err := visiblePack(r, tx, user, id)
		if err != nil {
			return err
		}
		if p.Source == "shipped" {
			return errBuiltInTeam
		}
		if p.UploadedBy == nil || *p.UploadedBy != user {
			return forbidden("Only the Team owner can delete it")
		}
		used, err := tx.Q.TeamHasActiveUses(r.Context(), db.TeamHasActiveUsesParams{WorkspaceID: ws, PackID: id})
		if err != nil {
			return err
		}
		if used != nil && *used {
			return conflict("This Team is used by an active study or Autoresearch.")
		}
		agents, err := tx.Q.TeamAgentIDs(r.Context(), db.TeamAgentIDsParams{WorkspaceID: ws, PackID: id})
		if err != nil {
			return err
		}
		for _, agentID := range agents {
			if _, err := tx.Q.GetAgentForUpdate(r.Context(), db.GetAgentForUpdateParams{WorkspaceID: ws, ID: agentID}); err != nil {
				return err
			}
			if err := agentInUse(r, tx, agentID); err != nil {
				return err
			}
			live, err := tx.Q.TeamAgentHasLiveRuns(r.Context(), db.TeamAgentHasLiveRunsParams{WorkspaceID: ws, AgentID: &agentID})
			if err != nil {
				return err
			}
			if live {
				return conflict("A Team agent has an active run.")
			}
			shared, err := tx.Q.AgentBelongsToOtherTeam(r.Context(), db.AgentBelongsToOtherTeamParams{WorkspaceID: ws, AgentID: agentID, PackID: id})
			if err != nil {
				return err
			}
			if shared {
				return conflict("A Team agent belongs to another Team.")
			}
		}
		for _, agentID := range agents {
			if err := tx.Q.DeleteTeamAgent(r.Context(), db.DeleteTeamAgentParams{WorkspaceID: ws, AgentID: agentID}); err != nil {
				return err
			}
		}
		if err := tx.Q.DeleteTeam(r.Context(), db.DeleteTeamParams{WorkspaceID: ws, PackID: id}); err != nil {
			return err
		}
		return tx.Emit(r.Context(), "team.deleted", nil, store.User(user), map[string]any{})
	})
	if answer(w, r, err) {
		writeJSON(w, 200, map[string]any{"deleted": true})
	}
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
	err := h.inWS(r, func(tx *store.Tx, ws, user uuid.UUID) error {
		if err := tx.Q.LockWorkspaceEvents(r.Context(), ws); err != nil {
			return err
		}
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
		writeJSON(w, 201, map[string]any{"id": row.ID, "name": row.Name, "sha256": row.Sha256, "agents": agents})
	}
}

// teamInput turns the team package's input refusals into 400s.
func teamInput(err error) error {
	if errors.Is(err, team.ErrInvalid) || errors.Is(err, team.ErrNoHarness) {
		return cardInputError(err.Error())
	}
	return err
}
