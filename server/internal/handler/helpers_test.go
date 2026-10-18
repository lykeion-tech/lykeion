package handler

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"lykeion/server/internal/auth"
	"lykeion/server/internal/autoresearch"
	"lykeion/server/internal/connectors"
	"lykeion/server/internal/flow"
	"lykeion/server/internal/realtime"
	"lykeion/server/internal/service"
	"lykeion/server/internal/storage"
	"lykeion/server/internal/store"
	"lykeion/server/internal/testutil"
)

type fakeMail struct {
	mu   sync.Mutex
	last map[string]string
}

func (f *fakeMail) Send(_ context.Context, to, _, body string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.last == nil {
		f.last = map[string]string{}
	}
	f.last[to] = body
	return nil
}

func (f *fakeMail) code(to string) string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return regexp.MustCompile(`\d{6}`).FindString(f.last[to])
}

type server struct {
	*httptest.Server
	H    *H
	Mail *fakeMail
	Env  *testutil.Env
}

func newServer(t *testing.T) *server {
	t.Helper()
	env := testutil.Setup(t)
	return newServerOn(t, env)
}

func newServerOn(t *testing.T, env *testutil.Env) *server {
	st := &store.Store{Pool: env.App}
	hub := &realtime.Hub{Store: st, Redis: env.Redis, Poll: 2 * time.Second}
	st.Publish = hub.Publish
	mail := &fakeMail{}
	h := &H{Store: st, JWT: auth.Issuer{Secret: []byte("test-secret"), TTL: time.Hour}, Mail: mail, Limit: auth.Limiter{R: env.Redis}, Hub: hub,
		Connectors: &connectors.Client{Secret: bytes.Repeat([]byte{7}, 32)}}
	h.AgentRuns = &service.AgentRuns{Store: st, MCPURL: "http://test/mcp/sources", Stale: time.Second}
	h.Blobs = storage.Local{Dir: t.TempDir()}
	h.Engine = &flow.Engine{Store: st, AgentRuns: h.AgentRuns, Blobs: h.Blobs}
	h.Autoresearch = &autoresearch.Service{Store: st, AgentRuns: h.AgentRuns, Key: bytes.Repeat([]byte{7}, 32), Engine: h.Engine}
	h.AgentRuns.OnSettlement = h.Autoresearch.OnSettlement(h.Engine.OnSettlement)
	srv := httptest.NewServer(NewRouter(h))
	t.Cleanup(func() { srv.Close(); h.MCPBroker.Close(); hub.Close() })
	return &server{Server: srv, H: h, Mail: mail, Env: env}
}

// call sends a JSON request with an optional bearer token and workspace, and
// decodes the JSON answer into out when out is not nil.
func (s *server) call(t *testing.T, method, path, token, ws string, in, out any) int {
	t.Helper()
	var body io.Reader
	if in != nil {
		b, _ := json.Marshal(in)
		body = bytes.NewReader(b)
	}
	req, _ := http.NewRequest(method, s.URL+path, body)
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	if ws != "" {
		req.Header.Set("X-Workspace-ID", ws)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if out != nil {
		if err := json.NewDecoder(resp.Body).Decode(out); err != nil {
			t.Fatalf("decode %s %s (%d): %v", method, path, resp.StatusCode, err)
		}
	}
	return resp.StatusCode
}

// signIn runs the emailed-code flow and returns a session JWT.
func (s *server) signIn(t *testing.T, email string) string {
	t.Helper()
	if c := s.call(t, "POST", "/auth/send-code", "", "", map[string]string{"email": email}, nil); c != 204 {
		t.Fatalf("send-code: %d", c)
	}
	var out struct{ Token string }
	norm := strings.ToLower(strings.TrimSpace(email))
	if c := s.call(t, "POST", "/auth/verify-code", "", "", map[string]string{"email": email, "code": s.Mail.code(norm)}, &out); c != 200 {
		t.Fatalf("verify-code: %d", c)
	}
	return out.Token
}

func jsonBody(v any) io.Reader { b, _ := json.Marshal(v); return bytes.NewReader(b) }

func itoa(n int64) string { return strconv.FormatInt(n, 10) }
