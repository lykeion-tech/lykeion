package agent

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// TestMain points HOME at an empty folder, so no test copies the developer's
// real sign-in files.
func TestMain(m *testing.M) {
	home, err := os.MkdirTemp("", "agent-home")
	if err != nil {
		panic(err)
	}
	os.Setenv("HOME", home)
	os.Setenv("CLAUDE_CONFIG_DIR", filepath.Join(home, ".claude"))
	os.Unsetenv("CODEX_HOME")
	os.Unsetenv("CURSOR_DATA_DIR")
	os.Setenv("XDG_CONFIG_HOME", filepath.Join(home, ".config"))
	os.Setenv("XDG_DATA_HOME", filepath.Join(home, ".local/share"))
	code := m.Run()
	os.RemoveAll(home)
	os.Exit(code)
}

// testCatalog is what a fake Codex lists for `debug models`.
const testCatalog = `{"models":[{"slug":"gpt-5.6-sol","visibility":"list"}]}`

func script(t *testing.T, body string) string {
	p := filepath.Join(t.TempDir(), "bin")
	catalog := "if [ \"$1\" = debug ]; then echo '" + testCatalog + "'; exit 0; fi\n"
	if err := os.WriteFile(p, []byte("#!/bin/sh\n"+catalog+body), 0o755); err != nil {
		t.Fatal(err)
	}
	return p
}

func collect(t *testing.T, s *Session) ([]Message, Result) {
	t.Helper()
	var ms []Message
	for m := range s.Messages {
		ms = append(ms, m)
	}
	return ms, <-s.Done
}

func TestClaudeStreamJSON(t *testing.T) {
	args := filepath.Join(t.TempDir(), "args")
	bin := script(t, `printf '%s\n' "$@" > `+args+`
cat <<'EOF'
{"type":"system","subtype":"init","session_id":"s1"}
{"type":"assistant","message":{"content":[{"type":"thinking","thinking":"hmm"},{"type":"text","text":"Searching."},{"type":"tool_use","name":"mcp__lyk_sources__search_literature","input":{"q":"x"}}]}}
{"type":"user","message":{"content":[{"type":"tool_result","content":"3 hits"}]}}
{"type":"result","subtype":"success","is_error":false,"total_cost_usd":0.42,"session_id":"s1","result":"done","usage":{"input_tokens":100,"output_tokens":20,"cache_read_input_tokens":300,"cache_creation_input_tokens":50},"modelUsage":{"model-b":{"inputTokens":10,"outputTokens":5,"cacheReadInputTokens":0,"cacheCreationInputTokens":0,"costUSD":0.02},"model-a":{"inputTokens":90,"outputTokens":15,"cacheReadInputTokens":300,"cacheCreationInputTokens":50,"costUSD":0.4}}}
EOF`)
	dir := t.TempDir()
	s, err := Claude{Bin: bin}.Execute(context.Background(), "scope it", ExecOptions{Dir: dir, MCPURL: "http://x/mcp/sources", MCPToken: "ltk_abc"})
	if err != nil {
		t.Fatal(err)
	}
	ms, res := collect(t, s)
	types := []string{}
	for _, m := range ms {
		types = append(types, m.Type)
	}
	if strings.Join(types, ",") != "log,thinking,text,tool_use,tool_result" || ms[3].Tool != "mcp__lyk_sources__search_literature" {
		t.Fatalf("%v %+v", types, ms)
	}
	if res.Err != nil || res.SessionID != "s1" || len(res.Usage) != 2 {
		t.Fatalf("%+v", res)
	}
	a, b := res.Usage[0], res.Usage[1]
	if a.Model != "model-a" || a.InputTokens != 90 || a.OutputTokens != 15 || a.CacheReadTokens != 300 || a.CacheWriteTokens != 50 ||
		a.CostUSD == nil || *a.CostUSD != 0.4 || b.Model != "model-b" || b.CostUSD == nil || *b.CostUSD != 0.02 {
		t.Fatalf("usage %+v %+v", a, b)
	}
	args2, _ := os.ReadFile(args)
	for _, want := range []string{"-p", "stream-json", "--verbose", "--strict-mcp-config", "dontAsk"} {
		if !strings.Contains(string(args2), want) {
			t.Errorf("claude args missing %q", want)
		}
	}
	cfg, _ := os.ReadFile(filepath.Join(dir, ".lyk-mcp.json"))
	if !strings.Contains(string(cfg), "Bearer ltk_abc") || !strings.Contains(string(cfg), `"type":"http"`) {
		t.Fatalf("mcp config: %s", cfg)
	}
}

func TestClaudeResultWithoutModelUsage(t *testing.T) {
	bin := script(t, `echo '{"type":"result","is_error":true,"total_cost_usd":0.1,"result":"max turns reached"}'`)
	s, _ := Claude{Bin: bin}.Execute(context.Background(), "p", ExecOptions{Dir: t.TempDir()})
	_, res := collect(t, s)
	if res.Err == nil || !strings.Contains(res.Err.Error(), "max turns") || res.Reason != ReasonAgentError {
		t.Fatalf("%+v", res)
	}
	if len(res.Usage) != 1 || res.Usage[0].CostUSD == nil || *res.Usage[0].CostUSD != 0.1 {
		t.Fatalf("usage %+v", res.Usage)
	}
}

func TestClaudePromptTooLongIsContextExhausted(t *testing.T) {
	bin := script(t, `echo '{"type":"result","subtype":"error_during_execution","is_error":true,"terminal_reason":"prompt_too_long","result":"stopped"}'`)
	s, _ := Claude{Bin: bin}.Execute(context.Background(), "p", ExecOptions{Dir: t.TempDir()})
	if _, res := collect(t, s); res.Reason != ReasonContextExhausted {
		t.Fatalf("%+v", res)
	}
}

