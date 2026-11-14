package handler

import (
	"net/http"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"

	"lykeion/server/internal/auth"
	"lykeion/server/internal/middleware"
	db "lykeion/server/pkg/db/generated"
)

const tokenTTL = 90 * 24 * time.Hour

func (h *H) createToken(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Name string `json:"name"`
	}
	if err := readJSON(r, &in); err != nil || strings.TrimSpace(in.Name) == "" {
		writeErr(w, 400, "name required")
		return
	}
	plain, hash, shown := auth.NewToken(auth.PersonalPrefix)
	t, err := h.Store.Global().CreateToken(r.Context(), db.CreateTokenParams{
		UserID: middleware.IdentityFrom(r.Context()).UserID, TokenHash: hash, Prefix: shown,
		Name: in.Name, ExpiresAt: time.Now().Add(tokenTTL),
	})
	if err != nil {
		fail(w, r, err)
		return
	}
	writeJSON(w, 201, map[string]any{"id": t.ID, "token": plain, "prefix": t.Prefix, "name": t.Name, "expires_at": t.ExpiresAt})
}

func (h *H) listTokens(w http.ResponseWriter, r *http.Request) {
	ts, err := h.Store.Global().ListTokens(r.Context(), middleware.IdentityFrom(r.Context()).UserID)
	if err != nil {
		fail(w, r, err)
		return
	}
	if ts == nil {
		ts = []db.ListTokensRow{}
	}
	writeJSON(w, 200, ts)
}

func (h *H) revokeToken(w http.ResponseWriter, r *http.Request) {
	id, err := uuid.Parse(chi.URLParam(r, "id"))
	if err != nil {
		writeErr(w, 404, "not found")
		return
	}
	n, err := h.Store.Global().RevokeToken(r.Context(), db.RevokeTokenParams{ID: id, UserID: middleware.IdentityFrom(r.Context()).UserID})
	if err != nil {
		fail(w, r, err)
		return
	}
	if n == 0 {
		writeErr(w, 404, "not found")
		return
	}
	w.WriteHeader(204)
}
