package handler

import (
	"crypto/rand"
	"crypto/rsa"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/google/uuid"
)

type studyPRFixture struct {
	mirrorFixture
	path, member string
	calls        *atomic.Int32
}

func newStudyPRFixture(t *testing.T, duringFetch func(mirrorFixture)) studyPRFixture {
	t.Helper()
	f := newMirrorFixture(t)
	c := f.study(t, "first")
	member := f.member(t, f.token, f.ws, "member@example.test", "member")
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}
	f.pr["state"] = "closed"
	var calls atomic.Int32
	api := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		switch r.URL.Path {
		case "/repos/org/repo/installation":
			if r.Method != "GET" || !strings.HasPrefix(r.Header.Get("Authorization"), "Bearer ey") {
				t.Error("expected signed app lookup")
			}
			fmt.Fprint(w, `{"id":42}`)
		case "/app/installations/42/access_tokens":
			fmt.Fprint(w, `{"token":"ghs_fixture"}`)
		case "/repos/org/repo/pulls/1":
			if r.Header.Get("Authorization") != "Bearer ghs_fixture" || r.Method != "GET" {
				t.Error("expected installation read")
			}
			if duringFetch != nil {
				duringFetch(f)
			}
			json.NewEncoder(w).Encode(f.pr)
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(api.Close)
	f.H.GitHub.Config.PrivateKey = key
	f.H.GitHub.Config.AppID = 1
	f.H.GitHub.Config.APIURL = api.URL
	return studyPRFixture{f, "/api/cards/" + c.ID + "/pull-requests", member, &calls}
}

func (f studyPRFixture) paste(t *testing.T, raw string, want int) map[string]any {
	t.Helper()
	var row map[string]any
	if got := f.call(t, "POST", f.path, f.member, f.ws, map[string]string{"url": raw}, &row); got != want {
		t.Fatalf("paste: %d %v", got, row)
	}
	return row
}

const studyPRURL = "https://github.com/org/repo/pull/1"

func TestStudyPullRequestsList(t *testing.T) {
	f := newStudyPRFixture(t, nil)
	var rows []map[string]any
	if c := f.call(t, "GET", f.path, f.member, f.ws, nil, &rows); c != 200 || rows == nil || len(rows) != 0 {
		t.Fatalf("empty list: %d %v", c, rows)
	}
	f.connect(t, f.ws)
	f.send(t)
	f.pr["number"] = 2
	f.pr["state"] = "open"
	// The fixture exercises webhook mirroring without snapshot HTTP.
	f.H.GitHub.Config.PrivateKey = nil
	f.send(t)
	if c := f.call(t, "GET", f.path, f.member, f.ws, nil, &rows); c != 200 || len(rows) != 2 || rows[0]["number"] != float64(2) || rows[1]["state"] != "closed" {
		t.Fatal(c, rows)
	}
	keys := strings.Fields("id repo_owner repo_name number title state html_url author_login author_avatar_url additions deletions changed_files ci_state ci_passed ci_total ci_failing mergeability snapshot_stale source")
	if len(rows[0]) != len(keys) {
		t.Fatal(rows[0])
	}
	for _, k := range keys {
		if _, ok := rows[0][k]; !ok {
			t.Fatal(k)
		}
	}
}

func TestManualLinkByURL(t *testing.T) {
	f := newStudyPRFixture(t, nil)
	f.connect(t, f.ws)
	f.member = f.personalToken(t, f.member)
	other := f.workspace(t, f.token, "other").ID
	f.connect(t, other)
	row := f.paste(t, studyPRURL, 201)
	if row["source"] != "manual" || row["title"] != "LYK-1" {
		t.Fatal(row)
	}
	var list []map[string]any
	if c := f.call(t, "GET", f.path, f.member, f.ws, nil, &list); c != 200 || len(list) != 1 || !reflect.DeepEqual(row, list[0]) {
		t.Fatal(c, list)
	}
	if again := f.paste(t, studyPRURL, 201); !reflect.DeepEqual(again, row) {
		t.Fatal("repeated manual link changed", again)
	}
	f.count(t, 1, "select count(*) from event where type='github.pr.changed' and workspace_id=$1", f.ws)
	f.count(t, 0, "select count(*) from github_pull_request where workspace_id=$1", other)
	f.pr["title"] = "renamed"
	f.send(t)
	f.links(t, 1)
}

