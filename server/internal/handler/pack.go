package handler

import (
	"net/http"
	"slices"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"lykeion/server/internal/pack"
	"lykeion/server/internal/store"
	db "lykeion/server/pkg/db/generated"
)

// visibleTo says whether user may see and approve the pack: shipped packs are
// everyone's, an upload is its uploader's alone.
func visibleTo(p db.FieldPack, user uuid.UUID) bool {
	return p.Source == "shipped" || (p.UploadedBy != nil && *p.UploadedBy == user)
}

// approve records user's approval of p and emits pack.approved, only when
// the approval is new.
func approve(r *http.Request, tx *store.Tx, user uuid.UUID, p db.FieldPack) error {
	n, err := tx.Q.ApproveFieldPack(r.Context(), db.ApproveFieldPackParams{WorkspaceID: tx.WS, PackID: p.ID, ApprovedBy: user})
	if err != nil || n == 0 {
		return err
	}
	payload := map[string]any{"pack_id": p.ID}
	// Every member sees workspace events, and an uploaded pack is its
	// uploader's alone, so only a shipped pack's event says which one.
	if p.Source == "shipped" {
		payload["sha256"], payload["version"] = p.Sha256, p.Version
	}
	return tx.Emit(r.Context(), "pack.approved", nil, store.User(user), payload)
}

// insertUpload stores a parsed archive as user's private upload, refusing a
// name another Team holds.
func (h *H) insertUpload(r *http.Request, tx *store.Tx, user uuid.UUID, key string, p *pack.Pack) (db.FieldPack, error) {
	if slices.Contains(pack.ShippedNames(h.Shipped), p.Manifest.Name) {
		return db.FieldPack{}, conflict("a Team Lykeion ships already has this name")
	}
	// Serialises uploads in the workspace so two people cannot both
	// claim a new name between the check and the insert.
	if err := tx.Q.LockWorkspaceEvents(r.Context(), tx.WS); err != nil {
		return db.FieldPack{}, err
	}
	taken, err := tx.Q.PackNameTaken(r.Context(), db.PackNameTakenParams{WorkspaceID: tx.WS, Name: p.Manifest.Name, UserID: user})
	if err != nil {
		return db.FieldPack{}, err
	}
	if taken {
		return db.FieldPack{}, conflict("another Team already has this name")
	}
	row, err := pack.Insert(r.Context(), tx, key, p, &user)
	if isUnique(err) {
		return row, conflict("this version of the Team is already uploaded")
	}
	return row, err
}

// packVersions lists the versions of one pack the caller may see.
func (h *H) packVersions(w http.ResponseWriter, r *http.Request) {
	rows := []db.ListPackVersionsRow{}
	err := h.inWS(r, func(tx *store.Tx, ws, user uuid.UUID) error {
		var err error
		rows, err = tx.Q.ListPackVersions(r.Context(), db.ListPackVersionsParams{WorkspaceID: ws, Name: r.URL.Query().Get("name"), UserID: user})
		return err
	})
	if err != nil {
		fail(w, r, err)
		return
	}
	if rows == nil {
		rows = []db.ListPackVersionsRow{}
	}
	writeJSON(w, 200, rows)
}

// visiblePack loads the pack in the URL, as not found when the caller may not
// see it.
func visiblePack(r *http.Request, tx *store.Tx, user uuid.UUID, id uuid.UUID) (db.FieldPack, error) {
	p, err := tx.Q.GetFieldPack(r.Context(), db.GetFieldPackParams{WorkspaceID: tx.WS, ID: id})
	if err == nil && !visibleTo(p, user) {
		err = pgx.ErrNoRows
	}
	return p, err
}

func (h *H) approvePack(w http.ResponseWriter, r *http.Request) {
	id, err := uuid.Parse(chi.URLParam(r, "id"))
	if err != nil {
		writeErr(w, 404, "not found")
		return
	}
	err = h.inWS(r, func(tx *store.Tx, _, user uuid.UUID) error {
		p, err := visiblePack(r, tx, user, id)
		if err != nil {
			return err
		}
		return approve(r, tx, user, p)
	})
	if err != nil {
		fail(w, r, err)
		return
	}
	writeJSON(w, 200, map[string]any{"pack_id": id})
}
