package skill

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"github.com/google/uuid"
	"io"
	"lykeion/server/internal/storage"
	"lykeion/server/internal/store"
	"lykeion/server/internal/testutil"
	"net/http"
	"strings"
	"testing"
)

func TestArchiveImportEnvelope(t *testing.T) {
	main := []byte("Research")
	for _, tc := range []struct{ name, prefix, want string }{{"fallback.zip", "", "fallback"}, {"fallback.skill", "wrapper/", "wrapper"}} {
		b, err := archiveBundle(zipFixture(t, map[string][]byte{tc.prefix + "SKILL.md": main, tc.prefix + "assets/raw.bin": {0, 255}, tc.prefix + ".hidden": []byte("kept")}), tc.name)
		if err != nil || b.Name != tc.want || len(b.Files) != 3 || !bytes.Equal(b.Files["assets/raw.bin"], []byte{0, 255}) {
			t.Fatalf("%s: %+v %v", tc.name, b, err)
		}
	}
	for _, files := range []map[string][]byte{{"missing": main}, {"SKILL.md": main, "../bad": {1}}, {"SKILL.md": main, "a/../bad": {1}, "bad": {2}}, {"SKILL.md": main, "large": bytes.Repeat([]byte("x"), FileBytes+1)}} {
		if _, err := archiveBundle(zipFixture(t, files), "bad.zip"); err == nil {
			t.Fatal("unsafe/incomplete import accepted")
		}
	}
	if _, err := archiveBundle(make([]byte, ArchiveBytes+1), "big.zip"); err == nil {
		t.Fatal("oversized compressed bundle accepted")
	}
	if _, err := archiveBundle(zipFixture(t, map[string][]byte{"SKILL.md": main}), "bad.tar"); err == nil {
		t.Fatal("unsupported archive accepted")
	}
	files := map[string][]byte{"SKILL.md": main}
	for i := 0; i < 9; i++ {
		files[fmt.Sprint(i)] = bytes.Repeat([]byte("x"), FileBytes)
	}
	if _, err := archiveBundle(zipFixture(t, files), "big.zip"); err == nil {
		t.Fatal("oversized decompressed bundle accepted")
	}
}

type importTransport func(*http.Request) (*http.Response, error)

