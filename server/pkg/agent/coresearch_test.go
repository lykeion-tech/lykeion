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
				if strings.Contains(text, "--disable-slash-commands") || !strings.Contains(text, "--setting-sources\nproject\n") || !strings.Contains(text, "Skill") || !strings.Contains(text, "--strict-mcp-config") {
					t.Fatalf("managed project skills unavailable: %s", text)
				}
			} else if !strings.Contains(text, "--ignore-user-config") || !strings.Contains(text, "--ignore-rules") {
				t.Fatalf("Codex config isolation missing: %s", text)
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

func TestCoresearchCodexProjectResetIsConfined(t *testing.T) {
	dir, account, outside := t.TempDir(), t.TempDir(), t.TempDir()
	if err := os.WriteFile(filepath.Join(outside, "auth.json"), []byte("account sentinel"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outside, filepath.Join(dir, ".codex")); err != nil {
		t.Fatal(err)
	}
	if err := prepareCoresearchCodexProject(dir, account); err != nil {
		t.Fatal(err)
	}
	b, err := os.ReadFile(filepath.Join(outside, "auth.json"))
	if err != nil || string(b) != "account sentinel" {
		t.Fatalf("escaped reset: %q %v", b, err)
	}
	st, err := os.Lstat(filepath.Join(dir, ".codex"))
	if err != nil || !st.IsDir() || st.Mode()&os.ModeSymlink != 0 {
		t.Fatalf("unsafe project configuration: %v %v", st, err)
	}
	// Never reset a caller's account store as project configuration.
	if err := os.WriteFile(filepath.Join(dir, ".codex", "auth.json"), []byte("preserve"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := prepareCoresearchCodexProject(dir, filepath.Join(dir, ".codex")); err == nil {
		t.Fatal("accepted an account store inside managed project configuration")
	}
	if b, err := os.ReadFile(filepath.Join(dir, ".codex", "auth.json")); err != nil || string(b) != "preserve" {
		t.Fatalf("account store changed: %q %v", b, err)
	}
}

func TestCoresearchCodexProjectResetRejectsAccountCaseAlias(t *testing.T) {
	dir := t.TempDir()
	project := filepath.Join(dir, ".codex")
	if err := os.Mkdir(project, 0700); err != nil {
		t.Fatal(err)
	}
	alias := filepath.Join(dir, ".CODEX")
	a, err := os.Stat(alias)
	if os.IsNotExist(err) {
		t.Skip("filesystem is case-sensitive")
	}
	if err != nil {
		t.Fatal(err)
	}
	p, err := os.Stat(project)
	if err != nil {
		t.Fatal(err)
	}
	if !os.SameFile(a, p) {
		t.Skip("case alias is a different directory")
	}
	if err := os.WriteFile(filepath.Join(project, "auth.json"), []byte("preserve account"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := prepareCoresearchCodexProject(dir, alias); err == nil {
		t.Fatal("accepted a case alias of the account store as managed configuration")
	}
	data, err := os.ReadFile(filepath.Join(project, "auth.json"))
	if err != nil || string(data) != "preserve account" {
		t.Fatalf("account was modified: %q %v", data, err)
	}
}
