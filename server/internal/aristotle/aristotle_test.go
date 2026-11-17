package aristotle

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestParseOutcome(t *testing.T) {
	o, err := parseOutcome(`{"status":"fixed","title":"fix(cli): x\nmore","summary":"body"}`)
	if err != nil || o.Title != "fix(cli): x" {
		t.Fatalf("got %+v, %v", o, err)
	}
	for _, bad := range []string{`{"status":"merged","title":"t","summary":"s"}`, `{"status":"fixed","title":" ","summary":"s"}`, `{"status":"needs_info","title":"","summary":""}`, `nope`} {
		if _, err := parseOutcome(bad); err == nil {
			t.Errorf("accepted %s", bad)
		}
	}
	if _, err := parseOutcome(`{"status":"needs_info","title":"","summary":"which version?"}`); err != nil {
		t.Error(err)
	}
}

func TestAccountEmail(t *testing.T) {
	if got := (Account{Login: "aristotle-agent", ID: 42}).Email(); got != "42+aristotle-agent@users.noreply.github.com" {
		t.Fatal(got)
	}
}

func gitT(t *testing.T, dir string, env []string, args ...string) string {
	t.Helper()
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	cmd.Env = append(os.Environ(), env...)
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git %v: %v: %s", args, err, out)
	}
	return strings.TrimSpace(string(out))
}

func TestImportChecksCommits(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not installed")
	}
	const email = "42+aristotle-agent@users.noreply.github.com"
	who := func(e string) []string {
		return []string{"GIT_AUTHOR_NAME=a", "GIT_AUTHOR_EMAIL=" + e, "GIT_COMMITTER_NAME=a", "GIT_COMMITTER_EMAIL=" + e, "GIT_CONFIG_GLOBAL=/dev/null"}
	}
	root := t.TempDir()
	work := filepath.Join(root, "work")
	gitT(t, root, who(email), "init", "-q", "-b", "main", work)
	gitT(t, work, who("maintainer@example.com"), "commit", "-q", "--allow-empty", "-m", "base")
	base := gitT(t, work, nil, "rev-parse", "HEAD")
	mirror := filepath.Join(root, "mirror.git")
	g := Git{Home: root}
	if _, err := g.run(context.Background(), "", "init", "--bare", "-q", mirror); err != nil {
		t.Fatal(err)
	}
	gitT(t, mirror, nil, "fetch", "-q", work, "main:main")
	bundle := func(name string) string {
		b := filepath.Join(root, name)
		gitT(t, work, nil, "bundle", "create", b, "HEAD", "^"+base)
		return b
	}
	gitT(t, work, who(email), "commit", "-q", "--allow-empty", "-m", "fix")
	if n, err := g.Import(context.Background(), mirror, bundle("ok.bundle"), "refs/aristotle/1", base, email); err != nil || n != 1 {
		t.Fatalf("own commit: %d, %v", n, err)
	}
	gitT(t, work, who("someone@example.com"), "commit", "-q", "--allow-empty", "-m", "spoof")
	if _, err := g.Import(context.Background(), mirror, bundle("spoof.bundle"), "refs/aristotle/2", base, email); err == nil {
		t.Fatal("accepted a commit by someone else")
	}
}

func TestGitHubAssignedSkipsPulls(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer tok" || r.URL.Query().Get("assignee") != "aristotle-agent" {
			http.Error(w, "bad request", 400)
			return
		}
		w.Write([]byte(`[{"number":1,"title":"bug"},{"number":2,"pull_request":{}}]`))
	}))
	defer srv.Close()
	g := &GitHub{Base: srv.URL, Token: "tok", Client: srv.Client()}
	issues, err := g.Assigned(context.Background(), "aristotle-agent")
	if err != nil || len(issues) != 1 || issues[0].Number != 1 {
		t.Fatalf("got %+v, %v", issues, err)
	}
}
