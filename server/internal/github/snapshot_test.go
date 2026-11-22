package github

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"slices"
	"sync/atomic"
	"testing"
	"time"

	"github.com/google/uuid"
	"lykeion/server/internal/store"
	"lykeion/server/internal/testutil"
	db "lykeion/server/pkg/db/generated"
)

func TestRollup(t *testing.T) {
	for _, tc := range []struct {
		name          string
		runs          []CheckRun
		statuses      []Status
		state         string
		passed, total int
		failing       []string
	}{
		{"zero", nil, nil, "", 0, 0, nil},
		{"all success", []CheckRun{{"unit", "completed", "success"}, {"platform", "completed", "skipped"}}, []Status{{"lint", "success"}}, "passed", 3, 3, nil},
		{"one failed", []CheckRun{{"test", "completed", "failure"}, {"build", "queued", ""}}, nil, "failed", 0, 2, []string{"test"}},
		{"in progress", []CheckRun{{"test", "in_progress", ""}}, nil, "pending", 0, 1, nil},
		{"status error", nil, []Status{{"deploy", "error"}}, "failed", 0, 1, []string{"deploy"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			state, passed, total, fail := rollup(tc.runs, tc.statuses)
			if (state == nil) != (tc.state == "") || state != nil && *state != tc.state || passed != tc.passed || total != tc.total || !slices.Equal(fail, tc.failing) {
				t.Fatalf("rollup = %v %d/%d %v", state, passed, total, fail)
			}
		})
	}
}

func TestMergeability(t *testing.T) {
	for in, want := range map[string]string{"clean": "mergeable", "dirty": "conflicting", "blocked": "blocked", "behind": "behind", "unknown": "", "unstable": ""} {
		v := mergeability(in)
		if (v == nil) != (want == "") || v != nil && *v != want {
			t.Errorf("%q: got %v", in, v)
		}
	}
}

type snapshotFixture struct {
	s   *Service
	ws  uuid.UUID
	env *testutil.Env
}

