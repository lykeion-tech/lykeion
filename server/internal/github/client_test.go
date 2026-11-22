package github

import (
	"context"
	"crypto/rand"
	"crypto/rsa"
	"crypto/x509"
	"encoding/pem"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/golang-jwt/jwt/v5"
)

func testAppKey(t *testing.T) *rsa.PrivateKey {
	t.Helper()
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}
	return key
}

func TestConfigFromEnv(t *testing.T) {
	key := testAppKey(t)
	env := map[string]string{
		"GITHUB_APP_ID": "42", "GITHUB_APP_SLUG": "lykeion", "GITHUB_APP_CLIENT_ID": "client-id",
		"GITHUB_APP_CLIENT_SECRET": "client-secret", "GITHUB_WEBHOOK_SECRET": "webhook-secret",
		"GITHUB_APP_PRIVATE_KEY": string(pem.EncodeToMemory(&pem.Block{
			Type: "RSA PRIVATE KEY", Bytes: x509.MarshalPKCS1PrivateKey(key),
		})),
	}
	getenv := func(k string) string { return env[k] }
	cfg, err := ConfigFromEnv(getenv)
	if err != nil || !cfg.CanConnect() || cfg.AppID != 42 || cfg.Slug != "lykeion" ||
		cfg.PrivateKey == nil || !cfg.PrivateKey.Equal(key) || cfg.ClientID != "client-id" ||
		cfg.ClientSecret != "client-secret" || cfg.WebhookSecret != "webhook-secret" ||
		cfg.APIURL != "https://api.github.com" || cfg.WebURL != "https://github.com" {
		t.Fatalf("usable config was not loaded, error=%v", err)
	}
	delete(env, "GITHUB_APP_CLIENT_SECRET")
	cfg, err = ConfigFromEnv(getenv)
	if err != nil || cfg.CanConnect() {
		t.Fatalf("missing client secret should disable Connect, error=%v", err)
	}
	for _, name := range []string{"GITHUB_APP_ID", "GITHUB_APP_PRIVATE_KEY"} {
		t.Run(name, func(t *testing.T) {
			original := env[name]
			defer func() { env[name] = original }()
			env[name] = "sensitive-invalid-value"
			_, err := ConfigFromEnv(getenv)
			if err == nil || !strings.Contains(err.Error(), name) || strings.Contains(err.Error(), env[name]) {
				t.Fatal("config error did not identify the variable safely")
			}
		})
	}
}

func TestAppJWTAndInstallationToken(t *testing.T) {
	key := testAppKey(t)
	now := time.Unix(1_800_000_000, 0)
	api := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Accept") != "application/vnd.github+json" || r.Header.Get("X-GitHub-Api-Version") != "2022-11-28" {
			t.Error("missing GitHub API request headers")
		}
		switch r.URL.Path {
		case "/app/installations/19/access_tokens":
			if r.Method != http.MethodPost {
				t.Error("installation token request used the wrong method")
			}
			raw, ok := strings.CutPrefix(r.Header.Get("Authorization"), "Bearer ")
			if !ok {
				t.Error("installation token request lacks bearer authorization")
			}
			claims := new(jwt.RegisteredClaims)
			token, err := jwt.ParseWithClaims(raw, claims, func(*jwt.Token) (any, error) {
				return &key.PublicKey, nil
			}, jwt.WithValidMethods([]string{"RS256"}), jwt.WithTimeFunc(func() time.Time { return now }))
			if err != nil || !token.Valid || claims.Issuer != "42" || claims.IssuedAt == nil ||
				!claims.IssuedAt.Equal(now.Add(-time.Minute)) || claims.ExpiresAt == nil ||
				!claims.ExpiresAt.Equal(now.Add(9*time.Minute)) {
				t.Error("installation token request used an invalid App JWT")
			}
			fmt.Fprint(w, `{"token":"ghs_secret"}`)
		case "/broken":
			if r.Method != http.MethodGet || r.Header.Get("Authorization") != "Bearer ghs_secret" {
				t.Error("API GET used the wrong method or authorization")
			}
			w.WriteHeader(http.StatusInternalServerError)
			fmt.Fprint(w, `{"message":"ghs_secret ----BEGIN RSA PRIVATE KEY---- https://host.example/private"}`)
		default:
			t.Error("unexpected GitHub API path")
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	defer api.Close()
	s := &Service{Config: Config{AppID: 42, PrivateKey: key, APIURL: api.URL}, now: func() time.Time { return now }}
	token, err := s.installationToken(context.Background(), 19)
	if err != nil || token != "ghs_secret" {
		t.Fatalf("installation token exchange failed, error=%v", err)
	}
	err = s.get(context.Background(), token, "/broken", nil)
	var apiErr *APIError
	if !errors.As(err, &apiErr) || apiErr.Status != http.StatusInternalServerError || apiErr.Message == "" {
		t.Fatalf("expected an API error with status 500, got %v", err)
	}
	for _, value := range []string{"ghs_", "BEGIN", "host.example"} {
		if strings.Contains(err.Error(), value) || strings.Contains(apiErr.Message, value) {
			t.Fatal("API error exposed remote response content")
		}
	}
}

