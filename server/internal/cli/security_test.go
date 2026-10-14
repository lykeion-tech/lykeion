package cli

import (
	"bytes"
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func TestConfigAtomicPrivate(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "config")
	if err := os.Mkdir(dir, 0755); err != nil {
		t.Fatal(err)
	}
	p := filepath.Join(dir, "config.json")
	if err := os.WriteFile(p, []byte("old"), 0644); err != nil {
		t.Fatal(err)
	}
	t.Setenv("LYK_CONFIG", p)
	want := Config{Server: "http://localhost:8080", Token: "secret", Workspace: "w"}
	if err := SaveConfig(p, want); err != nil {
		t.Fatal(err)
	}
	got, path, err := LoadConfig()
	if err != nil || got != want || path != p {
		t.Fatalf("%+v %s %v", got, path, err)
	}
	for path, mode := range map[string]os.FileMode{p: 0600, dir: 0755} {
		st, err := os.Stat(path)
		if err != nil || st.Mode().Perm() != mode {
			t.Fatalf("%s permissions: %v %v", path, st, err)
		}
	}
	entries, err := os.ReadDir(dir)
	if err != nil || len(entries) != 1 {
		t.Fatalf("temporary files: %v %v", entries, err)
	}
	if err := os.WriteFile(p, []byte("{"), 0600); err != nil {
		t.Fatal(err)
	}
	if _, _, err := LoadConfig(); err == nil {
		t.Fatal("invalid config accepted")
	}
	if err := os.Remove(p); err != nil {
		t.Fatal(err)
	}
	if got, _, err := LoadConfig(); err != nil || got != (Config{}) {
		t.Fatalf("missing config: %+v %v", got, err)
	}
}

func TestClientResponsesAndContext(t *testing.T) {
	s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer secret" || r.Header.Get("X-Workspace-ID") != "w" {
			t.Error("missing scoped headers")
		}
		switch r.URL.Path {
		case "/json":
			w.Write([]byte(`{"name":"Ada"}`))
		case "/error":
			w.WriteHeader(403)
			w.Write([]byte(`{"error":"denied"}`))
		case "/bad":
			w.Write([]byte(`not json`))
		case "/empty":
			w.WriteHeader(204)
		case "/plain":
			w.WriteHeader(502)
			w.Write([]byte("upstream unavailable"))
		case "/wait":
			<-r.Context().Done()
		}
	}))
	defer s.Close()
	c := Client{Server: s.URL + "/", Token: "secret", Workspace: "w"}
	var out struct{ Name string }
	if err := c.Do(context.Background(), "GET", "/json", nil, &out); err != nil || out.Name != "Ada" {
		t.Fatalf("%+v %v", out, err)
	}
	for _, path := range []string{"/error", "/plain", "/bad"} {
		if err := c.Do(context.Background(), "GET", path, nil, &out); err == nil {
			t.Fatalf("accepted %s", path)
		}
	}
	if err := c.Do(context.Background(), "DELETE", "/empty", nil, &out); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Millisecond)
	defer cancel()
	if err := c.Do(ctx, "GET", "/wait", nil, nil); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("context: %v", err)
	}
	if err := c.Do(context.Background(), "POST", "/json", make(chan int), nil); err == nil {
		t.Fatal("accepted unencodable input")
	}
	c.Server = "%"
	if err := c.Do(context.Background(), "GET", "/json", nil, nil); err == nil {
		t.Fatal("accepted invalid server")
	}
}

