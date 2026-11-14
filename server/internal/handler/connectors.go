package handler

import (
	"errors"
	"net/http"
	"strings"
	"time"
	"unicode"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"lykeion/server/internal/connectors"
	"lykeion/server/internal/store"
	"lykeion/server/internal/workspacemcp"
	db "lykeion/server/pkg/db/generated"
)

type connectorView struct {
	PolicyEnabled    bool   `json:"policy_enabled"`
	Revision         int64  `json:"revision"`
	Name             string `json:"name"`
	Domain           string `json:"domain"`
	Purpose          string `json:"purpose"`
	Tier             string `json:"tier"`
	Terms            string `json:"terms"`
	Enabled          bool   `json:"enabled"`
	Reason           string `json:"reason,omitempty"`
	KeySet           bool   `json:"key_set"`
	TakesKey         bool   `json:"takes_key"`
	LicenceConfirmed bool   `json:"licence_confirmed"`
}

// viewOf shows a connector's state in a workspace, and never its key.
func (h *H) viewOf(e connectors.Entry, row *db.WorkspaceConnector) connectorView {
	v := connectorView{PolicyEnabled: true, Name: e.Name, Domain: e.Domain, Purpose: e.Purpose, Tier: e.Tier, Terms: e.Terms,
		TakesKey: e.KeyHeader != "" || e.KeyQuery != ""}
	if row != nil {
		v.Revision = row.Revision
		v.KeySet, v.LicenceConfirmed = len(row.KeyCiphertext) > 0, row.LicenceConfirmedAt != nil
	}
	if err := connectors.Ready(e, row, h.Connectors.Contact); err != nil {
		v.Reason = err.Error()
	} else {
		v.Enabled = true
	}
	return v
}

func (h *H) listConnectors(w http.ResponseWriter, r *http.Request) {
	out := []connectorView{}
	err := h.inWS(r, func(tx *store.Tx, ws, _ uuid.UUID) error {
		rows, err := tx.Q.ListWorkspaceConnectors(r.Context(), ws)
		if err != nil {
			return err
		}
		byName := map[string]db.WorkspaceConnector{}
		for _, row := range rows {
			byName[row.Name] = row
		}
		for _, d := range connectors.Domains {
			for _, e := range connectors.Catalogue {
				if e.Domain != d {
					continue
				}
				var row *db.WorkspaceConnector
				if x, ok := byName[e.Name]; ok {
					row = &x
				}
				out = append(out, h.viewOf(e, row))
			}
		}
		return nil
	})
	if answer(w, r, err) {
		writeJSON(w, 200, out)
	}
}

// openAlexConnector shows the workspace's OpenAlex availability and key status.
func (h *H) openAlexConnector(w http.ResponseWriter, r *http.Request) {
	var out connectorView
	err := h.inWS(r, func(tx *store.Tx, ws, _ uuid.UUID) error {
		row, err := tx.Q.GetWorkspaceConnector(r.Context(), db.GetWorkspaceConnectorParams{WorkspaceID: ws, Name: connectors.OpenAlex.Name})
		switch {
		case errors.Is(err, pgx.ErrNoRows):
			out = h.viewOf(connectors.OpenAlex, nil)
		case err != nil:
			return err
		default:
			out = h.viewOf(connectors.OpenAlex, &row)
		}
		return nil
	})
	if answer(w, r, err) {
		writeJSON(w, 200, out)
	}
}

type connectorIn struct {
	Revision         *int64  `json:"revision"`
	Enabled          *bool   `json:"enabled"`
	Key              *string `json:"key"`
	LicenceConfirmed *bool   `json:"licence_confirmed"`
}

// setConnector changes one connector for the workspace. A key is sealed at
// once; it can be replaced or cleared, never read back.
func (h *H) setConnector(w http.ResponseWriter, r *http.Request) {
	name := chi.URLParam(r, "name")
	e, ok := connectors.Get(name)
	if name == connectors.OpenAlex.Name {
		e, ok = connectors.OpenAlex, true
	}
	if !ok {
		writeErr(w, 404, "no connector of that name")
		return
	}
	var in connectorIn
	if err := readJSON(r, &in); err != nil {
		writeErr(w, 400, "invalid connector settings")
		return
	}
	if in.Revision == nil || *in.Revision < 0 {
		writeErr(w, 400, "revision is required")
		return
	}
	if e.Name == connectors.OpenAlex.Name && in.LicenceConfirmed != nil {
		writeErr(w, 400, "OpenAlex does not use licence confirmation")
		return
	}
	if in.Key != nil && *in.Key != "" {
		k := *in.Key
		if e.KeyHeader == "" && e.KeyQuery == "" {
			writeErr(w, 400, e.Name+" takes no key")
			return
		}
		if len(k) < 8 || len(k) > 1024 || strings.ContainsFunc(k, func(c rune) bool { return unicode.IsSpace(c) || unicode.IsControl(c) }) {
			writeErr(w, 400, "a key is 8 to 1024 characters with no spaces")
			return
		}
	}
	var out connectorView
	err := h.inWS(r, func(tx *store.Tx, ws, user uuid.UUID) error {
		ctx := r.Context()
		if err := tx.Q.EnsureWorkspaceConnector(ctx, db.EnsureWorkspaceConnectorParams{WorkspaceID: ws, Name: e.Name, UpdatedBy: user}); err != nil {
			return err
		}
		row, err := tx.Q.LockWorkspaceConnector(ctx, db.LockWorkspaceConnectorParams{WorkspaceID: ws, Name: e.Name})
		if err != nil {
			return err
		}
		if row.Revision != *in.Revision {
			return &workspacemcp.StaleRevision{Revision: row.Revision}
		}
		if in.Key != nil {
			row.KeyCiphertext = nil
			if *in.Key != "" {
				sealed, err := h.Connectors.Seal(ws, e.Name, *in.Key)
				if errors.Is(err, connectors.ErrNoSecret) {
					return cardInputError(err.Error())
				}
				if err != nil {
					return err
				}
				row.KeyCiphertext = sealed
			}
		}
		if in.LicenceConfirmed != nil {
			switch {
			case !*in.LicenceConfirmed:
				row.LicenceConfirmedAt, row.LicenceConfirmedBy = nil, nil
			case row.LicenceConfirmedAt == nil:
				now := time.Now()
				row.LicenceConfirmedAt, row.LicenceConfirmedBy = &now, &user
			}
		}
		saved, err := tx.Q.UpdateWorkspaceConnectorRevision(ctx, db.UpdateWorkspaceConnectorRevisionParams{ExpectedRevision: *in.Revision, Enabled: row.Enabled, KeyCiphertext: row.KeyCiphertext,
			LicenceConfirmedBy: row.LicenceConfirmedBy, LicenceConfirmedAt: row.LicenceConfirmedAt, UpdatedBy: user, WorkspaceID: ws, Name: e.Name})
		if err != nil {
			return err
		}
		out = h.viewOf(e, &saved)
		return tx.Emit(ctx, "connector.updated", nil, store.User(user), map[string]any{"connector": e.Name, "enabled": saved.Enabled,
			"key_set": out.KeySet, "licence_confirmed": out.LicenceConfirmed, "revision": saved.Revision})
	})
	var stale *workspacemcp.StaleRevision
	if errors.As(err, &stale) {
		writeJSON(w, 409, map[string]any{"error": stale.Error(), "code": "stale_revision", "revision": stale.Revision})
		return
	}
	if answer(w, r, err) {
		writeJSON(w, 200, out)
	}
}