func TestExitWithoutResultIsClassified(t *testing.T) {
	for stderr, want := range map[string]string{
		"getaddrinfo ENOTFOUND api.anthropic.com": ReasonProviderNetwork,
		"TypeError: x is not a function":          ReasonAgentError,
	} {
		bin := script(t, "echo '"+stderr+"' >&2\nexit 1")
		s, _ := Claude{Bin: bin}.Execute(context.Background(), "p", ExecOptions{Dir: t.TempDir()})
		if _, res := collect(t, s); res.Err == nil || res.Reason != want {
			t.Fatalf("%q: %+v", stderr, res)
		}
	}
}

func TestCodexInputSizeLimitIsContextExhausted(t *testing.T) {
	bin := script(t, `echo '{"type":"thread.started","thread_id":"local-input-limit"}'
cat >&2 <<'EOF'
Error: turn/start: turn/start failed: Input exceeds the maximum length of 1048576 characters. (code -32602), data: {"input_error_code":"input_too_large","max_chars":1048576,"actual_chars":7200013}
EOF
exit 1`)
	s, err := (Codex{Bin: bin}).Execute(t.Context(), "oversized input", ExecOptions{Dir: t.TempDir()})
	if err != nil {
		t.Fatal(err)
	}
	_, res := collect(t, s)
	if res.Err == nil || res.Reason != ReasonContextExhausted || len(res.Usage) != 0 || res.SessionID != "local-input-limit" {
		t.Fatalf("local input rejection: %+v", res)
	}
}

func TestCodexJSONL(t *testing.T) {
	args := filepath.Join(t.TempDir(), "args")
	bin := script(t, `printf '%s\n' "$@" > `+args+`
cat <<'EOF'
{"type":"thread.started","thread_id":"th1"}
{"type":"item.completed","item":{"type":"reasoning","text":"plan"}}
{"type":"item.completed","item":{"type":"command_execution","command":"lyk ground question"}}
{"type":"item.completed","item":{"type":"mcp_tool_call","tool":"search_literature"}}
{"type":"item.completed","item":{"type":"agent_message","text":"Done."}}
{"type":"turn.completed","usage":{"input_tokens":1000,"cached_input_tokens":400,"output_tokens":50}}
EOF`)
	s, err := Codex{Bin: bin}.Execute(context.Background(), "scope it", ExecOptions{Dir: t.TempDir(), MCPURL: "http://x/mcp/sources"})
	if err != nil {
		t.Fatal(err)
	}
	ms, res := collect(t, s)
	types := []string{}
	for _, m := range ms {
		types = append(types, m.Type)
	}
	if strings.Join(types, ",") != "log,thinking,tool_use,tool_use,text,status,status" || res.SessionID != "th1" || res.Err != nil {
		t.Fatalf("%v %+v", types, res)
	}
	if len(res.Usage) != 1 {
		t.Fatalf("usage %+v", res.Usage)
	}
	u := res.Usage[0]
	if u.Model != "gpt-5.6-sol" || res.Model != "gpt-5.6-sol" || u.InputTokens != 600 || u.CacheReadTokens != 400 || u.OutputTokens != 50 || u.CostUSD != nil {
		t.Fatalf("usage %+v", u)
	}
	a, _ := os.ReadFile(args)
	for _, want := range []string{"exec", "--json", "-m\ngpt-5.6-sol", "--disable\napps", "--disable\nplugins", "--disable\nremote_plugin", "mcp_servers.lyk_sources.url=", "bearer_token_env_var", "-"} {
		if !strings.Contains(string(a), want) {
			t.Errorf("codex args missing %q", want)
		}
	}
}

func TestCodexTurnFailed(t *testing.T) {
	bin := script(t, `echo '{"type":"turn.failed","error":{"message":"rate limited"}}'`)
	s, _ := Codex{Bin: bin}.Execute(context.Background(), "p", ExecOptions{Dir: t.TempDir()})
	_, res := collect(t, s)
	if res.Err == nil || !strings.Contains(res.Err.Error(), "rate limited") || res.Reason != ReasonProviderNetwork {
		t.Fatalf("%+v", res)
	}
}

// TestConfigHomeIsIsolated puts a personal skill and a sign-in file in each
// CLI's real config home. The agent run's config home must hold the sign-in file
// and nothing else, and the copy must be gone once the CLI exits.
func TestConfigHomeIsIsolated(t *testing.T) {
	real := t.TempDir()
	t.Setenv("HOME", real)
	t.Setenv("CLAUDE_CONFIG_DIR", "")
	for _, c := range []struct{ harness, env, home, cred string }{
		{"claude", "CLAUDE_CONFIG_DIR", ".claude", claudeCredFile},
		{"codex", "CODEX_HOME", ".codex", codexCredFile},
	} {
		t.Run(c.harness, func(t *testing.T) {
			skill := filepath.Join(real, c.home, "skills", "personal")
			os.MkdirAll(skill, 0o700)
			os.WriteFile(filepath.Join(skill, "SKILL.md"), []byte("# my own skill"), 0o600)
			os.WriteFile(filepath.Join(real, c.home, c.cred), []byte("secret"), 0o600)
			seen := filepath.Join(t.TempDir(), "seen")
			bin := script(t, `echo "$`+c.env+`" > `+seen+`
ls -A "$`+c.env+`" >> `+seen+`
cat "$`+c.env+`/`+c.cred+`" >> `+seen)
			dir := t.TempDir()
			b, _ := New(c.harness, bin)
			s, err := b.Execute(context.Background(), "p", ExecOptions{Dir: dir})
			if err != nil {
				t.Fatal(err)
			}
			collect(t, s)
			resolved, err := filepath.EvalSymlinks(dir)
			if err != nil {
				t.Fatal(err)
			}
			home := filepath.Join(resolved, ".cli-home")
			got, _ := os.ReadFile(seen)
			if want := home + "\n" + c.cred + "\nsecret"; strings.TrimSpace(string(got)) != want {
				t.Fatalf("the CLI saw\n%s\nwant\n%s", got, want)
			}
			if _, err := os.Stat(filepath.Join(home, c.cred)); !os.IsNotExist(err) {
				t.Fatal("the copied sign-in file outlived the run")
			}
		})
	}
}