func (f importTransport) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }
func importResponse(status int, body any) *http.Response {
	var data []byte
	switch b := body.(type) {
	case []byte:
		data = b
	case string:
		data = []byte(b)
	default:
		data, _ = json.Marshal(body)
	}
	return &http.Response{StatusCode: status, Body: io.NopCloser(bytes.NewReader(data)), Header: make(http.Header)}
}
func githubFixture(t *testing.T, fail, truncated bool) *http.Client {
	t.Setenv("GITHUB_TOKEN", "")
	return &http.Client{Transport: importTransport(func(r *http.Request) (*http.Response, error) {
		if r.URL.Scheme != "https" || r.Header.Get("Authorization") != "" {
			t.Fatalf("unsafe request %s", r.URL)
		}
		p := r.URL.Path
		switch {
		case r.URL.Host == "api.github.com" && p == "/repos/lab/repo":
			return importResponse(200, map[string]string{"default_branch": "main"}), nil
		case strings.Contains(p, "/commits/"):
			if strings.HasSuffix(p, "/release/v2") || strings.HasSuffix(p, "/main") {
				return importResponse(200, "sha"), nil
			}
			return importResponse(404, "missing"), nil
		case strings.Contains(p, "/git/trees/"):
			return importResponse(200, map[string]any{"truncated": truncated, "tree": []map[string]any{{"path": "skills/method/SKILL.md", "type": "blob", "mode": "100644", "size": 50}, {"path": "skills/method/assets/raw.bin", "type": "blob", "mode": "100644", "size": 2}, {"path": "skills/method/.hidden", "type": "blob", "mode": "100644", "size": 4}, {"path": "skills/method/LICENSE", "type": "blob", "mode": "100644", "size": 7}}}), nil
		case strings.Contains(p, "/contents/"):
			return importResponse(503, "unavailable"), nil
		case r.URL.Host == "raw.githubusercontent.com":
			switch {
			case strings.HasSuffix(p, "/SKILL.md"):
				return importResponse(200, "---\nname: method\ndescription: Method\n---\nResearch"), nil
			case strings.HasSuffix(p, "/assets/raw.bin"):
				if fail {
					return importResponse(503, "failed"), nil
				}
				return importResponse(200, []byte{0, 255}), nil
			case strings.HasSuffix(p, "/.hidden"):
				return importResponse(200, "kept"), nil
			case strings.HasSuffix(p, "/LICENSE"):
				return importResponse(200, "license"), nil
			}
		}
		t.Fatalf("unexpected request %s", r.URL)
		return nil, fmt.Errorf("unexpected request")
	})}
}
func TestURLImportSourcesAndWholeFolder(t *testing.T) {
	for _, raw := range []string{"https://github.com/lab/repo/tree/main/skills/method", "github.com/lab/repo/blob/main/skills/method/SKILL.md", "https://github.com/lab/repo/tree/release/v2/skills/method", "https://skills.sh/lab/repo/method"} {
		t.Run(raw, func(t *testing.T) {
			b, _, err := fetchURLBundle(context.Background(), githubFixture(t, false, false), raw)
			if err != nil {
				t.Fatal(err)
			}
			if b.Name != "method" || len(b.Files) != 4 || !bytes.Equal(b.Files["assets/raw.bin"], []byte{0, 255}) {
				t.Fatalf("incomplete import %+v", b)
			}
		})
	}
	for _, raw := range []string{"https://example.com/method", "https://github.com/lab/repo/issues/1", "https://skills.sh/lab/repo", "https://github.com/lab/repo/blob/main/README.md"} {
		if _, _, err := fetchURLBundle(context.Background(), githubFixture(t, false, false), raw); err == nil {
			t.Fatalf("unsupported URL %s", raw)
		}
	}
	client := &http.Client{Transport: importTransport(func(r *http.Request) (*http.Response, error) {
		if r.Header.Get("Authorization") != "" || r.URL.Host != "clawhub.ai" {
			t.Fatalf("credential/source leak %s", r.URL)
		}
		switch r.URL.Path {
		case "/api/v1/skills/method":
			return importResponse(200, map[string]any{"skill": map[string]any{"tags": map[string]string{"latest": "1.0"}}}), nil
		case "/api/v1/skills/method/versions/1.0":
			return importResponse(200, map[string]any{"version": map[string]any{"files": []map[string]string{{"path": "SKILL.md"}, {"path": "assets/raw.bin"}}}}), nil
		case "/api/v1/skills/method/file":
			if r.URL.Query().Get("path") == "SKILL.md" {
				return importResponse(200, "Research"), nil
			}
			return importResponse(200, []byte{0, 255}), nil
		}
		t.Fatalf("unexpected ClawHub request %s", r.URL)
		return nil, nil
	})}
	for _, raw := range []string{"https://clawhub.ai/author/method", "clawhub.ai/method", "method"} {
		b, _, err := fetchURLBundle(context.Background(), client, raw)
		if err != nil || b.Name != "method" || len(b.Files) != 2 {
			t.Fatalf("%s: %+v %v", raw, b, err)
		}
	}
}
func TestImportNeverSilentlyDropsFiles(t *testing.T) {
	for _, tc := range []struct{ fail, truncated bool }{{true, false}, {false, true}} {
		if _, _, err := fetchURLBundle(context.Background(), githubFixture(t, tc.fail, tc.truncated), "https://github.com/lab/repo/tree/main/skills/method"); err == nil {
			t.Fatal("incomplete upstream bundle accepted")
		}
	}
}

func TestURLImportSkillsShSourcePreference(t *testing.T) {
	client := &http.Client{Transport: importTransport(func(r *http.Request) (*http.Response, error) {
		return importResponse(200, "---\nname: research-method\n---\nResearch"), nil
	})}
	dir, err := skillsShDirectory(context.Background(), client, "https://raw.githubusercontent.com/lab/repo/main/", "research-method", []githubEntry{{Path: "a/unrelated/SKILL.md", Type: "blob"}, {Path: "z/method/SKILL.md", Type: "blob"}})
	if err != nil || dir != "z/method" {
		t.Fatalf("source preference: %q %v", dir, err)
	}
}

