package handler

import (
	"bytes"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"regexp"
	"strings"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/google/uuid"
)

// A victim opening an attacker's link must not authorize the attacker's app.
func TestDesktopGoogleManualCodeBindsVictimToTheirOwnFlow(t *testing.T) {
	s := googleServer(t, true, "manual-binding")
	encode := base64.RawURLEncoding.EncodeToString
	attackerVerifier := encode(bytes.Repeat([]byte{1}, 32))
	victimVerifier := encode(bytes.Repeat([]byte{2}, 32))
	start := func(verifier string) string {
		t.Helper()
		hash := sha256.Sum256([]byte(verifier))
		w := httptest.NewRecorder()
		req := httptest.NewRequest("POST", "/auth/desktop/google/start", strings.NewReader(`{"challenge":"`+encode(hash[:])+`"}`))
		s.H.desktopGoogleStart(w, req)
		if w.Code != 200 {
			t.Fatalf("start: %d %s", w.Code, w.Body.String())
		}
		var v struct {
			Ticket string `json:"ticket"`
		}
		if err := json.Unmarshal(w.Body.Bytes(), &v); err != nil {
			t.Fatal(err)
		}
		return v.Ticket
	}
	complete := func(ticket string) string {
		t.Helper()
		req := httptest.NewRequest("GET", "/auth/google/callback", nil)
		w := httptest.NewRecorder()
		if !s.H.desktopGoogleBegin(w, req, ticket) {
			t.Fatal("begin failed")
		}
		s.H.desktopGoogleComplete(w, req, ticket, uuid.New(), "victim+<script>@example.org")
		if w.Code != 200 {
			t.Fatalf("complete: %d", w.Code)
		}
		for key, value := range map[string]string{
			"Cache-Control":           "no-store",
			"Referrer-Policy":         "no-referrer",
			"Content-Security-Policy": "default-src 'none'; base-uri 'none'; form-action 'none'; frame-ancestors 'none'",
			"X-Content-Type-Options":  "nosniff",
		} {
			if w.Header().Get(key) != value {
				t.Fatalf("unsafe %s: %q", key, w.Header().Get(key))
			}
		}
		if strings.Contains(w.Body.String(), "<script>") || !strings.Contains(w.Body.String(), "&lt;script&gt;") {
			t.Fatal("Google account was not HTML escaped")
		}
		if w.Header().Get("Location") != "" || len(w.Result().Cookies()) != 0 || !strings.Contains(w.Header().Get("Content-Security-Policy"), "frame-ancestors 'none'") {
			t.Fatal("browser completion leaked a credential or permits framing")
		}
		found := regexp.MustCompile(`id="desktop-code">([A-Za-z0-9_-]{43})</code>`).FindStringSubmatch(w.Body.String())
		if len(found) != 2 {
			t.Fatalf("missing browser-only code: %s", w.Body.String())
		}
		return found[1]
	}
	exchange := func(ticket, verifier, code string) int {
		t.Helper()
		w := httptest.NewRecorder()
		req := httptest.NewRequest("POST", "/auth/desktop/google/exchange", strings.NewReader(`{"ticket":"`+ticket+`","verifier":"`+verifier+`","code":"`+code+`"}`))
		s.H.desktopGoogleExchange(w, req)
		return w.Code
	}
	attackerTicket := start(attackerVerifier)
	victimTicket := start(victimVerifier)
	attackerLinkCode := complete(attackerTicket)
	victimCode := complete(victimTicket)
	stored, err := s.H.Limit.R.HGet(t.Context(), desktopGoogleKey(victimTicket), "code_hash").Result()
	if err != nil || stored == victimCode || stored == "" {
		t.Fatalf("browser code was not stored as a hash: %v", err)
	}
	if status := exchange(attackerTicket, attackerVerifier, ""); status == 200 {
		t.Fatal("attacker exchanged without browser secret")
	}
	if status := exchange(victimTicket, victimVerifier, attackerLinkCode); status == 200 {
		t.Fatal("attacker-link code authorized victim flow")
	}
	if status := exchange(attackerTicket, attackerVerifier, victimCode); status == 200 {
		t.Fatal("victim code authorized attacker flow")
	}
	if status := exchange(victimTicket, victimVerifier, victimCode); status != 200 {
		t.Fatalf("victim flow failed: %d", status)
	}
	if status := exchange(victimTicket, victimVerifier, victimCode); status == 200 {
		t.Fatal("browser code reused")
	}
	concurrentTicket := start(victimVerifier)
	concurrentCode := complete(concurrentTicket)
	var success atomic.Int32
	var wait sync.WaitGroup
	for range 2 {
		wait.Add(1)
		go func() {
			defer wait.Done()
			if exchange(concurrentTicket, victimVerifier, concurrentCode) == 200 {
				success.Add(1)
			}
		}()
	}
	wait.Wait()
	if success.Load() != 1 {
		t.Fatalf("concurrent exchange succeeded %d times", success.Load())
	}
	expiredTicket := start(victimVerifier)
	expiredCode := complete(expiredTicket)
	if err := s.H.Limit.R.Del(t.Context(), desktopGoogleKey(expiredTicket)).Err(); err != nil {
		t.Fatal(err)
	}
	if status := exchange(expiredTicket, victimVerifier, expiredCode); status == 200 {
		t.Fatal("expired browser code accepted")
	}
	limitedTicket := start(victimVerifier)
	limitedCode := complete(limitedTicket)
	for range 5 {
		if status := exchange(limitedTicket, victimVerifier, attackerLinkCode); status == 200 {
			t.Fatal("wrong browser code accepted")
		}
	}
	if status := exchange(limitedTicket, victimVerifier, limitedCode); status == 200 {
		t.Fatal("ticket survived too many wrong browser codes")
	}
}