func TestSourcesEntryIsWrittenLast(t *testing.T) {
	o := func(dir string) ExecOptions {
		return ExecOptions{Dir: dir, MCPURL: "http://lyk/mcp/sources", MCPToken: "ltk_x",
			MCP: []MCPServer{{Name: "tools", URL: "http://tools"}, {Name: "lyk_sources", URL: "http://evil"}}}
	}
	dir := t.TempDir()
	s, _ := Claude{Bin: script(t, "exit 0")}.Execute(context.Background(), "p", o(dir))
	collect(t, s)
	cfg, _ := os.ReadFile(filepath.Join(dir, ".lyk-mcp.json"))
	var parsed struct {
		Servers map[string]struct{ URL string } `json:"mcpServers"`
	}
	// encoding/json, like JSON.parse, keeps the last of two equal keys.
	if err := json.Unmarshal(cfg, &parsed); err != nil || parsed.Servers["lyk_sources"].URL != "http://lyk/mcp/sources" ||
		parsed.Servers["lyk_tools"].URL != "http://tools" || !strings.HasSuffix(strings.TrimSpace(string(cfg)), `"Bearer ltk_x"},"type":"http","url":"http://lyk/mcp/sources"}}}`) {
		t.Fatalf("claude mcp config: %s", cfg)
	}

	args := filepath.Join(t.TempDir(), "args")
	s, _ = Codex{Bin: script(t, `printf '%s\n' "$@" > `+args)}.Execute(context.Background(), "p", o(t.TempDir()))
	collect(t, s)
	a, _ := os.ReadFile(args)
	evil, real := strings.Index(string(a), `"http://evil"`), strings.Index(string(a), `mcp_servers.lyk_sources.url="http://lyk/mcp/sources"`)
	if evil < 0 || real < evil {
		t.Fatalf("codex args: %s", a)
	}
	bad := ExecOptions{Dir: t.TempDir(), MCP: []MCPServer{{Name: "x.y", URL: "http://x"}}}
	if _, err := (Codex{Bin: script(t, "exit 0")}).Execute(context.Background(), "p", bad); err == nil {
		t.Fatal("a server name that is not a plain key reached codex -c")
	}
}