func newSnapshotFixture(t *testing.T, api http.HandlerFunc) snapshotFixture {
	t.Helper()
	env := testutil.Setup(t)
	ws, user := uuid.New(), uuid.New()
	for _, q := range []struct {
		sql  string
		args []any
	}{
		{`insert into "user"(id,email) values($1,'snapshot@example.test')`, []any{user}},
		{`insert into workspace(id,slug,name) values($1,'lab','lab')`, []any{ws}},
		{`insert into github_connection(workspace_id,installation_id,account_login,account_type,connected_by) values($1,42,'org','Organization',$2)`, []any{ws, user}},
		{`insert into github_pull_request(workspace_id,installation_id,repo_owner,repo_name,number,title,state,html_url,head_sha,pr_created_at,pr_updated_at) values($1,42,'org','repo',1,'demo','open','','abc',now(),now())`, []any{ws}},
	} {
		if _, err := env.Owner.Exec(t.Context(), q.sql, q.args...); err != nil {
			t.Fatal(err)
		}
	}
	srv := httptest.NewServer(api)
	t.Cleanup(srv.Close)
	return snapshotFixture{&Service{Store: &store.Store{Pool: env.App}, Config: Config{APIURL: srv.URL, AppID: 1, PrivateKey: testAppKey(t)}}, ws, env}
}
func (f snapshotFixture) row(t *testing.T) db.GithubPullRequest {
	t.Helper()
	row, err := db.New(f.env.Owner).GetGithubPullRequestByRepo(t.Context(), db.GetGithubPullRequestByRepoParams{WorkspaceID: f.ws, RepoOwner: "org", RepoName: "repo", Number: 1})
	if err != nil {
		t.Fatal(err)
	}
	return row
}
func TestSnapshotWritten(t *testing.T) {
	f := newSnapshotFixture(t, func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/app/installations/42/access_tokens":
			fmt.Fprint(w, `{"token":"installation-token"}`)
		case "/repos/org/repo/pulls/1":
			fmt.Fprint(w, `{"state":"open","head":{"sha":"abc"},"mergeable_state":"clean"}`)
		case "/repos/org/repo/commits/abc/check-runs":
			if r.URL.Query().Get("page") == "" {
				w.Header().Set("Link", `</repositories/9/commits/abc/check-runs?page=2>; rel="next"`)
			}
			fmt.Fprint(w, `{"check_runs":[{"name":"unit","status":"completed","conclusion":"success"}]}`)
		case "/repos/org/repo/commits/abc/status":
			if r.URL.Query().Get("page") == "" {
				w.Header().Set("Link", `<?page=2>; rel="next"`)
			}
			fmt.Fprint(w, `{"statuses":[{"context":"build","state":"success"}]}`)
		default:
			t.Errorf("unexpected %s", r.URL.Path)
			http.NotFound(w, r)
		}
	})
	card := uuid.New()
	if _, err := f.env.Owner.Exec(t.Context(), `insert into card(id,workspace_id,number,title,question,owner_id,board_column) select $1,workspace_id,1,'Study','Question',connected_by,'grounding' from github_connection`, card); err != nil {
		t.Fatal(err)
	}
	if _, err := f.env.Owner.Exec(t.Context(), `insert into github_pr_link(workspace_id,card_id,pr_id,source) select workspace_id,$1,id,'manual' from github_pull_request`, card); err != nil {
		t.Fatal(err)
	}
	f.s.refresh(t.Context(), 42, "org", "repo", 1)
	f.s.refresh(t.Context(), 42, "org", "repo", 1)
	row := f.row(t)
	if row.CiState == nil || *row.CiState != "passed" || row.CiTotal != 4 || row.CiPassed != 4 || row.Mergeability == nil || *row.Mergeability != "mergeable" || row.SnapshotAt == nil || row.SnapshotStale {
		t.Fatalf("snapshot %+v", row)
	}
	var events int
	if err := f.env.Owner.QueryRow(t.Context(), `select count(*) from event where type='github.pr.changed' and card_id=$1 and actor_type='system'`, card).Scan(&events); err != nil || events != 1 {
		t.Fatalf("card changed events %d: %v", events, err)
	}

}
func TestSnapshotStaleOnGitHubError(t *testing.T) {
	started, release := make(chan struct{}), make(chan struct{})
	f := newSnapshotFixture(t, func(w http.ResponseWriter, r *http.Request) {
		close(started)
		<-release
		http.Error(w, "unavailable", 502)
	})
	if _, err := f.env.Owner.Exec(t.Context(), `update github_pull_request set ci_state='passed',ci_passed=2,ci_total=2,mergeability='behind',snapshot_at=now()`); err != nil {
		t.Fatal(err)
	}
	prev := f.row(t)
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	err := f.s.Handle(ctx, "check_suite", "snapshot-1", []byte(`{"installation":{"id":42},"repository":{"name":"repo","owner":{"login":"org"}},"check_suite":{"head_sha":"abc"}}`))
	cancel()
	close(release)
	if err != nil {
		t.Fatal(err)
	}
	select {
	case <-started:
	case <-time.After(3 * time.Second):
		t.Fatal("no refresh")
	}
	until := time.Now().Add(3 * time.Second)
	for {
		row := f.row(t)
		if row.SnapshotStale {
			if row.CiState == nil || *row.CiState != "passed" || row.CiTotal != 2 || row.CiPassed != 2 || row.Mergeability == nil || *row.Mergeability != "behind" || !row.SnapshotAt.Equal(*prev.SnapshotAt) {
				t.Fatalf("lost previous snapshot: %+v", row)
			}
			return
		}
		if time.Now().After(until) {
			t.Fatal("not marked stale")
		}
		time.Sleep(10 * time.Millisecond)
	}
}
func TestNoSnapshotForMerged(t *testing.T) {
	var calls atomic.Int32
	f := newSnapshotFixture(t, func(w http.ResponseWriter, r *http.Request) { calls.Add(1); http.Error(w, "unexpected", 500) })
	if _, err := f.env.Owner.Exec(t.Context(), `update github_pull_request set state='merged'`); err != nil {
		t.Fatal(err)
	}
	f.s.refresh(t.Context(), 42, "org", "repo", 1)
	if row := f.row(t); calls.Load() != 0 || row.SnapshotStale || row.SnapshotAt != nil {
		t.Fatalf("requests %d row %+v", calls.Load(), row)
	}
}