func TestDesktopGoogleTwoBrowserTwoAppPhishing(t *testing.T) {
	s := googleServer(t, true, "manual-http-victim")
	mux := http.NewServeMux()
	mux.HandleFunc("POST /auth/desktop/google/start", s.H.desktopGoogleStart)
	mux.HandleFunc("POST /auth/desktop/google/exchange", s.H.desktopGoogleExchange)
	mux.HandleFunc("GET /auth/google", func(w http.ResponseWriter, r *http.Request) { s.H.googleStartWithDesktop(w, r, true) })
	mux.HandleFunc("GET /auth/google/callback", func(w http.ResponseWriter, r *http.Request) { s.H.googleCallbackWithDesktop(w, r, true) })
	server := httptest.NewServer(mux)
	defer server.Close()
	enc := base64.RawURLEncoding.EncodeToString
	attackerVerifier := enc(bytes.Repeat([]byte{21}, 32))
	victimVerifier := enc(bytes.Repeat([]byte{22}, 32))
	newTicket := func(verifier string) string {
		t.Helper()
		challenge := sha256.Sum256([]byte(verifier))
		r, err := http.Post(server.URL+"/auth/desktop/google/start", "application/json", strings.NewReader(`{"challenge":"`+enc(challenge[:])+`"}`))
		if err != nil {
			t.Fatal(err)
		}
		defer r.Body.Close()
		if r.StatusCode != 200 {
			t.Fatalf("start %d", r.StatusCode)
		}
		var data struct {
			Ticket string `json:"ticket"`
		}
		if err = json.NewDecoder(r.Body).Decode(&data); err != nil {
			t.Fatal(err)
		}
		return data.Ticket
	}
	browserComplete := func(ticket string) string {
		t.Helper()
		browser := &http.Client{CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
		r, err := browser.Get(server.URL + "/auth/google?desktop_ticket=" + ticket)
		if err != nil {
			t.Fatal(err)
		}
		defer r.Body.Close()
		if r.StatusCode != 302 {
			t.Fatalf("browser start %d", r.StatusCode)
		}
		target, err := url.Parse(r.Header.Get("Location"))
		if err != nil {
			t.Fatal(err)
		}
		cookies := r.Cookies()
		if len(cookies) != 1 {
			t.Fatal("missing OAuth cookie")
		}
		req, err := http.NewRequest("GET", server.URL+"/auth/google/callback?state="+url.QueryEscape(target.Query().Get("state"))+"&code=good", nil)
		if err != nil {
			t.Fatal(err)
		}
		req.AddCookie(cookies[0])
		result, err := browser.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		defer result.Body.Close()
		raw, err := io.ReadAll(result.Body)
		if err != nil {
			t.Fatal(err)
		}
		if result.StatusCode != 200 {
			t.Fatalf("callback %d: %s", result.StatusCode, raw)
		}
		if result.Header.Get("Location") != "" || result.Header.Get("Cache-Control") != "no-store" || result.Header.Get("Referrer-Policy") != "no-referrer" {
			t.Fatal("unsafe browser response")
		}
		for _, cookie := range result.Cookies() {
			if cookie.Name == "lyk_session" {
				t.Fatal("browser received desktop session")
			}
		}
		code := regexp.MustCompile(`id="desktop-code">([A-Za-z0-9_-]{43})</code>`).FindStringSubmatch(string(raw))
		if len(code) != 2 {
			t.Fatalf("missing manual code: %s", raw)
		}
		return code[1]
	}
	exchange := func(ticket, verifier, code string) int {
		t.Helper()
		r, err := http.Post(server.URL+"/auth/desktop/google/exchange", "application/json", strings.NewReader(`{"ticket":"`+ticket+`","verifier":"`+verifier+`","code":"`+code+`"}`))
		if err != nil {
			t.Fatal(err)
		}
		defer r.Body.Close()
		return r.StatusCode
	}
	// Attacker owns A; the victim merely follows A's link in their own browser.
	attackerTicket := newTicket(attackerVerifier)
	attackerLinkCode := browserComplete(attackerTicket)
	// Victim independently starts B on their own app and browser.
	victimTicket := newTicket(victimVerifier)
	victimCode := browserComplete(victimTicket)
	if exchange(attackerTicket, attackerVerifier, "") == 200 {
		t.Fatal("attacker exchanged without browser code")
	}
	if exchange(victimTicket, victimVerifier, attackerLinkCode) == 200 {
		t.Fatal("attacker link code authorized victim app")
	}
	if exchange(attackerTicket, attackerVerifier, victimCode) == 200 {
		t.Fatal("victim flow code authorized attacker app")
	}
	if got := exchange(victimTicket, victimVerifier, victimCode); got != 200 {
		t.Fatalf("victim exchange %d", got)
	}
	if exchange(victimTicket, victimVerifier, victimCode) == 200 {
		t.Fatal("replay accepted")
	}
}
