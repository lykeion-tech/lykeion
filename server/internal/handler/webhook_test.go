package handler

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"net/http"
	"strings"
	"sync"
	"testing"

	"github.com/google/uuid"

	"lykeion/server/internal/auth"
	"lykeion/server/internal/autoresearch"
	"lykeion/server/internal/seal"
)

// hook creates a create_card autoresearch with one webhook trigger and returns
// the autoresearch and the URL token.
func (s *server) hook(t *testing.T, tok, wsID string, trigger map[string]any) (apView, string) {
	t.Helper()
	ap := s.autoresearch(t, tok, wsID, map[string]any{"title": "Dataset watch", "description": "Is the new dataset usable?", "execution_mode": "create_card"})
	trigger["kind"] = "webhook"
	var created struct{ Token string }
	if code := s.call(t, "POST", "/api/autoresearch/"+ap.ID+"/triggers", tok, wsID, trigger, &created); code != 201 {
		t.Fatalf("create the webhook: %d", code)
	}
	return ap, created.Token
}

func (s *server) post(t *testing.T, token string, body []byte, hdr ...string) (int, map[string]any) {
	t.Helper()
	req, err := http.NewRequest("POST", s.URL+"/api/webhooks/autoresearch/"+token, bytes.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Content-Type", "application/json")
	for i := 0; i < len(hdr); i += 2 {
		req.Header.Set(hdr[i], hdr[i+1])
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	out := map[string]any{}
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		t.Fatal(err)
	}
	return resp.StatusCode, out
}

func sign(secret string, body []byte) string {
	m := hmac.New(sha256.New, []byte(secret))
	m.Write(body)
	return "sha256=" + hex.EncodeToString(m.Sum(nil))
}

func (s *server) count(t *testing.T, q string, args ...any) int {
	t.Helper()
	var n int
	if err := s.Env.Owner.QueryRow(context.Background(), q, args...).Scan(&n); err != nil {
		t.Fatal(err)
	}
	return n
}

func TestOnlyTheHashOfAWebhookTokenIsStored(t *testing.T) {
	s := newServer(t)
	a := s.signIn(t, "a@lab.org")
	w := s.workspace(t, a, "lab")
	_, token := s.hook(t, a, w.ID, map[string]any{})
	if s.count(t, `SELECT count(*) FROM autoresearch_trigger WHERE token_hash = $1`, auth.Hash(token)) != 1 {
		t.Fatal("the token's hash is not what is stored")
	}
	if s.count(t, `SELECT count(*) FROM autoresearch_trigger t WHERE strpos(row_to_json(t)::text, $1) > 0`, token) != 0 ||
		s.count(t, `SELECT count(*) FROM event WHERE strpos(payload::text, $1) > 0`, token) != 0 {
		t.Fatal("the plain token was stored")
	}
}

func TestAWebhookIsDedupedAndDispatchedOnce(t *testing.T) {
	s := newServer(t)
	a := s.signIn(t, "a@lab.org")
	w := s.workspace(t, a, "lab")
	ap, token := s.hook(t, a, w.ID, map[string]any{})
	body := []byte(`{"event":"dataset.published","accession":"GSE9"}`)
	code, out := s.post(t, token, body, "Idempotency-Key", "k-1")
	if code != 200 || out["status"] != "accepted" || out["delivery_id"] == nil {
		t.Fatalf("first: %d %v", code, out)
	}
	if code, out = s.post(t, token, body, "Idempotency-Key", "k-1"); code != 200 || out["status"] != "skipped" || out["reason"] != "duplicate" {
		t.Fatalf("repeat: %d %v", code, out)
	}
	var wg sync.WaitGroup
	for i := 0; i < 3; i++ { // workers on three servers
		wg.Add(1)
		worker := *s.H.Autoresearch
		go func(svc autoresearch.Service) {
			defer wg.Done()
			if err := svc.Deliver(context.Background()); err != nil {
				t.Error(err)
			}
		}(worker)
	}
	wg.Wait()
	var runs []runView
	if code := s.call(t, "GET", "/api/autoresearch/"+ap.ID+"/runs", a, w.ID, nil, &runs); code != 200 {
		t.Fatal(code)
	}
	if len(runs) != 1 || runs[0].Source != "webhook" || runs[0].Status != "running" || runs[0].ActingUserID != s.userID(t, a) || runs[0].CardID == nil {
		t.Fatalf("runs: %+v", runs)
	}
	if s.count(t, `SELECT count(*) FROM webhook_delivery WHERE state = 'dispatched' AND run_id IS NOT NULL`) != 1 ||
		s.count(t, `SELECT count(*) FROM record WHERE kind = 'trigger_payload' AND body->'payload'->>'accession' = 'GSE9'`) != 1 {
		t.Fatal("the delivery was not dispatched once with its payload")
	}
	if code, out := s.post(t, token, []byte(`{"event":"a"}`)); code != 200 || out["status"] != "accepted" {
		t.Fatalf("keyless: %d %v", code, out)
	}
	if code, out := s.post(t, token, []byte(`{"event":"a"}`)); code != 200 || out["status"] != "accepted" {
		t.Fatalf("keyless: %d %v", code, out)
	}
	if s.count(t, `SELECT count(*) FROM webhook_delivery`) != 3 {
		t.Fatal("deliveries without a key were deduplicated")
	}
}

func TestGithubSignaturesAreChecked(t *testing.T) {
	s := newServer(t)
	a := s.signIn(t, "a@lab.org")
	w := s.workspace(t, a, "lab")
	_, token := s.hook(t, a, w.ID, map[string]any{"provider": "github", "signing_secret": "s3cret"})
	body := []byte(`{"action":"opened","number":7}`)
	for _, sig := range []string{"", sign("wrong", body), "sha1=" + sign("s3cret", body)[7:]} {
		if code, _ := s.post(t, token, body, "X-GitHub-Event", "pull_request", "X-Hub-Signature-256", sig); code != 401 {
			t.Fatalf("signature %q: %d", sig, code)
		}
	}
	code, out := s.post(t, token, body, "X-GitHub-Event", "pull_request", "X-GitHub-Delivery", "g-1", "X-Hub-Signature-256", sign("s3cret", body))
	if code != 200 || out["status"] != "accepted" {
		t.Fatalf("signed: %d %v", code, out)
	}
	if s.count(t, `SELECT count(*) FROM webhook_delivery WHERE envelope->>'event' = 'pull_request' AND envelope->>'action' = 'opened'
		AND delivery_key = 'key:g-1'`) != 1 || s.count(t, `SELECT count(*) FROM webhook_delivery`) != 1 {
		t.Fatal("a refused request was stored, or the accepted one was not")
	}
	_, open := s.hook(t, a, w.ID, map[string]any{"provider": "github"})
	if code, out := s.post(t, open, body, "X-GitHub-Event", "pull_request"); code != 200 || out["status"] != "accepted" {
		t.Fatalf("an unsigned github trigger: %d %v", code, out)
	}
}

func TestASigningSecretIsSealedAndFailsClosedWithoutTheKey(t *testing.T) {
	s := newServer(t)
	a := s.signIn(t, "a@lab.org")
	w := s.workspace(t, a, "lab")
	ap, token := s.hook(t, a, w.ID, map[string]any{"provider": "github", "signing_secret": "s3cret"})
	var id uuid.UUID
	var sealed []byte
	if err := s.Env.Owner.QueryRow(context.Background(), `SELECT id, signing_secret_enc FROM autoresearch_trigger`).Scan(&id, &sealed); err != nil {
		t.Fatal(err)
	}
	if bytes.Contains(sealed, []byte("s3cret")) {
		t.Fatal("the secret is stored in the clear")
	}
	if plain, err := seal.Open(s.H.Autoresearch.Key, sealed, id[:]); err != nil || string(plain) != "s3cret" {
		t.Fatalf("open: %q %v", plain, err)
	}

	key := s.H.Autoresearch.Key
	s.H.Autoresearch.Key = nil
	defer func() { s.H.Autoresearch.Key = key }()
	body := []byte(`{"action":"opened"}`)
	if code, _ := s.post(t, token, body, "X-GitHub-Event", "pull_request", "X-Hub-Signature-256", sign("s3cret", body)); code != 503 {
		t.Fatalf("no key: %d", code)
	}
	if s.count(t, `SELECT count(*) FROM webhook_delivery`) != 0 {
		t.Fatal("a delivery was stored without checking its signature")
	}
	if code := s.call(t, "POST", "/api/autoresearch/"+ap.ID+"/triggers", a, w.ID,
		map[string]any{"kind": "webhook", "provider": "github", "signing_secret": "x"}, nil); code != 400 {
		t.Fatalf("store a secret without a key: %d", code)
	}
}

func TestFilteredSkippedAndIgnoredDeliveriesStoreNothing(t *testing.T) {
	s := newServer(t)
	a := s.signIn(t, "a@lab.org")
	w := s.workspace(t, a, "lab")
	ap, token := s.hook(t, a, w.ID, map[string]any{"filters": []map[string]any{
		{"event": "dataset.published"}, {"event": "pull_request", "actions": []string{"opened"}}}})
	for _, c := range []struct {
		body, eventHeader, status, reason string
	}{
		{`{"event":"dataset.retracted"}`, "", "skipped", "filtered"},
		{`{"event":"dataset.published"}`, "", "accepted", ""},
		{`{"action":"closed"}`, "pull_request", "skipped", "filtered"},
		{`{"action":"opened"}`, "pull_request", "accepted", ""},
	} {
		hdr := []string{}
		if c.eventHeader != "" {
			hdr = []string{"X-Event-Type", c.eventHeader}
		}
		if code, out := s.post(t, token, []byte(c.body), hdr...); code != 200 || out["status"] != c.status || (c.reason != "" && out["reason"] != c.reason) {
			t.Fatalf("%s: %d %v", c.body, code, out)
		}
	}
	var view apView
	if code := s.call(t, "GET", "/api/autoresearch/"+ap.ID, a, w.ID, nil, &view); code != 200 {
		t.Fatal(code)
	}
	if code := s.call(t, "POST", "/api/autoresearch/"+ap.ID+"/pause", a, w.ID, nil, nil); code != 200 {
		t.Fatal(code)
	}
	if _, out := s.post(t, token, []byte(`{"event":"dataset.published"}`)); out["status"] != "ignored" || out["reason"] != "paused" {
		t.Fatalf("paused: %v", out)
	}
	if code := s.call(t, "POST", "/api/autoresearch/"+ap.ID+"/resume", a, w.ID, nil, nil); code != 200 {
		t.Fatal(code)
	}
	if code := s.call(t, "PATCH", "/api/autoresearch/"+ap.ID+"/triggers/"+view.Triggers[0].ID, a, w.ID, map[string]any{"enabled": false}, nil); code != 200 {
		t.Fatal(code)
	}
	if _, out := s.post(t, token, []byte(`{"event":"dataset.published"}`)); out["status"] != "ignored" || out["reason"] != "trigger_disabled" {
		t.Fatalf("disabled: %v", out)
	}
	if s.count(t, `SELECT count(*) FROM webhook_delivery`) != 2 {
		t.Fatal("only the two accepted deliveries may be stored")
	}
}

func TestWebhookBodiesAreBoundedAndChecked(t *testing.T) {
	s := newServer(t)
	a := s.signIn(t, "a@lab.org")
	w := s.workspace(t, a, "lab")
	_, token := s.hook(t, a, w.ID, map[string]any{})
	if code, _ := s.post(t, "lwh_unknown", []byte(`{}`)); code != 404 {
		t.Fatalf("unknown token: %d", code)
	}
	for _, bad := range []string{"", "42", "{", `{"note":"a\u0000b"}`} {
		if code, _ := s.post(t, token, []byte(bad)); code != 400 {
			t.Fatalf("%q: %d", bad, code)
		}
	}
	if code, _ := s.post(t, token, []byte(`{}`), "Idempotency-Key", strings.Repeat("k", 201)); code != 400 {
		t.Fatalf("a long key: %d", code)
	}
	fill := func(n int) []byte { return []byte(`{"p":"` + strings.Repeat("a", n) + `"}`) }
	if code, out := s.post(t, token, fill(1<<20-8)); code != 200 || out["status"] != "accepted" {
		t.Fatalf("exactly 1 MiB: %d %v", code, out)
	}
	if code, _ := s.post(t, token, fill(1<<20-7)); code != 413 {
		t.Fatalf("over 1 MiB: %d", code)
	}
}

func TestWebhooksAreRateLimitedPerTokenAndPerIP(t *testing.T) {
	s := newServer(t)
	a := s.signIn(t, "a@lab.org")
	w := s.workspace(t, a, "lab")
	_, token := s.hook(t, a, w.ID, map[string]any{})
	for i := 1; i <= 61; i++ {
		code, _ := s.post(t, token, []byte(`{"event":"x"}`), "Idempotency-Key", "k"+itoa(int64(i)))
		if (i <= 60 && code != 200) || (i == 61 && code != 429) {
			t.Fatalf("request %d to one token: %d", i, code)
		}
	}
	for i := 62; i <= 121; i++ { // 61 requests from this address so far
		code, _ := s.post(t, "lwh_"+uuid.NewString(), []byte(`{}`))
		if (i <= 120 && code != 404) || (i == 121 && code != 429) {
			t.Fatalf("request %d from one address: %d", i, code)
		}
	}
}

func TestTheDeliveryWorkerGivesUpAfterFiveAttempts(t *testing.T) {
	s := newServer(t)
	a := s.signIn(t, "a@lab.org")
	w := s.workspace(t, a, "lab")
	s.hook(t, a, w.ID, map[string]any{})
	ctx := context.Background()
	if _, err := s.Env.Owner.Exec(ctx, `INSERT INTO webhook_delivery (workspace_id, trigger_id, delivery_key, envelope, state, attempts, lease_until)
		SELECT workspace_id, id, 'key:stuck', '{}', 'dispatching', 5, now() - interval '1 second' FROM autoresearch_trigger`); err != nil {
		t.Fatal(err)
	}
	if err := s.H.Autoresearch.Deliver(ctx); err != nil {
		t.Fatal(err)
	}
	if s.count(t, `SELECT count(*) FROM webhook_delivery WHERE state = 'failed' AND run_id IS NULL`) != 1 ||
		s.count(t, `SELECT count(*) FROM event WHERE type = 'webhook_delivery.failed'`) != 1 || s.count(t, `SELECT count(*) FROM autoresearch_run`) != 0 {
		t.Fatal("a delivery past its attempts was not failed")
	}
}

func TestWebhookLimiterFailsClosed(t *testing.T) {
	s := newServer(t)
	s.H.Limit = auth.Limiter{}
	if code, _ := s.post(t, "lwh_unknown", []byte(`{}`)); code != 503 {
		t.Fatalf("unavailable limiter: %d", code)
	}
	if s.count(t, `SELECT count(*) FROM webhook_delivery`) != 0 {
		t.Fatal("stored delivery")
	}
}

func TestWebhookSignatureUsesRawBodyAndAuthenticatesSecret(t *testing.T) {
	s := newServer(t)
	a := s.signIn(t, "raw@lab.org")
	w := s.workspace(t, a, "lab")
	_, token := s.hook(t, a, w.ID, map[string]any{"provider": "github", "signing_secret": "secret"})
	body := []byte("\xef\xbb\xbf{\"event\":\"bom\"}")
	if code, _ := s.post(t, token, body, "X-Hub-Signature-256", sign("secret", body[3:])); code != 401 {
		t.Fatal(code)
	}
	if code, out := s.post(t, token, body, "X-Hub-Signature-256", sign("secret", body)); code != 200 || out["status"] != "accepted" {
		t.Fatalf("raw body: %d %v", code, out)
	}
	if _, err := s.Env.Owner.Exec(context.Background(), `UPDATE autoresearch_trigger SET signing_secret_enc = decode('00','hex')`); err != nil {
		t.Fatal(err)
	}
	if code, _ := s.post(t, token, body, "X-Hub-Signature-256", sign("secret", body)); code != 503 {
		t.Fatal(code)
	}
	if s.count(t, `SELECT count(*) FROM webhook_delivery`) != 1 {
		t.Fatal("corrupt secret accepted")
	}
}

func TestWebhookDeliveryRetriesAfterAdmissionError(t *testing.T) {
	s := newServer(t)
	a := s.signIn(t, "retry@lab.org")
	w := s.workspace(t, a, "lab")
	_, token := s.hook(t, a, w.ID, map[string]any{})
	if code, out := s.post(t, token, []byte(`{}`)); code != 200 || out["status"] != "accepted" {
		t.Fatalf("receive: %d %v", code, out)
	}
	ctx := context.Background()
	if _, err := s.Env.Owner.Exec(ctx, `ALTER TABLE autoresearch_run ADD CONSTRAINT refuse_test_run CHECK (source <> 'webhook')`); err != nil {
		t.Fatal(err)
	}
	if err := s.H.Autoresearch.Deliver(ctx); err != nil {
		t.Fatal(err)
	}
	if s.count(t, `SELECT count(*) FROM webhook_delivery WHERE state='pending' AND attempts=1 AND lease_until > now() + interval '25 seconds'`) != 1 {
		t.Fatal("failed admission was not delayed")
	}
	if err := s.H.Autoresearch.Deliver(ctx); err != nil {
		t.Fatal(err)
	}
	if s.count(t, `SELECT count(*) FROM webhook_delivery WHERE attempts=1`) != 1 {
		t.Fatal("retried before delay elapsed")
	}
	if _, err := s.Env.Owner.Exec(ctx, `ALTER TABLE autoresearch_run DROP CONSTRAINT refuse_test_run`); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Env.Owner.Exec(ctx, `UPDATE webhook_delivery SET lease_until=now()-interval '1 second'`); err != nil {
		t.Fatal(err)
	}
	if err := s.H.Autoresearch.Deliver(ctx); err != nil {
		t.Fatal(err)
	}
	if s.count(t, `SELECT count(*) FROM webhook_delivery WHERE state='dispatched' AND attempts=2 AND run_id IS NOT NULL`) != 1 {
		t.Fatal("retry did not dispatch")
	}
}
