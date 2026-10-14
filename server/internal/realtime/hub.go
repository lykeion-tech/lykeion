// Package realtime streams a workspace's events to WebSocket clients. The
// event table is what clients receive; Redis only wakes sockets up early.
package realtime

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"sync"
	"time"

	"github.com/google/uuid"
	"github.com/gorilla/websocket"
	"github.com/jackc/pgx/v5"
	"github.com/redis/go-redis/v9"

	"lykeion/server/internal/middleware"
	"lykeion/server/internal/store"
	db "lykeion/server/pkg/db/generated"
)

const (
	page         = 500
	flushTimeout = 2 * time.Second
	redisTimeout = 250 * time.Millisecond
)

type Hub struct {
	Store *store.Store
	Redis *redis.Client // nil: polling only
	Poll  time.Duration

	mu      sync.Mutex
	closed  bool
	clients map[*websocket.Conn]context.CancelFunc
	active  sync.WaitGroup
}

var upgrader = websocket.Upgrader{} // Default origin protection requires the same host.
func channel(ws uuid.UUID) string   { return "lyk:ws:" + ws.String() }

// OwnerChannel wakes the daemons owned by a card's responsible user.
func OwnerChannel(owner uuid.UUID) string { return "lyk:owner:" + owner.String() }

// Publish wakes clients only after the store has committed their events.
func (h *Hub) Publish(ctx context.Context, evs []db.Event) {
	if h.Redis == nil {
		return
	}
	done := map[string]bool{}
	for _, e := range evs {
		chans := []string{channel(e.WorkspaceID)}
		if e.Type == "agent_run.queued" {
			var payload struct {
				OwnerID uuid.UUID `json:"owner_id"`
			}
			if json.Unmarshal(e.Payload, &payload) == nil && payload.OwnerID != uuid.Nil {
				chans = append(chans, OwnerChannel(payload.OwnerID))
			}
		}
		for _, ch := range chans {
			if done[ch] {
				continue
			}
			done[ch] = true
			if err := h.Redis.Publish(ctx, ch, e.ID).Err(); err != nil {
				slog.Warn("realtime wake-up not sent; clients fall back to polling", "err", err)
			}
		}
	}
}

// Close stops active sockets and waits for their readers and subscriptions.
// HTTP servers must call it when shutting down, since upgrades are hijacked.
func (h *Hub) Close() error {
	h.mu.Lock()
	h.closed = true
	for c, cancel := range h.clients {
		cancel()
		c.Close()
	}
	h.mu.Unlock()
	h.active.Wait()
	return nil
}

func (h *Hub) register(c *websocket.Conn, cancel context.CancelFunc) bool {
	h.mu.Lock()
	defer h.mu.Unlock()
	if h.closed {
		return false
	}
	if h.clients == nil {
		h.clients = map[*websocket.Conn]context.CancelFunc{}
	}
	h.clients[c] = cancel
	h.active.Add(1)
	return true
}

func (h *Hub) unregister(c *websocket.Conn) {
	h.mu.Lock()
	delete(h.clients, c)
	h.mu.Unlock()
	h.active.Done()
}

type clientMsg struct {
	Type      string    `json:"type"`
	Workspace uuid.UUID `json:"workspace"`
	Cursor    int64     `json:"cursor"`
}
type serverMsg struct {
	Type  string    `json:"type"`
	Event *db.Event `json:"event,omitempty"`
	Error string    `json:"error,omitempty"`
}

func (h *Hub) poll() time.Duration {
	if h.Poll > 0 {
		return h.Poll
	}
	return 2 * time.Second
}

// ServeDaemonWS sends owner-level queue hints. The daemon still polls for work.
func (h *Hub) ServeDaemonWS(w http.ResponseWriter, r *http.Request) {
	user := middleware.IdentityFrom(r.Context()).UserID
	conn, err := upgrader.Upgrade(w, r, nil)
	if err != nil {
		return
	}
	ctx, cancel := context.WithCancel(r.Context())
	if !h.register(conn, cancel) {
		cancel()
		conn.Close()
		return
	}
	defer h.unregister(conn)
	defer cancel()
	defer conn.Close()
	conn.SetReadLimit(4096)
	readerDone := make(chan struct{})
	go func() {
		defer close(readerDone)
		for {
			if _, _, err := conn.ReadMessage(); err != nil {
				cancel()
				return
			}
		}
	}()
	defer func() { cancel(); conn.Close(); <-readerDone }()
	var wake <-chan *redis.Message
	if h.Redis != nil {
		opt := *h.Redis.Options()
		opt.DialTimeout = redisTimeout
		opt.ReadTimeout = redisTimeout
		opt.WriteTimeout = redisTimeout
		opt.ContextTimeoutEnabled = true
		opt.MaxRetries = -1
		rd := redis.NewClient(&opt)
		defer rd.Close()
		subCtx, stop := context.WithTimeout(ctx, redisTimeout)
		ps := rd.Subscribe(subCtx, OwnerChannel(user))
		defer ps.Close()
		if _, err := ps.ReceiveTimeout(subCtx, redisTimeout); err == nil {
			wake = ps.Channel()
		}
		stop()
	}
	// This first frame acknowledges that the subscription is active, allowing
	// callers to enqueue without guessing how long Redis setup takes.
	conn.SetWriteDeadline(time.Now().Add(flushTimeout))
	if err := conn.WriteJSON(map[string]string{"type": "ready"}); err != nil {
		return
	}
	for {
		select {
		case <-ctx.Done():
			return
		case _, ok := <-wake:
			if !ok {
				wake = nil
				continue
			}
			conn.SetWriteDeadline(time.Now().Add(flushTimeout))
			if err := conn.WriteJSON(map[string]string{"type": "agent_run_available"}); err != nil {
				return
			}
		}
	}
}

