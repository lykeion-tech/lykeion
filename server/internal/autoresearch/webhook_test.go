package autoresearch

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"net/http"
	"strings"
	"testing"
	"time"
)

func header(kv ...string) http.Header {
	h := http.Header{}
	for i := 0; i < len(kv); i += 2 {
		h.Set(kv[i], kv[i+1])
	}
	return h
}

func TestNormaliseFindsTheEventAndAction(t *testing.T) {
	now := time.Now()
	for _, c := range []struct {
		provider string
		h        http.Header
		body     string
		event    string
		action   string
	}{
		{"github", header("X-GitHub-Event", "pull_request"), `{"action":"opened","number":1}`, "pull_request", "opened"},
		{"generic", header("X-GitHub-Event", "push"), `{"event":"dataset.published"}`, "dataset.published", ""},
		{"generic", header("X-Event-Type", "build.done"), `{"event":"x"}`, "build.done", ""},
		{"generic", header(), `{"type":"ping"}`, "ping", ""},
		{"generic", header(), `[1, 2]`, "webhook.received", ""},
		{"generic", header(), "\xef\xbb\xbf{\"event\":\"bom\"}", "bom", ""},
	} {
		e, err := normalise(c.provider, c.h, []byte(c.body), now)
		if err != nil || e.Event != c.event || e.Action != c.action || e.Provider != c.provider || !e.ReceivedAt.Equal(now.UTC()) {
			t.Errorf("%s %q: %+v %v", c.provider, c.body, e, err)
		}
	}
	for _, bad := range []string{"", "42", `"text"`, "null", "{", `{"note":"a\u0000b"}`} {
		if _, err := normalise("generic", header(), []byte(bad), now); !errors.Is(err, ErrBadPayload) {
			t.Errorf("%q: %v", bad, err)
		}
	}
}

func TestFiltersMatchEventAndAction(t *testing.T) {
	fs := []Filter{{Event: "dataset.published"}, {Event: "pull_request", Actions: []string{"opened", "reopened"}}}
	for _, c := range []struct {
		event, action string
		want          bool
	}{
		{"dataset.published", "", true}, {"dataset.published", "anything", true}, {"pull_request", "opened", true},
		{"pull_request", "closed", false}, {"pull_request", "", false}, {"push", "", false},
	} {
		if got := matches(fs, Envelope{Event: c.event, Action: c.action}); got != c.want {
			t.Errorf("%s/%s: %v", c.event, c.action, got)
		}
	}
	if !matches(nil, Envelope{Event: "anything"}) {
		t.Error("no filters must match everything")
	}
}

func TestDeliveryKeys(t *testing.T) {
	if k, _ := deliveryKey("github", header("X-GitHub-Delivery", "d-1", "Idempotency-Key", "i-1")); k != "key:d-1" {
		t.Errorf("github: %q", k)
	}
	if k, _ := deliveryKey("generic", header("X-GitHub-Delivery", "d-1", "Idempotency-Key", "i-1")); k != "key:i-1" {
		t.Errorf("generic: %q", k)
	}
	a, _ := deliveryKey("generic", header())
	b, _ := deliveryKey("generic", header())
	if !strings.HasPrefix(a, "auto:") || a == b {
		t.Errorf("keyless deliveries: %q %q", a, b)
	}
	if _, err := deliveryKey("generic", header("Idempotency-Key", strings.Repeat("k", 201))); !errors.Is(err, ErrBadPayload) {
		t.Errorf("a long key: %v", err)
	}
}

func TestHubSignature(t *testing.T) {
	body := []byte(`{"zen":"Keep it logically awesome."}`)
	m := hmac.New(sha256.New, []byte("s3cret"))
	m.Write(body)
	good := "sha256=" + hex.EncodeToString(m.Sum(nil))
	if !validHubSignature([]byte("s3cret"), good, body) {
		t.Fatal("a good signature was refused")
	}
	for _, bad := range []string{"", good[:len(good)-1] + "0", "sha1=" + good[7:], strings.ToUpper(good[:7]) + good[7:], "sha256=zz"} {
		if validHubSignature([]byte("s3cret"), bad, body) {
			t.Fatalf("%q accepted", bad)
		}
	}
	if validHubSignature([]byte("other"), good, body) {
		t.Fatal("a signature under another secret was accepted")
	}
}

func TestNormaliseRejectsUnstorableUnicode(t *testing.T) {
	for _, body := range []string{`{"x":"\ud800"}`, `{"x":{"\udfff":1}}`, `[{"x":["\ud800x"]}]`, "{\"x\":\"\xff\"}", "{\"\xff\":[]}", `["\u0000"]`, `{"\u0000":1}`, `{"x":{"v":"\u0000"}}`, `{"x":"\\u0000"}`} {
		if _, err := normalise("generic", header(), []byte(body), time.Now()); !errors.Is(err, ErrBadPayload) {
			t.Errorf("%q: %v", body, err)
		}
	}
	for _, body := range []string{`{"x":"\ud83d\ude00"}`, `{"x":"\\ud800"}`, `{"x":"�"}`} {
		e, err := normalise("generic", header(), []byte(body), time.Now())
		if err != nil || string(e.Payload) != body {
			t.Errorf("valid %q: %s %v", body, e.Payload, err)
		}
	}
}
