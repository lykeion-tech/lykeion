package cli

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestTeamStepPostsTheMemberAndContent(t *testing.T) {
	t.Setenv("LYK_CONFIG", filepath.Join(t.TempDir(), "config.json"))
	var got []map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != "POST" || r.URL.Path != "/api/role/steps" || r.Header.Get("Authorization") != "Bearer ltk_run" {
			t.Errorf("unexpected request %s %s", r.Method, r.URL.Path)
		}
		var body map[string]any
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Error(err)
		}
		got = append(got, body)
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(201)
		w.Write([]byte(`{"key":"r3","agent_run_id":"run-1"}`))
	}))
	defer srv.Close()
	t.Setenv("LYK_TOKEN", "ltk_run")
	t.Setenv("LYK_SERVER", srv.URL)

	if out := lyk(t, "", "team", "step", "member-1", "--content", "List the rivals."); out != "r3\n" {
		t.Fatalf("output %q", out)
	}
	file := filepath.Join(t.TempDir(), "step.md")
	if err := os.WriteFile(file, []byte("Line one.\nLine two.\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if out := lyk(t, "", "team", "step", "member-2", "--content-file", file, "--output", "json"); !strings.Contains(out, `"agent_run_id":"run-1"`) {
		t.Fatalf("json output %q", out)
	}
	lyk(t, "From stdin.", "team", "step", "--self", "--content-stdin")
	want := []map[string]any{
		{"member": "member-1", "content": "List the rivals."},
		{"member": "member-2", "content": "Line one.\nLine two.\n"},
		{"self": true, "content": "From stdin."},
	}
	if len(got) != len(want) {
		t.Fatalf("requests %+v", got)
	}
	for i := range want {
		for k, v := range want[i] {
			if got[i][k] != v {
				t.Fatalf("request %d: %s = %v, want %v (%+v)", i, k, got[i][k], v, got[i])
			}
		}
	}
	cmd := Root(strings.NewReader(""), &strings.Builder{})
	cmd.SetArgs([]string{"team", "step", "member-1", "--content", "a", "--content-stdin"})
	cmd.SetErr(&strings.Builder{})
	if err := cmd.Execute(); err == nil {
		t.Fatal("two content sources accepted")
	}
}
