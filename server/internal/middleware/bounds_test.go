package middleware

import (
	"context"
	"fmt"
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
	}{{"POST", "/api/coresearch/uploads", true}, {"PUT", "/api/coresearch/uploads", false}, {"POST", "/api/coresearch/uploads/", false}, {"POST", "/api/role/blobs", true}, {"PUT", "/api/role/blobs", false}, {"POST", "/api/role/blobs/", false}, {"POST", "/api/role/records", false}} {
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

func TestRemovedPackUploadUsesOrdinaryBounds(t *testing.T) {
	r := httptest.NewRequest(http.MethodPost, "/api/packs", strings.NewReader("{}"))
	if got := JSONBodyLimit(r); got != MaxBodyBytes {
		t.Fatalf("body limit: %d", got)
	}
	RequestBounds(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		deadline, ok := r.Context().Deadline()
		if !ok || time.Until(deadline) > RequestTimeout {
			t.Errorf("removed upload kept extended timeout")
		}
	})).ServeHTTP(httptest.NewRecorder(), r)
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

func TestWorkspaceMCPBrokerTimeoutIsExactAndCancellable(t *testing.T) {
	id := "12345678-1234-1234-1234-123456789012"
	for _, tc := range []struct {
		method, path string
		broker       bool
	}{
		{"POST", "/api/role/mcp-servers/" + id + "/tools", true},
		{"POST", "/api/role/mcp-servers/" + id + "/call", true},
		{"GET", "/api/role/mcp-servers/" + id + "/call", false},
		{"POST", "/api/role/mcp-servers/nope/call", false},
		{"POST", "/api/role/mcp-servers/" + id + "/calls", false},
		{"POST", "/api/role/mcp-servers/" + id + "/call/extra", false},
	} {
		t.Run(tc.method+tc.path, func(t *testing.T) {
			parent, cancel := context.WithCancel(context.Background())
			defer cancel()
			req := httptest.NewRequest(tc.method, tc.path, strings.NewReader(`{}`)).WithContext(parent)
			req.Header.Set("Content-Type", "application/json")
			RequestBounds(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				deadline, ok := r.Context().Deadline()
				if !ok {
					t.Fatal("no deadline")
				}
				remaining := time.Until(deadline)
				want := RequestTimeout
				if tc.broker {
					want = 5 * time.Minute
				}
				if remaining > want || remaining < want-time.Second {
					t.Fatalf("timeout %s want %s", remaining, want)
				}
				if JSONBodyLimit(r) != MaxBodyBytes {
					t.Fatal("broker widened body limit")
				}
				cancel()
				select {
				case <-r.Context().Done():
				case <-time.After(time.Second):
					t.Fatal("parent cancellation lost")
				}
			})).ServeHTTP(httptest.NewRecorder(), req)
		})
	}
}

func TestArchiveAndOutputBoundsRemainSeparate(t *testing.T) {
	id := "11111111-1111-1111-1111-111111111111"
	for _, tt := range []struct {
		method, path string
		want         time.Duration
	}{
		{"POST", "/api/attachments", 3 * time.Hour},
		{"GET", "/api/cards/" + id + "/attachments/" + id, 3 * time.Hour},
		{"GET", "/api/role/attachments/" + id, 3 * time.Hour},
		{"POST", "/api/role/outputs", 15 * time.Minute},
		{"GET", "/api/cards/" + id + "/outputs/o1", 15 * time.Minute},
		{"GET", "/api/role/outputs/o1", 15 * time.Minute},
	} {
		t.Run(tt.path, func(t *testing.T) {
			h := RequestBounds(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				d, ok := r.Context().Deadline()
				if !ok {
					t.Fatal("no deadline")
				}
				if left := time.Until(d); left > tt.want || left < tt.want-time.Second {
					t.Fatalf("budget %v, want %v", left, tt.want)
				}
			}))
			h.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest(tt.method, tt.path, nil))
		})
	}
}

type readBudgetRecorder struct {
	*httptest.ResponseRecorder
	deadlines []time.Time
}

func (w *readBudgetRecorder) SetReadDeadline(d time.Time) error {
	w.deadlines = append(w.deadlines, d)
	return nil
}
func TestArchiveAndOutputReadBounds(t *testing.T) {
	for _, tt := range []struct {
		path string
		want time.Duration
	}{{"/api/attachments", 2 * time.Hour}, {"/api/role/outputs", 10 * time.Minute}} {
		t.Run(tt.path, func(t *testing.T) {
			w := &readBudgetRecorder{ResponseRecorder: httptest.NewRecorder()}
			h := RequestBounds(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {}))
			h.ServeHTTP(w, httptest.NewRequest("POST", tt.path, strings.NewReader("body")))
			if len(w.deadlines) == 0 {
				t.Fatal("no read deadline")
			}
			if left := time.Until(w.deadlines[0]); left > tt.want || left < tt.want-time.Second {
				t.Fatalf("read budget %v, want %v", left, tt.want)
			}
		})
	}
}

