package cli

import (
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
)

func TestAddonSetSendsOnlyTheGivenFields(t *testing.T) {
	var method, path string
	var body map[string]json.RawMessage
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		method, path = r.Method, r.URL.Path
		body = nil
		json.NewDecoder(r.Body).Decode(&body)
		w.Write([]byte(`{"addon_hash":"abc123"}`))
	}))
	defer srv.Close()
	dir := t.TempDir()
	cfg := filepath.Join(dir, "config.json")
	t.Setenv("LYK_CONFIG", cfg)
	if err := SaveConfig(cfg, Config{Server: srv.URL, Token: "lyk_t", Workspace: "ws1"}); err != nil {
		t.Fatal(err)
	}
	servers := filepath.Join(dir, "servers.json")
	os.WriteFile(servers, []byte(`[{"name":"lab-index","url":"https://tools.lab.org/mcp"}]`), 0o600)

	out := lyk(t, "Also search the lab index.\n", "addon", "set", "grounding", "--skill", "-", "--mcp", servers)
	if method != "PATCH" || path != "/api/workspaces/ws1/addons/grounding" || string(body["skill_md"]) != `"Also search the lab index.\n"` ||
		!strings.Contains(string(body["mcp"]), "lab-index") || !strings.Contains(out, "abc123") {
		t.Fatalf("%s %s %v %q", method, path, body, out)
	}

	lyk(t, "", "addon", "set", "hypotheses", "--mcp", servers)
	if _, sent := body["skill_md"]; sent || path != "/api/workspaces/ws1/addons/hypotheses" {
		t.Fatalf("an unset field was sent: %v", body)
	}

	lyk(t, "", "addon", "set", "grounding", "--clear")
	if string(body["skill_md"]) != `""` || string(body["mcp"]) != "[]" {
		t.Fatalf("clear: %v", body)
	}
}

func TestAddonSetRejectsInvalidUTF8BeforeRequest(t *testing.T) {
	for _, input := range []string{"file", "stdin"} {
		t.Run(input, func(t *testing.T) {
			requests := 0
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { requests++; w.Write([]byte("{}")) }))
			defer srv.Close()
			cfg := filepath.Join(t.TempDir(), "config.json")
			t.Setenv("LYK_CONFIG", cfg)
			if err := SaveConfig(cfg, Config{Server: srv.URL, Token: "lyk_t", Workspace: "ws1"}); err != nil {
				t.Fatal(err)
			}
			invalid := string([]byte{0xff})
			source := "-"
			if input == "file" {
				source = filepath.Join(t.TempDir(), "skill.md")
				if err := os.WriteFile(source, []byte(invalid), 0600); err != nil {
					t.Fatal(err)
				}
			}
			cmd := Root(strings.NewReader(invalid), io.Discard)
			cmd.SetArgs([]string{"addon", "set", "grounding", "--skill", source})
			if err := cmd.Execute(); err == nil || !strings.Contains(err.Error(), "UTF-8") {
				t.Fatalf("error = %v", err)
			}
			if requests != 0 {
				t.Fatalf("sent %d requests", requests)
			}
		})
	}
}