func TestCancelKillsTheProcessTree(t *testing.T) {
	pidfile := filepath.Join(t.TempDir(), "child")
	bin := script(t, `sleep 300 & echo $! > `+pidfile+`
wait`)
	ctx, cancel := context.WithCancel(context.Background())
	s, err := Claude{Bin: bin}.Execute(ctx, "p", ExecOptions{Dir: t.TempDir()})
	if err != nil {
		t.Fatal(err)
	}
	time.Sleep(300 * time.Millisecond)
	cancel()
	select {
	case res := <-s.Done:
		if res.Reason != ReasonRuntimeOffline {
			t.Fatalf("reason after cancel: %q", res.Reason)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("cancel did not stop the agent")
	}
	pid, _ := os.ReadFile(pidfile)
	time.Sleep(200 * time.Millisecond)
	if exec.Command("kill", "-0", strings.TrimSpace(string(pid))).Run() == nil {
		t.Fatal("the agent's child process survived cancel")
	}
}

func TestClaudeStructuredExhaustionOverridesSuccess(t *testing.T) {
	for _, result := range []string{"", "stopped"} {
		bin := script(t, `echo '{"type":"result","is_error":false,"terminal_reason":" prompt_too_long ","result":"`+result+`","session_id":"full"}'`)
		s, err := (Claude{Bin: bin}).Execute(context.Background(), "p", ExecOptions{Dir: t.TempDir()})
		if err != nil {
			t.Fatal(err)
		}
		_, r := collect(t, s)
		if r.Err == nil || r.Reason != ReasonContextExhausted || r.SessionID != "full" || !strings.Contains(r.Err.Error(), "prompt_too_long") {
			t.Fatalf("%+v", r)
		}
	}
}

func TestPromptStdinWithStartupBurst(t *testing.T) {
	for _, harness := range []string{"claude", "codex"} {
		t.Run(harness, func(t *testing.T) {
			captured, args := filepath.Join(t.TempDir(), "stdin"), filepath.Join(t.TempDir(), "args")
			terminal := `{"type":"result","is_error":false}`
			if harness == "codex" {
				terminal = `{"type":"turn.completed","usage":{}}`
			}
			bin := script(t, `printf '%s\n' "$@" > '`+args+`'
i=0; while [ "$i" -lt 3000 ]; do echo '{"type":"log","message":"startup"}'; i=$((i+1)); done
cat > '`+captured+`'
echo '`+terminal+`'`)
			ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
			defer cancel()
			b, _ := New(harness, bin)
			prompt := strings.Repeat("Large prompt.\n", 200000)
			s, err := b.Execute(ctx, prompt, ExecOptions{Dir: t.TempDir()})
			if err != nil {
				t.Fatal(err)
			}
			_, r := collect(t, s)
			if r.Err != nil {
				t.Fatal(r.Err)
			}
			got, err := os.ReadFile(captured)
			if err != nil || string(got) != prompt {
				t.Fatalf("stdin length %d: %v", len(got), err)
			}
			argv, err := os.ReadFile(args)
			if err != nil || strings.Contains(string(argv), "Large prompt") {
				t.Fatalf("prompt in argv or missing args: %v", err)
			}
		})
	}
}

func TestMissingTerminalAndScannerFailure(t *testing.T) {
	for _, harness := range []string{"claude", "codex"} {
		for _, body := range []string{
			"exit 0", `echo '{"type":"system","session_id":"s"}'`, `printf '{broken json}\n'`,
			`head -c 18000000 /dev/zero | tr '\000' x; sleep 300`,
		} {
			t.Run(harness+body[:5], func(t *testing.T) {
				ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
				defer cancel()
				b, _ := New(harness, script(t, body))
				s, err := b.Execute(ctx, "p", ExecOptions{Dir: t.TempDir()})
				if err != nil {
					t.Fatal(err)
				}
				_, r := collect(t, s)
				if r.Err == nil || r.Reason != ReasonAgentError {
					t.Fatalf("%+v", r)
				}
			})
		}
	}
}

func TestCancelWithFullMessageBuffer(t *testing.T) {
	for _, harness := range []string{"claude", "codex"} {
		t.Run(harness, func(t *testing.T) {
			ready := filepath.Join(t.TempDir(), "ready")
			bin := script(t, `echo ready > '`+ready+`'
while :; do echo '{"type":"log","message":"busy"}'; done`)
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			b, _ := New(harness, bin)
			dir := t.TempDir()
			s, err := b.Execute(ctx, "p", ExecOptions{Dir: dir})
			if err != nil {
				t.Fatal(err)
			}
			waitFile(t, ready, func() { logAgentTestFailure(t, dir, s) })
			time.Sleep(100 * time.Millisecond)
			cancel()
			select {
			case r := <-s.Done:
				if r.Reason != ReasonRuntimeOffline {
					t.Fatalf("%+v", r)
				}
			case <-time.After(3 * time.Second):
				logAgentTestFailure(t, dir, s)
				t.Fatal("cancellation blocked behind message consumer")
			}
			for range s.Messages {
			}
		})
	}
}
func waitFile(t *testing.T, p string, diagnostics ...func()) {
	t.Helper()
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		if _, err := os.Stat(p); err == nil {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	for _, diagnose := range diagnostics {
		diagnose()
	}
	t.Fatalf("file never appeared: %s", p)
}

func TestChildEnvironment(t *testing.T) {
	t.Setenv("LYK_TOKEN", "lyk_owner")
	t.Setenv("LYK_CONFIG", "/personal/token")
	t.Setenv("CLAUDECODE", "1")
	t.Setenv("CLAUDE_CODE_SESSION_ID", "parent")
	t.Setenv("CLAUDECODE_TEST", "parent")
	t.Setenv("CLAUDE_CODE_USE_BEDROCK", "1")
	t.Setenv("ANTHROPIC_API_KEY", "provider-key")
	t.Setenv("CODEX_THREAD_ID", "parent")
	t.Setenv("CODEX_SESSION_ID", "parent")
	t.Setenv("CODEX_APP_TOOLS_PIPE_PATH", "/session/socket")
	for _, harness := range []string{"claude", "codex"} {
		for _, explicit := range []bool{false, true} {
			t.Run(harness+map[bool]string{true: "explicit", false: "inherited"}[explicit], func(t *testing.T) {
				dir := t.TempDir()
				capture := filepath.Join(t.TempDir(), "env")
				b, _ := New(harness, script(t, `env > '`+capture+`'`))
				o := ExecOptions{Dir: dir, MCPToken: "ltk_run"}
				if explicit {
					o.Env = []string{"LYK_TOKEN=ltk_run", "LYK_CONFIG=" + filepath.Join(dir, "lyk.json"), "CLAUDECODE=bad", "HOME=/personal", "CODEX_HOME=/personal", "XDG_CONFIG_HOME=/personal"}
				}
				s, err := b.Execute(context.Background(), "p", o)
				if err != nil {
					t.Fatal(err)
				}
				collect(t, s)
				data, err := os.ReadFile(capture)
				if err != nil {
					t.Fatal(err)
				}
				env := string(data)
				resolved, err := filepath.EvalSymlinks(dir)
				if err != nil {
					t.Fatal(err)
				}
				for _, bad := range []string{"lyk_owner", "/personal", "CLAUDECODE=", "CLAUDECODE_TEST=", "CLAUDE_CODE_SESSION_ID=", "CODEX_THREAD_ID=", "CODEX_SESSION_ID=", "CODEX_APP_TOOLS_PIPE_PATH="} {
					if strings.Contains(env, bad) {
						t.Fatalf("leaked %q", bad)
					}
				}
				if explicit && !strings.Contains(env, "LYK_CONFIG="+filepath.Join(resolved, "lyk.json")) {
					t.Fatal("explicit scratch LYK_CONFIG was lost")
				}
				for _, want := range []string{"CLAUDE_CODE_USE_BEDROCK=1", "ANTHROPIC_API_KEY=provider-key", "LYK_TOKEN=ltk_run", "HOME=" + filepath.Join(resolved, ".home"), "XDG_CONFIG_HOME=" + filepath.Join(resolved, ".home", ".config")} {
					if !strings.Contains(env, want) {
						t.Fatalf("missing %q", want)
					}
				}
			})
		}
	}
}

func TestCredentialsRemovedOnSetupAndStartFailure(t *testing.T) {
	for _, harness := range []string{"claude", "codex"} {
		for _, failure := range []string{"start", "setup"} {
			t.Run(harness+failure, func(t *testing.T) {
				real := t.TempDir()
				cred := "auth.json"
				envVar := "CODEX_HOME"
				if harness == "claude" {
					cred = ".credentials.json"
					envVar = "CLAUDE_CONFIG_DIR"
				}
				t.Setenv(envVar, real)
				if err := os.WriteFile(filepath.Join(real, cred), []byte("secret"), 0600); err != nil {
					t.Fatal(err)
				}
				dir := t.TempDir()
				if failure == "setup" {
					if err := os.Mkdir(filepath.Join(dir, ".lyk-mcp.json"), 0700); err != nil {
						t.Fatal(err)
					}
				}
				b, _ := New(harness, filepath.Join(t.TempDir(), "missing"))
				_, err := b.Execute(context.Background(), "p", ExecOptions{Dir: dir})
				if err == nil {
					t.Fatal("expected setup/start failure")
				}
				if _, err := os.Stat(filepath.Join(dir, ".cli-home", cred)); !os.IsNotExist(err) {
					t.Fatalf("credential outlived failure: %v", err)
				}
			})
		}
	}
}

func TestCodexResumeAndModelOverride(t *testing.T) {
	args := filepath.Join(t.TempDir(), "args")
	b := Codex{Bin: script(t, `printf '%s\n' "$@" > '`+args+`'
echo '{"type":"turn.completed","usage":{"input_tokens":9,"cached_input_tokens":4,"output_tokens":2}}'`)}
	s, err := b.Execute(context.Background(), "p", ExecOptions{Dir: t.TempDir(), Resume: "session", Model: "custom"})
	if err != nil {
		t.Fatal(err)
	}
	_, r := collect(t, s)
	a, _ := os.ReadFile(args)
	if !strings.Contains(string(a), "resume\nsession\n-") || r.Usage[0].Model != "custom" {
		t.Fatalf("args %s result %+v", a, r)
	}
}

func TestClaudeFallbackAndMissingCost(t *testing.T) {
	for _, tc := range []struct {
		frame, model, want string
		cost               bool
	}{
		{`{"type":"result","total_cost_usd":0.4}`, "", "claude-unattributed", true},
		{`{"type":"result","model":"actual","usage":{"input_tokens":20,"cache_read_input_tokens":40}}`, "configured", "actual", false},
		{`{"type":"result","modelUsage":{"actual":{"inputTokens":20,"cacheReadInputTokens":40}}}`, "", "actual", false},
	} {
		b := Claude{Bin: script(t, "echo '"+tc.frame+"'")}
		s, err := b.Execute(context.Background(), "p", ExecOptions{Dir: t.TempDir(), Model: tc.model})
		if err != nil {
			t.Fatal(err)
		}
		_, r := collect(t, s)
		if len(r.Usage) != 1 || r.Usage[0].Model != tc.want || (r.Usage[0].CostUSD != nil) != tc.cost {
			t.Fatalf("%+v", r)
		}
		if !tc.cost && (r.Usage[0].InputTokens != 20 || r.Usage[0].CacheReadTokens != 40) {
			t.Fatalf("cache double-counted: %+v", r.Usage)
		}
	}
}

func TestUnsafeAncestorsAndSymlinkScratchRefused(t *testing.T) {
	for _, artifact := range []string{".codex", ".agents", ".claude", "AGENTS.md", "CLAUDE.md", ".mcp.json"} {
		t.Run(artifact, func(t *testing.T) {
			parent := t.TempDir()
			if err := os.WriteFile(filepath.Join(parent, artifact), []byte("personal"), 0600); err != nil {
				t.Fatal(err)
			}
			dir := filepath.Join(parent, "run")
			if err := os.Mkdir(dir, 0700); err != nil {
				t.Fatal(err)
			}
			alias := filepath.Join(t.TempDir(), "alias")
			if err := os.Symlink(dir, alias); err != nil {
				t.Fatal(err)
			}
			for _, h := range []string{"claude", "codex"} {
				b, _ := New(h, script(t, "exit 0"))
				if _, err := b.Execute(context.Background(), "p", ExecOptions{Dir: alias}); err == nil {
					t.Fatal("unsafe ancestor accepted via symlink")
				}
			}
		})
	}
}

func TestScratchPrivateAndSymlinksRefused(t *testing.T) {
	for _, h := range []string{"claude", "codex"} {
		t.Run(h, func(t *testing.T) {
			dir := t.TempDir()
			if err := os.Chmod(dir, 0755); err != nil {
				t.Fatal(err)
			}
			b, _ := New(h, script(t, "exit 0"))
			s, err := b.Execute(context.Background(), "p", ExecOptions{Dir: dir})
			if err != nil {
				t.Fatal(err)
			}
			collect(t, s)
			for _, p := range []string{dir, filepath.Join(dir, ".cli-home"), filepath.Join(dir, ".home")} {
				st, err := os.Stat(p)
				if err != nil || st.Mode().Perm() != 0700 {
					t.Fatalf("nonprivate %s: %v %v", p, st, err)
				}
			}
			unsafe := t.TempDir()
			outside := t.TempDir()
			if err := os.Symlink(outside, filepath.Join(unsafe, ".cli-home")); err != nil {
				t.Fatal(err)
			}
			if _, err := b.Execute(context.Background(), "p", ExecOptions{Dir: unsafe}); err == nil {
				t.Fatal("symlink config home accepted")
			}
		})
	}
}

func TestOwnedHistoryResumeAndConcurrentRefusal(t *testing.T) {
	for _, h := range []string{"claude", "codex"} {
		t.Run(h, func(t *testing.T) {
			dir := t.TempDir()
			marker := filepath.Join(t.TempDir(), "started")
			b, _ := New(h, script(t, `mkdir -p "$CODEX_HOME/sessions"
echo history > "$CODEX_HOME/sessions/test"
echo started > '`+marker+`'
sleep 300`))
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			s, err := b.Execute(ctx, "p", ExecOptions{Dir: dir})
			if err != nil {
				t.Fatal(err)
			}
			waitFile(t, marker)
			if _, err := b.Execute(context.Background(), "p", ExecOptions{Dir: dir}); err == nil {
				t.Fatal("concurrent scratch execution accepted")
			}
			cancel()
			collect(t, s)
			b, _ = New(h, script(t, `test "$(cat "$CODEX_HOME/sessions/test")" = history || exit 9
echo '{"type":"result","is_error":false}'
echo '{"type":"turn.completed","usage":{}}'`))
			s, err = b.Execute(context.Background(), "p", ExecOptions{Dir: dir, Resume: "session"})
			if h == "claude" {
				if err == nil || !strings.Contains(err.Error(), "baseline") {
					t.Fatalf("cancelled Claude history resumed without baseline: %v", err)
				}
				history, e := os.ReadFile(filepath.Join(dir, ".cli-home", "sessions", "test"))
				if e != nil || string(history) != "history\n" {
					t.Fatalf("owned history was removed: %v", e)
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			_, r := collect(t, s)
			if r.Err != nil {
				t.Fatal(r.Err)
			}
		})
	}
}
func TestPreexistingConfigAndHistoryRefused(t *testing.T) {
	for _, h := range []string{"claude", "codex"} {
		for _, name := range []string{"config.toml", "settings.json", "sessions/personal"} {
			t.Run(h+name, func(t *testing.T) {
				dir := t.TempDir()
				p := filepath.Join(dir, ".cli-home", name)
				if err := os.MkdirAll(filepath.Dir(p), 0700); err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(p, []byte("personal"), 0600); err != nil {
					t.Fatal(err)
				}
				b, _ := New(h, script(t, "exit 0"))
				if _, err := b.Execute(context.Background(), "p", ExecOptions{Dir: dir}); err == nil {
					t.Fatal("unowned config/history accepted")
				}
			})
		}
	}
}
func TestDiagnosticsBoundedAndRedacted(t *testing.T) {
	b := Claude{Bin: script(t, `head -c 10000 /dev/zero | tr '\000' x >&2
printf '\ngetaddrinfo ENOTFOUND api.anthropic.com\nAuthorization: Bearer secret-value\napi_key=hidden-value\nltk_secrettoken\n' >&2
exit 1`)}
	s, err := b.Execute(context.Background(), "p", ExecOptions{Dir: t.TempDir()})
	if err != nil {
		t.Fatal(err)
	}
	_, r := collect(t, s)
	if r.Err == nil || r.Reason != ReasonProviderNetwork {
		t.Fatalf("%+v", r)
	}
	text := r.Err.Error()
	if len(text) > 4400 {
		t.Fatalf("unbounded stderr %d", len(text))
	}
	for _, secret := range []string{"secret-value", "hidden-value", "ltk_secrettoken"} {
		if strings.Contains(text, secret) {
			t.Fatalf("leaked %q", secret)
		}
	}
}

func TestCurrentDirectoryDiscoveryAndForgedOwnershipRefused(t *testing.T) {
	for _, h := range []string{"claude", "codex"} {
		for _, artifact := range []string{".codex", ".claude", ".agents", "AGENTS.md", "CLAUDE.md", ".agent-owned"} {
			t.Run(h+artifact, func(t *testing.T) {
				dir := t.TempDir()
				if artifact == ".agent-owned" {
					outside := filepath.Join(t.TempDir(), "marker")
					if err := os.WriteFile(outside, []byte("lykeion-agent-v1:"+h), 0600); err != nil {
						t.Fatal(err)
					}
					if err := os.Symlink(outside, filepath.Join(dir, artifact)); err != nil {
						t.Fatal(err)
					}
				} else {
					if err := os.WriteFile(filepath.Join(dir, artifact), []byte("personal"), 0600); err != nil {
						t.Fatal(err)
					}
				}
				b, _ := New(h, script(t, "exit 0"))
				if _, err := b.Execute(context.Background(), "p", ExecOptions{Dir: dir}); err == nil {
					t.Fatal("unsafe current directory accepted")
				}
			})
		}
	}
}
func TestOwnerTokenCannotBePassedAsRunToken(t *testing.T) {
	for _, h := range []string{"claude", "codex"} {
		b, _ := New(h, script(t, "exit 0"))
		if _, err := b.Execute(context.Background(), "p", ExecOptions{Dir: t.TempDir(), MCPToken: "lyk_owner"}); err == nil {
			t.Fatal("owner token accepted as MCPToken")
		}
	}
}
func TestCopiedCredentialsArePrivateAndRemovedWhileConsumerBlocked(t *testing.T) {
	for _, h := range []string{"claude", "codex"} {
		t.Run(h, func(t *testing.T) {
			home := t.TempDir()
			cred := "auth.json"
			envVar := "CODEX_HOME"
			if h == "claude" {
				cred = ".credentials.json"
				envVar = "CLAUDE_CONFIG_DIR"
			}
			t.Setenv(envVar, home)
			if err := os.WriteFile(filepath.Join(home, cred), []byte("fake-signin"), 0600); err != nil {
				t.Fatal(err)
			}
			dir := t.TempDir()
			ready := filepath.Join(t.TempDir(), "ready")
			release := filepath.Join(t.TempDir(), "release")
			b, _ := New(h, script(t, `echo ready > '`+ready+`'
while [ ! -f '`+release+`' ]; do sleep 0.01; done
i=0; while [ "$i" -lt 100 ]; do echo '{"type":"log"}'; i=$((i+1)); done`))
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			s, err := b.Execute(ctx, "p", ExecOptions{Dir: dir})
			if err != nil {
				t.Fatal(err)
			}
			waitFile(t, ready)
			copied := filepath.Join(dir, ".cli-home", cred)
			st, err := os.Stat(copied)
			if err != nil || st.Mode().Perm() != 0600 {
				t.Fatalf("credential permissions: %v %v", st, err)
			}
			if err := os.WriteFile(release, nil, 0600); err != nil {
				t.Fatal(err)
			}
			deadline := time.Now().Add(3 * time.Second)
			for time.Now().Before(deadline) {
				if _, err := os.Stat(copied); os.IsNotExist(err) {
					break
				}
				time.Sleep(10 * time.Millisecond)
			}
			if _, err := os.Stat(copied); !os.IsNotExist(err) {
				t.Fatal("credential cleanup blocked behind message consumer")
			}
			collect(t, s)
		})
	}
}

func TestCancellationRetainsEveryParsedBacklogMessage(t *testing.T) {
	dir := t.TempDir()
	ready := filepath.Join(t.TempDir(), "written")
	bin := script(t, `i=0; while [ "$i" -lt 200 ]; do echo '{"type":"log","message":"evidence"}'; i=$((i+1)); done
echo ready > '`+ready+`'
sleep 300`)
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	sess, err := (Claude{Bin: bin}).Execute(ctx, "p", ExecOptions{Dir: dir})
	if err != nil {
		t.Fatal(err)
	}
	waitFile(t, ready, func() { logAgentTestFailure(t, dir, sess) })
	cancel()
	select {
	case <-sess.Done:
	case <-time.After(3 * time.Second):
		logAgentTestFailure(t, dir, sess)
		t.Fatal("blocked behind consumer")
	}
	n := 0
	for range sess.Messages {
		n++
	}
	paths, err := filepath.Glob(filepath.Join(dir, ".undelivered-*.jsonl"))
	if err != nil {
		t.Fatal(err)
	}
	for _, path := range paths {
		retained, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		n += strings.Count(string(retained), "\n")
	}
	if n != 200 {
		t.Fatalf("parsed backlog lost: delivered+retained=%d want 200", n)
	}
}

func TestDurableSinkReceivesCancellationBacklogWithoutChannelDuplication(t *testing.T) {
	ready := filepath.Join(t.TempDir(), "ready")
	bin := script(t, `i=0; while [ "$i" -lt 200 ]; do echo '{"type":"log","message":"evidence"}'; i=$((i+1)); done
echo ready > '`+ready+`'
sleep 300`)
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	entered := make(chan struct{})
	release := make(chan struct{})
	n := 0
	sess, err := (Claude{Bin: bin}).Execute(ctx, "p", ExecOptions{Dir: t.TempDir(), OnMessage: func(m Message) error {
		n++
		if n == 1 {
			close(entered)
			<-release
		}
		return nil
	}})
	if err != nil {
		t.Fatal(err)
	}
	<-entered
	waitFile(t, ready)
	cancel()
	close(release)
	select {
	case r := <-sess.Done:
		if r.Reason != ReasonRuntimeOffline {
			t.Fatalf("%+v", r)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("sink drain blocked")
	}
	if n != 200 {
		t.Fatalf("sink lost backlog: %d", n)
	}
	if _, ok := <-sess.Messages; ok {
		t.Fatal("sink also emitted channel message")
	}
}
func TestDurableSinkFailureStopsChildAndSurfaces(t *testing.T) {
	want := errors.New("durable storage unavailable")
	sess, err := (Claude{Bin: script(t, `echo '{"type":"log","message":"evidence"}'; sleep 300`)}).Execute(t.Context(), "p", ExecOptions{Dir: t.TempDir(), OnMessage: func(Message) error { return want }})
	if err != nil {
		t.Fatal(err)
	}
	select {
	case r := <-sess.Done:
		if !errors.Is(r.Err, want) {
			t.Fatalf("sink error hidden: %+v", r)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("sink failure did not stop child")
	}
}

func TestCodexAccountingNoticeAndCacheWrite(t *testing.T) {
	for _, tc := range []struct {
		name               string
		input, read, write int64
		upper              bool
	}{
		{"base", 272000, 100000, 50000, false},
		{"upper", 272001, 100000, 50000, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			line := fmt.Sprintf(`{"type":"turn.completed","usage":{"input_tokens":%d,"cached_input_tokens":%d,"cache_write_input_tokens":%d,"output_tokens":7}}`, tc.input, tc.read, tc.write)
			var messages []Message
			res := &Result{}
			terminal, err := parseCodex("gpt-5.6-sol")([]byte(line), func(m Message) { messages = append(messages, m) }, res)
			if err != nil || !terminal || len(res.Usage) != 1 {
				t.Fatalf("result %+v, %v", res, err)
			}
			u := res.Usage[0]
			if u.InputTokens != tc.input-tc.read-tc.write || u.CacheReadTokens != tc.read || u.CacheWriteTokens != tc.write || u.OutputTokens != 7 || u.CostUSD != nil {
				t.Fatalf("usage %+v", u)
			}
			var notice struct {
				Type, Model, Policy    string
				ConservativeUpperBound bool `json:"conservative_upper_bound"`
				Message                string
			}
			if len(messages) != 2 || json.Unmarshal(messages[1].Content, &notice) != nil || notice.Type != "accounting" || notice.Model != "gpt-5.6-sol" || notice.Policy != "api_list_price_estimate" || notice.ConservativeUpperBound != tc.upper || !strings.Contains(notice.Message, "not ChatGPT billed") {
				t.Fatalf("missing truthful accounting notice: %+v", messages)
			}
		})
	}
}

func TestCodexRejectsInvalidUsageCounts(t *testing.T) {
	for _, usage := range []string{
		`{"input_tokens":-1}`, `{"input_tokens":10,"output_tokens":-1}`,
		`{"input_tokens":10,"cached_input_tokens":-1}`, `{"input_tokens":10,"cache_write_input_tokens":-1}`,
		`{"input_tokens":10,"cached_input_tokens":11}`, `{"input_tokens":10,"cached_input_tokens":6,"cache_write_input_tokens":5}`,
	} {
		res := &Result{}
		_, err := parseCodex("gpt-5.6-sol")([]byte(`{"type":"turn.completed","usage":`+usage+`}`), func(Message) {}, res)
		if err == nil || len(res.Usage) != 0 {
			t.Fatalf("accepted invalid usage %s: %+v, %v", usage, res, err)
		}
	}
}

func TestCodexFeatureIsolationFlags(t *testing.T) {
	path := filepath.Join(t.TempDir(), "args")
	bin := script(t, `printf '%s\n' "$@" > `+path+`
echo '{"type":"turn.completed","usage":{}}'`)
	s, err := (Codex{Bin: bin}).Execute(t.Context(), "ready", ExecOptions{Dir: t.TempDir(), MCPURL: "http://localhost/sources"})
	if err != nil {
		t.Fatal(err)
	}
	collect(t, s)
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"--disable\napps", "--disable\nplugins", "--disable\nremote_plugin", "mcp_servers.lyk_sources.url=\"http://localhost/sources\""} {
		if !strings.Contains(string(b), want) {
			t.Errorf("missing %q in %s", want, b)
		}
	}
}

func TestCodexAccountingNoticeUsesDurableSink(t *testing.T) {
	var messages []Message
	bin := script(t, `echo '{"type":"turn.completed","usage":{"input_tokens":1,"output_tokens":1}}'`)
	s, err := (Codex{Bin: bin}).Execute(t.Context(), "ready", ExecOptions{Dir: t.TempDir(), OnMessage: func(m Message) error { messages = append(messages, m); return nil }})
	if err != nil {
		t.Fatal(err)
	}
	streamed, res := collect(t, s)
	if res.Err != nil || len(streamed) != 0 || len(messages) != 2 || !strings.Contains(string(messages[1].Content), "api_list_price_estimate") {
		t.Fatalf("sink=%+v streamed=%+v result=%+v", messages, streamed, res)
	}
}

func TestCodexAggregateUsageHasConservativeNotice(t *testing.T) {
	var messages []Message
	res := &Result{}
	parse := parseCodex("gpt-5.6-sol")
	for i := 0; i < 2; i++ {
		_, err := parse([]byte(`{"type":"turn.completed","usage":{"input_tokens":200000,"output_tokens":1}}`), func(m Message) { messages = append(messages, m) }, res)
		if err != nil {
			t.Fatal(err)
		}
	}
	if res.Usage[0].InputTokens != 400000 || len(messages) != 4 || !strings.Contains(string(messages[3].Content), `"conservative_upper_bound":true`) {
		t.Fatalf("result=%+v messages=%+v", res, messages)
	}
}

// A read-only run swaps the permission mode or sandbox in both modes, and
// never keeps the writable one beside it.
func TestReadOnlyClaudeAndCodex(t *testing.T) {
	for _, c := range []struct {
		harness     string
		inherit     bool
		want, never string
	}{
		{"claude", false, "--permission-mode\nplan\n", "dontAsk"},
		{"claude", true, "--permission-mode\nplan\n", "--permission-mode\ndefault\n"},
		{"codex", false, "-s\nread-only\n", "-s\nworkspace-write\n"},
		{"codex", true, "-s\nread-only\n", "-s\nworkspace-write\n"},
		{"codex", false, "-s\nread-only\n", "danger-full-access"},
		{"codex", true, "-s\nread-only\n", "network_access"},
	} {
		t.Run(fmt.Sprintf("%s inherit=%v", c.harness, c.inherit), func(t *testing.T) {
			args := filepath.Join(t.TempDir(), "args")
			b, _ := New(c.harness, script(t, `printf '%s\n' "$@" > `+args))
			s, err := b.Execute(context.Background(), "p", ExecOptions{Dir: t.TempDir(), ReadOnly: true, Inherit: c.inherit})
			if err != nil {
				t.Fatal(err)
			}
			collect(t, s)
			a, _ := os.ReadFile(args)
			if !strings.Contains(string(a), c.want) || strings.Contains(string(a), c.never) {
				t.Fatalf("read-only %s ran with\n%s", c.harness, a)
			}
		})
	}
}
