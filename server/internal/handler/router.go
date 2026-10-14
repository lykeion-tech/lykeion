package handler

import (
	"net/http"

	"github.com/go-chi/chi/v5"
	chimw "github.com/go-chi/chi/v5/middleware"

	"lykeion/server/internal/middleware"
)

func NewRouter(h *H) http.Handler {
	r := chi.NewRouter()
	r.Use(chimw.RequestID, chimw.Recoverer, middleware.RequestBounds)

	r.Get("/health", func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(204) })
	r.Get("/readyz", h.readyz)
	r.Post("/auth/send-code", h.sendCode)
	r.Post("/auth/verify-code", h.verifyCode)
	r.Post("/auth/logout", h.logout)
	r.Get("/auth/google", h.googleStart)
	r.Get("/auth/google/callback", h.googleCallback)

	r.Route("/api/role", func(r chi.Router) {
		r.Use(middleware.AgentRunAuth(h.Store))
		r.Post("/records", h.createRecord)
		r.Get("/records", h.listRecords)
		r.Post("/blobs", h.putBlob)
		r.Get("/blobs/{sha}", h.getBlob)
	})

	r.Group(func(r chi.Router) {
		r.Use(middleware.Auth(h.Store, h.JWT))
		if h.Hub != nil {
			r.Get("/ws", h.Hub.ServeWS)
		}
		r.Route("/api/daemon", func(r chi.Router) {
			r.Use(personalOnly)
			r.Get("/workspaces", h.listWorkspaces)
			if h.Hub != nil {
				r.Get("/ws", h.Hub.ServeDaemonWS)
			}
			r.Post("/register", h.daemonRegister)
			r.Post("/heartbeat", h.daemonHeartbeat)
			r.Post("/agent-runs/claim", h.daemonClaim)
			r.Post("/agent-runs/{id}/prepare-lease", h.daemonPrepare())
			r.Post("/agent-runs/{id}/start", h.daemonStart())
			r.Post("/agent-runs/{id}/messages", h.daemonMessages())
			r.Post("/agent-runs/{id}/complete", h.daemonSettle(true))
			r.Post("/agent-runs/{id}/fail", h.daemonSettle(false))
			r.Get("/agent-runs/{id}/status", h.daemonStatus)
			r.Post("/agent-runs/{id}/cancel-ack", h.daemonCancelAck())
		})
		// Cancel opens its own service transaction; keep it outside Scope's transaction.
		r.Post("/api/agent-runs/{id}/cancel", h.cancelAgentRun)
		r.Get("/api/me", h.me)
		r.Patch("/api/me", h.updateMe)
		r.Post("/api/cli-token", h.cliToken)
		r.Get("/api/tokens", h.listTokens)
		r.Post("/api/tokens", h.createToken)
		r.Delete("/api/tokens/{id}", h.revokeToken)

		r.Get("/api/workspaces", h.listWorkspaces)
		r.Post("/api/workspaces", h.createWorkspace)
		r.Post("/api/invitations/{token}/accept", h.acceptInvitation)
		r.Route("/api/workspaces/{id}", func(r chi.Router) {
			r.Use(middleware.Scope(h.Store, middleware.FromURL("id")))
			r.Get("/", h.getWorkspace)
			r.With(middleware.RequireAdmin).Patch("/", h.renameWorkspace)
			r.With(middleware.RequireOwner).Delete("/", h.deleteWorkspace)
			r.Get("/members", h.listMembers)
			r.With(middleware.RequireAdmin).Patch("/members/{user}", h.setMemberRole)
			r.With(middleware.RequireAdmin).Delete("/members/{user}", h.removeMember)
			r.With(middleware.RequireAdmin).Get("/invitations", h.listInvitations)
			r.With(middleware.RequireAdmin).Post("/invitations", h.createInvitation)
			r.With(middleware.RequireAdmin).Delete("/invitations/{inv}", h.revokeInvitation)
			r.Post("/leave", h.leave)
		})

		r.Group(func(r chi.Router) {
			r.Use(middleware.Scope(h.Store, middleware.FromHeader))
			r.Get("/api/runtimes", h.listRuntimes)
			r.Patch("/api/runtimes/{id}", h.patchRuntime)
			r.Delete("/api/runtimes/{id}", h.deleteRuntime)
			r.Get("/api/agent-runs/{id}", h.getAgentRun)
			r.Get("/api/agent-runs/{id}/messages", h.agentRunMessages)
			r.Get("/api/cards", h.listCards)
			r.Post("/api/cards", h.createCard)
			r.Get("/api/cards/{id}", h.getCard)
			r.Get("/api/cards/{id}/comments", h.listComments)
			r.Post("/api/cards/{id}/comments", h.createComment)
			r.Get("/api/cards/{id}/events", h.cardEvents)
			r.Get("/api/inbox", h.listInbox)
			r.Post("/api/inbox/{id}/read", h.readInbox)
		})
	})
	return r
}

func (h *H) readyz(w http.ResponseWriter, r *http.Request) {
	if err := h.Store.Pool.Ping(r.Context()); err != nil {
		writeErr(w, 503, "database unavailable")
		return
	}
	if h.Limit.R != nil {
		if err := h.Limit.R.Ping(r.Context()).Err(); err != nil {
			writeErr(w, 503, "redis unavailable")
			return
		}
	}
	w.WriteHeader(204)
}