func TestAddonShowAndClearOverrides(t *testing.T) {
	var body map[string]json.RawMessage
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == "GET" {
			if r.URL.Path != "/api/workspaces/ws1/addons" {
				t.Errorf("path = %s", r.URL.Path)
			}
			w.Write([]byte(`[{"board_column":"grounding","addon_hash":"hash1","invalid":"broken rule","mcp":[{"name":"lab","url":"https://lab.org"}],"skill_md":"lab text"},{"board_column":"hypotheses","addon_hash":"hash2","mcp":[]}]`))
			return
		}
		body = nil
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Error(err)
		}
		w.Write([]byte("{}"))
	}))
	defer srv.Close()
	cfg := filepath.Join(t.TempDir(), "config.json")
	t.Setenv("LYK_CONFIG", cfg)
	if err := SaveConfig(cfg, Config{Server: srv.URL, Token: "lyk_t", Workspace: "ws1"}); err != nil {
		t.Fatal(err)
	}
	out := lyk(t, "", "addon", "show", "grounding")
	for _, want := range []string{"== grounding hash1", "INVALID: broken rule", "mcp:", "https://lab.org", "lab text"} {
		if !strings.Contains(out, want) {
			t.Fatalf("missing %q: %q", want, out)
		}
	}
	if strings.Contains(out, "hypotheses") {
		t.Fatalf("unfiltered output: %q", out)
	}
	if out := lyk(t, "", "addon", "show"); !strings.Contains(out, "hypotheses hash2") {
		t.Fatalf("missing stored row: %q", out)
	}
	out = lyk(t, "replacement", "addon", "set", "grounding", "--clear", "--skill", "-")
	if string(body["skill_md"]) != `"replacement"` || string(body["mcp"]) != "[]" || !strings.Contains(out, "(none)") {
		t.Fatalf("body=%v out=%q", body, out)
	}
	out = lyk(t, "", "addon", "set", "grounding", "--skill", "-")
	if string(body["skill_md"]) != `""` || len(body) != 1 {
		t.Fatalf("explicit empty skill: %v", body)
	}
}

func TestAddonSetErrors(t *testing.T) {
	requests := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests++
		w.WriteHeader(http.StatusForbidden)
		w.Write([]byte(`{"error":"owners only"}`))
	}))
	defer srv.Close()
	cfg := filepath.Join(t.TempDir(), "config.json")
	t.Setenv("LYK_CONFIG", cfg)
	if err := SaveConfig(cfg, Config{Server: srv.URL, Token: "lyk_t", Workspace: "ws1"}); err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		args        []string
		input, want string
	}{
		{[]string{"addon", "set", "grounding"}, "", "nothing to set"},
		{[]string{"addon", "set", "grounding", "--mcp", "-"}, "invalid", "not JSON"},
		{[]string{"addon", "set", "grounding", "--clear"}, "", "owners only"},
	} {
		cmd := Root(strings.NewReader(tc.input), io.Discard)
		cmd.SetArgs(tc.args)
		if err := cmd.Execute(); err == nil || !strings.Contains(err.Error(), tc.want) {
			t.Fatalf("%v: %v", tc.args, err)
		}
	}
	if requests != 1 {
		t.Fatalf("requests=%d", requests)
	}
}

type addonFailingReader struct{ err error }

func (r addonFailingReader) Read([]byte) (int, error) { return 0, r.err }

func TestAddonSetInputReadErrorsBeforeRequest(t *testing.T) {
	for _, flag := range []string{"--skill", "--mcp"} {
		for _, input := range []string{"missing file", "failing stdin"} {
			t.Run(flag+"/"+input, func(t *testing.T) {
				var requests atomic.Int32
				srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { requests.Add(1); w.Write([]byte("{}")) }))
				defer srv.Close()
				dir := t.TempDir()
				cfg := filepath.Join(dir, "config.json")
				t.Setenv("LYK_CONFIG", cfg)
				if err := SaveConfig(cfg, Config{Server: srv.URL, Token: "lyk_t", Workspace: "ws1"}); err != nil {
					t.Fatal(err)
				}
				source := filepath.Join(dir, "missing-input")
				var reader io.Reader = strings.NewReader("")
				want := error(os.ErrNotExist)
				if input == "failing stdin" {
					source = "-"
					want = errors.New("stdin read failed")
					reader = addonFailingReader{err: want}
				}
				cmd := Root(reader, io.Discard)
				cmd.SetArgs([]string{"addon", "set", "grounding", flag, source})
				if err := cmd.Execute(); !errors.Is(err, want) {
					t.Fatalf("error = %v, want %v", err, want)
				}
				if n := requests.Load(); n != 0 {
					t.Fatalf("sent %d requests", n)
				}
			})
		}
	}
}
