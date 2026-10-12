package control

import (
	"errors"
	"github.com/jackc/pgx/v5"
	"github.com/lykeion-ia/lykeion-redesign/backend/internal/store"
	"net"
	"net/http"
	"time"
)

func (s *Server) allowRequest(w http.ResponseWriter, r *http.Request, key string, limit int) bool {
	window := time.Now().Unix() / 60
	var n int
	err := s.DB.Pool.QueryRow(r.Context(), `INSERT INTO request_limits(key,bucket,count) VALUES($1,$2,1) ON CONFLICT(key,bucket) DO UPDATE SET count=request_limits.count+1 WHERE request_limits.count<$3 RETURNING count`, store.Hash(key), window, limit).Scan(&n)
	if errors.Is(err, pgx.ErrNoRows) {
		w.Header().Set("Retry-After", "60")
		problem(w, 429, "RATE_LIMITED")
		return false
	}
	if err != nil {
		problem(w, 503, "STORE_UNAVAILABLE")
		return false
	}
	// Bounded retention; identities in this table are one-way digests.
	if n == 1 {
		s.DB.Pool.Exec(r.Context(), "DELETE FROM request_limits WHERE bucket<$1", window-1)
	}
	return true
}
func peerKey(r *http.Request) string {
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		return r.RemoteAddr
	}
	return host
}