func TestSnapshotLateResponsePreservesNewerSuccess(t *testing.T) {
	for _, response := range []int{200, 502} {
		t.Run(fmt.Sprint(response), func(t *testing.T) {
			var calls atomic.Int32
			started, release := make(chan struct{}), make(chan struct{})
			f := newSnapshotFixture(t, func(w http.ResponseWriter, r *http.Request) {
				switch r.URL.Path {
				case "/app/installations/42/access_tokens":
					fmt.Fprint(w, `{"token":"fake-installation"}`)
				case "/repos/org/repo/pulls/1":
					fmt.Fprint(w, `{"state":"open","head":{"sha":"abc"}}`)
				case "/repos/org/repo/commits/abc/check-runs":
					fmt.Fprint(w, `{"check_runs":[]}`)
				case "/repos/org/repo/commits/abc/status":
					if calls.Add(1) == 1 {
						close(started)
						select {
						case <-release:
						case <-r.Context().Done():
							return
						}
						w.WriteHeader(response)
						fmt.Fprint(w, `{"statuses":[{"context":"build","state":"pending"}]}`)
						return
					}
					fmt.Fprint(w, `{"statuses":[{"context":"build","state":"success"}]}`)
				default:
					http.NotFound(w, r)
				}
			})
			done := make(chan struct{})
			go func() { defer close(done); f.s.refresh(t.Context(), 42, "org", "repo", 1) }()
			select {
			case <-started:
			case <-time.After(3 * time.Second):
				close(release)
				t.Fatal("first request did not reach status endpoint")
			}
			f.s.refresh(t.Context(), 42, "org", "repo", 1)
			close(release)
			<-done
			row := f.row(t)
			if row.CiState == nil || *row.CiState != "passed" || row.SnapshotStale {
				t.Fatalf("older result replaced newer success: %+v", row)
			}
		})
	}
}

func TestSnapshotIgnoresUnmatchedDestinations(t *testing.T) {
	var calls atomic.Int32
	f := newSnapshotFixture(t, func(w http.ResponseWriter, r *http.Request) { calls.Add(1); http.Error(w, "unexpected", 500) })
	for _, tc := range []struct {
		id               int64
		owner, repo, sha string
	}{
		{43, "org", "repo", "abc"},
		{42, "other", "repo", "abc"},
		{42, "org", "other", "abc"},
		{42, "org", "repo", "other"},
	} {
		f.s.refreshSnapshots(t.Context(), tc.id, tc.owner, tc.repo, 0, tc.sha)
	}
	if _, err := f.env.Owner.Exec(t.Context(), `update github_connection set status='suspended'`); err != nil {
		t.Fatal(err)
	}
	f.s.refreshSnapshots(t.Context(), 42, "org", "repo", 0, "abc")
	if calls.Load() != 0 {
		t.Fatalf("requests for unmatched destinations: %d", calls.Load())
	}
}

func TestSnapshotRechecksConnectionAfterGitHubFetch(t *testing.T) {
	started, release := make(chan struct{}), make(chan struct{})
	f := newSnapshotFixture(t, func(w http.ResponseWriter, r *http.Request) {
		close(started)
		select {
		case <-release:
		case <-r.Context().Done():
			return
		}
		http.Error(w, "unavailable", 502)
	})
	done := make(chan struct{})
	go func() { defer close(done); f.s.refresh(t.Context(), 42, "org", "repo", 1) }()
	select {
	case <-started:
	case <-time.After(3 * time.Second):
		close(release)
		t.Fatal("no request")
	}
	ctx, cancel := context.WithTimeout(t.Context(), 2*time.Second)
	defer cancel()
	_, err := f.env.Owner.Exec(ctx, `update github_connection set status='suspended'`)
	close(release)
	<-done
	if err != nil {
		t.Fatal(err)
	}
	if row := f.row(t); row.SnapshotStale || row.SnapshotAt != nil {
		t.Fatalf("disconnected target changed: %+v", row)
	}
}