func TestGetRefusesUntrustedURLAndRedirect(t *testing.T) {
	target := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
		t.Error("external server received a request")
	}))
	defer target.Close()
	api := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/redirect" {
			t.Error("API server received an invalid path")
		}
		http.Redirect(w, r, target.URL, http.StatusTemporaryRedirect)
	}))
	defer api.Close()
	s := &Service{Config: Config{APIURL: api.URL}, HTTP: api.Client()}
	for _, path := range []string{target.URL, "//" + strings.TrimPrefix(target.URL, "http://"), "/redirect"} {
		err := s.get(context.Background(), "ghs_secret", path, nil)
		if err == nil || strings.Contains(err.Error(), target.URL) || strings.Contains(err.Error(), "ghs_secret") {
			t.Fatal("untrusted request path or redirect was not safely rejected")
		}
	}
}

func TestPageLinksStayOnTheCurrentEndpoint(t *testing.T) {
	var origin string
	api := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		link := map[string]string{
			"1": origin + "/repos/acme/tools/pulls?state=open&page=2",
			"2": "?state=open&page=3",
			"3": "https://external.test/repos/acme/tools/pulls?page=4",
			"4": origin + "/other?page=5",
			"5": origin + "/repositories/39/pulls?state=all&page=6",
		}[r.URL.Query().Get("page")]
		w.Header().Set("Link", fmt.Sprintf(`<%s>; rel="next", </repos/acme/tools/pulls?page=9>; rel="last"`, link))
		fmt.Fprint(w, `[]`)
	}))
	defer api.Close()
	origin = api.URL
	s := &Service{Config: Config{APIURL: origin}}
	for _, tc := range []struct {
		page, want string
		wantErr    bool
	}{
		{"1", "/repos/acme/tools/pulls?page=2&state=open", false},
		{"2", "/repos/acme/tools/pulls?page=3&state=open", false},
		{"3", "", true},
		{"4", "", true},
		{"5", "/repos/acme/tools/pulls?page=6&state=open", false},
	} {
		var out []int
		next, err := s.getPage(context.Background(), "ghs_secret", "/repos/acme/tools/pulls?state=open&page="+tc.page, &out)
		if next != tc.want || (err != nil) != tc.wantErr || (err != nil && strings.Contains(err.Error(), "external.test")) || out == nil {
			t.Fatalf("page %s produced next=%q, err=%v, out=%v", tc.page, next, err, out)
		}
	}
}

func TestAPIRateLimitMetadata(t *testing.T) {
	api := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Retry-After", "7")
		w.Header().Set("X-RateLimit-Reset", "1800000009")
		w.WriteHeader(http.StatusForbidden)
	}))
	defer api.Close()
	s := &Service{Config: Config{APIURL: api.URL}}
	err := s.get(context.Background(), "ghs_secret", "/limited", nil)
	var apiErr *APIError
	if !errors.As(err, &apiErr) || apiErr.Status != 403 || !apiErr.RetryAfterSet || apiErr.RetryAfter != 7*time.Second ||
		!apiErr.RateLimitReset.Equal(time.Unix(1_800_000_009, 0)) {
		t.Fatalf("rate-limit metadata was not retained, error=%v", err)
	}
}

type githubRoundTripper func(*http.Request) (*http.Response, error)

func (f githubRoundTripper) RoundTrip(req *http.Request) (*http.Response, error) {
	return f(req)
}

func TestGetDropsTransportErrorDetails(t *testing.T) {
	s := &Service{HTTP: &http.Client{Transport: githubRoundTripper(func(*http.Request) (*http.Response, error) {
		return nil, errors.New("transport details and ghs_secret")
	})}}
	err := s.get(context.Background(), "ghs_secret", "/endpoint?credential=sensitive-query", nil)
	if err == nil || errors.Unwrap(err) != nil || strings.Contains(err.Error(), "ghs_secret") ||
		strings.Contains(err.Error(), "transport details") || strings.Contains(err.Error(), "sensitive-query") {
		t.Fatal("transport error exposed request details")
	}
}

func TestScrubRemovesSecretsWithoutUnwrapping(t *testing.T) {
	err := scrub(fmt.Errorf("request: %w", errors.New("ghs_secret and client-secret")), "ghs_secret", "client-secret", "")
	if err == nil || errors.Unwrap(err) != nil || strings.Contains(err.Error(), "ghs_secret") ||
		strings.Contains(err.Error(), "client-secret") {
		t.Fatal("scrub exposed a supplied credential")
	}
	if scrub(nil, "ghs_secret") != nil {
		t.Fatal("scrub introduced an error")
	}
}

func TestDoRejectsCredentialedRequestOutsideConfiguredOrigins(t *testing.T) {
	target := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
		t.Error("untrusted server received a request")
	}))
	defer target.Close()
	s := &Service{}
	req, err := http.NewRequest(http.MethodPost, target.URL, strings.NewReader("client_secret=secret"))
	if err != nil {
		t.Fatal(err)
	}
	resp, err := s.do(req)
	if resp != nil {
		resp.Body.Close()
	}
	if err == nil || strings.Contains(err.Error(), target.URL) || strings.Contains(err.Error(), "secret") {
		t.Fatal("untrusted request was not safely rejected")
	}
}

func TestDoDoesNotForwardOAuthFormOnRedirect(t *testing.T) {
	api := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/redirected" {
			t.Error("redirect received a second request")
			return
		}
		http.Redirect(w, r, "/redirected", http.StatusTemporaryRedirect)
	}))
	defer api.Close()
	s := &Service{Config: Config{WebURL: api.URL}}
	req, err := http.NewRequest(http.MethodPost, api.URL+"/login/oauth/access_token", strings.NewReader("client_secret=secret"))
	if err != nil {
		t.Fatal(err)
	}
	resp, err := s.do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusTemporaryRedirect {
		t.Fatalf("returned status = %d, want 307", resp.StatusCode)
	}
}
