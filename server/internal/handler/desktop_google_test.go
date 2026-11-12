package handler

import (
	"bytes"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"sync"
	"testing"
	"time"
)

func TestDesktopGoogleUntrustedDeviceCannotStartSignIn(t *testing.T) {
	s := googleServer(t, true, "desktop-phishing")
	verifier := base64.RawURLEncoding.EncodeToString(bytes.Repeat([]byte{7}, 32))
	challenge := sha256.Sum256([]byte(verifier))
	resp, err := http.Post(s.URL+"/auth/desktop/google/start", "application/json", strings.NewReader(`{"challenge":"`+base64.RawURLEncoding.EncodeToString(challenge[:])+`"}`))
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusNotFound {
		t.Fatalf("attacker could access ticket endpoint: %d", resp.StatusCode)
	}
	ticket := base64.RawURLEncoding.EncodeToString(bytes.Repeat([]byte{8}, 32))
	browser, err := noRedirect.Get(s.URL + "/auth/google?desktop_ticket=" + ticket)
	if err != nil {
		t.Fatal(err)
	}
	defer browser.Body.Close()
	if browser.StatusCode != http.StatusServiceUnavailable {
		t.Fatalf("victim desktop handoff: %d", browser.StatusCode)
	}

	// A browser may still have a valid in-flight cookie from before disablement.
	state := base64.RawURLEncoding.EncodeToString(bytes.Repeat([]byte{9}, 32))
	sig := base64.RawURLEncoding.EncodeToString(oauthSignature(s.H.JWT.Secret, state, "", "", ticket))
	oldCookie := &http.Cookie{Name: oauthCookie, Value: state + "||desktop|" + ticket + "|" + sig}
	old := callback(t, s, state, "good", oldCookie)
	if old.StatusCode != http.StatusServiceUnavailable {
		t.Fatalf("old desktop callback: %d", old.StatusCode)
	}
	for _, cookie := range old.Cookies() {
		if cookie.Name == "lyk_session" {
			t.Fatal("old desktop callback set a session")
		}
	}
	exchange, err := http.Post(s.URL+"/auth/desktop/google/exchange", "application/json", strings.NewReader(`{"ticket":"`+ticket+`","verifier":"`+verifier+`"}`))
	if err != nil {
		t.Fatal(err)
	}
	defer exchange.Body.Close()
	if exchange.StatusCode != http.StatusNotFound {
		t.Fatalf("old exchange endpoint: %d", exchange.StatusCode)
	}
}

func TestDesktopGoogleVerifierBoundSingleUse(t *testing.T) {
	t.Skip("desktop Google sign-in is disabled pending cross-device confirmation")
	s := googleServer(t, true, "desktop-google")
	verifier := base64.RawURLEncoding.EncodeToString(bytes.Repeat([]byte{7}, 32))
	challengeRaw := sha256.Sum256([]byte(verifier))
	challenge := base64.RawURLEncoding.EncodeToString(challengeRaw[:])
	post := func(path, body string) *http.Response {
		t.Helper()
		resp, err := http.Post(s.URL+path, "application/json", strings.NewReader(body))
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { resp.Body.Close() })
		return resp
	}
	initial := post("/auth/desktop/google/start", `{"challenge":"`+challenge+`"}`)
	if initial.StatusCode != http.StatusOK {
		t.Fatalf("start: %d", initial.StatusCode)
	}
	var started struct {
		Ticket string `json:"ticket"`
	}
	if err := json.NewDecoder(initial.Body).Decode(&started); err != nil || len(started.Ticket) != 43 {
		t.Fatalf("ticket: %q, %v", started.Ticket, err)
	}
	mixed, err := noRedirect.Get(s.URL + "/auth/google?desktop_ticket=" + started.Ticket + "&cli_port=43123")
	if err != nil || mixed.StatusCode != http.StatusBadRequest {
		t.Fatalf("mixed start: %v %v", mixed, err)
	}
	mixed.Body.Close()
	state, ck := start(t, s, "?desktop_ticket="+started.Ticket)
	replayed, err := noRedirect.Get(s.URL + "/auth/google?desktop_ticket=" + started.Ticket)
	if err != nil || replayed.StatusCode == http.StatusFound {
		t.Fatalf("start ticket reused: %v %v", replayed, err)
	}
	replayed.Body.Close()
	done := callback(t, s, state, "good", ck)
	if done.Header.Get("Referrer-Policy") != "no-referrer" || done.Header.Get("Cache-Control") != "no-store" {
		t.Fatal("completion lacks privacy headers")
	}
	if done.StatusCode != http.StatusOK {
		t.Fatalf("callback: %d", done.StatusCode)
	}
	if loc := done.Header.Get("Location"); loc != "" {
		t.Fatalf("secret leaked to redirect: %q", loc)
	}
	for _, cookie := range done.Cookies() {
		if cookie.Name == "lyk_session" {
			t.Fatal("browser received desktop session")
		}
	}
	repeated := callback(t, s, state, "good", ck)
	if repeated.StatusCode == http.StatusOK {
		t.Fatal("callback reused")
	}
	wrong := base64.RawURLEncoding.EncodeToString(bytes.Repeat([]byte{9}, 32))
	r := post("/auth/desktop/google/exchange", `{"ticket":"`+started.Ticket+`","verifier":"`+wrong+`"}`)
	if r.StatusCode == http.StatusOK {
		t.Fatal("wrong verifier accepted")
	}
	r = post("/auth/desktop/google/exchange", `{"ticket":"`+started.Ticket+`","verifier":"`+verifier+`"}`)
	if r.StatusCode != http.StatusOK {
		b, _ := io.ReadAll(r.Body)
		t.Fatalf("exchange: %d %s", r.StatusCode, b)
	}
	var result struct {
		Token string `json:"token"`
	}
	if err := json.NewDecoder(r.Body).Decode(&result); err != nil || result.Token == "" {
		t.Fatalf("missing token: %v", err)
	}
	r = post("/auth/desktop/google/exchange", `{"ticket":"`+started.Ticket+`","verifier":"`+verifier+`"}`)
	if r.StatusCode == http.StatusOK {
		t.Fatal("ticket reused")
	}
}

