package cli

import (
	"bytes"
	"context"
	"io"
	"net/http/httptest"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"testing"
	"time"

	"lykeion/server/internal/auth"
	"lykeion/server/internal/handler"
	"lykeion/server/internal/realtime"
	"lykeion/server/internal/service"
	"lykeion/server/internal/storage"
	"lykeion/server/internal/store"
	"lykeion/server/internal/testutil"
)

type mail struct {
	mu   sync.Mutex
	last map[string]string
}

func (m *mail) Send(_ context.Context, to, _, body string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.last == nil {
		m.last = map[string]string{}
	}
	m.last[to] = body
	return nil
}

func (m *mail) code(to string) string {
	m.mu.Lock()
	defer m.mu.Unlock()
	return regexp.MustCompile(`\d{6}`).FindString(m.last[to])
}

func serve(t *testing.T) (*httptest.Server, *mail) {
	env := testutil.Setup(t)
	st := &store.Store{Pool: env.App}
	hub := &realtime.Hub{Store: st, Redis: env.Redis}
	st.Publish = hub.Publish
	t.Cleanup(func() { _ = hub.Close() })
	m := &mail{}
	srv := httptest.NewServer(handler.NewRouter(&handler.H{Store: st, JWT: auth.Issuer{Secret: []byte("s"), TTL: time.Hour},
		Mail: m, Limit: auth.Limiter{R: env.Redis}, Hub: hub,
		AgentRuns: &service.AgentRuns{Store: st}, Blobs: storage.Local{Dir: t.TempDir()}}))
	t.Cleanup(srv.Close)
	return srv, m
}

// lyk runs one command; stdin feeds prompts.
func lyk(t *testing.T, stdin string, args ...string) string {
	t.Helper()
	var out bytes.Buffer
	cmd := Root(strings.NewReader(stdin), &out)
	cmd.SetArgs(args)
	cmd.SetErr(&out)
	if err := cmd.Execute(); err != nil {
		t.Fatalf("lyk %s: %v\n%s", strings.Join(args, " "), err, out.String())
	}
	return out.String()
}

func TestPeopleCommands(t *testing.T) {
	srv, m := serve(t)
	t.Setenv("LYK_CONFIG", filepath.Join(t.TempDir(), "config.json"))

	// login waits for the code on stdin; send-code runs first, so feed the
	// code through a pipe filled once the mail arrives.
	loginPerson(t, srv, m, "ada@lab.org")

	if got := lyk(t, "", "whoami"); !strings.Contains(got, "ada@lab.org") {
		t.Fatalf("whoami: %s", got)
	}
	lyk(t, "", "workspace", "create", "Smith Lab", "smith-lab")
	lyk(t, "", "workspace", "switch", "smith-lab")
	if got := lyk(t, "", "card", "new", "--usd", "20", "Does X raise Y in mouse liver?"); !strings.Contains(got, "#1") {
		t.Fatalf("card new: %s", got)
	}
	lyk(t, "", "card", "comment", "1", "Focus on the fasting state.")
	got := lyk(t, "", "card", "show", "1")
	if !strings.Contains(got, "backlog") || !strings.Contains(got, "fasting state") {
		t.Fatalf("card show: %s", got)
	}
	if got := lyk(t, "", "card", "list"); !strings.Contains(got, "Does X raise Y") {
		t.Fatalf("card list: %s", got)
	}
	lyk(t, "", "logout")
	var o bytes.Buffer
	c := Root(strings.NewReader(""), &o)
	c.SetArgs([]string{"whoami"})
	c.SetErr(&o)
	if err := c.Execute(); err == nil {
		t.Fatal("whoami worked after logout")
	}
}
func ioPipe() (*io.PipeReader, *io.PipeWriter) { return io.Pipe() }
