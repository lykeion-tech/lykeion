package handler

import (
	"crypto/rand"
	"crypto/rsa"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync/atomic"
	"testing"

	"lykeion/server/internal/github"
	"lykeion/server/internal/middleware"
)

func useFakeGitHub(t *testing.T, s *server) *atomic.Int32 {
	t.Helper()
	var exchanges atomic.Int32
	api := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/login/oauth/access_token":
			exchanges.Add(1)
			if r.Method != "POST" || r.FormValue("client_id") != "client" || r.FormValue("client_secret") != "client-secret" || r.FormValue("code") != "c" || r.Header.Get("Accept") != "application/json" {
				t.Error("incorrect OAuth exchange")
			}
			fmt.Fprint(w, `{"access_token":"gho_fake"}`)
		case "/app/installations/1":
			fmt.Fprint(w, `{"account":{"id":12,"type":"User","login":"lab","avatar_url":"https://example.test/avatar"}}`)
		case "/user":
			if r.Header.Get("Authorization") != "Bearer gho_fake" {
				t.Error("missing OAuth token")
			}
			fmt.Fprint(w, `{"id":12}`)
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(api.Close)
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}
	s.H.GitHub = &github.Service{Store: s.H.Store, StateSecret: s.H.JWT.Secret, PublicURL: "https://lab.test", Config: github.Config{APIURL: api.URL, WebURL: api.URL, AppID: 42, Slug: "lab", PrivateKey: key, ClientID: "client", ClientSecret: "client-secret", WebhookSecret: "webhook-secret"}}
	return &exchanges
}

func githubSetupPath(t *testing.T, s *server, owner, ws string) string {
	t.Helper()
	var out struct{ URL string }
	if code := s.call(t, "POST", "/api/github/connect", owner, ws, nil, &out); code != 200 {
		t.Fatal(code)
	}
	u, err := url.Parse(out.URL)
	if err != nil || u.Query().Get("state") == "" {
		t.Fatalf("invalid install URL: %v", err)
	}
	return "/api/github/setup?installation_id=1&code=c&state=" + url.QueryEscape(u.Query().Get("state"))
}