func TestManualLinkUnconnectedRepo(t *testing.T) {
	f := newStudyPRFixture(t, nil)
	if row := f.paste(t, "https://example.com", 400); row["error"] != "Paste a GitHub pull request link." || f.calls.Load() != 0 {
		t.Fatal(row)
	}
	if row := f.paste(t, studyPRURL, 400); row["error"] != "That repository isn't connected to this workspace." {
		t.Fatal(row)
	}
}

func TestManualLinkStaleProviderObservation(t *testing.T) {
	f := newStudyPRFixture(t, nil)
	f.connect(t, f.ws)
	f.pr["title"] = "newer title"
	f.send(t)
	f.pr["updated_at"] = "2026-11-01T01:00:00Z"
	f.pr["title"] = "old title"
	if row := f.paste(t, studyPRURL, 201); row["title"] != "newer title" || row["source"] != "manual" {
		t.Fatal(row)
	}
	f.exec(t, "update github_pull_request set installation_id=99 where workspace_id=$1", f.ws)
	if row := f.paste(t, studyPRURL, 400); row["error"] != "That repository isn't connected to this workspace." {
		t.Fatal(row)
	}
}

func TestManualLinkIdempotent(t *testing.T) {
	f := newStudyPRFixture(t, nil)
	f.connect(t, f.ws)
	f.send(t)
	for range 2 {
		if row := f.paste(t, studyPRURL, 201); row["source"] != "auto" {
			t.Fatal(row)
		}
	}
	f.links(t, 1)
}

func TestUnlinkNeverRelinks(t *testing.T) {
	f := newStudyPRFixture(t, nil)
	f.connect(t, f.ws)
	row := f.paste(t, studyPRURL, 201)
	if c := f.call(t, "DELETE", f.path+"/"+uuid.NewString(), f.member, f.ws, nil, nil); c != 404 {
		t.Fatal(c)
	}
	f.count(t, 0, "select count(*) from github_pr_unlink")
	if c := f.call(t, "DELETE", f.path+"/"+row["id"].(string), f.member, f.ws, nil, nil); c != 204 {
		t.Fatal(c)
	}
	f.send(t)
	f.links(t, 0)
	if row := f.paste(t, studyPRURL, 201); row["source"] != "manual" {
		t.Fatal(row)
	}
	f.count(t, 0, "select count(*) from github_pr_unlink")
}

func TestStudyPRsOtherWorkspace404(t *testing.T) {
	f := newStudyPRFixture(t, nil)
	other := f.workspace(t, f.token, "foreign").ID
	f.connect(t, other)
	for _, tok := range []string{f.token, f.member} {
		for _, method := range []string{"GET", "POST", "DELETE"} {
			path := f.path
			if method == "DELETE" {
				path += "/" + uuid.NewString()
			}
			if c := f.call(t, method, path, tok, other, map[string]string{"url": studyPRURL}, nil); c != 404 {
				t.Fatal(c, method)
			}
		}
	}
	if f.calls.Load() != 0 {
		t.Fatal("unauthorized provider request")
	}
}

func TestManualLinkRechecksMembershipAndConnection(t *testing.T) {
	for _, tc := range []struct {
		name   string
		change func(mirrorFixture)
		status int
	}{
		{"membership", func(f mirrorFixture) { f.exec(t, "delete from member where workspace_id=$1 and role='member'", f.ws) }, 404},
		{"reconnect", func(f mirrorFixture) {
			f.exec(t, "delete from github_connection where workspace_id=$1", f.ws)
			f.connect(t, f.ws)
		}, 400},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f := newStudyPRFixture(t, tc.change)
			f.connect(t, f.ws)
			f.paste(t, studyPRURL, tc.status)
			f.count(t, 0, "select count(*) from github_pull_request")
		})
	}
}
