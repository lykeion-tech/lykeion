package github

import (
	"context"
	"fmt"
	"net/http"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func importFixture(t *testing.T, handler http.HandlerFunc) snapshotFixture {
	t.Helper()
	f := newSnapshotFixture(t, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/app/installations/42/access_tokens" {
			fmt.Fprint(w, `{"token":"test"}`)
			return
		}
		handler(w, r)
	})
	if _, err := f.env.Owner.Exec(t.Context(), `delete from github_pull_request; update workspace set card_prefix='LYK'; insert into card(workspace_id,number,title,question,owner_id,board_column) select workspace_id,1,'Study','Question',connected_by,'grounding' from github_connection`); err != nil {
		t.Fatal(err)
	}
	return f
}
func importState(t *testing.T, f snapshotFixture, want string) {
	t.Helper()
	var got string
	if err := f.env.Owner.QueryRow(t.Context(), `select import_state from github_connection where workspace_id=$1`, f.ws).Scan(&got); err != nil || got != want {
		t.Fatalf("import state %q want %q: %v", got, want, err)
	}
}
func TestImportOpenPRs(t *testing.T) {
	var details atomic.Int32
	f := importFixture(t, func(w http.ResponseWriter, r *http.Request) {
		p := r.URL.Path
		switch {
		case p == "/installation/repositories":
			if r.URL.Query().Get("page") == "" {
				w.Header().Set("Link", `<?page=2>; rel="next"`)
				fmt.Fprint(w, `{"repositories":[{"full_name":"org/alpha"}]}`)
			} else {
				fmt.Fprint(w, `{"repositories":[{"name":"beta","owner":{"login":"org"}}]}`)
			}
		case strings.HasSuffix(p, "/pulls"):
			if r.URL.Query().Get("state") != "open" {
				t.Errorf("missing state=open: %s", r.URL)
			}
			if strings.Contains(p, "alpha") && r.URL.Query().Get("page") == "" {
				w.Header().Set("Link", `</repositories/1/pulls?page=2&state=all>; rel="next"`)
				fmt.Fprint(w, `[{"number":1}]`)
			} else {
				fmt.Fprint(w, `[{"number":2}]`)
			}
		case strings.Contains(p, "/pulls/"):
			details.Add(1)
			n := p[len(p)-1:]
			title := "ordinary"
			if n == "1" {
				title = "LYK-1"
			}
			fmt.Fprintf(w, `{"number":%s,"title":"%s","state":"open","mergeable_state":"clean","head":{"sha":"abc"},"additions":9,"changed_files":3,"created_at":"2025-01-01T00:00:00Z","updated_at":"2025-01-01T00:00:00Z"}`, n, title)
		case strings.HasSuffix(p, "/check-runs"):
			fmt.Fprint(w, `{"check_runs":[]}`)
		case strings.HasSuffix(p, "/status"):
			fmt.Fprint(w, `{"statuses":[]}`)
		default:
			t.Errorf("unexpected %s", p)
			http.NotFound(w, r)
		}
	})
	// This installation is shared. The other workspace has its own retained mirror.
	if _, err := f.env.Owner.Exec(t.Context(), `insert into workspace(id,slug,name) values(gen_random_uuid(),'other','Other');
 insert into github_connection(workspace_id,installation_id,account_login,account_type,connected_by) select w.id,42,'org','Organization',u.id from workspace w,"user" u where w.slug='other';
 insert into github_pull_request(workspace_id,installation_id,repo_owner,repo_name,number,title,state,html_url,head_sha,pr_created_at,pr_updated_at) select id,42,'org','alpha',1,'Retained','open','','abc',now(),now() from workspace where slug='other'`); err != nil {
		t.Fatal(err)
	}
	f.s.Import(t.Context(), f.ws, 42)
	importState(t, f, "done")
	var n, links, events int
	if err := f.env.Owner.QueryRow(t.Context(), `select (select count(*) from github_pull_request where additions=9 and changed_files=3 and mergeability='mergeable'),(select count(*) from github_pr_link where source='auto'),(select count(*) from event where type='github.changed')`).Scan(&n, &links, &events); err != nil || n != 3 || links != 1 || events != 2 || details.Load() != 3 {
		t.Fatalf("mirrors=%d links=%d transitions=%d detail calls=%d: %v", n, links, events, details.Load(), err)
	}
	var untouched int
	if err := f.env.Owner.QueryRow(t.Context(), `select count(*) from github_pull_request where workspace_id!=$1 and title='Retained' and snapshot_at is null`, f.ws).Scan(&untouched); err != nil || untouched != 1 {
		t.Fatalf("other workspace mirror changed: %d, %v", untouched, err)
	}

}
func TestImportSkipsClosed(t *testing.T) {
	f := importFixture(t, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/installation/repositories" {
			fmt.Fprint(w, `{"repositories":[{"full_name":"org/repo"}]}`)
			return
		}
		if r.URL.Path == "/repos/org/repo/pulls" && r.URL.Query().Get("state") == "open" {
			fmt.Fprint(w, "[]")
			return
		}
		t.Errorf("unexpected %s", r.URL)
		http.NotFound(w, r)
	})
	f.s.Import(t.Context(), f.ws, 42)
	importState(t, f, "done")
}
func TestImportRateLimitBackoff(t *testing.T) {
	var calls atomic.Int32
	f := importFixture(t, func(w http.ResponseWriter, r *http.Request) {
		if calls.Add(1) == 1 {
			w.Header().Set("Retry-After", "1")
			w.WriteHeader(403)
			return
		}
		fmt.Fprint(w, `{"repositories":[]}`)
	})
	var sleep time.Duration
	f.s.sleep = func(ctx context.Context, d time.Duration) error { sleep += d; return nil }
	f.s.Import(t.Context(), f.ws, 42)
	importState(t, f, "done")
	if sleep != time.Second || calls.Load() != 2 {
		t.Fatalf("sleep %s calls %d", sleep, calls.Load())
	}
}
func TestImportStopsAfterDisconnect(t *testing.T) {
	for _, reconnect := range []bool{false, true} {
		t.Run(fmt.Sprint(reconnect), func(t *testing.T) {
			var f snapshotFixture
			var late atomic.Bool
			f = importFixture(t, func(w http.ResponseWriter, r *http.Request) {
				if r.URL.Path == "/installation/repositories" {
					fmt.Fprint(w, `{"repositories":[{"full_name":"org/repo"}]}`)
					return
				}
				if r.URL.Path == "/repos/org/repo/pulls" {
					w.Header().Set("Link", `<?page=2>; rel="next"`)
					fmt.Fprint(w, `[{"number":1}]`)
					if r.URL.Query().Get("page") != "" {
						late.Store(true)
					}
					return
				}
				if r.URL.Path != "/repos/org/repo/pulls/1" {
					late.Store(true)
					http.NotFound(w, r)
					return
				}
				if _, err := f.env.Owner.Exec(t.Context(), `delete from github_connection`); err != nil {
					t.Error(err)
				}
				if reconnect {
					if _, err := f.env.Owner.Exec(t.Context(), `insert into github_connection(workspace_id,installation_id,account_login,account_type,connected_by) select $1,42,'org','Organization',id from "user"`, f.ws); err != nil {
						t.Error(err)
					}
				}
				fmt.Fprint(w, `{"number":1,"state":"open","title":"LYK-1","head":{"sha":"abc"},"created_at":"2025-01-01T00:00:00Z","updated_at":"2025-01-01T00:00:00Z"}`)
			})
			f.s.Import(t.Context(), f.ws, 42)
			if reconnect {
				importState(t, f, "pending")
			}
			var n int
			if err := f.env.Owner.QueryRow(t.Context(), `select count(*) from github_pull_request`).Scan(&n); err != nil || n != 0 || late.Load() {
				t.Fatalf("late=%t rows=%d err=%v", late.Load(), n, err)
			}
		})
	}
}
func TestResumeImports(t *testing.T) {
	f := importFixture(t, func(w http.ResponseWriter, r *http.Request) { fmt.Fprint(w, `{"repositories":[]}`) })
	f.s.ResumeImports(t.Context())
	importState(t, f, "done")
}

