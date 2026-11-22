package handler

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"net/http"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	"lykeion/server/internal/github"
	"lykeion/server/internal/store"
)

type githubWebhooks struct{ *server }

func (g githubWebhooks) post(t *testing.T, event, delivery string, payload any, valid bool, want int) {
	t.Helper()
	body, err := json.Marshal(payload)
	if err != nil {
		t.Fatal(err)
	}
	req, err := http.NewRequest("POST", g.URL+"/api/webhooks/github", bytes.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	mac := hmac.New(sha256.New, []byte("sign"))
	mac.Write(body)
	if valid {
		req.Header.Set("X-Hub-Signature-256", "sha256="+hex.EncodeToString(mac.Sum(nil)))
	}
	req.Header.Set("X-GitHub-Delivery", delivery)
	req.Header.Set("X-GitHub-Event", event)
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer res.Body.Close()
	if res.StatusCode != want {
		t.Fatalf("%s: status %d, want %d", event, res.StatusCode, want)
	}
}
func (g githubWebhooks) exec(t *testing.T, query string, args ...any) {
	t.Helper()
	if _, err := g.Env.Owner.Exec(t.Context(), query, args...); err != nil {
		t.Fatal(err)
	}
}
func (g githubWebhooks) count(t *testing.T, want int, query string, args ...any) {
	t.Helper()
	var n int
	if err := g.Env.Owner.QueryRow(t.Context(), query, args...).Scan(&n); err != nil {
		t.Fatal(err)
	}
	if n != want {
		t.Fatalf("%s: got %d, want %d", query, n, want)
	}
}
func (g githubWebhooks) connect(t *testing.T, ws string) {
	t.Helper()
	g.exec(t, `insert into github_connection (workspace_id,installation_id,account_login,account_type,connected_by) select workspace_id,42,'org','Organization',user_id from member where workspace_id=$1 and role='owner'`, ws)
}

type mirrorFixture struct {
	githubWebhooks
	token, ws   string
	pr, payload map[string]any
}

func newMirrorFixture(t *testing.T) mirrorFixture {
	t.Helper()
	g := githubWebhooks{newServer(t)}
	g.H.GitHub = &github.Service{Store: g.H.Store, Config: github.Config{WebhookSecret: "sign"}}
	tok := g.signIn(t, "owner@example.test")
	ws := g.workspace(t, tok, "lyk").ID
	pr := map[string]any{
		"number": 1, "title": "LYK-1", "state": "open", "html_url": "https://github.com/org/repo/pull/1",
		"head": map[string]string{"ref": "branch", "sha": "abc"}, "created_at": "2026-11-01T00:00:00Z", "updated_at": "2026-11-02T00:00:00Z",
	}
	payload := map[string]any{"action": "opened", "installation": map[string]int{"id": 42}, "repository": map[string]any{"name": "Repo", "owner": map[string]string{"login": "Org"}}, "pull_request": pr}
	return mirrorFixture{githubWebhooks: g, token: tok, ws: ws, pr: pr, payload: payload}
}
func (f mirrorFixture) send(t *testing.T) {
	t.Helper()
	f.post(t, "pull_request", uuid.NewString(), f.payload, true, 200)
}
func (f mirrorFixture) study(t *testing.T, question string) card {
	t.Helper()
	return f.idleCard(t, f.token, f.ws, question)
}
func (f mirrorFixture) links(t *testing.T, n int) {
	t.Helper()
	f.count(t, n, "select count(*) from github_pr_link where workspace_id=$1", f.ws)
}

func TestWebhookRejectsBadSignature(t *testing.T) {
	f := newMirrorFixture(t)
	f.connect(t, f.ws)
	f.post(t, "pull_request", "invalid", f.payload, false, 401)
	f.count(t, 0, "select count(*) from github_webhook_delivery")
	f.count(t, 0, "select count(*) from github_pull_request")
}
func TestUnconnectedInstallationIgnored(t *testing.T) {
	f := newMirrorFixture(t)
	f.send(t)
	f.count(t, 0, "select count(*) from github_webhook_delivery")
	f.count(t, 0, "select count(*) from github_pull_request")
}
func TestWebhookDuplicateDelivery(t *testing.T) {
	f := newMirrorFixture(t)
	f.connect(t, f.ws)
	c := f.study(t, "first")
	f.post(t, "pull_request", "once", f.payload, true, 200)
	f.pr["title"] = "unmatched"
	f.post(t, "pull_request", "once", f.payload, true, 200)
	f.links(t, 1)
	f.count(t, 1, "select count(*) from github_pull_request where title='LYK-1'")
	f.pr["title"] = "LYK-1"
	f.send(t)
	f.count(t, 1, "select count(*) from event where type='github.pr.changed' and card_id=$1 and actor_type='system'", c.ID)
}
func TestAutoLinkByBranchTitleBody(t *testing.T) {
	f := newMirrorFixture(t)
	f.connect(t, f.ws)
	c := f.study(t, "first")
	f.send(t)
	f.pr["number"] = 2
	f.pr["title"] = "other"
	f.pr["head"] = map[string]string{"ref": "fix/lyk-1"}
	f.send(t)
	f.pr["number"] = 3
	f.pr["head"] = map[string]string{"ref": "branch"}
	f.pr["body"] = "Fixes LYK-1"
	f.send(t)
	f.count(t, 3, "select count(*) from github_pr_link where card_id=$1 and matched_key='LYK-1' and source='auto'", c.ID)
}
func TestUnknownStudyNumberLinksNothing(t *testing.T) {
	f := newMirrorFixture(t)
	f.connect(t, f.ws)
	f.pr["title"] = "LYK-9999"
	f.send(t)
	f.links(t, 0)
	f.count(t, 1, "select count(*) from github_pull_request")
}
func TestAutoLinkDroppedOnTitleEdit(t *testing.T) {
	f := newMirrorFixture(t)
	f.connect(t, f.ws)
	auto := f.study(t, "auto")
	manual := f.study(t, "manual")
	f.send(t)
	f.exec(t, "insert into github_pr_link(workspace_id,card_id,pr_id,source) select workspace_id,$1,id,'manual' from github_pull_request", manual.ID)
	f.pr["title"] = "no key"
	f.send(t)
	f.links(t, 1)
	f.count(t, 1, "select count(*) from github_pr_link where source='manual' and card_id=$1", manual.ID)
	f.count(t, 2, "select count(*) from event where type='github.pr.changed' and card_id=$1", auto.ID)
	f.exec(t, "insert into github_pr_unlink(workspace_id,card_id,pr_id) select workspace_id,$1,id from github_pull_request", auto.ID)
	f.pr["title"] = "LYK-1"
	f.send(t)
	f.links(t, 1)
}
func TestPrefixChangeKeepsLinks(t *testing.T) {
	f := newMirrorFixture(t)
	f.connect(t, f.ws)
	f.study(t, "first")
	f.study(t, "second")
	f.send(t)
	f.exec(t, "update workspace set card_prefix='LAB' where id=$1", f.ws)
	f.pr["title"] = "LAB-2"
	f.send(t)
	f.links(t, 2)
	f.count(t, 2, "select count(*) from github_pr_link where matched_key in ('LYK-1','LAB-2')")
}
func TestAmbiguousPrefixLinksNowhere(t *testing.T) {
	f := newMirrorFixture(t)
	f.connect(t, f.ws)
	f.study(t, "first")
	f.send(t)
	f.links(t, 1)
	b := f.workspace(t, f.token, "other").ID
	f.idleCard(t, f.token, b, "other")
	f.exec(t, "update workspace set card_prefix='LYK' where id=$1", b)
	f.connect(t, b)
	f.send(t)
	f.count(t, 2, "select count(*) from github_pull_request")
	f.count(t, 0, "select count(*) from github_pr_link")
}
func TestGithubCrossWorkspaceIsolation(t *testing.T) {
	f := newMirrorFixture(t)
	f.connect(t, f.ws)
	b := f.workspace(t, f.token, "other").ID
	f.send(t)
	f.count(t, 0, "select count(*) from github_pull_request where workspace_id=$1", b)
}
func TestMergedPRLeavesStudyAlone(t *testing.T) {
	f := newMirrorFixture(t)
	f.connect(t, f.ws)
	c := f.study(t, "first")
	f.send(t)
	state := func() string {
		t.Helper()
		var out string
		if err := f.Env.Owner.QueryRow(t.Context(), "select row(board_column,state,ending,version)::text from card where id=$1", c.ID).Scan(&out); err != nil {
			t.Fatal(err)
		}
		return out
	}
	before := state()
	f.pr["state"] = "closed"
	f.pr["merged"] = true
	f.pr["updated_at"] = "2026-11-03T00:00:00Z"
	f.send(t)
	f.pr["merged"] = false
	f.pr["state"] = "open"
	f.pr["updated_at"] = "2026-11-02T00:00:00Z"
	f.pr["title"] = "no key"
	f.send(t)
	if after := state(); before != after {
		t.Fatalf("merge changed study: %s => %s", before, after)
	}
	f.links(t, 1)
	f.count(t, 1, "select count(*) from github_pull_request where state='merged'")
	f.count(t, 2, "select count(*) from event where type='github.pr.changed' and card_id=$1", c.ID)
}
func TestUninstallRemovesConnections(t *testing.T) {
	f := newMirrorFixture(t)
	f.connect(t, f.ws)
	b := f.workspace(t, f.token, "other").ID
	f.connect(t, b)
	f.send(t)
	for i, action := range []string{"suspend", "unsuspend", "suspend", "deleted"} {
		f.payload["installation"] = map[string]any{"id": 42, "updated_at": time.Unix(int64(i)+1000, 0).UTC()}
		f.payload["action"] = action
		f.post(t, "installation", uuid.NewString(), f.payload, true, 200)
		switch action {
		case "suspend":
			f.count(t, 2, "select count(*) from github_connection where status='suspended'")
			f.pr["number"] = 2
			f.send(t)
			f.count(t, 2, "select count(*) from github_pull_request")
		case "unsuspend":
			f.count(t, 2, "select count(*) from github_connection where status='active'")
		}
	}
	f.count(t, 0, "select count(*) from github_connection")
	f.count(t, 2, "select count(*) from github_pull_request")
}
func TestAutoLinkOff(t *testing.T) {
	f := newMirrorFixture(t)
	f.connect(t, f.ws)
	f.study(t, "first")
	f.send(t)
	f.exec(t, "update workspace set settings=jsonb_set(settings,'{github_auto_link}','false') where id=$1", f.ws)
	f.pr["title"] = "no key"
	f.send(t)
	f.pr["title"] = "LYK-1"
	f.pr["number"] = 2
	f.send(t)
	f.count(t, 2, "select count(*) from github_pull_request")
	f.links(t, 1)
}

func TestWebhookRetryAfterPartialFanout(t *testing.T) {
	g := githubWebhooks{newServer(t)}
	g.H.GitHub = &github.Service{Store: g.H.Store, Config: github.Config{WebhookSecret: "sign"}}
	token := g.signIn(t, "owner@example.test")
	for _, slug := range []string{"alpha", "bravo"} {
		ws := g.workspace(t, token, slug).ID
		g.idleCard(t, token, ws, "study")
		g.connect(t, ws)
	}
	// Fail the second workspace's transaction once, regardless of resolver ordering.
	g.exec(t, `create function fail_second_pr() returns trigger language plpgsql security definer as $$
 begin if exists(select from github_pull_request where workspace_id<>new.workspace_id)
 then raise exception 'test failure'; end if; return new; end $$;
 create trigger fail_second before insert on github_pull_request for each row execute function fail_second_pr()`)
	payload := map[string]any{
		"installation": map[string]int{"id": 42},
		"repository":   map[string]any{"name": "Repo", "owner": map[string]string{"login": "Org"}},
		"pull_request": map[string]any{"number": 4, "title": "ALPH-1 BRAV-1", "state": "open", "created_at": "2026-11-01T00:00:00Z", "updated_at": "2026-11-02T00:00:00Z"},
	}
	g.post(t, "pull_request", "retry", payload, true, 500)
	g.count(t, 0, "select count(*) from github_webhook_delivery")
	g.count(t, 1, "select count(*) from github_pr_link")
	g.count(t, 1, "select count(*) from event where type='github.pr.changed'")
	g.exec(t, `drop trigger fail_second on github_pull_request`)
	g.post(t, "pull_request", "retry", payload, true, 200)
	g.count(t, 1, "select count(*) from github_webhook_delivery where id='retry'")
	g.count(t, 2, "select count(*) from github_pr_link")
	g.count(t, 2, "select count(*) from event where type='github.pr.changed'")
}

func TestWebhookSimultaneousDuplicate(t *testing.T) {
	f := newMirrorFixture(t)
	f.connect(t, f.ws)
	// Closed PRs have no asynchronous snapshot write, so updated_at isolates mirror writes.
	f.pr["state"] = "closed"
	body, err := json.Marshal(f.payload)
	if err != nil {
		t.Fatal(err)
	}
	start := make(chan struct{})
	results := make(chan error, 4)
	for range cap(results) {
		go func() {
			<-start
			results <- f.H.GitHub.Handle(t.Context(), "pull_request", "simultaneous", body)
		}()
	}
	close(start)
	for range cap(results) {
		if err := <-results; err != nil {
			t.Fatal(err)
		}
	}
	// A second upsert would replace updated_at with its own transaction time.
	f.count(t, 1, "select count(*) from github_pull_request where created_at=updated_at")
	f.count(t, 1, "select count(*) from github_webhook_delivery")
}

func TestWebhookStaleUnsuspend(t *testing.T) {
	f := newMirrorFixture(t)
	f.connect(t, f.ws)
	status := func(action, version string) {
		t.Helper()
		f.payload["action"] = action
		f.payload["installation"] = map[string]any{"id": 42, "updated_at": version}
		f.post(t, "installation", uuid.NewString(), f.payload, true, 200)
	}
	status("suspend", "2026-11-01T12:00:00-04:00")
	f.payload["installation"] = map[string]any{"id": 42, "updated_at": "2026-11-01T12:00:00.000-04:00", "suspended_at": "2026-11-01T16:10:00Z"}
	f.post(t, "installation", uuid.NewString(), f.payload, true, 200)
	status("unsuspend", "2026-11-01T16:05:00Z")
	f.count(t, 1, "select count(*) from github_connection where status='suspended'")
	status("unsuspend", "2026-11-01T16:10:00Z")
	f.count(t, 1, "select count(*) from github_connection where status='suspended'")
	f.count(t, 1, "select count(*) from event where type='github.changed'")
	f.payload["installation"] = map[string]any{"id": 42}
	f.post(t, "installation", uuid.NewString(), f.payload, true, 400)
	f.count(t, 1, "select count(*) from github_connection where status='suspended'")
	status("unsuspend", "2026-11-01T16:11:00Z")
	f.count(t, 1, "select count(*) from github_connection where status='active'")
}
func TestWebhookMergedTerminalSameSecond(t *testing.T) {
	f := newMirrorFixture(t)
	f.connect(t, f.ws)
	f.study(t, "first")
	f.send(t)
	f.pr["title"] = "edited LYK-1"
	f.send(t)
	f.count(t, 1, "select count(*) from github_pull_request where title='edited LYK-1'")
	f.pr["merged"] = true
	f.pr["state"] = "closed"
	f.send(t)
	f.pr["merged"] = false
	f.pr["state"] = "open"
	f.pr["title"] = "no key"
	f.send(t)
	f.links(t, 1)
	f.count(t, 1, "select count(*) from github_pull_request where state='merged'")
}

func TestWebhookConcurrentMirrorResolution(t *testing.T) {
	f := newMirrorFixture(t)
	f.connect(t, f.ws)
	f.study(t, "first")
	other := f.workspace(t, f.token, "other").ID
	f.idleCard(t, f.token, other, "other")
	f.exec(t, "update workspace set card_prefix='LYK' where id=$1", other)
	// Pause the first mirror after it resolves a unique study and before its write.
	ctx, cancel := context.WithTimeout(t.Context(), 10*time.Second)
	defer cancel()
	tx, err := f.Env.Owner.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback(t.Context())
	if _, err = tx.Exec(ctx, "select id from workspace where id=$1 for update", f.ws); err != nil {
		t.Fatal(err)
	}
	start := func(s *github.Service) <-chan error {
		t.Helper()
		body, err := json.Marshal(f.payload)
		if err != nil {
			t.Fatal(err)
		}
		done := make(chan error, 1)
		go func() { done <- s.Handle(ctx, "pull_request", uuid.NewString(), body) }()
		return done
	}
	waitForLock := func(pattern string) {
		t.Helper()
		for ctx.Err() == nil {
			var n int
			if err := f.Env.Owner.QueryRow(ctx, "select count(*) from pg_stat_activity where datname=current_database() and wait_event_type='Lock' and query like $1", pattern).Scan(&n); err != nil {
				t.Fatal(err)
			}
			if n > 0 {
				return
			}
			time.Sleep(10 * time.Millisecond)
		}
		t.Fatalf("query never waited: %s", pattern)
	}
	first := start(f.H.GitHub)
	waitForLock("%LockWorkspaceForMutation%")
	f.connect(t, other)
	second := start(&github.Service{Store: f.H.Store, Config: f.H.GitHub.Config})
	waitForLock("%pg_advisory_lock%")
	if err = tx.Commit(ctx); err != nil {
		t.Fatal(err)
	}
	for _, done := range []<-chan error{first, second} {
		if err = <-done; err != nil {
			t.Fatal(err)
		}
	}
	f.count(t, 2, "select count(*) from github_pull_request")
	f.count(t, 0, "select count(*) from github_pr_link")
}

func TestWebhookMirrorWithinOnePoolSlot(t *testing.T) {
	f := newMirrorFixture(t)
	f.connect(t, f.ws)
	f.study(t, "first")
	other := f.workspace(t, f.token, "other").ID
	f.connect(t, other)
	// The workspace write must reuse the same backend which holds the PR advisory lock.
	f.exec(t, `create function assert_pr_lock() returns trigger language plpgsql as $$
 begin if not exists(select from pg_locks where pid=pg_backend_pid() and locktype='advisory' and granted)
 then raise exception 'PR write used an extra database session'; end if; return new; end $$;
 create trigger assert_pr_lock before insert on github_pull_request for each row execute function assert_pr_lock()`)
	cfg := f.Env.App.Config()
	cfg.MinConns = 0
	cfg.MaxConns = 1
	pool, err := pgxpool.NewWithConfig(t.Context(), cfg)
	if err != nil {
		t.Fatal(err)
	}
	defer pool.Close()
	svc := &github.Service{Store: &store.Store{Pool: pool}, Config: f.H.GitHub.Config}
	ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
	defer cancel()
	body, err := json.Marshal(f.payload)
	if err != nil {
		t.Fatal(err)
	}
	if err = svc.Handle(ctx, "pull_request", "pooled", body); err != nil {
		t.Fatal(err)
	}
	f.count(t, 2, "select count(*) from github_pull_request")
	f.links(t, 1)
	var locks int
	if err = pool.QueryRow(ctx, "select count(*) from pg_locks where pid=pg_backend_pid() and locktype='advisory'").Scan(&locks); err != nil || locks != 0 {
		t.Fatalf("locks after release: %d (%v)", locks, err)
	}
}