func (h *Hub) ServeWS(w http.ResponseWriter, r *http.Request) {
	user := middleware.IdentityFrom(r.Context()).UserID
	conn, err := upgrader.Upgrade(w, r, nil)
	if err != nil {
		return
	}
	ctx, cancel := context.WithCancel(r.Context())
	if !h.register(conn, cancel) {
		cancel()
		conn.Close()
		return
	}
	defer h.unregister(conn)
	defer cancel()
	defer conn.Close()
	conn.SetReadLimit(4096)
	conn.SetReadDeadline(time.Now().Add(10 * time.Second))
	var sub clientMsg
	if err := conn.ReadJSON(&sub); err != nil || sub.Type != "subscribe" || sub.Cursor < 0 || sub.Workspace == uuid.Nil {
		return
	}
	conn.SetReadDeadline(time.Time{})
	readerDone := make(chan struct{})
	go func() {
		defer close(readerDone)
		for {
			if _, _, err := conn.ReadMessage(); err != nil {
				cancel()
				return
			}
		}
	}()
	// Closing the connection unblocks the reader before waiting for its exit.
	defer func() { cancel(); conn.Close(); <-readerDone }()

	var wake <-chan *redis.Message
	if h.Redis != nil {
		// PubSub's initial dial and handshake need socket timeouts as well as a
		// context deadline; a reachable but silent Redis must not stall replay.
		opt := *h.Redis.Options()
		opt.DialTimeout = redisTimeout
		opt.ReadTimeout = redisTimeout
		opt.WriteTimeout = redisTimeout
		opt.ContextTimeoutEnabled = true
		opt.MaxRetries = -1
		rd := redis.NewClient(&opt)
		defer rd.Close()
		attempt, stop := context.WithTimeout(ctx, redisTimeout)
		ps := rd.Subscribe(attempt, channel(sub.Workspace))
		defer ps.Close()
		if _, err := ps.ReceiveTimeout(attempt, redisTimeout); err == nil {
			wake = ps.Channel()
		}
		stop()
	}
	cursor := sub.Cursor
	tick := time.NewTicker(h.poll())
	defer tick.Stop()
	for {
		member, err := h.flush(ctx, conn, sub.Workspace, user, &cursor)
		if !member {
			conn.SetWriteDeadline(time.Now().Add(flushTimeout))
			conn.WriteJSON(serverMsg{Type: "error", Error: "workspace not found"})
			return
		}
		if err != nil {
			return
		}
		select {
		case <-ctx.Done():
			return
		case _, ok := <-wake:
			if !ok {
				wake = nil
			}
		case <-tick.C:
		}
	}
}

var errNotMember = errors.New("not a member")

// Each page holds the member's SHARE lock through its writes. Removal cannot
// commit between authorization and delivery. A single deadline bounds the
// entire flush, including all pages, so slow peers cannot prolong the lock.
func (h *Hub) flush(ctx context.Context, conn *websocket.Conn, ws, user uuid.UUID, cursor *int64) (bool, error) {
	ctx, cancel := context.WithTimeout(ctx, flushTimeout)
	defer cancel()
	deadline, _ := ctx.Deadline()
	conn.SetWriteDeadline(deadline)
	for {
		count := 0
		err := h.Store.InWorkspace(ctx, ws, user, func(tx *store.Tx) error {
			if _, err := tx.Q.LockMember(ctx, db.LockMemberParams{WorkspaceID: ws, UserID: user}); err != nil {
				if errors.Is(err, pgx.ErrNoRows) {
					return errNotMember
				}
				return err
			}
			events, err := tx.Q.EventsAfter(ctx, db.EventsAfterParams{WorkspaceID: ws, After: *cursor, Lim: page})
			if err != nil {
				return err
			}
			count = len(events)
			for i := range events {
				if err := ctx.Err(); err != nil {
					return err
				}
				if err := conn.WriteJSON(serverMsg{Type: "event", Event: &events[i]}); err != nil {
					return err
				}
				*cursor = events[i].ID
			}
			return nil
		})
		if errors.Is(err, errNotMember) {
			return false, nil
		}
		if err != nil {
			return true, err
		}
		if count < page {
			return true, nil
		}
	}
}
