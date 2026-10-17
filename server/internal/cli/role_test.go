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

func TestRoleCommandsNeedAnAgentRunToken(t *testing.T) {
	t.Setenv("LYK_TOKEN", "")
	cmd := Root(strings.NewReader("q"), &strings.Builder{})
	cmd.SetArgs([]string{"records"})
	cmd.SetErr(&strings.Builder{})
	if err := cmd.Execute(); err == nil || !strings.Contains(err.Error(), "LYK_TOKEN") {
		t.Fatalf("%v", err)
	}
}

func TestResultCommandsNeedAnAgentRunToken(t *testing.T) {
	t.Setenv("LYK_TOKEN", "")
	cmd := Root(strings.NewReader(""), &strings.Builder{})
	cmd.SetArgs([]string{"result", "failed", "--reason", "x"})
	cmd.SetErr(&strings.Builder{})
	if err := cmd.Execute(); err == nil || !strings.Contains(err.Error(), "LYK_TOKEN") {
		t.Fatalf("%v", err)
	}
}

func TestRoleCommandsUseOnlyRunCredentials(t *testing.T) {
	t.Setenv("LYK_CONFIG", filepath.Join(t.TempDir(), "config.json"))
	if err := SaveConfig(os.Getenv("LYK_CONFIG"), Config{Server: "http://127.0.0.1:1", Token: "lyk_personal", Workspace: "personal-workspace"}); err != nil {
		t.Fatal(err)
	}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer ltk_run" || r.Header.Get("X-Workspace-ID") != "" {
			t.Error("role used personal credentials")
		}
		var body struct {
			Kind string
			Body map[string]string
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Fatal(err)
		}
		if r.URL.Path != "/api/role/records" || body.Kind != "failed" || body.Body["reason"] != "stuck" {
			t.Errorf("unexpected request: %s %+v", r.URL.Path, body)
		}
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`{"key":"record-key"}`))
	}))
	defer srv.Close()
	t.Setenv("LYK_TOKEN", "ltk_run")
	t.Setenv("LYK_SERVER", srv.URL)
	if got := lyk(t, "", "result", "failed", "--reason", "stuck"); got != "record-key\n" {
		t.Fatal(got)
	}
}

func TestRoleBlobDoesNotFollowRedirects(t *testing.T) {
	t.Setenv("LYK_CONFIG", filepath.Join(t.TempDir(), "config.json"))
	var followed bool
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/elsewhere" {
			followed = true
			w.Write([]byte("other"))
			return
		}
		http.Redirect(w, r, "/elsewhere", http.StatusFound)
	}))
	defer srv.Close()
	t.Setenv("LYK_TOKEN", "ltk_run")
	t.Setenv("LYK_SERVER", srv.URL)
	cmd := Root(strings.NewReader(""), &strings.Builder{})
	cmd.SetArgs([]string{"blob", "abc"})
	if err := cmd.Execute(); err == nil || !strings.Contains(err.Error(), "302") {
		t.Fatalf("redirect accepted: %v", err)
	}
	if followed {
		t.Fatal("followed redirect")
	}
}
