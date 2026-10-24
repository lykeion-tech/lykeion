package agent

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func namingCodexHome(t *testing.T) {
	t.Helper()
	h := t.TempDir()
	t.Setenv("CODEX_HOME", h)
	if e := os.WriteFile(filepath.Join(h, "models_cache.json"), []byte(`{"models":[{"slug":"m","model_messages":{"instructions_template":"actual metadata instructions"},"comp_hash":3000,"context_window":12345,"apply_patch_tool_type":"freeform","shell_type":"unified_exec","experimental_supported_tools":["clock"],"tool_mode":"code_mode_only"}]}`), 0600); e != nil {
		t.Fatal(e)
	}
}

const namingFeatures = `if [ "$1" = --version ]; then echo "codex-cli 0.146.1"; exit; fi
if [ "$CODEX_EXEC_SERVER_URL" != none ] || [ -n "$CODEX_EXEC_SERVER_NOISE_REGISTRY_URL" ]; then exit 29; fi
if [ "$1" = debug ]; then cat "$CODEX_HOME/models_cache.json"; exit; fi
if [ "$1" = features ]; then printf 'shell_tool stable true\napps stable true\nhooks stable true\ncode_mode stable true\n'; exit; fi
`

func TestCodexNamingBatches(t *testing.T) {
	t.Setenv("CODEX_EXEC_SERVER_URL", "http://must-not-connect")
	t.Setenv("CODEX_EXEC_SERVER_NOISE_REGISTRY_URL", "http://must-not-connect")
	namingCodexHome(t)
	args := filepath.Join(t.TempDir(), "args")
	bin := namingCodexScript(t, namingFeatures+`printf '%s\n' "$@" >> `+args+`
cat >/dev/null
echo '{"type":"item.completed","item":{"type":"agent_message","text":"1. Save a research record"}}'
echo '{"type":"turn.completed","usage":{"input_tokens":10,"cached_input_tokens":3,"output_tokens":2}}'`)
	dir := t.TempDir()
	n, e := OpenNamingSession(t.Context(), "codex", bin, dir, "m", testSandbox)
	if e != nil {
		t.Fatal(e)
	}
	for range 2 {
		s, e := n.Ask(t.Context(), "name")
		if e != nil || s != "1. Save a research record" {
			t.Fatalf("%q %v", s, e)
		}
	}
	r := n.Close()
	if len(r.Usage) != 2 || r.Usage[0].InputTokens != 7 || r.Usage[1].InputTokens != 7 {
		t.Fatalf("usage %+v", r)
	}
	b, _ := os.ReadFile(args)
	for _, s := range []string{"--ignore-user-config", "--ignore-rules", "--ephemeral", "--strict-config", "features.shell_tool=false", "features.apps=false", "features.hooks=false", "features.code_mode=false", "project_doc_max_bytes=0", "skills.include_instructions=false"} {
		if !strings.Contains(string(b), s) {
			t.Errorf("missing %s: %s", s, b)
		}
	}
	if strings.Contains(string(b), "resume") {
		t.Fatal("resume")
	}
	var c struct{ Models []map[string]any }
	b, _ = os.ReadFile(filepath.Join(dir, ".cli-home", "naming-models.json"))
	if json.Unmarshal(b, &c) != nil || len(c.Models) != 1 {
		t.Fatalf("catalog %s", b)
	}
	m := c.Models[0]
	if m["apply_patch_tool_type"] != nil || m["shell_type"] != "disabled" || m["tool_mode"] != "direct" || m["multi_agent_version"] != "disabled" || m["supports_parallel_tool_calls"] != false || m["comp_hash"] != "3000" || m["base_instructions"] != "actual metadata instructions" || m["context_window"] != float64(12345) || len(m["experimental_supported_tools"].([]any)) != 0 {
		t.Fatalf("catalog %+v", m)
	}
	lock, e := ReserveScratch(dir)
	if e != nil {
		t.Fatal(e)
	}
	lock.Release()
}
func TestCodexNamingRejectsCapabilities(t *testing.T) {
	for _, kind := range []string{"command_execution", "file_change", "mcp_tool_call", "web_search", "unknown_capability"} {
		t.Run(kind, func(t *testing.T) {
			namingCodexHome(t)
			bin := namingCodexScript(t, namingFeatures+`cat >/dev/null
echo '{"type":"item.started","item":{"type":"`+kind+`"}}'
echo '{"type":"turn.completed","usage":{"input_tokens":8,"output_tokens":1}}'`)
			n, e := OpenNamingSession(t.Context(), "codex", bin, t.TempDir(), "m", testSandbox)
			if e != nil {
				t.Fatal(e)
			}
			if _, e = n.Ask(t.Context(), "name"); e == nil {
				t.Fatal("capability accepted")
			}
			r := n.Close()
			if len(r.Usage) != 1 || r.Usage[0].InputTokens != 8 {
				t.Fatalf("lost usage %+v", r)
			}
		})
	}
}
func TestCodexNamingCancellation(t *testing.T) {
	namingCodexHome(t)
	bin := namingCodexScript(t, namingFeatures+`cat >/dev/null
echo '{"type":"turn.completed","usage":{"input_tokens":8,"output_tokens":1}}'
sleep 10`)
	dir := t.TempDir()
	n, e := OpenNamingSession(t.Context(), "codex", bin, dir, "m", testSandbox)
	if e != nil {
		t.Fatal(e)
	}
	ctx, cancel := context.WithTimeout(t.Context(), 100*time.Millisecond)
	defer cancel()
	if _, e = n.Ask(ctx, "name"); e == nil {
		t.Fatal("canceled request passed")
	}
	r := n.Close()
	if len(r.Usage) != 1 {
		t.Fatalf("lost usage %+v", r)
	}
	lock, e := ReserveScratch(dir)
	if e != nil {
		t.Fatal(e)
	}
	lock.Release()
	if _, e = OpenNamingSession(t.Context(), "codex", bin, t.TempDir(), "absent", testSandbox); e == nil {
		t.Fatal("invented metadata")
	}
	if _, e = OpenNamingSession(t.Context(), "codex", bin, t.TempDir(), "m", nil); e != ErrNoSandbox {
		t.Fatalf("sandbox %v", e)
	}
}