func TestURLImportRootAndStrictListings(t *testing.T) {
	for _, tc := range []struct {
		name      string
		entries   []githubEntry
		truncated bool
		wantError bool
	}{
		{"root", []githubEntry{{Path: "SKILL.md", Type: "blob", Mode: "100644", Size: 8}, {Path: "asset.bin", Type: "blob", Mode: "100644", Size: 2}}, false, false},
		{"duplicate", []githubEntry{{Path: "SKILL.md", Type: "blob"}, {Path: "SKILL.md", Type: "blob"}}, false, true},
		{"unsafe", []githubEntry{{Path: "SKILL.md", Type: "blob"}, {Path: "../escape", Type: "blob"}}, false, true},
		{"symlink", []githubEntry{{Path: "SKILL.md", Type: "blob", Mode: "120000"}}, false, true},
		{"oversized", []githubEntry{{Path: "SKILL.md", Type: "blob", Size: FileBytes + 1}}, false, true},
		{"missing", []githubEntry{{Path: "other", Type: "blob"}}, false, true},
		{"truncated-success", nil, true, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			client := &http.Client{Transport: importTransport(func(r *http.Request) (*http.Response, error) {
				switch {
				case r.URL.Path == "/repos/lab/repo":
					return importResponse(200, map[string]string{"default_branch": "main"}), nil
				case strings.Contains(r.URL.Path, "/git/trees/"):
					return importResponse(200, map[string]any{"tree": tc.entries, "truncated": tc.truncated}), nil
				case strings.Contains(r.URL.Path, "/contents/"):
					return importResponse(200, []map[string]any{{"path": "SKILL.md", "type": "file", "size": 8}, {"path": "asset.bin", "type": "file", "size": 2}}), nil
				case strings.HasSuffix(r.URL.Path, "/SKILL.md"):
					return importResponse(200, "Research"), nil
				case strings.HasSuffix(r.URL.Path, "/asset.bin"):
					return importResponse(200, []byte{0, 255}), nil
				}
				t.Fatalf("unexpected %s", r.URL)
				return nil, nil
			})}
			b, _, err := fetchURLBundle(context.Background(), client, "https://github.com/lab/repo")
			if (err != nil) != tc.wantError {
				t.Fatalf("bundle %+v err %v", b, err)
			}
			if err == nil && (b.Name != "repo" || len(b.Files) != 2 || !bytes.Equal(b.Files["asset.bin"], []byte{0, 255})) {
				t.Fatalf("root folder incomplete %+v", b)
			}
		})
	}
}

func TestURLImportSkillsShTruncatedTree(t *testing.T) {
	client := &http.Client{Transport: importTransport(func(r *http.Request) (*http.Response, error) {
		switch {
		case r.URL.Path == "/repos/lab/repo":
			return importResponse(200, map[string]string{"default_branch": "main"}), nil
		case strings.Contains(r.URL.Path, "/git/trees/"):
			return importResponse(200, map[string]any{"tree": []githubEntry{}, "truncated": true}), nil
		case r.URL.Path == "/repos/lab/repo/contents/skills":
			return importResponse(200, []map[string]any{{"path": "skills/method", "type": "dir"}}), nil
		case r.URL.Path == "/repos/lab/repo/contents/skills/method":
			return importResponse(200, []map[string]any{{"path": "skills/method/SKILL.md", "type": "file", "size": 8}, {"path": "skills/method/raw.bin", "type": "file", "size": 2}}), nil
		case r.URL.Host == "raw.githubusercontent.com" && strings.HasSuffix(r.URL.Path, "/SKILL.md"):
			return importResponse(200, "---\nname: method\n---\nResearch"), nil
		case r.URL.Host == "raw.githubusercontent.com" && strings.HasSuffix(r.URL.Path, "/raw.bin"):
			return importResponse(200, []byte{0, 255}), nil
		default:
			return importResponse(404, "missing"), nil
		}
	})}
	b, _, err := fetchURLBundle(context.Background(), client, "https://skills.sh/lab/repo/method")
	if err != nil || len(b.Files) != 2 || !bytes.Equal(b.Files["raw.bin"], []byte{0, 255}) {
		t.Fatalf("truncated tree folder: %+v %v", b, err)
	}
}

