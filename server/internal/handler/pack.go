package handler

import (
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"slices"
	"strings"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"lykeion/server/internal/board"
	"lykeion/server/internal/pack"
	"lykeion/server/internal/store"
	db "lykeion/server/pkg/db/generated"
)

const errApproveTeam = conflict("approve a version of your Team first")

// visibleTo says whether user may see and approve the pack: shipped packs are
// everyone's, an upload is its uploader's alone.
func visibleTo(p db.FieldPack, user uuid.UUID) bool {
	return p.Source == "shipped" || (p.UploadedBy != nil && *p.UploadedBy == user)
}

type catalogEntry struct {
	Name        string   `json:"name"`
	Description string   `json:"description"`
	StartKinds  []string `json:"start_kinds"`
	Version     string   `json:"version"`
	Source      string   `json:"source"`
}

// packCatalog lists the shipped packs at their current version and the
// newest version of each of the caller's own uploads.
func (h *H) packCatalog(w http.ResponseWriter, r *http.Request) {
	out := []catalogEntry{}
	for _, name := range pack.ShippedNames(h.Shipped) {
		_, p, err := pack.ReadShipped(h.Shipped, name)
		if err != nil {
			fail(w, r, err)
			return
		}
		m := p.Manifest
		out = append(out, catalogEntry{m.Name, m.Description, m.StartKinds, m.Version, "shipped"})
	}
	err := h.inWS(r, func(tx *store.Tx, ws, user uuid.UUID) error {
		own, err := tx.Q.ListOwnPacks(r.Context(), db.ListOwnPacksParams{WorkspaceID: ws, UserID: &user})
		if err != nil {
			return err
		}
		for i, p := range own {
			if i+1 < len(own) && own[i+1].Name == p.Name {
				continue // rows are by name, then age: keep the newest of each
			}
			var m pack.Manifest
			if err := json.Unmarshal(p.Manifest, &m); err != nil {
				return err
			}
			out = append(out, catalogEntry{m.Name, m.Description, m.StartKinds, m.Version, "uploaded"})
		}
		return nil
	})
	if err != nil {
		fail(w, r, err)
		return
	}
	writeJSON(w, 200, out)
}

// approve records user's approval of p and emits pack.approved, only when
// the approval is new.
func approve(r *http.Request, tx *store.Tx, user uuid.UUID, p db.FieldPack) error {
	n, err := tx.Q.ApproveFieldPack(r.Context(), db.ApproveFieldPackParams{WorkspaceID: tx.WS, PackID: p.ID, ApprovedBy: user})
	if err != nil || n == 0 {
		return err
	}
	return tx.Emit(r.Context(), "pack.approved", nil, store.User(user),
		map[string]any{"pack_id": p.ID, "sha256": p.Sha256, "version": p.Version})
}

// setMyPack makes name the caller's Team. A shipped name also approves its
// current version for the caller; an own upload needs an approved version.
func (h *H) setMyPack(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Name string `json:"name"`
	}
	if err := readJSON(r, &in); err != nil {
		writeErr(w, 400, "invalid Team: "+err.Error())
		return
	}
	err := h.inWS(r, func(tx *store.Tx, ws, user uuid.UUID) error {
		if slices.Contains(pack.ShippedNames(h.Shipped), in.Name) {
			p, err := pack.EnsureShipped(r.Context(), tx, h.Blobs, h.Shipped, in.Name)
			if err != nil {
				return err
			}
			if err := approve(r, tx, user, p); err != nil {
				return err
			}
		} else {
			own, err := tx.Q.ListOwnPacks(r.Context(), db.ListOwnPacksParams{WorkspaceID: ws, UserID: &user})
			if err != nil {
				return err
			}
			if !slices.ContainsFunc(own, func(p db.FieldPack) bool { return p.Name == in.Name }) {
				return cardInputError("unknown Team")
			}
			_, err = tx.Q.NewestApprovedVersion(r.Context(), db.NewestApprovedVersionParams{WorkspaceID: ws, Name: in.Name, UserID: user})
			if errors.Is(err, pgx.ErrNoRows) {
				return errApproveTeam
			}
			if err != nil {
				return err
			}
		}
		return tx.Q.SetUserPack(r.Context(), db.SetUserPackParams{PackName: in.Name, ID: user})
	})
	if !answer(w, r, err) {
		return
	}
	writeJSON(w, 200, map[string]string{"pack_name": in.Name})
}

