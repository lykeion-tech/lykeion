package handler

import (
	"errors"
	"net/http"
	"strings"
	"time"
	"unicode"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"

	"lykeion/server/internal/connectors"
	"lykeion/server/internal/store"
	db "lykeion/server/pkg/db/generated"
)

type connectorView struct {
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
	v := connectorView{Name: e.Name, Domain: e.Domain, Purpose: e.Purpose, Tier: e.Tier, Terms: e.Terms,
		TakesKey: e.KeyHeader != "" || e.KeyQuery != ""}
	if row != nil {
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

type connectorIn struct {
	Enabled          *bool   `json:"enabled"`
	Key              *string `json:"key"`
	LicenceConfirmed *bool   `json:"licence_confirmed"`
}

// setConnector changes one connector for the workspace. A key is sealed at
// once; it can be replaced or cleared, never read back.
func (h *H) setConnector(w http.ResponseWriter, r *http.Request) {
	e, ok := connectors.Get(chi.URLParam(r, "name"))
	if !ok {
		writeErr(w, 404, "no connector of that name")
		return
	}
	var in connectorIn
	if err := readJSON(r, &in); err != nil {
		writeErr(w, 400, "invalid connector settings: "+err.Error())
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
		if in.Enabled != nil {
			row.Enabled = *in.Enabled
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
		saved, err := tx.Q.UpdateWorkspaceConnector(ctx, db.UpdateWorkspaceConnectorParams{Enabled: row.Enabled, KeyCiphertext: row.KeyCiphertext,
			LicenceConfirmedBy: row.LicenceConfirmedBy, LicenceConfirmedAt: row.LicenceConfirmedAt, UpdatedBy: user, WorkspaceID: ws, Name: e.Name})
		if err != nil {
			return err
		}
		out = h.viewOf(e, &saved)
		return tx.Emit(ctx, "connector.updated", nil, store.User(user), map[string]any{"connector": e.Name, "enabled": saved.Enabled,
			"key_set": out.KeySet, "licence_confirmed": out.LicenceConfirmed})
	})
	if answer(w, r, err) {
		writeJSON(w, 200, out)
	}
}
