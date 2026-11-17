package handler

import (
	"errors"
	"net/http"
	"strings"

	"github.com/jackc/pgx/v5"

	"lykeion/server/internal/jev"
	"lykeion/server/internal/middleware"
	db "lykeion/server/pkg/db/generated"
)

// maxTypesafeKey bounds what is sealed; TypeSafe keys are far shorter.
const maxTypesafeKey = 512

// putTypesafeKey seals the caller's own TypeSafe key. Jev then reviews the
// studies they own under it. The key is never returned.
func (h *H) putTypesafeKey(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Key string `json:"key"`
	}
	if err := readJSON(r, &in); err != nil || strings.TrimSpace(in.Key) == "" || len(in.Key) > maxTypesafeKey {
		writeErr(w, 400, "key required")
		return
	}
	if h.Connectors == nil || len(h.Connectors.Secret) == 0 {
		writeErr(w, 503, "this server cannot store keys: it has no LYK_SECRET_KEY")
		return
	}
	user := middleware.IdentityFrom(r.Context()).UserID
	sealed, err := jev.SealKey(h.Connectors.Secret, user, strings.TrimSpace(in.Key))
	if err != nil {
		fail(w, r, err)
		return
	}
	if err := h.Store.Global().SetUserTypesafeKey(r.Context(), db.SetUserTypesafeKeyParams{UserID: user, Ciphertext: sealed}); err != nil {
		fail(w, r, err)
		return
	}
	w.WriteHeader(204)
}

func (h *H) deleteTypesafeKey(w http.ResponseWriter, r *http.Request) {
	if err := h.Store.Global().ClearUserTypesafeKey(r.Context(), middleware.IdentityFrom(r.Context()).UserID); err != nil {
		fail(w, r, err)
		return
	}
	w.WriteHeader(204)
}

// meView is the caller's account, with whether they saved a TypeSafe key.
type meView struct {
	db.User
	TypesafeKeySet bool `json:"typesafe_key_set"`
}

func (h *H) meView(r *http.Request, u db.User) (meView, error) {
	_, err := h.Store.Global().GetUserTypesafeKey(r.Context(), u.ID)
	if errors.Is(err, pgx.ErrNoRows) {
		return meView{User: u}, nil
	}
	return meView{User: u, TypesafeKeySet: err == nil}, err
}