// uploadPack stores the caller's own pack archive, sent as the raw body.
func (h *H) uploadPack(w http.ResponseWriter, r *http.Request) {
	// The bounds middleware has already buffered the body and refused one
	// over pack.MaxArchive with 413.
	z, err := io.ReadAll(http.MaxBytesReader(w, r.Body, pack.MaxArchive))
	if err != nil {
		writeErr(w, 413, "request body exceeds endpoint limit")
		return
	}
	p, err := pack.Parse(z)
	if err != nil {
		writeErr(w, 400, err.Error())
		return
	}
	if slices.Contains(pack.ShippedNames(h.Shipped), p.Manifest.Name) {
		writeErr(w, 409, "a Team Lykeion ships already has this name")
		return
	}
	var row db.FieldPack
	err = h.inWS(r, func(tx *store.Tx, ws, user uuid.UUID) error {
		// Serialises uploads in the workspace so two people cannot both
		// claim a new name between the check and the insert.
		if err := tx.Q.LockWorkspaceEvents(r.Context(), ws); err != nil {
			return err
		}
		taken, err := tx.Q.PackNameTaken(r.Context(), db.PackNameTakenParams{WorkspaceID: ws, Name: p.Manifest.Name, UserID: user})
		if err != nil {
			return err
		}
		if taken {
			return conflict("another Team already has this name")
		}
		if row, err = pack.Insert(r.Context(), tx, h.Blobs, z, p, &user); err != nil {
			if isUnique(err) {
				return conflict("this version of the Team is already uploaded")
			}
			return err
		}
		return tx.Emit(r.Context(), "pack.uploaded", nil, store.User(user),
			map[string]any{"pack_id": row.ID, "name": row.Name, "version": row.Version, "sha256": row.Sha256})
	})
	if !answer(w, r, err) {
		return
	}
	writeJSON(w, 201, map[string]any{"id": row.ID, "name": row.Name, "version": row.Version, "sha256": row.Sha256})
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

// cardPack resolves a new card's pack: the newest version of the creator's
// Team they approved. A creator with no Team gets a packless card.
func cardPack(r *http.Request, tx *store.Tx, user uuid.UUID, startKind string) (*db.FieldPack, int32, error) {
	u, err := tx.Q.GetUser(r.Context(), user)
	if err != nil {
		return nil, 0, err
	}
	if u.PackName == "" {
		if startKind != "" {
			return nil, 0, cardInputError("start_kind needs a Team")
		}
		return nil, board.DefaultCap, nil
	}
	p, err := tx.Q.NewestApprovedVersion(r.Context(), db.NewestApprovedVersionParams{WorkspaceID: tx.WS, Name: u.PackName, UserID: user})
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, 0, errApproveTeam
	}
	if err != nil {
		return nil, 0, err
	}
	var m pack.Manifest
	if err := json.Unmarshal(p.Manifest, &m); err != nil {
		return nil, 0, err
	}
	if !slices.Contains(m.StartKinds, startKind) {
		return nil, 0, cardInputError("your Team starts studies from: " + strings.Join(m.StartKinds, ", "))
	}
	if m.Cap > 0 {
		return &p, m.Cap, nil
	}
	return &p, board.DefaultCap, nil
}

// moveCardPack moves a card to another approved version of its pack. Only
// the card's owner may, and the move is recorded.
func (h *H) moveCardPack(w http.ResponseWriter, r *http.Request) {
	id, ok := cardID(w, r)
	if !ok {
		return
	}
	var in struct {
		PackID uuid.UUID `json:"pack_id"`
	}
	if err := readJSON(r, &in); err != nil {
		writeErr(w, 400, "invalid pack: "+err.Error())
		return
	}
	var c db.Card
	err := h.inWS(r, func(tx *store.Tx, ws, user uuid.UUID) error {
		var err error
		if c, err = tx.Q.GetCard(r.Context(), db.GetCardParams{WorkspaceID: ws, ID: id}); err != nil {
			return err
		}
		if c.OwnerID != user {
			return forbidden("only the study's owner may change its Team version")
		}
		if c.PackID == nil {
			return conflict("this study has no Team")
		}
		from, err := tx.Q.GetFieldPack(r.Context(), db.GetFieldPackParams{WorkspaceID: ws, ID: *c.PackID})
		if err != nil {
			return err
		}
		to, err := visiblePack(r, tx, user, in.PackID)
		if err != nil {
			return err
		}
		if to.Name != from.Name || to.ID == from.ID {
			return conflict("choose another version of the study's Team")
		}
		ok, err := tx.Q.IsFieldPackApprovedBy(r.Context(), db.IsFieldPackApprovedByParams{WorkspaceID: ws, ID: to.ID, UserID: user})
		if err != nil {
			return err
		}
		if !ok {
			return conflict("approve that version of your Team first")
		}
		if err := tx.Q.SetCardPack(r.Context(), db.SetCardPackParams{PackID: &to.ID, WorkspaceID: ws, CardID: id}); err != nil {
			return err
		}
		c.PackID = &to.ID
		return tx.Emit(r.Context(), "card.pack_moved", &id, store.User(user), map[string]any{"from": from.ID, "to": to.ID})
	})
	if !answer(w, r, err) {
		return
	}
	writeJSON(w, 200, c)
}
