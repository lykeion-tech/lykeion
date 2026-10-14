package cli

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
	"time"

	"lykeion/server/internal/auth"
	"lykeion/server/internal/handler"
	"lykeion/server/internal/realtime"
	"lykeion/server/internal/store"
	"lykeion/server/internal/testutil"
)

func loginPerson(t *testing.T, srv *httptest.Server, m *mail, email string) {
	t.Helper()
	pr, pw := ioPipe()
	defer pr.Close()
	done := make(chan struct{})
	go func() {
		defer close(done)
		defer pw.Close()
		deadline := time.NewTimer(3 * time.Second)
		defer deadline.Stop()
		tick := time.NewTicker(5 * time.Millisecond)
		defer tick.Stop()
		for {
			if code := m.code(email); code != "" {
				pw.Write([]byte(code + "\n"))
				return
			}
			select {
			case <-deadline.C:
				return
			case <-tick.C:
			}
		}
	}()
	var out bytes.Buffer
	c := Root(pr, &out)
	c.SetArgs([]string{"login", "--email", email, "--server", srv.URL})
	err := c.Execute()
	pr.Close()
	<-done
	if err != nil {
		t.Fatalf("login: %v %s", err, out.String())
	}
}

func TestWorkspaceInvitationsAndInbox(t *testing.T) {
	s, m := serve(t)
	owner := filepath.Join(t.TempDir(), "owner", "config.json")
	guest := filepath.Join(t.TempDir(), "guest", "config.json")
	t.Setenv("LYK_CONFIG", owner)
	loginPerson(t, s, m, "owner@lab.org")
	lyk(t, "", "workspace", "create", "Lab", "lab")
	if got := lyk(t, "", "workspace", "list"); !strings.Contains(got, "lab") {
		t.Fatal(got)
	}
	lyk(t, "", "workspace", "switch", "lab")
	if got := lyk(t, "", "workspace", "members"); !strings.Contains(got, "owner@lab.org") {
		t.Fatal(got)
	}
	lyk(t, "", "workspace", "invite", "guest@lab.org", "--role", "admin")
	m.mu.Lock()
	body := m.last["guest@lab.org"]
	m.mu.Unlock()
	matches := regexp.MustCompile(`invitations/([A-Za-z0-9_-]+)/accept`).FindStringSubmatch(body)
	if len(matches) != 2 {
		t.Fatalf("invitation: %s", body)
	}
	t.Setenv("LYK_CONFIG", guest)
	loginPerson(t, s, m, "guest@lab.org")
	lyk(t, "", "workspace", "accept", matches[1])
	cfg, _, err := LoadConfig()
	if err != nil || cfg.Workspace == "" {
		t.Fatalf("accepted selection: %+v %v", cfg, err)
	}
	if got := lyk(t, "", "workspace", "members"); !strings.Contains(got, "guest@lab.org") || !strings.Contains(got, "admin") {
		t.Fatal(got)
	}
	if got := lyk(t, "", "inbox"); got != "" {
		t.Fatalf("unexpected inbox: %s", got)
	}
	c := Root(strings.NewReader(""), &bytes.Buffer{})
	c.SetArgs([]string{"workspace", "switch", "absent"})
	if err := c.Execute(); err == nil {
		t.Fatal("unknown workspace accepted")
	}
}

func TestGoogleCommandEndToEnd(t *testing.T) {
	env := testutil.Setup(t)
	st := &store.Store{Pool: env.App}
	hub := &realtime.Hub{Store: st, Redis: env.Redis}
	st.Publish = hub.Publish
	t.Cleanup(func() { _ = hub.Close() })
	provider := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/token" {
			_ = json.NewEncoder(w).Encode(map[string]string{"access_token": "at"})
			return
		}
		if r.URL.Path == "/userinfo" {
			_ = json.NewEncoder(w).Encode(map[string]any{"sub": "subject", "email": "google@lab.org", "email_verified": true, "name": "Grace"})
			return
		}
		http.NotFound(w, r)
	}))
	defer provider.Close()
	h := &handler.H{Store: st, JWT: auth.Issuer{Secret: []byte("secret"), TTL: time.Hour}, Hub: hub, Limit: auth.Limiter{R: env.Redis}, Google: auth.Google{ClientID: "client", Secret: "secret", RedirectURL: "http://localhost/auth/google/callback", AuthURL: provider.URL + "/auth", TokenURL: provider.URL + "/token", UserinfoURL: provider.URL + "/userinfo"}}
	s := httptest.NewServer(handler.NewRouter(h))
	defer s.Close()
	t.Setenv("LYK_CONFIG", filepath.Join(t.TempDir(), "config.json"))
	var out bytes.Buffer
	opener := func(raw string) error {
		client := &http.Client{Timeout: 5 * time.Second, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
		resp, err := client.Get(raw)
		if err != nil {
			return err
		}
		resp.Body.Close()
		if resp.StatusCode != 302 {
			return fmt.Errorf("start: %d", resp.StatusCode)
		}
		cookies := resp.Cookies()
		if len(cookies) == 0 {
			return fmt.Errorf("no state cookie")
		}
		loc, err := url.Parse(resp.Header.Get("Location"))
		if err != nil {
			return err
		}
		req, err := http.NewRequest("GET", s.URL+"/auth/google/callback?"+url.Values{"state": {loc.Query().Get("state")}, "code": {"good"}}.Encode(), nil)
		if err != nil {
			return err
		}
		req.AddCookie(cookies[0])
		resp, err = client.Do(req)
		if err != nil {
			return err
		}
		resp.Body.Close()
		if resp.StatusCode != 302 {
			return fmt.Errorf("exchange: %d", resp.StatusCode)
		}
		resp, err = client.Get(resp.Header.Get("Location"))
		if err != nil {
			return err
		}
		resp.Body.Close()
		if resp.StatusCode != 200 {
			return fmt.Errorf("loopback: %d", resp.StatusCode)
		}
		return nil
	}
	c := rootWithBrowser(strings.NewReader(""), &out, opener)
	c.SetArgs([]string{"login", "--google", "--server", s.URL})
	if err := c.Execute(); err != nil {
		t.Fatalf("google CLI: %v %s", err, out.String())
	}
	cfg, _, err := LoadConfig()
	if err != nil || !strings.HasPrefix(cfg.Token, "lyk_") {
		t.Fatalf("personal token: %+v %v", cfg, err)
	}
	if got := lyk(t, "", "whoami"); !strings.Contains(got, "google@lab.org") {
		t.Fatal(got)
	}
}
