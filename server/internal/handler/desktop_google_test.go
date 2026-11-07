package handler

import (
	"bytes"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"testing"
)

func desktopCode(t *testing.T, s *server, verifier string) string {
	t.Helper()
	if s.H.PublicURL == "" {
		s.H.PublicURL = s.URL
	}
	digest := sha256.Sum256([]byte(verifier))
	challenge := base64.RawURLEncoding.EncodeToString(digest[:])
	nonce := base64.RawURLEncoding.EncodeToString(bytes.Repeat([]byte{2}, 32))
	q := url.Values{"desktop_port": {"45123"}, "desktop_state": {nonce}, "desktop_challenge": {challenge}}
	state, c := start(t, s, "?"+q.Encode())
	response := callback(t, s, state, "good", c)
	if response.StatusCode != http.StatusFound {
		t.Fatalf("callback status %d", response.StatusCode)
	}
	loc, err := url.Parse(response.Header.Get("Location"))
	if err != nil {
		t.Fatal(err)
	}
	if loc.Scheme != "http" || loc.Host != "127.0.0.1:45123" || loc.Path != "/callback" || loc.RawQuery != "" {
		t.Fatalf("unexpected handoff %q", loc)
	}
	fragment, err := url.ParseQuery(loc.Fragment)
	if err != nil || fragment.Get("state") != nonce || fragment.Get("code") == "" || strings.Contains(loc.String(), "token=") {
		t.Fatalf("unsafe handoff %q", loc)
	}
	return fragment.Get("code")
}
func redeemDesktop(t *testing.T, s *server, code, verifier, host string) *http.Response {
	t.Helper()
	body, _ := json.Marshal(map[string]string{"code": code, "verifier": verifier})
	req, err := http.NewRequest("POST", s.URL+"/auth/desktop/exchange", bytes.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Content-Type", "application/json")
	if host != "" {
		req.Host = host
	}
	resp, err := noRedirect.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { resp.Body.Close() })
	return resp
}
func TestDesktopGoogleProofBoundSingleUse(t *testing.T) {
	s := googleServer(t, true, "desktop-subject")
	verifier := base64.RawURLEncoding.EncodeToString(bytes.Repeat([]byte{1}, 32))
	code := desktopCode(t, s, verifier)
	if resp := redeemDesktop(t, s, code, base64.RawURLEncoding.EncodeToString(bytes.Repeat([]byte{3}, 32)), ""); resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("wrong verifier: %d", resp.StatusCode)
	}
	if resp := redeemDesktop(t, s, code, verifier, "other.invalid"); resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("wrong origin: %d", resp.StatusCode)
	}
	var wg sync.WaitGroup
	statuses := make(chan int, 2)
	for range 2 {
		wg.Add(1)
		go func() { defer wg.Done(); statuses <- redeemDesktop(t, s, code, verifier, "").StatusCode }()
	}
	wg.Wait()
	close(statuses)
	ok := 0
	for status := range statuses {
		if status == http.StatusNoContent {
			ok++
		} else if status != http.StatusUnauthorized {
			t.Fatalf("status %d", status)
		}
	}
	if ok != 1 {
		t.Fatalf("successful redemptions=%d", ok)
	}
}

func TestDesktopExchangeRejectsCrossSiteFormsWithoutConsumingProof(t *testing.T) {
	s := googleServer(t, true, "desktop-csrf-subject")
	verifier := base64.RawURLEncoding.EncodeToString(bytes.Repeat([]byte{4}, 32))
	for _, tc := range []struct{ name, contentType, origin, site, mode string }{
		{"simple form", "text/plain", "https://attacker.example", "cross-site", "navigate"},
		{"foreign JSON", "application/json", "https://attacker.example", "cross-site", "cors"},
		{"navigation without origin", "application/json", "", "none", "navigate"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			code := desktopCode(t, s, verifier)
			payload := `{"code":"=","code":"` + code + `","verifier":"` + verifier + `"}` + "\r\n"
			req, err := http.NewRequest("POST", s.URL+"/auth/desktop/exchange", strings.NewReader(payload))
			if err != nil {
				t.Fatal(err)
			}
			req.Header.Set("Content-Type", tc.contentType)
			if tc.origin != "" {
				req.Header.Set("Origin", tc.origin)
			}
			req.Header.Set("Sec-Fetch-Site", tc.site)
			req.Header.Set("Sec-Fetch-Mode", tc.mode)
			resp, err := noRedirect.Do(req)
			if err != nil {
				t.Fatal(err)
			}
			resp.Body.Close()
			if resp.StatusCode == http.StatusNoContent || len(resp.Cookies()) != 0 {
				t.Fatalf("accepted cross-site request: %d", resp.StatusCode)
			}
			if valid := redeemDesktop(t, s, code, verifier, ""); valid.StatusCode != http.StatusNoContent {
				t.Fatalf("proof consumed by rejected request: %d", valid.StatusCode)
			}
		})
	}
}

func TestDesktopExchangeIsBoundToConfiguredDeployment(t *testing.T) {
	s := googleServer(t, true, "desktop-deployment-subject")
	s.H.PublicURL = s.URL
	verifier := base64.RawURLEncoding.EncodeToString(bytes.Repeat([]byte{5}, 32))
	code := desktopCode(t, s, verifier)
	for _, other := range []string{"https://other.example", "http://127.0.0.1:9999", "https://127.0.0.1:9999"} {
		s.H.PublicURL = other
		if got := redeemDesktop(t, s, code, verifier, ""); got.StatusCode == http.StatusNoContent {
			t.Fatalf("redeemed in %s", other)
		}
	}
	s.H.PublicURL = s.URL
	if got := redeemDesktop(t, s, code, verifier, ""); got.StatusCode != http.StatusNoContent {
		t.Fatalf("proof consumed: %d", got.StatusCode)
	}
}
