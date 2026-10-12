package control

import (
	"context"
	"encoding/json"
	"sync"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/lykeion-ia/lykeion-redesign/backend/internal/store"
)

// Notifications are coalesced hints only. Every delivery still reads committed
// rows and checks current authorization; periodic catch-up survives hint loss.
type wakeHub struct {
	mu        sync.Mutex
	listeners map[chan struct{}]struct{}
}

func (h *wakeHub) subscribe() (chan struct{}, func()) {
	h.mu.Lock()
	defer h.mu.Unlock()
	if h.listeners == nil {
		h.listeners = make(map[chan struct{}]struct{})
	}
	c := make(chan struct{}, 1)
	h.listeners[c] = struct{}{}
	return c, func() { h.mu.Lock(); delete(h.listeners, c); h.mu.Unlock() }
}
func (h *wakeHub) wake() {
	h.mu.Lock()
	defer h.mu.Unlock()
	for c := range h.listeners {
		select {
		case c <- struct{}{}:
		default:
		}
	}
}
func (s *Server) startHints() {
	s.hintsOnce.Do(func() {
		ctx, cancel := context.WithCancel(context.Background())
		s.hintsCancel = cancel
		s.hintsDone = make(chan struct{})
		go func() {
			defer close(s.hintsDone)
			for ctx.Err() == nil {
				conn, err := pgx.ConnectConfig(ctx, s.DB.Pool.Config().ConnConfig.Copy())
				if err == nil {
					_, err = conn.Exec(ctx, "LISTEN lykeion_events")
					for err == nil {
						_, err = conn.WaitForNotification(ctx)
						if err == nil {
							s.hints.wake()
						}
					}
					conn.Close(context.Background())
				}
				select {
				case <-ctx.Done():
					return
				case <-time.After(time.Second):
				}
			}
		}()
	})
}
func (s *Server) Close() {
	if s.hintsCancel != nil {
		s.hintsCancel()
		<-s.hintsDone
	}
}

func (s *Server) openLease(ctx context.Context, workspace, user, token string, epoch int64) (string, error) {
	tx, err := s.DB.Pool.Begin(ctx)
	if err != nil {
		return "", err
	}
	defer tx.Rollback(ctx)
	// Match command lock order: workspace before account.
	if _, err = tx.Exec(ctx, "SELECT id FROM workspaces WHERE id=$1 FOR UPDATE", workspace); err != nil {
		return "", err
	}
	// Serialize per-account limits across instances and workspaces.
	if _, err = tx.Exec(ctx, "SELECT id FROM users WHERE id=$1 FOR UPDATE", user); err != nil {
		return "", err
	}
	if _, err = tx.Exec(ctx, "DELETE FROM realtime_leases WHERE expires_at<=clock_timestamp()"); err != nil {
		return "", err
	}
	var n int
	if err = tx.QueryRow(ctx, "SELECT count(*) FROM realtime_leases WHERE user_id=$1", user).Scan(&n); err != nil {
		return "", err
	}
	if n >= 8 {
		return "", errConnectionLimit
	}
	var workspaceCount int
	if err = tx.QueryRow(ctx, "SELECT count(*) FROM realtime_leases WHERE workspace_id=$1", workspace).Scan(&workspaceCount); err != nil {
		return "", err
	}
	if workspaceCount >= 256 {
		return "", errConnectionLimit
	}
	id := store.ID("live")
	_, err = tx.Exec(ctx, "INSERT INTO realtime_leases(id,workspace_id,user_id,session_digest,auth_epoch,expires_at) VALUES($1,$2,$3,$4,$5,clock_timestamp()+interval '30 seconds')", id, workspace, user, store.Hash(token), epoch)
	if err != nil {
		return "", err
	}
	return id, tx.Commit(ctx)
}
func (s *Server) presenceSnapshot(ctx context.Context, workspace string, epoch int64) (json.RawMessage, error) {
	rows, err := s.DB.Pool.Query(ctx, `SELECT l.user_id,l.id,to_char(l.presence_until AT TIME ZONE 'UTC','YYYY-MM-DD"T"HH24:MI:SS.US"Z"') FROM realtime_leases l JOIN memberships m ON m.workspace_id=l.workspace_id AND m.user_id=l.user_id JOIN sessions s ON s.digest=l.session_digest WHERE l.workspace_id=$1 AND l.auth_epoch=$2 AND l.expires_at>clock_timestamp() AND l.presence_until>clock_timestamp() AND m.state='active' AND s.expires_at>clock_timestamp() ORDER BY l.id`, workspace, epoch)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	items := []any{}
	for rows.Next() {
		var user, id, expires string
		if err = rows.Scan(&user, &id, &expires); err != nil {
			return nil, err
		}
		items = append(items, map[string]any{"user_id": user, "session_id": id, "subject_ref": ref{Kind: "workspace", ID: workspace, Revision: "1"}, "state": "viewing", "expires_at": expires})
	}
	if rows.Err() != nil {
		return nil, rows.Err()
	}
	return json.Marshal(map[string]any{"participants": items})
}
