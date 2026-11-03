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
	"lykeion/server/internal/board"
	"lykeion/server/internal/connectors"
	"lykeion/server/internal/coresearch"
	"lykeion/server/internal/flow"
	"lykeion/server/internal/realtime"
	"lykeion/server/internal/service"
	"lykeion/server/internal/storage"
	"lykeion/server/internal/store"
	"lykeion/server/internal/testutil"
	db "lykeion/server/pkg/db/generated"

	"github.com/google/uuid"
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
	h.AgentRuns.Blobs = h.Blobs
	h.Engine = &flow.Engine{Store: st, AgentRuns: h.AgentRuns, Blobs: h.Blobs}
	h.Autoresearch = &autoresearch.Service{Store: st, AgentRuns: h.AgentRuns, Key: bytes.Repeat([]byte{7}, 32), Engine: h.Engine}
	h.AgentRuns.OnSettlement = (&coresearch.Service{Store: st, Blobs: h.Blobs}).OnSettlement(h.Autoresearch.OnSettlement(h.Engine.OnSettlement))
	srv := httptest.NewServer(NewRouter(h))
	t.Cleanup(func() { srv.Close(); h.MCPBroker.Close(); hub.Close() })
	return &server{Server: srv, H: h, Mail: mail, Env: env}
}

// idleCard files a study with no Team and no agent, which the engine never
// starts: a holder for runs a test queues itself.
func (s *server) idleCard(t *testing.T, tok, wsID, question string) card {
	t.Helper()
	return s.seedCard(t, tok, wsID, question, nil, board.Grounding, board.Validation)
}

// agentCard files a study worked by one agent, as Autoresearch opens one, and
// starts it.
func (s *server) agentCard(t *testing.T, tok, wsID, agentID, from, to, question string) card {
	t.Helper()
	id := uuid.MustParse(agentID)
	c := s.seedCard(t, tok, wsID, question, &id, from, to)
	s.H.Engine.Kick(uuid.MustParse(wsID), uuid.MustParse(c.ID))
	return c
}

func (s *server) seedCard(t *testing.T, tok, wsID, question string, agent *uuid.UUID, from, to string) card {
	t.Helper()
	ws, owner := uuid.MustParse(wsID), uuid.MustParse(s.userID(t, tok))
	var c db.Card
	err := s.H.Store.InWorkspace(t.Context(), ws, owner, func(tx *store.Tx) error {
		n, err := tx.Q.NextCardNumber(t.Context(), ws)
		if err != nil {
			return err
		}
		p := db.CreateCardParams{WorkspaceID: ws, Number: n, Title: question, Question: question, OwnerID: owner,
			ScopeFrom: from, ScopeTo: to, BoardColumn: from, Round: 1}
		if agent != nil {
			typ := "agent"
			p.AssigneeType, p.AssigneeID = &typ, agent
		}
		c, err = tx.Q.CreateCard(t.Context(), p)
		return err
	})
	if err != nil {
		t.Fatal(err)
	}
	return card{ID: c.ID.String(), Number: int(c.Number), Title: c.Title, BoardColumn: c.BoardColumn, OwnerID: c.OwnerID.String()}
}

// call sends a JSON request with an optional bearer token and workspace, and
// decodes the JSON answer into out when out is not nil.
func (s *server) call(t *testing.T, method, path, token, ws string, in, out any) int {
	path = csContinuation(t, s, path)
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
