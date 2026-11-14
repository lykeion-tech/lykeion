package handler

import (
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/redis/go-redis/v9"
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

func TestDesktopExchangeIsNotExposed(t *testing.T) {
	s := googleServer(t, true, "disabled-desktop")
	resp, err := http.Post(s.URL+"/auth/desktop/exchange", "application/json", strings.NewReader(`{}`))
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusNotFound {
		t.Fatalf("desktop exchange exposed: %d", resp.StatusCode)
	}
}

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

func TestGoogleCLINonceHandOff(t *testing.T) {
	s := googleServer(t, true, "nonce-subject")
	nonce := base64.RawURLEncoding.EncodeToString([]byte(strings.Repeat("n", 32)))
	state, c := start(t, s, "?cli_port=45123&cli_state="+nonce)
	loc, err := url.Parse(callback(t, s, state, "good", c).Header.Get("Location"))
	if err != nil || loc.Query().Get("state") != nonce || loc.Query().Get("token") == "" {
		t.Fatalf("nonce handoff: %v %v", loc, err)
	}
	tampered := *c
	parts := strings.Split(c.Value, "|")
	if len(parts) != 4 {
		t.Fatalf("nonce not bound in cookie: %s", c.Value)
	}
	parts[2] = base64.RawURLEncoding.EncodeToString([]byte(strings.Repeat("x", 32)))
	tampered.Value = strings.Join(parts, "|")
	if resp := callback(t, s, state, "good", &tampered); resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("tampered nonce: %d", resp.StatusCode)
	}
	for _, query := range []string{"?cli_state=" + nonce, "?cli_port=45123&cli_state=bad", "?cli_port=45123&cli_state=" + strings.Repeat("x", 200)} {
		resp, err := noRedirect.Get(s.URL + "/auth/google" + query)
		if err != nil {
			t.Fatal(err)
		}
		resp.Body.Close()
		if resp.StatusCode != http.StatusBadRequest {
			t.Fatalf("invalid nonce accepted: %s %d", query, resp.StatusCode)
		}
	}
}

func TestGoogleIPLimitsFailClosedBeforeProvider(t *testing.T) {
	for _, route := range []string{"start", "callback"} {
		for _, outage := range []bool{false, true} {
			t.Run(fmt.Sprintf("%s/outage=%v", route, outage), func(t *testing.T) {
				s := googleServer(t, true, "limited-subject")
				state, c := start(t, s, "")
				var contacted atomic.Int32
				provider := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { contacted.Add(1); w.WriteHeader(500) }))
				defer provider.Close()
				s.H.Google.TokenURL = provider.URL
				status := 429
				if outage {
					s.H.Limit.R = redis.NewClient(s.Env.Redis.Options())
					if err := s.H.Limit.R.Close(); err != nil {
						t.Fatal(err)
					}
					status = 503
				} else {
					key := "google:" + route + ":ip:127.0.0.1"
					if err := s.Env.Redis.Set(t.Context(), "lyk:rl:"+key, 1000, time.Hour).Err(); err != nil {
						t.Fatal(err)
					}
				}
				var resp *http.Response
				if route == "callback" {
					resp = callback(t, s, state, "good", c)
				} else {
					var err error
					resp, err = noRedirect.Get(s.URL + "/auth/google")
					if err != nil {
						t.Fatal(err)
					}
					defer resp.Body.Close()
				}
				if resp.StatusCode != status {
					t.Fatalf("want %d got %d", status, resp.StatusCode)
				}
				if contacted.Load() != 0 {
					t.Fatal("rate-limited/outage request contacted provider")
				}
				if len(resp.Cookies()) != 0 {
					t.Fatal("refused request changed auth cookies")
				}
			})
		}
	}
}

func TestGoogleVerifiedIdentityLimitAndOutageDoNotMintSession(t *testing.T) {
	for _, outage := range []bool{false, true} {
		t.Run(fmt.Sprintf("outage=%v", outage), func(t *testing.T) {
			s := googleServer(t, true, "identity-subject")
			state, c := start(t, s, "")
			status := 429
			if outage {
				s.H.Limit.R = redis.NewClient(s.Env.Redis.Options())
				// Make Redis disappear only after the callback IP check and verified identity.
				provider := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					if r.URL.Path == "/token" {
						json.NewEncoder(w).Encode(map[string]string{"access_token": "at"})
						return
					}
					if err := s.H.Limit.R.Close(); err != nil {
						t.Error(err)
					}
					json.NewEncoder(w).Encode(map[string]any{"sub": "identity-subject", "email": "G@Lab.org", "email_verified": true})
				}))
				defer provider.Close()
				s.H.Google.TokenURL, s.H.Google.UserinfoURL = provider.URL+"/token", provider.URL+"/userinfo"
				status = 503
			} else {
				if err := s.Env.Redis.Set(t.Context(), "lyk:rl:google:email:g@lab.org", 1000, time.Hour).Err(); err != nil {
					t.Fatal(err)
				}
			}
			resp := callback(t, s, state, "good", c)
			if resp.StatusCode != status {
				t.Fatalf("want %d got %d", status, resp.StatusCode)
			}
			for _, cookie := range resp.Cookies() {
				if cookie.Name == "lyk_session" {
					t.Fatal("minted session after identity refusal")
				}
			}
			var n int
			if err := s.Env.Owner.QueryRow(t.Context(), `SELECT count(*) FROM "user" WHERE email='g@lab.org'`).Scan(&n); err != nil {
				t.Fatal(err)
			}
			if n != 0 {
				t.Fatal("created user after identity refusal")
			}
		})
	}
}
