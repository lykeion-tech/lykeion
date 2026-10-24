package cli

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"testing"
)

type sentOutput struct {
	path string
	body []byte
}

// outputServer stands in for Lykeion's upload route and records what each
// request carried.
func outputServer(t *testing.T) (*httptest.Server, func() []sentOutput) {
	t.Helper()
	var mu sync.Mutex
	var sent []sentOutput
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != "POST" || r.URL.Path != "/api/role/outputs" || r.Header.Get("Authorization") != "Bearer ltk_run" {
			t.Errorf("unexpected request %s %s", r.Method, r.URL.Path)
		}
		mr, err := r.MultipartReader()
		if err != nil {
			t.Fatal(err)
		}
		first, err := mr.NextPart()
		if err != nil || first.FormName() != "path" {
			t.Fatalf("first part %v %v, want path", first, err)
		}
		name, _ := io.ReadAll(first)
		file, err := mr.NextPart()
		if err != nil || file.FormName() != "file" {
			t.Fatalf("second part %v %v, want file", file, err)
		}
		body, _ := io.ReadAll(file)
		mu.Lock()
		sent = append(sent, sentOutput{string(name), body})
		mu.Unlock()
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(201)
		fmt.Fprintf(w, `{"key":"k-%s","name":%q,"size_bytes":%d,"sha256":"h","content_type":"text/plain"}`, name, name, len(body))
	}))
	t.Cleanup(srv.Close)
	return srv, func() []sentOutput { mu.Lock(); defer mu.Unlock(); return slices.Clone(sent) }
}

// inRun makes a run folder, points the run's env at srv and moves into the
// folder, as the daemon starts a run.
func inRun(t *testing.T, srv *httptest.Server) string {
	t.Helper()
	dir, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Setenv("LYK_TOKEN", "ltk_run")
	t.Setenv("LYK_SERVER", srv.URL)
	t.Setenv("LYK_CONFIG", filepath.Join(dir, ".lyk-config.json"))
	t.Chdir(dir)
	return dir
}

func writeFile(t *testing.T, path, body string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
}

func runLyk(args ...string) (string, string, error) {
	var out, errOut strings.Builder
	cmd := Root(strings.NewReader(""), &out)
	cmd.SetErr(&errOut)
	cmd.SetArgs(args)
	err := cmd.Execute()
	return out.String(), errOut.String(), err
}

func TestOutputPutSendsAFile(t *testing.T) {
	srv, sent := outputServer(t)
	dir := inRun(t, srv)
	writeFile(t, filepath.Join(dir, "result.npz"), "numbers")
	out, _, err := runLyk("output", "put", "result.npz")
	if err != nil {
		t.Fatal(err)
	}
	if got := sent(); len(got) != 1 || got[0].path != "result.npz" || string(got[0].body) != "numbers" {
		t.Fatalf("sent %+v", got)
	}
	var printed map[string]any
	if err := json.Unmarshal([]byte(out), &printed); err != nil || printed["key"] != "k-result.npz" || printed["name"] != "result.npz" || printed["size_bytes"] != 7.0 || printed["sha256"] != "h" {
		t.Fatalf("printed %q (%v)", out, err)
	}
}

func TestOutputPutWalksAFolder(t *testing.T) {
	srv, sent := outputServer(t)
	dir := inRun(t, srv)
	writeFile(t, filepath.Join(dir, "pred_results", "a.csv"), "a")
	writeFile(t, filepath.Join(dir, "pred_results", "sub", "b.csv"), "b")
	outside := filepath.Join(t.TempDir(), "secret.txt")
	writeFile(t, outside, "secret")
	if err := os.Symlink(outside, filepath.Join(dir, "pred_results", "x")); err != nil {
		t.Fatal(err)
	}
	out, errOut, err := runLyk("output", "put", "pred_results/", "--output", "json")
	if err != nil {
		t.Fatal(err)
	}
	var names []string
	for _, s := range sent() {
		names = append(names, s.path)
		if strings.Contains(string(s.body), "secret") {
			t.Errorf("sent the link's target as %s", s.path)
		}
	}
	slices.Sort(names)
	if !slices.Equal(names, []string{"pred_results/a.csv", "pred_results/sub/b.csv"}) {
		t.Fatalf("sent %v", names)
	}
	if !strings.Contains(errOut, "pred_results/x") {
		t.Fatalf("stderr %q does not name the skipped link", errOut)
	}
	var list []map[string]any
	if err := json.Unmarshal([]byte(out), &list); err != nil || len(list) != 2 {
		t.Fatalf("printed %q (%v)", out, err)
	}
}

