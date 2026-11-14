package cli

import (
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
)

func TestTwoContentSourcesAreRefused(t *testing.T) {
	cmd := Root(strings.NewReader(""), &strings.Builder{})
	cmd.SetArgs([]string{"record", "put", "note", "--content", "{}", "--content-stdin"})
	cmd.SetErr(&strings.Builder{})
	if err := cmd.Execute(); err == nil {
		t.Fatal("two content sources accepted")
	}
}

func TestLykRunsListsTheRecordedRuns(t *testing.T) {
	t.Setenv("LYK_CONFIG", filepath.Join(t.TempDir(), "config.json"))
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != "GET" || r.URL.Path != "/api/role/runs" || r.Header.Get("Authorization") != "Bearer ltk_run" {
			t.Errorf("unexpected request %s %s", r.Method, r.URL.Path)
		}
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`[{"key":"x1","tool":"run_script","language":"python","path":"analysis/a.py"},{"key":"x2","tool":"run_code","language":"r"}]`))
	}))
	defer srv.Close()
	t.Setenv("LYK_TOKEN", "ltk_run")
	t.Setenv("LYK_SERVER", srv.URL)

	if out := lyk(t, "", "runs"); out != "x1 run_script python analysis/a.py\nx2 run_code r \n" {
		t.Fatalf("output %q", out)
	}
	if out := lyk(t, "", "runs", "--output", "json"); !strings.Contains(out, `"key":"x2"`) {
		t.Fatalf("json output %q", out)
	}
}
