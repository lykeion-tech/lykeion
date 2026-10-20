//go:build unix

package e2e

import (
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"testing"

	"github.com/google/uuid"
	"lykeion/server/internal/auth"
	"lykeion/server/internal/handler"
	"lykeion/server/internal/pack"
	"lykeion/server/internal/store"
	db "lykeion/server/pkg/db/generated"
)

// uiTeamFixture installs only the fixed demo Team as pre-existing test data.
// Browser tests still exercise the real Team approval and addition APIs.
func uiTeamFixture(t *testing.T, mux *http.ServeMux, h *handler.H, issuer auth.Issuer) {
	t.Helper()
	dir := "../../../tests/e2e/fixtures/demo-pack"
	raw, err := os.ReadFile(filepath.Join(dir, "pack.json"))
	must(t, err)
	var manifest pack.Manifest
	must(t, json.Unmarshal(raw, &manifest))
	skills := map[string]map[string][]byte{}
	for _, key := range []string{"pi/lead", "scout/search", "analyst/analyse"} {
		body, err := os.ReadFile(filepath.Join(dir, "skills", key, "SKILL.md"))
		must(t, err)
		skills[key] = map[string][]byte{"SKILL.md": body}
	}
	mux.HandleFunc("POST /lab/installed-teams", func(w http.ResponseWriter, r *http.Request) {
		cookie, err := r.Cookie("lyk_session")
		if err != nil {
			http.Error(w, "sign in required", 401)
			return
		}
		user, _, err := issuer.Parse(cookie.Value)
		if err != nil {
			http.Error(w, "sign in required", 401)
			return
		}
		ws, err := uuid.Parse(r.Header.Get("X-Workspace-ID"))
		if err != nil {
			http.Error(w, "workspace required", 400)
			return
		}
		var in struct{ Name, Domain, Stage string }
		if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 4096)).Decode(&in); err != nil || in.Name == "" {
			http.Error(w, "fixture name required", 400)
			return
		}
		err = h.Store.InWorkspace(r.Context(), ws, user, func(tx *store.Tx) error {
			_, err := tx.Q.GetMember(r.Context(), db.GetMemberParams{WorkspaceID: ws, UserID: user})
			return err
		})
		if err != nil {
			http.Error(w, "workspace membership required", 403)
			return
		}
		m := manifest
		m.Name = in.Name
		if in.Domain != "" {
			m.Domain = in.Domain
		}
		if in.Stage != "" {
			m.Stage = in.Stage
		}
		z, err := pack.Zip(m, skills)
		if err != nil {
			http.Error(w, err.Error(), 400)
			return
		}
		parsed, err := pack.Parse(z)
		if err != nil {
			http.Error(w, err.Error(), 400)
			return
		}
		key, err := pack.PutBlob(r.Context(), h.Blobs, ws, z, parsed.SHA256)
		if err != nil {
			http.Error(w, err.Error(), 500)
			return
		}
		var row db.FieldPack
		err = h.Store.InWorkspace(r.Context(), ws, user, func(tx *store.Tx) error {
			if err := tx.Q.LockWorkspaceEvents(r.Context(), ws); err != nil {
				return err
			}
			versions, err := tx.Q.ListPackVersions(r.Context(), db.ListPackVersionsParams{WorkspaceID: ws, Name: m.Name, UserID: user})
			if err != nil {
				return err
			}
			for _, version := range versions {
				if version.Version == m.Version {
					row, err = tx.Q.GetFieldPack(r.Context(), db.GetFieldPackParams{WorkspaceID: ws, ID: version.ID})
					return err
				}
			}
			row, err = pack.Insert(r.Context(), tx, key, parsed, &user)
			return err
		})
		if err != nil {
			http.Error(w, err.Error(), 500)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusCreated)
		json.NewEncoder(w).Encode(map[string]any{"id": row.ID, "name": row.Name})
	})
}