func TestHiddenServiceDownloadHasLongExactBudget(t *testing.T) {
	const route = "/api/daemon/runs/00000000-0000-0000-0000-000000000001/services/00000000-0000-0000-0000-000000000002"
	for _, tc := range []struct {
		method, path string
		long         bool
	}{{"GET", route, true}, {"POST", route, false}, {"GET", route + "/", false}, {"GET", strings.Replace(route, "00000000-0000-0000-0000-000000000001", "bad", 1), false}} {
		t.Run(tc.method+tc.path, func(t *testing.T) {
			RequestBounds(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				deadline, ok := r.Context().Deadline()
				if !ok {
					t.Fatal("missing deadline")
				}
				long := time.Until(deadline) > time.Hour
				if long != tc.long {
					t.Fatalf("long budget %v, want %v", long, tc.long)
				}
			})).ServeHTTP(httptest.NewRecorder(), httptest.NewRequest(tc.method, tc.path, nil))
		})
	}
}

func TestPersonCellOutputExactBounds(t *testing.T) {
	const id = "00000000-0000-0000-0000-000000000001"
	const route = "/api/coresearch/projects/" + id + "/chats/" + id + "/cells/" + id + "/outputs"
	cases := []struct {
		method, path string
		long         bool
	}{{"GET", route, true}, {"POST", route, false}, {"GET", route + "/", false}, {"GET", route + "/extra", false}, {"GET", strings.Replace(route, "/cells/", "/files/", 1), false}}
	for _, part := range []string{"projects", "chats", "cells"} {
		cases = append(cases, struct {
			method, path string
			long         bool
		}{"GET", strings.Replace(route, "/"+part+"/"+id, "/"+part+"/bad", 1), false})
	}
	for _, tc := range cases {
		t.Run(tc.method+tc.path, func(t *testing.T) {
			RequestBounds(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				d, ok := r.Context().Deadline()
				if !ok {
					t.Fatal("missing deadline")
				}
				long := time.Until(d) > 14*time.Minute
				if long != tc.long {
					t.Fatalf("long=%v want %v", long, tc.long)
				}
				if tc.long && time.Until(d) > 15*time.Minute {
					t.Fatal("overlong")
				}
			})).ServeHTTP(httptest.NewRecorder(), httptest.NewRequest(tc.method, tc.path, nil))
		})
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	RequestBounds(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Context().Err() != context.Canceled {
			t.Fatal("lost cancellation")
		}
	})).ServeHTTP(httptest.NewRecorder(), httptest.NewRequest("GET", route, nil).WithContext(ctx))
}

func TestDocStepsEscapedTextBounds(t *testing.T) {
	path := "/api/coresearch/projects/00000000-0000-0000-0000-000000000001/docs/00000000-0000-0000-0000-000000000002/steps"
	body := `{"base":0,"client_id":"x","steps":[[[0,"` + strings.Repeat(`\u0061`, 1<<20) + `"]]]}`
	called := false
	handler := RequestBounds(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { called = true; w.WriteHeader(204) }))
	req := httptest.NewRequest("POST", path, strings.NewReader(body))
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	if !called || rec.Code != 204 {
		t.Fatalf("escaped text refused: %d", rec.Code)
	}
	if n := JSONBodyLimit(httptest.NewRequest("POST", path+"/other", nil)); n != MaxBodyBytes {
		t.Fatalf("nonmatching route limit %d", n)
	}
}

func TestDaemonHandInBodyLimitIsExact(t *testing.T) {
	const path = "/api/daemon/agent-runs/00000000-0000-0000-0000-000000000001/hand-in"
	for _, tc := range []struct {
		method, path string
		size, want   int
	}{
		{"POST", path, 10 << 20, 204}, {"POST", path, (10 << 20) + 1, 413},
		{"POST", path + "/extra", (1 << 20) + 1, 413}, {"PUT", path, (1 << 20) + 1, 413},
		{"POST", "/api/daemon/agent-runs/not-a-uuid/hand-in", (1 << 20) + 1, 413},
		{"POST", "/api/daemon/agent-runs/00000000000000000000000000000001/hand-in", (1 << 20) + 1, 413},
		{"POST", "/api/cards", (1 << 20) + 1, 413},
	} {
		t.Run(fmt.Sprintf("%s_%s_%d", tc.method, tc.path, tc.size), func(t *testing.T) {
			called := false
			h := RequestBounds(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				called = true
				data, err := io.ReadAll(r.Body)
				if err != nil || len(data) != tc.size {
					t.Errorf("body length %d, error %v", len(data), err)
				}
				w.WriteHeader(204)
			}))
			rec := httptest.NewRecorder()
			h.ServeHTTP(rec, httptest.NewRequest(tc.method, tc.path, strings.NewReader(strings.Repeat("x", tc.size))))
			if rec.Code != tc.want || called != (tc.want == 204) {
				t.Fatalf("status %d called %v, want %d", rec.Code, called, tc.want)
			}
		})
	}
}

func TestQuestionAnswersBodyLimit(t *testing.T) {
	id := "e2580cdf-b924-4001-b80a-716d03e7c1f1"
	for _, tt := range []struct {
		method, path string
		want         int64
	}{
		{"POST", "/api/decisions/" + id + "/answers", 10 << 20},
		{"GET", "/api/decisions/" + id + "/answers", MaxBodyBytes},
		{"POST", "/api/decisions/not-a-uuid/answers", MaxBodyBytes},
		{"POST", "/api/decisions/" + id + "/decide", MaxBodyBytes},
	} {
		if got := JSONBodyLimit(httptest.NewRequest(tt.method, tt.path, nil)); got != tt.want {
			t.Errorf("%s %s got %d want %d", tt.method, tt.path, got, tt.want)
		}
	}
}
