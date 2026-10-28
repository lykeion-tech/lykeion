package agent

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestCoresearchUsesProviderDefaultAndSourcesOnly(t *testing.T) {
	for _, h := range []string{"claude", "codex"} {
		t.Run(h, func(t *testing.T) {
			home := t.TempDir()
			t.Setenv("HOME", home)
			t.Setenv("CODEX_HOME", filepath.Join(home, ".codex"))
			t.Setenv("CLAUDE_CONFIG_DIR", filepath.Join(home, ".claude"))
			args := filepath.Join(t.TempDir(), "args")
			bin := script(t, `printf '%s\n' "$@" > `+args+`
read -r line || true
`)
			o := ExecOptions{Dir: t.TempDir(), Sandbox: testSandbox, Coresearch: true, SourcesURL: "http://localhost/mcp/sources", MCPToken: "ltk_secret", Model: "must-not-use"}
			var b Backend = Claude{Bin: bin}
			if h == "codex" {
				b = Codex{Bin: bin}
			}
			sess, e := b.Execute(context.Background(), "research", o)
			if e != nil {
				t.Fatal(e)
			}
			for range sess.Messages {
			}
			<-sess.Done
			raw, e := os.ReadFile(args)
			if e != nil {
				t.Fatal(e)
			}
			text := string(raw)
			if strings.Contains(text, "--model") || strings.Contains(text, "\n-m\n") || strings.Contains(text, "must-not-use") || strings.Contains(text, "ltk_secret") {
				t.Fatalf("model/token override: %s", text)
			}
			if h == "claude" {
				raw, e = os.ReadFile(filepath.Join(o.Dir, ".lyk-mcp.json"))
				if e != nil {
					t.Fatal(e)
				}
				text = string(raw)
			}
			if !strings.Contains(text, "lyk_sources") || strings.Contains(text, "lyk_door") || strings.Contains(text, "ltk_secret") {
				t.Fatalf("MCP config: %s", text)
			}
		})
	}
}

func TestCoresearchCodexDefaultUsageIsReportable(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	t.Setenv("CODEX_HOME", filepath.Join(os.Getenv("HOME"), ".codex"))
	bin := script(t, `echo '{"type":"thread.started","thread_id":"default-model"}'
echo '{"type":"turn.completed","usage":{"input_tokens":3,"output_tokens":2}}'
`)
	session, e := (Codex{Bin: bin}).Execute(t.Context(), "research", ExecOptions{Dir: t.TempDir(), Sandbox: testSandbox, Coresearch: true})
	if e != nil {
		t.Fatal(e)
	}
	for range session.Messages {
	}
	r := <-session.Done
	if r.Err != nil || len(r.Usage) != 1 || r.Usage[0].Model != "unknown" || r.Model != "unknown" || r.Usage[0].InputTokens != 3 || r.Usage[0].OutputTokens != 2 {
		t.Fatalf("provider-default usage must retain tokens without inventing a model: %+v", r)
	}
}

func TestAristotleCoresearchHonorsConfiguredModel(t *testing.T) {
	for _, h := range []string{"claude", "codex"} {
		t.Run(h, func(t *testing.T) {
			t.Setenv("HOME", t.TempDir())
			t.Setenv("CODEX_HOME", filepath.Join(t.TempDir(), ".codex"))
			args := filepath.Join(t.TempDir(), "args")
			bin := script(t, `printf '%s\n' "$@" > `+args+`
read -r line || true
`)
			var b Backend = Claude{Bin: bin}
			if h == "codex" {
				b = Codex{Bin: bin}
			}
			o := ExecOptions{Dir: t.TempDir(), Sandbox: testSandbox, Coresearch: true, Model: "aristotle-model"}
			// This marker is set only by server-authorized conversation claims.
			o.Aristotle = true
			sess, e := b.Execute(t.Context(), "research", o)
			if e != nil {
				t.Fatal(e)
			}
			for range sess.Messages {
			}
			<-sess.Done
			raw, e := os.ReadFile(args)
			if e != nil {
				t.Fatal(e)
			}
			if !strings.Contains(string(raw), "\naristotle-model\n") {
				t.Fatalf("configured model absent: %s", raw)
			}
		})
	}
}