func TestGoogleLoginCallback(t *testing.T) {
	e := &env{cfg: Config{Server: "http://example.test"}, out: &bytes.Buffer{}}
	e.openBrowser = func(raw string) error {
		u, err := url.Parse(raw)
		if err != nil {
			return err
		}
		nonce := u.Query().Get("cli_state")
		if len(nonce) < 32 {
			t.Error("missing unpredictable nonce")
		}
		base := "http://127.0.0.1:" + u.Query().Get("cli_port") + "/callback"
		for _, tc := range []struct {
			method, query string
			status        int
		}{
			{"GET", "?token=session&state=wrong", 400},
			{"GET", "?state=" + nonce, 400},
			{"POST", "?token=session&state=" + nonce, 405},
			{"GET", "?token=session&state=" + nonce, 200},
			{"GET", "?token=session&state=" + nonce, 409},
		} {
			req, _ := http.NewRequest(tc.method, base+tc.query, nil)
			resp, err := http.DefaultClient.Do(req)
			if err != nil {
				return err
			}
			resp.Body.Close()
			if resp.StatusCode != tc.status {
				t.Errorf("callback %s %s: %d", tc.method, tc.query, resp.StatusCode)
			}
		}
		return nil
	}
	tok, err := googleLogin(context.Background(), e)
	if err != nil || tok != "session" {
		t.Fatalf("%s %v", tok, err)
	}
	e.openBrowser = func(string) error { return errors.New("browser unavailable") }
	if _, err := googleLogin(context.Background(), e); err == nil || !strings.Contains(err.Error(), "browser unavailable") {
		t.Fatalf("browser error: %v", err)
	}
	e.openBrowser = func(string) error { return nil }
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := googleLogin(ctx, e); !errors.Is(err, context.Canceled) {
		t.Fatalf("cancellation: %v", err)
	}
}

func TestLoginRejectsEmptyTokenAndConflictingMethods(t *testing.T) {
	s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.Write([]byte(`{"token":""}`)) }))
	defer s.Close()
	p := filepath.Join(t.TempDir(), "config.json")
	t.Setenv("LYK_CONFIG", p)
	for _, args := range [][]string{{"login", "--email", "ada@lab.org", "--server", s.URL}, {"login", "--google", "--email", "ada@lab.org", "--server", s.URL}} {
		c := Root(strings.NewReader("123456\n"), &bytes.Buffer{})
		c.SetArgs(args)
		if err := c.Execute(); err == nil {
			t.Fatal("invalid login accepted")
		}
	}
	if _, err := os.Stat(p); !os.IsNotExist(err) {
		t.Fatalf("invalid login saved config: %v", err)
	}
}

func TestConfigPreservesExistingParentPermissions(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "shared")
	if err := os.Mkdir(dir, 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(dir, 0755); err != nil {
		t.Fatal(err)
	}
	t.Chdir(dir)
	t.Setenv("LYK_CONFIG", "./config.json")
	_, path, err := LoadConfig()
	if err != nil {
		t.Fatal(err)
	}
	if err := SaveConfig(path, Config{Token: "private"}); err != nil {
		t.Fatal(err)
	}
	st, err := os.Stat(dir)
	if err != nil {
		t.Fatal(err)
	}
	if st.Mode().Perm() != 0755 {
		t.Fatalf("existing parent changed to %o; want 755", st.Mode().Perm())
	}
	nested := filepath.Join(dir, "new-cli", "nested", "config.json")
	if err := SaveConfig(nested, Config{Token: "private"}); err != nil {
		t.Fatal(err)
	}
	for _, p := range []string{filepath.Dir(nested), filepath.Dir(filepath.Dir(nested))} {
		st, err := os.Stat(p)
		if err != nil {
			t.Fatal(err)
		}
		if st.Mode().Perm() != 0700 {
			t.Fatalf("new directory %s mode %o; want 700", p, st.Mode().Perm())
		}
	}
}

func TestClientInjectedHTTPRejectsRedirect(t *testing.T) {
	var requests atomic.Int32
	destination := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { requests.Add(1); w.Write([]byte(`{"name":"leaked"}`)) }))
	defer destination.Close()
	origin := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, destination.URL+"/credentials", http.StatusFound)
	}))
	defer origin.Close()
	injected := &http.Client{Timeout: time.Second}
	c := Client{Server: origin.URL, Token: "secret", Workspace: "private-workspace", HTTP: injected}
	var out struct{ Name string }
	err := c.Do(context.Background(), "GET", "/redirect", nil, &out)
	if requests.Load() != 0 {
		t.Fatalf("redirect destination received %d requests with possible credentials", requests.Load())
	}
	if err == nil {
		t.Fatal("redirect accepted")
	}
	if injected.CheckRedirect != nil || injected.Timeout != time.Second {
		t.Fatal("caller client mutated")
	}
}