func TestImportRetryBudgetAndCancellation(t *testing.T) {
	for _, shutdown := range []bool{false, true} {
		t.Run(fmt.Sprint(shutdown), func(t *testing.T) {
			now := time.Unix(1800000000, 0)
			var calls atomic.Int32
			f := importFixture(t, func(w http.ResponseWriter, r *http.Request) {
				calls.Add(1)
				w.Header().Set("X-RateLimit-Reset", fmt.Sprint(now.Add(10*time.Minute).Unix()))
				w.WriteHeader(429)
			})
			ctx, cancel := context.WithCancel(t.Context())
			defer cancel()
			f.s.now = func() time.Time { return now }
			var slept time.Duration
			f.s.sleep = func(ctx context.Context, d time.Duration) error {
				slept += d
				if shutdown {
					cancel()
					return ctx.Err()
				}
				return nil
			}
			f.s.Import(ctx, f.ws, 42)
			if shutdown {
				importState(t, f, "running")
			} else {
				importState(t, f, "failed")
			}
			if slept != 10*time.Minute || !shutdown && calls.Load() != 2 {
				t.Fatalf("waited %s with %d attempts", slept, calls.Load())
			}
		})
	}
}
func TestImportReposAddedAndAmbiguousWorkspace(t *testing.T) {
	f := importFixture(t, func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/repos/org/added/pulls":
			fmt.Fprint(w, `[{"number":2}]`)
		case "/repos/org/added/pulls/2":
			fmt.Fprint(w, `{"number":2,"state":"open","title":"LYK-1","head":{"sha":"abc"},"mergeable_state":"clean","created_at":"2025-01-01T00:00:00Z","updated_at":"2025-01-01T00:00:00Z"}`)
		case "/repos/org/added/commits/abc/check-runs":
			fmt.Fprint(w, `{"check_runs":[]}`)
		case "/repos/org/added/commits/abc/status":
			fmt.Fprint(w, `{"statuses":[]}`)
		default:
			t.Errorf("unexpected %s", r.URL)
			http.NotFound(w, r)
		}
	})
	_, err := f.env.Owner.Exec(t.Context(), `insert into workspace(id,slug,name,card_prefix) values(gen_random_uuid(),'other','Other','LYK');
 insert into github_connection(workspace_id,installation_id,account_login,account_type,connected_by) select w.id,42,'org','Organization',u.id from workspace w,"user" u where w.slug='other';
 insert into card(workspace_id,number,title,question,owner_id,board_column) select w.id,1,'Study','Question',u.id,'grounding' from workspace w,"user" u where w.slug='other';
 update github_connection set import_state='done'`)
	if err != nil {
		t.Fatal(err)
	}
	payload := []byte(`{"action":"added","installation":{"id":42},"repositories_added":[{"full_name":"org/added"}]}`)
	if err := f.s.Handle(t.Context(), "installation_repositories", "added-repo", payload); err != nil {
		t.Fatal(err)
	}
	waitImportReceipt(t, f, "added-repo")
	var n, links, events, done int
	err = f.env.Owner.QueryRow(t.Context(), `select (select count(*) from github_pull_request where repo_name='added' and snapshot_at is not null),(select count(*) from github_pr_link),(select count(*) from event where type='github.changed'),(select count(*) from github_connection where import_state='done')`).Scan(&n, &links, &events, &done)
	if err != nil || n != 2 || links != 0 || events != 0 || done != 2 {
		t.Fatalf("mirrors=%d links=%d transitions=%d done=%d: %v", n, links, events, done, err)
	}
}
func TestImportUnsuspendResumesRunning(t *testing.T) {
	f := importFixture(t, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/installation/repositories" {
			t.Errorf("unexpected %s", r.URL)
		}
		fmt.Fprint(w, `{"repositories":[]}`)
	})
	_, err := f.env.Owner.Exec(t.Context(), `update github_connection set status='suspended',import_state='running'`)
	if err != nil {
		t.Fatal(err)
	}
	if err := f.s.Handle(t.Context(), "installation", "resume-running", []byte(`{"action":"unsuspend","installation":{"id":42,"updated_at":"2025-01-01T00:00:00Z"}}`)); err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(3 * time.Second)
	for {
		var done bool
		if err := f.env.Owner.QueryRow(t.Context(), `select import_state='done' from github_connection`).Scan(&done); err != nil {
			t.Fatal(err)
		}
		if done {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("unfinished import did not resume")
		}
		time.Sleep(5 * time.Millisecond)
	}
}

