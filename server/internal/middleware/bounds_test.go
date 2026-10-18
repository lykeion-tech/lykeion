package middleware

import (
	"io"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"
	"time"
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
					req.Header.Set("Sec-WebSocket-Version", "13")
					req.Header.Set("Sec-WebSocket-Key", "dGhlIHNhbXBsZSBub25jZQ==")
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

func TestDaemonMessageBodyBoundIsExact(t *testing.T) {
	const root = "/api/daemon/agent-runs/00000000-0000-0000-0000-000000000001/messages"
	for _, tc := range []struct {
		method, path string
		size, want   int
	}{
		{"POST", root, 2 << 20, 204}, {"POST", root, 32 << 20, 204}, {"POST", root, (32 << 20) + 1, 413},
		{"PUT", root, 2 << 20, 413}, {"POST", root + "/", 2 << 20, 413}, {"POST", "/api/daemon/agent-runs/not-uuid/messages", 2 << 20, 413}, {"POST", "/api/daemon/agent-runs/00000000-0000-0000-0000-000000000001/complete", 2 << 20, 413},
	} {
		t.Run(tc.method+tc.path+strconv.Itoa(tc.size), func(t *testing.T) {
			next := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				b, err := io.ReadAll(r.Body)
				if err != nil || len(b) != tc.size {
					t.Errorf("read %d %v", len(b), err)
				}
				if _, ok := r.Context().Deadline(); !ok {
					t.Error("no deadline")
				}
				w.WriteHeader(204)
			})
			rec := httptest.NewRecorder()
			RequestBounds(next).ServeHTTP(rec, httptest.NewRequest(tc.method, tc.path, strings.NewReader(strings.Repeat("x", tc.size))))
			if rec.Code != tc.want {
				t.Fatalf("status %d want %d", rec.Code, tc.want)
			}
		})
	}
}

func TestMalformedWebSocketHandshakeKeepsDeadline(t *testing.T) {
	for _, key := range []string{"", "broken", "YWJj"} {
		req := httptest.NewRequest("GET", "/api/daemon/ws", nil)
		req.Header.Set("Connection", "Upgrade")
		req.Header.Set("Upgrade", "websocket")
		req.Header.Set("Sec-WebSocket-Version", "13")
		req.Header.Set("Sec-WebSocket-Key", key)
		RequestBounds(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if _, ok := r.Context().Deadline(); !ok {
				t.Errorf("invalid key %q exempted", key)
			}
		})).ServeHTTP(httptest.NewRecorder(), req)
	}
	req := httptest.NewRequest("GET", "/ws", nil)
	req.Header.Set("Connection", "Upgrade")
	req.Header.Set("Upgrade", "websocket")
	req.Header.Set("Sec-WebSocket-Key", "dGhlIHNhbXBsZSBub25jZQ==")
	RequestBounds(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if _, ok := r.Context().Deadline(); !ok {
			t.Error("missing version exempted")
		}
	})).ServeHTTP(httptest.NewRecorder(), req)
}

// A sources-server call may wait on two upstream calls; only its POST
// outlives the ordinary deadline.
func TestOnlyTheSourcesServerPostGetsTheSourcesDeadline(t *testing.T) {
	for _, tc := range []struct {
		method, path string
		long         bool
	}{{"POST", "/mcp/sources", true}, {"GET", "/mcp/sources", false}, {"POST", "/mcp/sources/x", false}, {"POST", "/api/cards", false}} {
		next := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			d, ok := r.Context().Deadline()
			left := time.Until(d)
			if !ok || (tc.long && (left > 60*time.Second || left < 59*time.Second)) || (!tc.long && left > RequestTimeout) {
				t.Errorf("%s %s: deadline in %v", tc.method, tc.path, left)
			}
			w.WriteHeader(http.StatusNoContent)
		})
		RequestBounds(next).ServeHTTP(httptest.NewRecorder(), httptest.NewRequest(tc.method, tc.path, strings.NewReader("{}")))
	}
}

func TestPackUploadGetsTheBlobDeadlines(t *testing.T) {
	for _, tc := range []struct {
		method, path string
		long         bool
	}{{"POST", "/api/packs", true}, {"GET", "/api/packs", false}, {"POST", "/api/packs/x/approve", false}} {
		next := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			d, ok := r.Context().Deadline()
			left := time.Until(d)
			if !ok || (tc.long && left < BlobRequestTimeout-time.Second) || (!tc.long && left > RequestTimeout) {
				t.Errorf("%s %s: deadline in %v", tc.method, tc.path, left)
			}
			w.WriteHeader(http.StatusNoContent)
		})
		RequestBounds(next).ServeHTTP(httptest.NewRecorder(), httptest.NewRequest(tc.method, tc.path, strings.NewReader("{}")))
	}

	// A body that takes longer than BodyReadTimeout to arrive is still read.
	srv := httptest.NewServer(RequestBounds(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		if string(b) != "first-second" {
			t.Errorf("body %q", b)
		}
		w.WriteHeader(http.StatusNoContent)
	})))
	defer srv.Close()
	pr, pw := io.Pipe()
	go func() {
		pw.Write([]byte("first-"))
		time.Sleep(BodyReadTimeout + 500*time.Millisecond)
		pw.Write([]byte("second"))
		pw.Close()
	}()
	resp, err := http.Post(srv.URL+"/api/packs", "application/zip", pr)
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusNoContent {
		t.Fatalf("slow pack upload: %d", resp.StatusCode)
	}
}

