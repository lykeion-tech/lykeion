package handler

import (
	"io"
	"net/http"
	"strings"
	"sync"
	"testing"
	"time"
)

func TestEmailCodeSignIn(t *testing.T) {
	s := newServer(t)
	tok := s.signIn(t, "Ada@Lab.org ")
	var me struct{ Email string }
	if c := s.call(t, "GET", "/api/me", tok, "", nil, &me); c != 200 || me.Email != "ada@lab.org" {
		t.Fatalf("%d %+v", c, me)
	}
	if c := s.call(t, "GET", "/api/me", "", "", nil, nil); c != 401 {
		t.Fatalf("no token: %d", c)
	}
}

func TestTimezoneDefaultsToUTCAndAcceptsOnlyIANANames(t *testing.T) {
	s := newServer(t)
	tok := s.signIn(t, "tz@lab.org")
	var me struct{ Timezone string }
	if s.call(t, "GET", "/api/me", tok, "", nil, &me); me.Timezone != "UTC" {
		t.Fatalf("default: %q", me.Timezone)
	}
	for _, bad := range []string{"", "Local", "Mars/Olympus", "../etc/passwd"} {
		if c := s.call(t, "PATCH", "/api/me", tok, "", map[string]string{"timezone": bad}, nil); c != 400 {
			t.Fatalf("%q: %d", bad, c)
		}
	}
	if c := s.call(t, "PATCH", "/api/me", tok, "", map[string]string{"timezone": "Europe/Copenhagen"}, &me); c != 200 || me.Timezone != "Europe/Copenhagen" {
		t.Fatalf("%d %+v", c, me)
	}
	if s.call(t, "GET", "/api/me", tok, "", nil, &me); me.Timezone != "Europe/Copenhagen" {
		t.Fatalf("not stored: %q", me.Timezone)
	}
}

func TestCodeAllowsFiveAttemptsAndIsSingleUse(t *testing.T) {
	s := newServer(t)
	s.call(t, "POST", "/auth/send-code", "", "", map[string]string{"email": "b@lab.org"}, nil)
	good := s.Mail.code("b@lab.org")
	for i := 0; i < 5; i++ {
		if c := s.call(t, "POST", "/auth/verify-code", "", "", map[string]string{"email": "b@lab.org", "code": wrongCode(good)}, nil); c != 401 {
			t.Fatalf("wrong code %d: %d", i, c)
		}
	}
	if c := s.call(t, "POST", "/auth/verify-code", "", "", map[string]string{"email": "b@lab.org", "code": good}, nil); c != 401 {
		t.Fatal("the right code worked after five wrong attempts")
	}
	s.call(t, "POST", "/auth/send-code", "", "", map[string]string{"email": "b@lab.org"}, nil)
	good = s.Mail.code("b@lab.org")
	s.call(t, "POST", "/auth/verify-code", "", "", map[string]string{"email": "b@lab.org", "code": good}, nil)
	if c := s.call(t, "POST", "/auth/verify-code", "", "", map[string]string{"email": "b@lab.org", "code": good}, nil); c != 401 {
		t.Fatal("a code was accepted twice")
	}
}

func TestExpiredCodeRefused(t *testing.T) {
	s := newServer(t)
	s.call(t, "POST", "/auth/send-code", "", "", map[string]string{"email": "c@lab.org"}, nil)
	s.Env.Owner.Exec(t.Context(), "UPDATE verification_code SET expires_at = now() - interval '1 second'")
	if c := s.call(t, "POST", "/auth/verify-code", "", "", map[string]string{"email": "c@lab.org", "code": s.Mail.code("c@lab.org")}, nil); c != 401 {
		t.Fatalf("expired code: %d", c)
	}
}

func TestSendCodeRateLimited(t *testing.T) {
	s := newServer(t)
	codes := 0
	for i := 0; i < 7; i++ {
		if s.call(t, "POST", "/auth/send-code", "", "", map[string]string{"email": "d@lab.org"}, nil) == 204 {
			codes++
		}
	}
	if codes != 5 {
		t.Fatalf("sent %d codes, want 5 per hour", codes)
	}
}

func TestPersonalTokens(t *testing.T) {
	s := newServer(t)
	jwt := s.signIn(t, "e@lab.org")
	var created struct{ ID, Token string }
	if c := s.call(t, "POST", "/api/tokens", jwt, "", map[string]string{"name": "CLI (laptop)"}, &created); c != 201 {
		t.Fatalf("create: %d", c)
	}
	if c := s.call(t, "GET", "/api/me", created.Token, "", nil, nil); c != 200 {
		t.Fatalf("personal token refused: %d", c)
	}
	var list []map[string]any
	s.call(t, "GET", "/api/tokens", jwt, "", nil, &list)
	if len(list) != 1 || list[0]["token_hash"] != nil {
		t.Fatalf("list leaks or misses tokens: %+v", list)
	}
	if c := s.call(t, "DELETE", "/api/tokens/"+created.ID, jwt, "", nil, nil); c != 204 {
		t.Fatalf("revoke: %d", c)
	}
	if c := s.call(t, "GET", "/api/me", created.Token, "", nil, nil); c != 401 {
		t.Fatal("revoked token still works")
	}
}