func githubCallback(t *testing.T, s *server, path, cookie string) string {
	t.Helper()
	req, err := http.NewRequest("GET", s.URL+path, nil)
	if err != nil {
		t.Fatal(err)
	}
	if cookie != "" {
		req.AddCookie(&http.Cookie{Name: middleware.SessionCookie, Value: cookie})
	}
	client := http.Client{CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	res, err := client.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer res.Body.Close()
	if res.StatusCode != 302 {
		t.Fatal(res.StatusCode)
	}
	return res.Header.Get("Location")
}

func TestOwnerConnectsGitHub(t *testing.T) {
	s := newServer(t)
	exchanges := useFakeGitHub(t, s)
	owner := s.signIn(t, "owner@lab.org")
	ws := s.workspace(t, owner, "lab")
	member := s.member(t, owner, ws.ID, "m@lab.org", "member")
	for _, mutation := range []struct{ method, path string }{{"POST", "/api/github/connect"}, {"DELETE", "/api/github/connections/1"}, {"PATCH", "/api/github"}} {
		if c := s.call(t, mutation.method, mutation.path, member, ws.ID, nil, nil); c != 403 {
			t.Fatalf("member %s: %d", mutation.method, c)
		}
	}
	path := githubSetupPath(t, s, owner, ws.ID)
	for _, session := range []string{"", "invalid", member} {
		location := githubCallback(t, s, path, session)
		if !strings.HasPrefix(location, "https://lab.test/app/#/settings/code?github_error=") || exchanges.Load() != 0 {
			t.Fatal("forwarded state accepted or code exchanged")
		}
	}
	if loc := githubCallback(t, s, path, owner); loc != "https://lab.test/app/#/settings/code?github=connected" {
		t.Fatal(loc)
	}
	if c := s.call(t, "PATCH", "/api/github", owner, ws.ID, map[string]bool{"auto_link": false}, nil); c != 204 {
		t.Fatal(c)
	}
	var raw json.RawMessage
	if c := s.call(t, "GET", "/api/github", member, ws.ID, nil, &raw); c != 200 {
		t.Fatal(c)
	}
	for _, part := range []string{`"auto_link":false`, `"installation_id":1`, `"account_login":"lab"`, `"configured":true`} {
		if !strings.Contains(string(raw), part) {
			t.Fatal(string(raw))
		}
	}
	for _, secret := range []string{"ghs_", "gho_", "BEGIN", "client-secret", "webhook-secret"} {
		if strings.Contains(string(raw), secret) {
			t.Fatal("credential in status")
		}
	}
	if n := s.count(t, `SELECT count(*) FROM event WHERE workspace_id=$1 AND type='github.changed'`, ws.ID); n != 2 {
		t.Fatal(n)
	}
}

func TestGitHubNotConfigured(t *testing.T) {
	s := newServer(t)
	s.H.GitHub = &github.Service{}
	owner := s.signIn(t, "owner@lab.org")
	ws := s.workspace(t, owner, "lab")
	var raw json.RawMessage
	if c := s.call(t, "GET", "/api/github", owner, ws.ID, nil, &raw); c != 200 || !strings.Contains(string(raw), `"configured":false`) || !strings.Contains(string(raw), `"connections":[]`) {
		t.Fatal(c, string(raw))
	}
	if c := s.call(t, "POST", "/api/github/connect", owner, ws.ID, nil, &raw); c != 503 || !strings.Contains(string(raw), "GitHub isn't configured on this server.") {
		t.Fatal(c, string(raw))
	}
}

func TestGitHubSetupStateFromOtherWorkspaceOwner(t *testing.T) {
	s := newServer(t)
	useFakeGitHub(t, s)
	owner := s.signIn(t, "owner@lab.org")
	ws := s.workspace(t, owner, "lab")
	path := githubSetupPath(t, s, owner, ws.ID)
	s.mustUpdate(t, `UPDATE member SET role='member' WHERE workspace_id=$1`, ws.ID)
	if loc := githubCallback(t, s, path, owner); !strings.Contains(loc, "github_error=") {
		t.Fatal(loc)
	}
	if n := s.count(t, `SELECT count(*) FROM github_connection WHERE workspace_id=$1`, ws.ID); n != 0 {
		t.Fatal(n)
	}
}

func TestGitHubDisconnectKeepsLinks(t *testing.T) {
	s := newServer(t)
	useFakeGitHub(t, s)
	owner := s.signIn(t, "owner@lab.org")
	ws := s.workspace(t, owner, "lab")
	b := s.workspace(t, owner, "other")
	path := githubSetupPath(t, s, owner, ws.ID)
	if loc := githubCallback(t, s, path, owner); !strings.HasSuffix(loc, "github=connected") {
		t.Fatal(loc)
	}
	card := s.idleCard(t, owner, ws.ID, "Question?")
	s.mustUpdate(t, `WITH pr AS (INSERT INTO github_pull_request(workspace_id,installation_id,repo_owner,repo_name,number,title,state,html_url,pr_created_at,pr_updated_at) VALUES ($1,1,'lab','repo',1,'pr','open','',now(),now()) RETURNING id) INSERT INTO github_pr_link(workspace_id,card_id,pr_id,source) SELECT $1,$2,id,'manual' FROM pr`, ws.ID, card.ID)
	if c := s.call(t, "DELETE", "/api/github/connections/1", owner, b.ID, nil, nil); c != 404 {
		t.Fatal(c)
	}
	if n := s.count(t, `SELECT count(*) FROM github_connection WHERE workspace_id=$1`, ws.ID); n != 1 {
		t.Fatal(n)
	}
	if c := s.call(t, "DELETE", "/api/github/connections/1", owner, ws.ID, nil, nil); c != 204 {
		t.Fatal(c)
	}
	for _, tc := range []struct {
		table string
		want  int
	}{{"github_connection", 0}, {"github_pull_request", 1}, {"github_pr_link", 1}} {
		if n := s.count(t, "SELECT count(*) FROM "+tc.table+" WHERE workspace_id=$1", ws.ID); n != tc.want {
			t.Fatal(tc.table, n)
		}
	}
}