func TestURLImportPersistenceAndFailure(t *testing.T) {
	env := testutil.Setup(t)
	ctx := context.Background()
	ws, u := uuid.New(), uuid.New()
	for _, seed := range []struct {
		sql  string
		args []any
	}{
		{`INSERT INTO "user"(id,email) VALUES($1,'importer@lab.org')`, []any{u}},
		{`INSERT INTO workspace(id,slug,name) VALUES($1,'lab','Lab')`, []any{ws}},
		{`INSERT INTO member(workspace_id,user_id,role) VALUES($1,$2,'owner')`, []any{ws, u}},
	} {
		if _, err := env.Owner.Exec(ctx, seed.sql, seed.args...); err != nil {
			t.Fatal(err)
		}
	}
	service := &Service{Store: &store.Store{Pool: env.App}, Blobs: storage.Local{Dir: t.TempDir()}}
	previous := http.DefaultClient
	t.Cleanup(func() { http.DefaultClient = previous })
	http.DefaultClient = githubFixture(t, true, false)
	if _, err := ImportURL(ctx, service, ws, u, "https://github.com/lab/repo/tree/main/skills/method"); err == nil {
		t.Fatal("failed source import succeeded")
	}
	var n int
	if err := env.Owner.QueryRow(ctx, `SELECT count(*) FROM workspace_skill`).Scan(&n); err != nil || n != 0 {
		t.Fatalf("failed source published %d skills: %v", n, err)
	}
	http.DefaultClient = githubFixture(t, false, false)
	got, err := ImportURL(ctx, service, ws, u, "https://github.com/lab/repo/tree/main/skills/method")
	if err != nil {
		t.Fatal(err)
	}
	if got.Source.Kind != "github" || len(got.Files) != 4 {
		t.Fatalf("import metadata %+v", got)
	}
	data, err := service.File(ctx, ws, u, got.ID, "assets/raw.bin")
	if err != nil || !bytes.Equal(data, []byte{0, 255}) {
		t.Fatalf("persisted source bytes %v %v", data, err)
	}
	if _, err = ImportURL(ctx, service, ws, u, "https://github.com/lab/repo/tree/main/skills/method"); !errors.Is(err, ErrConflict) {
		t.Fatalf("duplicate source %v", err)
	}
}

func TestURLImportRawProvenanceAndSourceForms(t *testing.T) {
	t.Setenv("GITHUB_TOKEN", "")
	for _, raw := range []string{
		"http://www.github.com/lab/repo/tree/main/skills/method?from=publisher#usage",
		"https://reader:synthetic@github.com:8443/lab/repo/tree/main/skills/method?token=synthetic#anchor",
		"www.github.com/lab/repo/tree/main/skills/method?from=publisher#usage",
	} {
		b, source, err := fetchURLBundle(context.Background(), githubFixture(t, false, false), raw)
		expected := raw
		if !strings.HasPrefix(raw, "http") {
			expected = "https://" + raw
		}
		if err != nil || b.Name != "method" || source.Location != expected {
			t.Fatalf("source input %s: %+v %v", raw, source, err)
		}
	}
}
func TestURLImportGitHubCredentialsAndRedirects(t *testing.T) {
	t.Setenv("GITHUB_TOKEN", "synthetic-server-token")
	for _, target := range []string{"https://api.github.com/repos/lab/repo", "https://raw.githubusercontent.com/lab/repo/main/SKILL.md", "https://clawhub.ai/api/v1/skills/method", "https://api.github.com.unrelated.example/file"} {
		calls := 0
		client := &http.Client{Transport: importTransport(func(r *http.Request) (*http.Response, error) {
			calls++
			if calls == 1 {
				want := ""
				if r.URL.Hostname() == "api.github.com" || r.URL.Hostname() == "raw.githubusercontent.com" {
					want = "Bearer synthetic-server-token"
				}
				if r.Header.Get("Authorization") != want {
					t.Errorf("initial %s auth=%q want=%q", r.URL, r.Header.Get("Authorization"), want)
				}
				resp := importResponse(302, "")
				resp.Header.Set("Location", "https://unrelated.example/file")
				return resp, nil
			}
			if r.Header.Get("Authorization") != "" {
				t.Error("server token forwarded to unrelated source host")
			}
			return importResponse(200, "Research"), nil
		})}
		resp, err := sourceGet(context.Background(), client, target)
		if err != nil {
			t.Fatal(err)
		}
		resp.Body.Close()
		if calls != 2 {
			t.Fatalf("standard redirect not followed: %d", calls)
		}
	}
}