func TestSkillEditBodyBoundIsExact(t *testing.T) {
	const root = "/api/skills/00000000-0000-0000-0000-000000000001"
	for _, tc := range []struct {
		method, path string
		size, want   int
	}{
		{"PATCH", root, 7 << 20, 204}, {"PATCH", root, (7 << 20) + 1, 413},
		{"POST", root, 2 << 20, 413}, {"PATCH", root + "/files", 2 << 20, 413}, {"PATCH", root + "/", 2 << 20, 413},
		{"PATCH", "/api/skills/not-uuid", 2 << 20, 413}, {"PATCH", "/api/skills", 2 << 20, 413}, {"PATCH", "/api/cards/00000000-0000-0000-0000-000000000001", 2 << 20, 413},
	} {
		t.Run(tc.method+tc.path+strconv.Itoa(tc.size), func(t *testing.T) {
			next := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				data, err := io.ReadAll(r.Body)
				if err != nil || len(data) != tc.size {
					t.Errorf("read %d %v", len(data), err)
				}
				deadline, ok := r.Context().Deadline()
				if !ok || time.Until(deadline) > RequestTimeout {
					t.Error("skill edit changed ordinary deadline")
				}
				w.WriteHeader(204)
			})
			rec := httptest.NewRecorder()
			RequestBounds(next).ServeHTTP(rec, httptest.NewRequest(tc.method, tc.path, strings.NewReader(strings.Repeat("x", tc.size))))
			if rec.Code != tc.want {
				t.Fatalf("status %d want %d", rec.Code, tc.want)
			}
		})
	}
}

func TestArchiveImportBodyBound(t *testing.T) {
	for _, tc := range []struct {
		method, path, contentType string
		size, status              int
	}{
		{"POST", "/api/skills/import", "multipart/form-data; boundary=test", 16 << 20, 204},
		{"POST", "/api/skills/import", "multipart/form-data; boundary=test", 16<<20 + 1, 413},
		{"POST", "/api/skills/import", "application/json", 1<<20 + 1, 413},
		{"POST", "/api/skills/import", "multipart/form-data; broken", 1<<20 + 1, 413},
		{"POST", "/api/skills/imported", "multipart/form-data; boundary=test", 1<<20 + 1, 413},
		{"PATCH", "/api/skills/import", "multipart/form-data; boundary=test", 1<<20 + 1, 413},
	} {
		t.Run(tc.method+tc.path+tc.contentType+strconv.Itoa(tc.size), func(t *testing.T) {
			req := httptest.NewRequest(tc.method, tc.path, strings.NewReader(strings.Repeat("x", tc.size)))
			req.Header.Set("Content-Type", tc.contentType)
			rr := httptest.NewRecorder()
			RequestBounds(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				data, err := io.ReadAll(r.Body)
				if err != nil || len(data) != tc.size {
					t.Fatalf("read %d %v", len(data), err)
				}
				w.WriteHeader(204)
			})).ServeHTTP(rr, req)
			if rr.Code != tc.status {
				t.Fatalf("status %d want %d", rr.Code, tc.status)
			}
		})
	}
}

func TestRuntimeSkillZIPBodyBoundIsExact(t *testing.T) {
	const route = "/api/daemon/runtimes/00000000-0000-0000-0000-000000000001/local-skills/import/00000000-0000-0000-0000-000000000002/result"
	for _, tc := range []struct {
		method, path, media string
		want                int
	}{
		{"POST", route, "application/zip", 204},
		{"POST", route, "application/json", 413},
		{"PUT", route, "application/zip", 413},
		{"POST", route + "/", "application/zip", 413},
		{"POST", strings.Replace(route, "import/", "", 1), "application/zip", 413},
		{"POST", strings.Replace(route, "00000000-0000-0000-0000-000000000001", "bad", 1), "application/zip", 413},
	} {
		t.Run(tc.method+tc.path+tc.media, func(t *testing.T) {
			req := httptest.NewRequest(tc.method, tc.path, strings.NewReader(strings.Repeat("x", 2<<20)))
			req.Header.Set("Content-Type", tc.media)
			rec := httptest.NewRecorder()
			RequestBounds(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				d, ok := r.Context().Deadline()
				if !ok || time.Until(d) < 50*time.Second {
					t.Error("raw archive lost blob deadline")
				}
				w.WriteHeader(204)
			})).ServeHTTP(rec, req)
			if rec.Code != tc.want {
				t.Fatalf("status %d want %d", rec.Code, tc.want)
			}
		})
	}
}
