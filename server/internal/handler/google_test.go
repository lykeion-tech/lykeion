package handler

import (
	"encoding/base64"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"lykeion/server/internal/auth"
)

func fakeGoogle(t *testing.T, verified bool, sub string) *httptest.Server {
	t.Helper()
	mux := http.NewServeMux()
	mux.HandleFunc("/token", func(w http.ResponseWriter, r *http.Request) {
		if err := r.ParseForm(); err != nil || r.Form.Get("code") != "good" || r.Form.Get("client_secret") != "sec" {
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		_ = json.NewEncoder(w).Encode(map[string]string{"access_token": "at"})
	})
	mux.HandleFunc("/userinfo", func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer at" {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"sub": sub, "email": "G@Lab.org", "email_verified": verified, "name": "Grace"})
	})
	s := httptest.NewServer(mux)
	t.Cleanup(s.Close)
	return s
}

func googleServer(t *testing.T, verified bool, sub string) *server {
	t.Helper()
	s := newServer(t)
	g := fakeGoogle(t, verified, sub)
	s.H.Google = auth.Google{ClientID: "cid", Secret: "sec", RedirectURL: s.URL + "/auth/google/callback",
		AuthURL: g.URL + "/auth", TokenURL: g.URL + "/token", UserinfoURL: g.URL + "/userinfo"}
	return s
}

var noRedirect = &http.Client{CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}

func start(t *testing.T, s *server, query string) (string, *http.Cookie) {
	t.Helper()
	resp, err := noRedirect.Get(s.URL + "/auth/google" + query)
	if err != nil || resp.StatusCode != http.StatusFound {
		t.Fatalf("%v %v", resp, err)
	}
	t.Cleanup(func() { resp.Body.Close() })
	loc, err := url.Parse(resp.Header.Get("Location"))
	if err != nil {
		t.Fatal(err)
	}
	cookies := resp.Cookies()
	if len(cookies) == 0 {
		t.Fatal("missing OAuth state cookie")
	}
	return loc.Query().Get("state"), cookies[0]
}

func callback(t *testing.T, s *server, state, code string, c *http.Cookie) *http.Response {
	t.Helper()
	query := url.Values{"state": {state}, "code": {code}}
	req, err := http.NewRequest(http.MethodGet, s.URL+"/auth/google/callback?"+query.Encode(), nil)
	if err != nil {
		t.Fatal(err)
	}
	req.AddCookie(c)
	resp, err := noRedirect.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { resp.Body.Close() })
	return resp
}

func TestGoogleBrowserSignIn(t *testing.T) {
	s := googleServer(t, true, "google-subject-1")
	state, c := start(t, s, "")
	resp := callback(t, s, state, "good", c)
	if resp.StatusCode != http.StatusFound || resp.Header.Get("Location") != "/" {
		t.Fatalf("%d %s", resp.StatusCode, resp.Header.Get("Location"))
	}
	var session string
	for _, ck := range resp.Cookies() {
		if ck.Name == "lyk_session" {
			session = ck.Value
		}
	}
	var me struct{ Email, Name string }
	if s.call(t, "GET", "/api/me", session, "", nil, &me); me.Email != "g@lab.org" || me.Name != "Grace" {
		t.Fatalf("%+v", me)
	}
}

func TestGoogleCLIHandOff(t *testing.T) {
	s := googleServer(t, true, "google-subject-1")
	state, c := start(t, s, "?cli_port=45123")
	loc := callback(t, s, state, "good", c).Header.Get("Location")
	if !strings.HasPrefix(loc, "http://127.0.0.1:45123/callback?token=") {
		t.Fatalf("location %q", loc)
	}
}

func TestGoogleRefusals(t *testing.T) {
	s := googleServer(t, true, "google-subject-1")
	state, c := start(t, s, "")
	if resp := callback(t, s, state+"x", "good", c); resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("state mismatch: %d", resp.StatusCode)
	}
	if resp, _ := noRedirect.Get(s.URL + "/auth/google?cli_port=80"); resp.StatusCode != http.StatusBadRequest {
		t.Fatal("a privileged CLI port was accepted")
	}
	u := googleServer(t, false, "google-subject-1")
	state, c = start(t, u, "")
	if resp := callback(t, u, state, "good", c); resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("unverified email: %d", resp.StatusCode)
	}
	missingSub := googleServer(t, true, "")
	state, c = start(t, missingSub, "")
	if resp := callback(t, missingSub, state, "good", c); resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("missing subject: %d", resp.StatusCode)
	}
}

func TestGoogleCallbackRejectsEmptyStateAndTamperedCookie(t *testing.T) {
	s := googleServer(t, true, "google-subject-1")
	state, c := start(t, s, "?cli_port=45123")

	if resp := callback(t, s, "", "good", c); resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("empty state: %d", resp.StatusCode)
	}

	tampered := *c
	parts := strings.Split(tampered.Value, "|")
	if len(parts) != 3 {
		t.Fatalf("unexpected OAuth cookie %q", tampered.Value)
	}
	parts[1] = "45124"
	tampered.Value = strings.Join(parts, "|")
	resp := callback(t, s, state, "good", &tampered)
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("tampered CLI port: %d %s", resp.StatusCode, resp.Header.Get("Location"))
	}

	invalidPort := *c
	invalidPort.Value = state + "|80|" + base64.RawURLEncoding.EncodeToString(oauthSignature(s.H.JWT.Secret, state, "80"))
	if resp := callback(t, s, state, "good", &invalidPort); resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("signed privileged CLI port: %d", resp.StatusCode)
	}

	malformed := *c
	malformed.Value = "not-a-valid-oauth-cookie"
	if resp := callback(t, s, state, "good", &malformed); resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("malformed cookie: %d", resp.StatusCode)
	}
}

func TestGoogleCallbackHandlesMalformedProviderURL(t *testing.T) {
	s := googleServer(t, true, "google-subject-1")
	s.H.Google.TokenURL = "%"
	state, c := start(t, s, "")
	if resp := callback(t, s, state, "good", c); resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("malformed token URL: %d", resp.StatusCode)
	}
}