func TestDesktopGooglePollingIsBoundPerVerifier(t *testing.T) {
	t.Skip("desktop Google sign-in is disabled pending cross-device confirmation")
	s := googleServer(t, true, "desktop-poll")
	verifier := base64.RawURLEncoding.EncodeToString(bytes.Repeat([]byte{7}, 32))
	challengeRaw := sha256.Sum256([]byte(verifier))
	challenge := base64.RawURLEncoding.EncodeToString(challengeRaw[:])
	post := func(path, body string) *http.Response {
		t.Helper()
		resp, err := http.Post(s.URL+path, "application/json", strings.NewReader(body))
		if err != nil {
			t.Fatal(err)
		}
		return resp
	}
	for flow := 0; flow < 4; flow++ {
		r := post("/auth/desktop/google/start", `{"challenge":"`+challenge+`"}`)
		var started struct {
			Ticket string `json:"ticket"`
		}
		if err := json.NewDecoder(r.Body).Decode(&started); err != nil {
			t.Fatal(err)
		}
		r.Body.Close()
		for n := 0; n < 32; n++ {
			r = post("/auth/desktop/google/exchange", `{"ticket":"`+started.Ticket+`","verifier":"`+verifier+`"}`)
			if r.StatusCode != http.StatusAccepted {
				t.Fatalf("flow %d poll %d: %d", flow, n, r.StatusCode)
			}
			r.Body.Close()
		}
		r = post("/auth/desktop/google/exchange", `{"ticket":"`+started.Ticket+`","verifier":"`+verifier+`"}`)
		if r.StatusCode != http.StatusTooManyRequests {
			t.Fatalf("unbounded poll: %d", r.StatusCode)
		}
		r.Body.Close()
	}
	newFlow := post("/auth/desktop/google/start", `{"challenge":"`+challenge+`"}`)
	var blocked struct {
		Ticket string `json:"ticket"`
	}
	if err := json.NewDecoder(newFlow.Body).Decode(&blocked); err != nil {
		t.Fatal(err)
	}
	newFlow.Body.Close()
	ttl, err := s.Env.Redis.TTL(t.Context(), desktopGoogleKey(blocked.Ticket)).Result()
	if err != nil || ttl <= 0 || ttl > desktopGoogleTTL {
		t.Fatalf("ticket TTL: %v, %v", ttl, err)
	}
	if err := s.Env.Redis.Set(t.Context(), "lyk:rl:google:desktop:exchange:ip:127.0.0.1", 1000, time.Hour).Err(); err != nil {
		t.Fatal(err)
	}
	blockedPoll := post("/auth/desktop/google/exchange", `{"ticket":"`+blocked.Ticket+`","verifier":"`+verifier+`"}`)
	if blockedPoll.StatusCode != http.StatusTooManyRequests {
		t.Fatalf("blocked transaction: %d", blockedPoll.StatusCode)
	}
	blockedPoll.Body.Close()
	if s.Env.Redis.HExists(t.Context(), desktopGoogleKey(blocked.Ticket), "polls").Val() {
		t.Fatal("rate-limited request mutated transaction")
	}
	r := post("/auth/desktop/google/exchange", `{"ticket":"`+verifier+`","verifier":"`+verifier+`"}`)
	defer r.Body.Close()
	if r.StatusCode != http.StatusTooManyRequests {
		t.Fatalf("invalid verifier limit bypassed: %d", r.StatusCode)
	}
}

func TestDesktopGoogleConcurrentPollLimit(t *testing.T) {
	t.Skip("desktop Google sign-in is disabled pending cross-device confirmation")
	s := googleServer(t, true, "desktop-concurrent")
	verifier := base64.RawURLEncoding.EncodeToString(bytes.Repeat([]byte{8}, 32))
	challengeRaw := sha256.Sum256([]byte(verifier))
	challenge := base64.RawURLEncoding.EncodeToString(challengeRaw[:])
	resp, err := http.Post(s.URL+"/auth/desktop/google/start", "application/json", strings.NewReader(`{"challenge":"`+challenge+`"}`))
	if err != nil {
		t.Fatal(err)
	}
	var started struct {
		Ticket string `json:"ticket"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&started); err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	statuses := make(chan int, 40)
	var wg sync.WaitGroup
	for i := 0; i < 40; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			r, e := http.Post(s.URL+"/auth/desktop/google/exchange", "application/json", strings.NewReader(`{"ticket":"`+started.Ticket+`","verifier":"`+verifier+`"}`))
			if e != nil {
				statuses <- 0
				return
			}
			statuses <- r.StatusCode
			r.Body.Close()
		}()
	}
	wg.Wait()
	close(statuses)
	pending, limited := 0, 0
	for status := range statuses {
		switch status {
		case http.StatusAccepted:
			pending++
		case http.StatusTooManyRequests:
			limited++
		default:
			t.Fatalf("unexpected status %d", status)
		}
	}
	if pending != 32 || limited != 8 {
		t.Fatalf("pending %d limited %d", pending, limited)
	}
}
