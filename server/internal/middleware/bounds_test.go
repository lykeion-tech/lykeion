package middleware

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestRequestBoundsOnlyExemptsWebSocketUpgrades(t *testing.T) {
	for _, tc := range []struct {
		name, method, path, upgrade string
		wantDeadline                bool
	}{
		{"workspace upgrade", http.MethodGet, "/ws", "websocket", false},
		{"daemon upgrade", http.MethodGet, "/api/daemon/ws", "websocket", false},
		{"ordinary daemon GET", http.MethodGet, "/api/daemon/ws", "", true},
		{"incomplete daemon upgrade", http.MethodGet, "/api/daemon/ws", "websocket", true},
		{"ordinary daemon POST", http.MethodPost, "/api/daemon/ws", "websocket", true},
		{"ordinary API", http.MethodGet, "/api/workspaces", "", true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			seen := false
			next := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				seen = true
				_, hasDeadline := r.Context().Deadline()
				if hasDeadline != tc.wantDeadline {
					t.Errorf("deadline=%v, want %v", hasDeadline, tc.wantDeadline)
				}
				w.WriteHeader(http.StatusNoContent)
			})
			req := httptest.NewRequest(tc.method, tc.path, nil)
			if tc.upgrade != "" {
				req.Header.Set("Upgrade", tc.upgrade)
				if !tc.wantDeadline {
					req.Header.Set("Connection", "Upgrade")
				}
			}
			res := httptest.NewRecorder()
			RequestBounds(next).ServeHTTP(res, req)
			if !seen || res.Code != http.StatusNoContent {
				t.Fatalf("next=%v status=%d", seen, res.Code)
			}
		})
	}
}