func TestOutputPutRefusesBeforeSending(t *testing.T) {
	srv, sent := outputServer(t)
	dir := inRun(t, srv)
	writeFile(t, filepath.Join(filepath.Dir(dir), "outside"), "x")
	abs := filepath.Join(t.TempDir(), "abs.txt")
	writeFile(t, abs, "x")
	for _, p := range []string{".lyk-config.json", ".claude/settings.json", "attachments/data.csv", "CLAUDE.md", "ok.txt"} {
		writeFile(t, filepath.Join(dir, p), "{}")
	}
	big, err := os.Create(filepath.Join(dir, "big.bin"))
	if err != nil {
		t.Fatal(err)
	}
	if err := big.Truncate(100<<20 + 1); err != nil {
		t.Fatal(err)
	}
	big.Close()
	for i := range 101 {
		writeFile(t, filepath.Join(dir, "many", fmt.Sprintf("%03d.txt", i)), "x")
	}
	for _, args := range [][]string{
		{"https://x/y"},
		{"../outside"},
		{abs},
		{".lyk-config.json"},
		{".claude/settings.json"},
		{"attachments/data.csv"},
		{"CLAUDE.md"},
		{"big.bin"},
		{"many"},
		// A refused path refuses the whole call, even after a good one.
		{"ok.txt", "CLAUDE.md"},
	} {
		if _, _, err := runLyk(append([]string{"output", "put"}, args...)...); err == nil {
			t.Errorf("%v: no error", args)
		}
		if got := sent(); len(got) != 0 {
			t.Fatalf("%v: sent %d requests", args, len(got))
		}
	}
	// The same server and folder do take a file the rules allow.
	if _, _, err := runLyk("output", "put", "ok.txt"); err != nil || len(sent()) != 1 {
		t.Fatalf("ok.txt: %v, sent %d", err, len(sent()))
	}
}

func TestOutputGetWritesTheFile(t *testing.T) {
	var paths []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		paths = append(paths, r.Method+" "+r.URL.Path)
		if r.Header.Get("Authorization") != "Bearer ltk_run" {
			t.Error("output get did not use the run token")
		}
		switch r.URL.Path {
		case "/api/role/outputs/k1":
			w.Write([]byte("saved bytes"))
		case "/api/role/outputs/moved":
			http.Redirect(w, r, "/api/role/outputs/k1", http.StatusFound)
		default:
			http.NotFound(w, r)
		}
	}))
	defer srv.Close()
	dir := inRun(t, srv)
	if _, _, err := runLyk("output", "get", "k1", "--to", "copy.bin"); err != nil {
		t.Fatal(err)
	}
	if b, err := os.ReadFile(filepath.Join(dir, "copy.bin")); err != nil || string(b) != "saved bytes" {
		t.Fatalf("wrote %q (%v)", b, err)
	}
	out, _, err := runLyk("output", "get", "k1")
	if err != nil || out != "saved bytes" {
		t.Fatalf("stdout %q (%v)", out, err)
	}
	paths = nil
	if _, _, err := runLyk("output", "get", "moved"); err == nil {
		t.Fatal("a redirect was not refused")
	}
	if !slices.Equal(paths, []string{"GET /api/role/outputs/moved"}) {
		t.Fatalf("requests %v: the redirect was followed", paths)
	}
}

func TestOutputCommandsNeedAnAgentRunToken(t *testing.T) {
	t.Setenv("LYK_TOKEN", "")
	t.Chdir(t.TempDir())
	writeFile(t, "f.txt", "x")
	t.Setenv("LYK_CONFIG", filepath.Join(t.TempDir(), ".lyk-config.json"))
	for _, args := range [][]string{{"output", "put", "f.txt"}, {"output", "get", "k"}} {
		if _, _, err := runLyk(args...); err == nil || !strings.Contains(err.Error(), "LYK_TOKEN") {
			t.Fatalf("%v: %v", args, err)
		}
	}
}
