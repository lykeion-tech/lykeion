package middleware

import (
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
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

// The raw upload must authenticate before reading; every other route keeps the JSON bound.
func TestRequestBoundsStreamsOnlyExactBlobPost(t *testing.T) {
	for _, tc := range []struct {
		method, path string
		stream       bool
	}{{"POST", "/api/role/blobs", true}, {"PUT", "/api/role/blobs", false}, {"POST", "/api/role/blobs/", false}, {"POST", "/api/role/records", false}} {
		body := &countingBody{Reader: strings.NewReader(strings.Repeat("x", MaxBodyBytes+1))}
		reached := false
		next := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			reached = true
			if body.read != 0 {
				t.Errorf("body read before auth: %d", body.read)
			}
			if _, ok := r.Context().Deadline(); !ok {
				t.Error("missing request deadline")
			}
			w.WriteHeader(401)
		})
		req := httptest.NewRequest(tc.method, tc.path, body)
		rec := httptest.NewRecorder()
		RequestBounds(next).ServeHTTP(rec, req)
		if tc.stream {
			if !reached || rec.Code != 401 {
				t.Fatalf("stream %s %s: %d", tc.method, tc.path, rec.Code)
			}
		} else if reached || rec.Code != 413 {
			t.Fatalf("JSON bound %s %s: %d", tc.method, tc.path, rec.Code)
		}
	}
}

type countingBody struct {
	io.Reader
	read int
}

func (b *countingBody) Read(p []byte) (int, error) {
	n, e := b.Reader.Read(p)
	b.read += n
	return n, e
}