func TestSessionCookie(t *testing.T) {
	s := newServer(t)
	s.call(t, "POST", "/auth/send-code", "", "", map[string]string{"email": "f@lab.org"}, nil)
	resp, err := http.Post(s.URL+"/auth/verify-code", "application/json",
		jsonBody(map[string]string{"email": "f@lab.org", "code": s.Mail.code("f@lab.org")}))
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	var cookie *http.Cookie
	for _, c := range resp.Cookies() {
		if c.Name == "lyk_session" {
			cookie = c
		}
	}
	if cookie == nil || !cookie.HttpOnly || cookie.SameSite != http.SameSiteLaxMode {
		t.Fatalf("session cookie: %+v", cookie)
	}
}

func TestConcurrentCodeSingleUse(t *testing.T) {
	s := newServer(t)
	email := "race@lab.org"
	if c := s.call(t, "POST", "/auth/send-code", "", "", map[string]string{"email": email}, nil); c != 204 {
		t.Fatal(c)
	}
	good := s.Mail.code(email)
	codes := concurrentVerify(t, s, email, good, 12)
	success := 0
	for _, c := range codes {
		if c == 200 {
			success++
		} else if c != 401 {
			t.Fatalf("unexpected status %d", c)
		}
	}
	if success != 1 {
		t.Fatalf("successful consumptions %d", success)
	}
}

func TestConcurrentCodeAttemptsStopAtFive(t *testing.T) {
	s := newServer(t)
	email := "attempts@lab.org"
	s.call(t, "POST", "/auth/send-code", "", "", map[string]string{"email": email}, nil)
	good := s.Mail.code(email)
	bad := "000000"
	if good == bad {
		bad = "999999"
	}
	for _, c := range concurrentVerify(t, s, email, bad, 12) {
		if c != 401 {
			t.Fatal(c)
		}
	}
	var attempts int
	if err := s.Env.Owner.QueryRow(t.Context(), "SELECT attempts FROM verification_code WHERE email=$1", email).Scan(&attempts); err != nil {
		t.Fatal(err)
	}
	if attempts != 5 {
		t.Fatalf("attempts=%d", attempts)
	}
	if c := s.call(t, "POST", "/auth/verify-code", "", "", map[string]string{"email": email, "code": good}, nil); c != 401 {
		t.Fatal(c)
	}
}

func concurrentVerify(t *testing.T, s *server, email, code string, n int) []int {
	t.Helper()
	start := make(chan struct{})
	var wg sync.WaitGroup
	codes := make([]int, n)
	for i := range n {
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			codes[i] = s.call(t, "POST", "/auth/verify-code", "", "", map[string]string{"email": email, "code": code}, nil)
		}()
	}
	close(start)
	wg.Wait()
	return codes
}

func TestEmptyTokensAndExpiryAndOwnership(t *testing.T) {
	s := newServer(t)
	a := s.signIn(t, "owner@lab.org")
	b := s.signIn(t, "other@lab.org")
	resp, err := http.NewRequest("GET", s.URL+"/api/tokens", nil)
	if err != nil {
		t.Fatal(err)
	}
	resp.Header.Set("Authorization", "Bearer "+a)
	out, err := http.DefaultClient.Do(resp)
	if err != nil {
		t.Fatal(err)
	}
	defer out.Body.Close()
	var raw strings.Builder
	_, err = io.Copy(&raw, out.Body)
	if err != nil {
		t.Fatal(err)
	}
	if strings.TrimSpace(raw.String()) != "[]" {
		t.Fatal(raw.String())
	}
	var created struct {
		ID, Token string
		ExpiresAt time.Time `json:"expires_at"`
	}
	if c := s.call(t, "POST", "/api/tokens", a, "", map[string]string{"name": "expiry"}, &created); c != 201 {
		t.Fatal(c)
	}
	if d := time.Until(created.ExpiresAt); d < 89*24*time.Hour || d > 91*24*time.Hour {
		t.Fatal(d)
	}
	if c := s.call(t, "DELETE", "/api/tokens/"+created.ID, b, "", nil, nil); c != 404 {
		t.Fatal(c)
	}
	if _, err := s.Env.Owner.Exec(t.Context(), "UPDATE personal_access_token SET expires_at=now()-interval '1 second'"); err != nil {
		t.Fatal(err)
	}
	if c := s.call(t, "GET", "/api/me", created.Token, "", nil, nil); c != 401 {
		t.Fatal(c)
	}
}

func wrongCode(good string) string {
	if good == "000000" {
		return "999999"
	}
	return "000000"
}

func TestCookieAuthLogoutAndCLIToken(t *testing.T) {
	s := newServer(t)
	email := "cookie@lab.org"
	jwt := s.signIn(t, email)
	req, err := http.NewRequest("GET", s.URL+"/api/me", nil)
	if err != nil {
		t.Fatal(err)
	}
	req.AddCookie(&http.Cookie{Name: "lyk_session", Value: jwt})
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != 200 {
		t.Fatal(resp.StatusCode)
	}
	var cli struct{ Token string }
	if c := s.call(t, "POST", "/api/cli-token", jwt, "", nil, &cli); c != 200 {
		t.Fatal(c)
	}
	if c := s.call(t, "GET", "/api/me", cli.Token, "", nil, nil); c != 200 {
		t.Fatal(c)
	}
	resp, err = http.Post(s.URL+"/auth/logout", "application/json", nil)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != 204 {
		t.Fatal(resp.StatusCode)
	}
	cleared := false
	for _, c := range resp.Cookies() {
		if c.Name == "lyk_session" && c.MaxAge < 0 && c.Value == "" {
			cleared = true
		}
	}
	if !cleared {
		t.Fatal("session cookie not cleared")
	}
}