func TestImportAddedWebhookReturnsBeforeGitHub(t *testing.T) {
	started, release := make(chan struct{}), make(chan struct{})
	f := importFixture(t, func(w http.ResponseWriter, r *http.Request) { close(started); <-release; fmt.Fprint(w, "[]") })
	released := false
	defer func() {
		if !released {
			close(release)
		}
	}()
	result := make(chan error, 1)
	go func() {
		result <- f.s.Handle(t.Context(), "installation_repositories", "delayed", []byte(`{"action":"added","installation":{"id":42},"repositories_added":[{"full_name":"org/new"}]}`))
	}()
	select {
	case <-started:
	case <-time.After(2 * time.Second):
		t.Fatal("no list request")
	}
	select {
	case err := <-result:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(500 * time.Millisecond):
		t.Fatal("webhook waited for GitHub")
	}
	var receipts int
	if err := f.env.Owner.QueryRow(t.Context(), `select count(*) from github_webhook_delivery where id='delayed'`).Scan(&receipts); err != nil || receipts != 0 {
		t.Fatalf("premature receipts %d: %v", receipts, err)
	}
	close(release)
	released = true
	waitImportReceipt(t, f, "delayed")
}
func waitImportReceipt(t *testing.T, f snapshotFixture, id string) {
	t.Helper()
	deadline := time.Now().Add(3 * time.Second)
	for {
		var seen bool
		if err := f.env.Owner.QueryRow(t.Context(), `select exists(select from github_webhook_delivery where id=$1)`, id).Scan(&seen); err != nil {
			t.Fatal(err)
		}
		if seen {
			return
		}
		if time.Now().After(deadline) {
			t.Fatal("missing delivery receipt", id)
		}
		time.Sleep(5 * time.Millisecond)
	}
}