func TestCodexNamingFailureRetainsActualUsage(t *testing.T) {
	for _, prefix := range []string{`echo 'invalid-json'`, `echo '{"type":"error","message":"provider failed"}'`, `echo '{"type":"turn.failed","error":{"message":"provider failed"}}'`} {
		t.Run(prefix, func(t *testing.T) {
			namingCodexHome(t)
			bin := namingCodexScript(t, namingFeatures+"cat >/dev/null\n"+prefix+` 
echo '{"type":"turn.completed","usage":{"input_tokens":11,"output_tokens":2}}'`)
			n, e := OpenNamingSession(t.Context(), "codex", bin, t.TempDir(), "m", testSandbox)
			if e != nil {
				t.Fatal(e)
			}
			if _, e = n.Ask(t.Context(), "name"); e == nil {
				t.Fatal("failure accepted")
			}
			r := n.Close()
			if len(r.Usage) != 1 || r.Usage[0].InputTokens != 11 {
				t.Fatalf("lost usage %+v", r)
			}
		})
	}
}
func TestCodexNamingInitializationFailsClosedAndReleasesScratch(t *testing.T) {
	for _, mode := range []string{"empty-features", "custom-provider", "missing-catalog", "unverified-version"} {
		t.Run(mode, func(t *testing.T) {
			namingCodexHome(t)
			body := namingFeatures
			switch mode {
			case "unverified-version":
				body = `echo "codex-cli 99.0.0"`
			case "empty-features":
				body = `if [ "$1" = --version ]; then echo "codex-cli 0.146.1"; fi`
			case "custom-provider":
				if e := os.WriteFile(filepath.Join(os.Getenv("CODEX_HOME"), "config.toml"), []byte("model_provider='custom'"), 0600); e != nil {
					t.Fatal(e)
				}
			case "missing-catalog":
				if e := os.Remove(filepath.Join(os.Getenv("CODEX_HOME"), "models_cache.json")); e != nil {
					t.Fatal(e)
				}
			}
			dir := t.TempDir()
			if _, e := OpenNamingSession(t.Context(), "codex", namingCodexScript(t, body), dir, "m", testSandbox); e == nil {
				t.Fatal("unsafe initialization succeeded")
			}
			lock, e := ReserveScratch(dir)
			if e != nil {
				t.Fatal(e)
			}
			lock.Release()
		})
	}
}

func namingCodexScript(t *testing.T, body string) string {
	t.Helper()
	p := filepath.Join(t.TempDir(), "bin")
	if e := os.WriteFile(p, []byte("#!/bin/sh\n"+body), 0755); e != nil {
		t.Fatal(e)
	}
	return p
}

func TestCodexNamingDefaultUsesResearchModelSelection(t *testing.T) {
	namingCodexHome(t)
	if e := os.WriteFile(filepath.Join(os.Getenv("CODEX_HOME"), "config.toml"), []byte("model='unsupported-personal-default'"), 0600); e != nil {
		t.Fatal(e)
	}
	n, e := OpenNamingSession(t.Context(), "codex", namingCodexScript(t, namingFeatures), t.TempDir(), "", testSandbox)
	if e != nil {
		t.Fatal(e)
	}
	if r := n.Close(); r.Model != "m" {
		t.Fatalf("did not choose actual CLI catalog model: %+v", r)
	}
}

func TestCodexNamingRequestedModelMetadataFromCLI(t *testing.T) {
	namingCodexHome(t)
	b, e := os.ReadFile(filepath.Join(os.Getenv("CODEX_HOME"), "models_cache.json"))
	if e != nil {
		t.Fatal(e)
	}
	if e = os.WriteFile(filepath.Join(os.Getenv("CODEX_HOME"), "models_cache.json"), []byte(`{"models":[]}`), 0600); e != nil {
		t.Fatal(e)
	}
	bin := namingCodexScript(t, `if [ "$1" = debug ]; then echo '`+string(b)+`'; exit; fi
`+namingFeatures)
	n, e := OpenNamingSession(t.Context(), "codex", bin, t.TempDir(), "m", testSandbox)
	if e != nil {
		t.Fatal(e)
	}
	if r := n.Close(); r.Model != "m" {
		t.Fatalf("changed requested model: %+v", r)
	}
}

func TestCodexNamingAllowsNonfatalErrorNotification(t *testing.T) {
	namingCodexHome(t)
	bin := namingCodexScript(t, namingFeatures+`cat >/dev/null
echo '{"type":"item.completed","item":{"type":"error","message":"nonfatal startup notification"}}'
echo '{"type":"item.completed","item":{"type":"agent_message","text":"1. Save a research record"}}'
echo '{"type":"turn.completed","usage":{"input_tokens":10,"output_tokens":2}}'`)
	n, e := OpenNamingSession(t.Context(), "codex", bin, t.TempDir(), "m", testSandbox)
	if e != nil {
		t.Fatal(e)
	}
	text, e := n.Ask(t.Context(), "name")
	if e != nil || text != "1. Save a research record" {
		t.Fatalf("%q %v", text, e)
	}
	if r := n.Close(); len(r.Usage) != 1 {
		t.Fatalf("usage %+v", r)
	}
}
