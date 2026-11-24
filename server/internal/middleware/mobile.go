package middleware

import (
	"context"
	"errors"
	"net/http"
	"strings"

	"github.com/go-chi/chi/v5"
	"github.com/jackc/pgx/v5"

	"lykeion/server/internal/auth"
	"lykeion/server/internal/store"
)

// Match the concrete method and entire path. All other capabilities, including
// issuing general-purpose tokens and connecting to the event socket, are denied.
var mobileRoutes = func() chi.Router {
	r := chi.NewRouter()
	for _, path := range []string{"/api/me", "/api/workspaces", "/api/researches", "/api/agents", "/api/teams", "/api/study-proposals", "/api/study-runs", "/api/study-runs/{card}/{stage}/{round}", "/api/study-runs/{card}/{stage}/{round}/outputs/{output}", "/api/mobile/study-suggestions", "/api/decisions/count", "/api/mobile/availability"} {
		r.Method(http.MethodGet, path, http.NotFoundHandler())
	}
	r.Get("/api/mobile/machines", http.NotFound)
	r.Patch("/api/mobile/machines/{id}/name", http.NotFound)
	r.Get("/api/mobile/studies", http.NotFound)
	r.Get("/api/mobile/coresearch", http.NotFound)
	r.Get("/api/mobile/coresearch/{conversation}/messages", http.NotFound)
	r.Get("/api/workspaces/{id}/members/{user}/research-profile", http.NotFound)
	r.Post("/api/coresearch/conversations", http.NotFound)
	r.Post("/api/coresearch/conversations/{conversation}/turns", http.NotFound)
	r.Post("/auth/mobile/logout", http.NotFound)
	r.Post("/api/workspaces", http.NotFound)
	r.Get("/api/me/research-profile", http.NotFound)
	r.Put("/api/me/research-profile", http.NotFound)
	r.Patch("/api/me", http.NotFound)
	r.Delete("/api/study-proposals/{id}", http.NotFound)
	r.Put("/api/study-proposals/{id}", http.NotFound)
	r.Post("/api/study-proposals/{id}/start", http.NotFound)
	return r
}()

// MobileAdmission handles native sign-in and every request carrying its bearer
// before routing, including requests to endpoints outside the human API.
func MobileAdmission(st *store.Store) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			token := strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer ")
			mobile := strings.HasPrefix(r.Header.Get("Authorization"), "Bearer "+auth.MobilePrefix)
			if !mobile {
				if strings.HasPrefix(r.URL.Path, "/auth/mobile/") {
					if r.Method != http.MethodPost || (r.URL.Path != "/auth/mobile/send-code" && r.URL.Path != "/auth/mobile/verify-code") {
						deny(w, 401, "mobile session required")
						return
					}
				}
				next.ServeHTTP(w, r)
				return
			}
			var id Identity
			err := st.Pool.QueryRow(r.Context(), `SELECT u.id,u.email FROM mobile_session s JOIN "user" u ON u.id=s.user_id WHERE s.token_hash=$1 AND s.expires_at>now()`, auth.Hash(token)).Scan(&id.UserID, &id.Email)
			if errors.Is(err, pgx.ErrNoRows) {
				deny(w, 401, "expired or revoked session")
				return
			}
			if err != nil {
				deny(w, 503, "authentication unavailable")
				return
			}
			if !mobileRoutes.Match(chi.NewRouteContext(), r.Method, r.URL.EscapedPath()) {
				deny(w, 403, "unavailable on mobile")
				return
			}
			id.Mobile = true
			next.ServeHTTP(w, r.WithContext(context.WithValue(r.Context(), identityKey, id)))
		})
	}
}
