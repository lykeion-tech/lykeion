package handler

import (
	"net/http"

	"github.com/go-chi/chi/v5"
	chimw "github.com/go-chi/chi/v5/middleware"

	"lykeion/server/internal/middleware"
	"lykeion/server/internal/service"
)

func NewRouter(h *H) http.Handler {
	r := chi.NewRouter()
	r.Use(chimw.RequestID, chimw.Recoverer, middleware.RequestBounds)

	r.Post("/api/webhooks/autoresearch/{token}", h.autoresearchWebhook)
	r.Get("/health", func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(204) })
	r.Get("/readyz", h.readyz)
	r.Post("/auth/send-code", h.sendCode)
	r.Post("/auth/verify-code", h.verifyCode)
	r.Post("/auth/logout", h.logout)
	r.Get("/auth/google", h.googleStart)
	r.Get("/auth/google/callback", h.googleCallback)

	if h.Sources != nil {
		r.With(middleware.AgentRunAuth(h.Store), middleware.RequireScope(service.ScopeRole, service.ScopeReport)).Handle("/mcp/sources", h.Sources)
	}

	r.Route("/api/role", func(r chi.Router) {
		r.Use(middleware.AgentRunAuth(h.Store))
		r.Group(func(r chi.Router) {
			r.Use(middleware.RequireScope(service.ScopeRole))
			r.Post("/records", h.createRecord)
			r.Get("/records", h.listRecords)
			r.Post("/steps", h.postStep)
			r.Post("/done", h.postDone)
			r.Get("/calls", h.listCalls)
			r.Post("/blobs", h.putBlob)
			r.Get("/blobs/{sha}", h.getBlob)
		})
		// Only the daemon's tool door holds a call token; the agent's cannot record.
		r.With(middleware.RequireScope(service.ScopeCall)).Post("/calls", h.postCall)
		r.With(middleware.RequireScope(service.ScopeReport)).Post("/report", h.autoresearchReport)
		r.With(middleware.RequireScope(service.ScopeCardCreate)).Post("/cards", h.autoresearchCard)

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
		r.Post("/api/autoresearch/{id}/run", h.runAutoresearch)
		r.Post("/api/cards/{id}/stop", h.stopCard)
		r.Post("/api/cards/{id}/resume", h.resumeCard)

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
			// The logo routes check membership themselves, outside Scope's
			// transaction, because they call storage.
			r.Put("/logo", h.putLogo)
			r.Get("/logo", h.getLogo)
			r.Group(func(r chi.Router) {
				r.Use(middleware.Scope(h.Store, middleware.FromURL("id")))
				r.Get("/", h.getWorkspace)
				r.With(middleware.RequireAdmin).Patch("/", h.updateWorkspace)
				r.With(middleware.RequireOwner).Delete("/", h.deleteWorkspace)
				r.With(middleware.RequireOwner).Patch("/settings", h.setSettings)
				r.Get("/addons", h.listAddons)
				r.With(middleware.RequireOwner).Patch("/addons/{column}", h.setAddon)
				r.Get("/members", h.listMembers)
				r.With(middleware.RequireAdmin).Patch("/members/{user}", h.setMemberRole)
				r.With(middleware.RequireAdmin).Delete("/members/{user}", h.removeMember)
				r.With(middleware.RequireAdmin).Get("/invitations", h.listInvitations)
				r.With(middleware.RequireAdmin).Post("/invitations", h.createInvitation)
				r.With(middleware.RequireAdmin).Delete("/invitations/{inv}", h.revokeInvitation)
				r.Post("/leave", h.leave)
			})
		})

		r.Group(func(r chi.Router) {
			r.Use(middleware.Scope(h.Store, middleware.FromHeader))
			r.Get("/api/autoresearch", h.listAutoresearches)
			r.Post("/api/autoresearch", h.createAutoresearch)
			r.Get("/api/autoresearch/{id}", h.getAutoresearch)
			r.Patch("/api/autoresearch/{id}", h.updateAutoresearch)
			r.Delete("/api/autoresearch/{id}", h.deleteAutoresearch)
			r.Post("/api/autoresearch/{id}/pause", h.setAutoresearchStatus("paused"))
			r.Post("/api/autoresearch/{id}/resume", h.setAutoresearchStatus("active"))
			r.Get("/api/autoresearch/{id}/runs", h.listAutoresearchRuns)
			r.Post("/api/autoresearch/{id}/triggers", h.createTrigger)
			r.Patch("/api/autoresearch/{id}/triggers/{trigger}", h.setTriggerEnabled)
			r.Delete("/api/autoresearch/{id}/triggers/{trigger}", h.deleteTrigger)
			r.Put("/api/autoresearch/{id}/collaborators/{user}", h.people(true, true))
			r.Delete("/api/autoresearch/{id}/collaborators/{user}", h.people(true, false))
			r.Put("/api/autoresearch/{id}/subscribers/{user}", h.people(false, true))
			r.Delete("/api/autoresearch/{id}/subscribers/{user}", h.people(false, false))

			r.Get("/api/runtimes", h.listRuntimes)
			r.Patch("/api/runtimes/{id}", h.patchRuntime)
			r.Delete("/api/runtimes/{id}", h.deleteRuntime)
			r.Get("/api/agent-runs/{id}", h.getAgentRun)
			r.Get("/api/agent-runs/{id}/messages", h.agentRunMessages)
			r.Get("/api/researches", h.listResearches)
			r.Post("/api/researches", h.createResearch)
			r.Get("/api/researches/{id}", h.getResearch)
			r.Patch("/api/researches/{id}", h.updateResearch)
			r.Get("/api/agents", h.listAgents)
			r.Post("/api/agents", h.createAgent)
			r.Get("/api/agents/{id}", h.getAgent)
			r.Patch("/api/agents/{id}", h.updateAgent)
			r.Post("/api/agents/{id}/archive", h.archiveAgent)
			r.Patch("/api/cards/{id}/properties", h.updateCardProperties)
			r.Get("/api/cards", h.listCards)
			r.Post("/api/cards", h.createCard)
			r.Get("/api/packs", h.packVersions)
			r.Post("/api/packs", h.uploadPack)
			r.Post("/api/packs/{id}/approve", h.approvePack)
			r.Get("/api/teams", h.listTeams)
			r.Post("/api/teams", h.createTeam)
			r.Get("/api/teams/{id}", h.getTeam)
			r.Post("/api/teams/{id}/add", h.addTeam)
			r.Get("/api/cards/{id}", h.getCard)
			r.Patch("/api/cards/{id}", h.editCard)
			r.Patch("/api/cards/{id}/teams", h.setCardTeams)
			r.Delete("/api/cards/{id}", h.deleteCard)
			r.Get("/api/cards/{id}/comments", h.listComments)
			r.Post("/api/cards/{id}/comments", h.createComment)
			r.Get("/api/cards/{id}/events", h.cardEvents)
			r.Get("/api/cards/{id}/runs", h.cardRuns)
			r.Get("/api/cards/{id}/records", h.cardRecords)

			r.Get("/api/connectors", h.listConnectors)
			r.With(middleware.RequireOwner).Put("/api/connectors/{name}", h.setConnector)
			r.Get("/api/skills", h.listSkills)

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