func TestImportOverlappingTriggersShareOneRun(t *testing.T) {
	started, duplicate, release := make(chan struct{}), make(chan struct{}), make(chan struct{})
	var calls atomic.Int32
	f := importFixture(t, func(w http.ResponseWriter, r *http.Request) {
		if calls.Add(1) == 1 {
			close(started)
			<-release
		} else {
			close(duplicate)
		}
		fmt.Fprint(w, `{"repositories":[]}`)
	})
	finished := make(chan struct{}, 2)
	go func() { f.s.Import(t.Context(), f.ws, 42); finished <- struct{}{} }()
	<-started
	go func() { f.s.ResumeImports(t.Context()); finished <- struct{}{} }()
	select {
	case <-duplicate:
		t.Error("second trigger launched another HTTP import")
	case <-time.After(200 * time.Millisecond):
	}
	close(release)
	<-finished
	<-finished
	if calls.Load() != 1 {
		t.Errorf("list calls %d", calls.Load())
	}
	importState(t, f, "done")
}

func TestImportAddedRedeliveryAfterSigningCredentialsRestored(t *testing.T) {
	for _, missing := range []string{"key", "app_id"} {
		t.Run(missing, func(t *testing.T) {
			var lists atomic.Int32
			f := importFixture(t, func(w http.ResponseWriter, r *http.Request) {
				if r.URL.Path != "/repos/org/new/pulls" || r.URL.Query().Get("state") != "open" {
					t.Errorf("unexpected %s", r.URL)
					http.NotFound(w, r)
					return
				}
				lists.Add(1)
				fmt.Fprint(w, "[]")
			})
			cfg := f.s.Config
			if missing == "key" {
				cfg.PrivateKey = nil
			} else {
				cfg.AppID = 0
			}
			unavailable := &Service{Store: f.s.Store, Config: cfg}
			payload := []byte(`{"action":"added","installation":{"id":42},"repositories_added":[{"full_name":"org/new"}]}`)
			const delivery = "retry-added"
			if err := unavailable.Handle(t.Context(), "installation_repositories", delivery, payload); err != nil {
				t.Fatal(err)
			}
			var seen bool
			if err := f.env.Owner.QueryRow(t.Context(), `select exists(select from github_webhook_delivery where id=$1)`, delivery).Scan(&seen); err != nil || seen || lists.Load() != 0 {
				t.Fatalf("unavailable signer: receipt=%v list requests=%d err=%v", seen, lists.Load(), err)
			}
			if err := f.s.Handle(t.Context(), "installation_repositories", delivery, payload); err != nil {
				t.Fatal(err)
			}
			waitImportReceipt(t, f, delivery)
			if lists.Load() != 1 {
				t.Fatalf("redelivery made %d list requests", lists.Load())
			}
		})
	}
}