func TestArchiveImportRootFolderProvenance(t *testing.T) {
	b, source, err := folderBundle(map[string][]byte{"SKILL.md": []byte("---\nname: root-method\n---\nResearch")})
	if err != nil || b.Name != "root-method" || source.Location != "SKILL.md" {
		t.Fatalf("root folder source: %+v %+v %v", b, source, err)
	}
	if _, _, err = folderBundle(map[string][]byte{"SKILL.md": []byte("Research")}); err == nil {
		t.Fatal("invented root folder fallback name")
	}
}

func TestURLImportClawHubFallback(t *testing.T) {
	for _, tc := range []struct {
		display, main, want string
		invalid             bool
	}{
		{"published-method", "Research", "published-method", false},
		{"Display Name", "Research", "", true},
		{"Display Name", "---\nname: main-method\n---\nResearch", "main-method", false},
		{"", "Research", "method", false},
	} {
		t.Run(tc.display+tc.want, func(t *testing.T) {
			client := &http.Client{Transport: importTransport(func(r *http.Request) (*http.Response, error) {
				switch r.URL.Path {
				case "/api/v1/skills/method":
					return importResponse(200, map[string]any{"skill": map[string]any{"displayName": tc.display, "tags": map[string]string{"latest": "1.0"}}}), nil
				case "/api/v1/skills/method/versions/1.0":
					return importResponse(200, map[string]any{"version": map[string]any{"files": []map[string]string{{"path": "SKILL.md"}}}}), nil
				default:
					return importResponse(200, tc.main), nil
				}
			})}
			b, _, err := fetchURLBundle(context.Background(), client, "https://clawhub.ai/owner/method")
			if (err != nil) != tc.invalid || err == nil && b.Name != tc.want {
				t.Fatalf("ClawHub fallback %q: %+v %v", tc.display, b, err)
			}
		})
	}
}

func TestURLImportSkillsShPreferredPartialTree(t *testing.T) {
	for _, status := range []int{200, 503} {
		t.Run(fmt.Sprint(status), func(t *testing.T) {
			conventionalLookups := 0
			folderCrawls := 0
			client := &http.Client{Transport: importTransport(func(r *http.Request) (*http.Response, error) {
				switch {
				case r.URL.Path == "/repos/lab/repo":
					return importResponse(200, map[string]string{"default_branch": "main"}), nil
				case strings.Contains(r.URL.Path, "/git/trees/"):
					return importResponse(200, map[string]any{"tree": []githubEntry{{Path: "custom/method/SKILL.md", Type: "blob", Mode: "100644", Size: 28}}, "truncated": true}), nil
				case r.URL.Path == "/repos/lab/repo/contents/custom/method":
					folderCrawls++
					if status != 200 {
						return importResponse(status, "unavailable"), nil
					}
					return importResponse(200, []map[string]any{{"path": "custom/method/SKILL.md", "type": "file", "size": 28}, {"path": "custom/method/raw.bin", "type": "file", "size": 2}, {"path": "custom/method/.hidden", "type": "file", "size": 4}}), nil
				case r.URL.Host == "raw.githubusercontent.com" && r.URL.Path == "/lab/repo/main/custom/method/SKILL.md":
					return importResponse(200, "---\nname: method\n---\nResearch"), nil
				case r.URL.Host == "raw.githubusercontent.com" && r.URL.Path == "/lab/repo/main/custom/method/raw.bin":
					return importResponse(200, []byte{0, 255}), nil
				case r.URL.Host == "raw.githubusercontent.com" && r.URL.Path == "/lab/repo/main/custom/method/.hidden":
					return importResponse(200, "kept"), nil
				default:
					conventionalLookups++
					return importResponse(404, "missing conventional directory"), nil
				}
			})}
			b, _, err := fetchURLBundle(context.Background(), client, "https://skills.sh/lab/repo/method")
			if status == 200 {
				if err != nil || b.Name != "method" || len(b.Files) != 3 || !bytes.Equal(b.Files["raw.bin"], []byte{0, 255}) || string(b.Files[".hidden"]) != "kept" {
					t.Fatalf("preferred partial tree import: %+v %v", b, err)
				}
			} else {
				var fetch *FetchError
				if !errors.As(err, &fetch) || fetch.Status != 502 {
					t.Fatalf("incomplete crawl accepted or misclassified: %+v %v", b, err)
				}
			}
			if conventionalLookups != 0 || folderCrawls != 1 {
				t.Fatalf("partial tree not resolved first: conventional=%d complete folder crawls=%d", conventionalLookups, folderCrawls)
			}
		})
	}
}
