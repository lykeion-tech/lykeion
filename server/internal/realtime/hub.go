// Package realtime streams a workspace's events to WebSocket clients.
package realtime

import (
	"context"
	"net/http"
	"time"

	"github.com/redis/go-redis/v9"

	"lykeion/server/internal/store"
	db "lykeion/server/pkg/db/generated"
)

type Hub struct {
	Store *store.Store
	Redis *redis.Client
	Poll  time.Duration
}

func (h *Hub) Publish(ctx context.Context, evs []db.Event) {}

func (h *Hub) ServeWS(w http.ResponseWriter, r *http.Request) { http.Error(w, "not yet", 501) }
