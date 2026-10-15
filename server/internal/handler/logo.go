package handler

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"io"
	"net/http"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"

	"lykeion/server/internal/middleware"
	"lykeion/server/internal/storage"
	"lykeion/server/internal/store"
	db "lykeion/server/pkg/db/generated"
)

const maxLogoBytes = 512 << 10

var logoTypes = map[string]bool{"image/png": true, "image/jpeg": true, "image/webp": true}

// memberTx runs fn for a member of workspace ws. The logo routes sit outside
// Scope so that storage is never called inside a transaction; admin requires an
// owner or admin and takes the workspace lock first, as Scope does for writes.
func (h *H) memberTx(r *http.Request, ws uuid.UUID, admin bool, fn func(*store.Tx) error) error {
	who := user(r)
	return h.Store.InWorkspace(r.Context(), ws, who, func(tx *store.Tx) error {
		if admin {
			if _, err := tx.Q.LockWorkspaceForMutation(r.Context(), ws); err != nil {
				return err
			}
		}
		m, err := tx.Q.LockMember(r.Context(), db.LockMemberParams{WorkspaceID: ws, UserID: who})
		if err != nil {
			return err
		}
		if admin && m.Role != "owner" && m.Role != "admin" {
			return middleware.ErrForbidden
		}
		return fn(tx)
	})
}

func logoFail(w http.ResponseWriter, r *http.Request, err error) {
	switch {
	case errors.Is(err, middleware.ErrForbidden):
		writeErr(w, 403, "admins only")
	case errors.Is(err, storage.ErrNotFound):
		writeErr(w, 404, "not found")
	default:
		fail(w, r, err)
	}
}

func logoWorkspace(w http.ResponseWriter, r *http.Request) (uuid.UUID, bool) {
	ws, err := uuid.Parse(chi.URLParam(r, "id"))
	if err != nil {
		writeErr(w, 404, "workspace not found")
		return uuid.Nil, false
	}
	return ws, true
}

// putLogo stores the body as the workspace's logo: PNG, JPEG or WebP, at most
// 512 KiB, its bytes matching its Content-Type. The blob is keyed by its hash
// under the workspace, and recorded with a blob.created event.
func (h *H) putLogo(w http.ResponseWriter, r *http.Request) {
	ws, ok := logoWorkspace(w, r)
	if !ok {
		return
	}
	// Outsiders and members get their 404 or 403 before the body is judged, and
	// nothing reaches storage for them.
	if err := h.memberTx(r, ws, true, func(*store.Tx) error { return nil }); err != nil {
		logoFail(w, r, err)
		return
	}
	typ := r.Header.Get("Content-Type")
	if !logoTypes[typ] {
		writeErr(w, 415, "logo must be image/png, image/jpeg or image/webp")
		return
	}
	data, err := io.ReadAll(io.LimitReader(r.Body, maxLogoBytes+1))
	switch {
	case err != nil:
		writeErr(w, 400, "invalid request body")
		return
	case len(data) == 0:
		writeErr(w, 400, "logo image required")
		return
	case len(data) > maxLogoBytes:
		writeErr(w, 413, "logo exceeds 512 KiB")
		return
	case http.DetectContentType(data) != typ:
		writeErr(w, 400, "logo content does not match its Content-Type")
		return
	}
	sum := sha256.Sum256(data)
	hash := hex.EncodeToString(sum[:])
	key := ws.String() + "/logo/" + hash
	if err := h.Blobs.Put(r.Context(), key, bytes.NewReader(data), int64(len(data))); err != nil {
		fail(w, r, err)
		return
	}
	who := user(r)
	err = h.memberTx(r, ws, true, func(tx *store.Tx) error {
		if err := tx.Q.SetWorkspaceLogo(r.Context(), db.SetWorkspaceLogoParams{LogoBlob: key, ID: ws}); err != nil {
			return err
		}
		if err := tx.Emit(r.Context(), "blob.created", nil, store.User(who), map[string]any{"blob_key": key, "sha256": hash, "size": len(data)}); err != nil {
			return err
		}
		return tx.Emit(r.Context(), "workspace.updated", nil, store.User(who), map[string]any{"fields": []string{"logo"}})
	})
	if err != nil {
		logoFail(w, r, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// getLogo serves the workspace's logo to its members, or 404 when it has none.
func (h *H) getLogo(w http.ResponseWriter, r *http.Request) {
	ws, ok := logoWorkspace(w, r)
	if !ok {
		return
	}
	var key string
	err := h.memberTx(r, ws, false, func(tx *store.Tx) error {
		row, err := tx.Q.GetWorkspace(r.Context(), ws)
		key = row.LogoBlob
		return err
	})
	if err == nil && key == "" {
		err = storage.ErrNotFound
	}
	if err != nil {
		logoFail(w, r, err)
		return
	}
	rc, err := h.Blobs.Get(r.Context(), key)
	if err != nil {
		logoFail(w, r, err)
		return
	}
	defer rc.Close()
	data, err := io.ReadAll(io.LimitReader(rc, maxLogoBytes+1))
	if err != nil {
		fail(w, r, err)
		return
	}
	w.Header().Set("Content-Type", http.DetectContentType(data))
	w.Header().Set("X-Content-Type-Options", "nosniff")
	http.ServeContent(w, r, "", time.Time{}, bytes.NewReader(data))
}
