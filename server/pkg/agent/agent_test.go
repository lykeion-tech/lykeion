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
	os.Unsetenv("GEMINI_CLI_HOME")
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
{"type":"assistant","message":{"content":[{"type":"thinking","thinking":"hmm"},{"type":"text","text":"Searching."},{"type":"tool_use","id":"toolu_1","name":"mcp__lyk_sources__search_literature","input":{"q":"x"}}]}}
{"type":"user","message":{"content":[{"type":"tool_result","tool_use_id":"toolu_1","content":"3 hits"}]}}
{"type":"result","subtype":"success","is_error":false,"total_cost_usd":0.42,"session_id":"s1","result":"done","usage":{"input_tokens":100,"output_tokens":20,"cache_read_input_tokens":300,"cache_creation_input_tokens":50},"modelUsage":{"model-b":{"inputTokens":10,"outputTokens":5,"cacheReadInputTokens":0,"cacheCreationInputTokens":0,"costUSD":0.02},"model-a":{"inputTokens":90,"outputTokens":15,"cacheReadInputTokens":300,"cacheCreationInputTokens":50,"costUSD":0.4}}}
EOF`)
	dir := t.TempDir()
	s, err := Claude{Bin: bin}.Execute(context.Background(), "scope it", ExecOptions{Sandbox: testSandbox, Dir: dir, DoorURL: "http://127.0.0.1:1/mcp", DoorToken: "door-abc", MCPToken: "ltk_abc"})
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
	// The call keeps its id and input, and its result names the same id.
	var call struct {
		ID    string `json:"id"`
		Input struct{ Q string }
	}
	var result struct {
		ToolUseID string `json:"tool_use_id"`
	}
	if json.Unmarshal(ms[3].Content, &call) != nil || json.Unmarshal(ms[4].Content, &result) != nil ||
		call.ID != "toolu_1" || call.Input.Q != "x" || result.ToolUseID != call.ID {
		t.Fatalf("tool_use %s, tool_result %s", ms[3].Content, ms[4].Content)
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
	for _, want := range []string{"-p", "stream-json", "--verbose", "--input-format", "default"} {
		if !strings.Contains(string(args2), want) {
			t.Errorf("claude args missing %q", want)
		}
	}
	cfg, _ := os.ReadFile(filepath.Join(dir, ".lyk-mcp.json"))
	if !strings.Contains(string(cfg), "Bearer door-abc") || strings.Contains(string(cfg), "ltk_abc") || !strings.Contains(string(cfg), `"type":"http"`) {
		t.Fatalf("mcp config: %s", cfg)
	}
}

func TestClaudeResultWithoutModelUsage(t *testing.T) {
	bin := script(t, `echo '{"type":"result","is_error":true,"total_cost_usd":0.1,"result":"max turns reached"}'`)
	s, _ := Claude{Bin: bin}.Execute(context.Background(), "p", ExecOptions{Sandbox: testSandbox, Dir: t.TempDir()})
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
	s, _ := Claude{Bin: bin}.Execute(context.Background(), "p", ExecOptions{Sandbox: testSandbox, Dir: t.TempDir()})
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
		s, _ := Claude{Bin: bin}.Execute(context.Background(), "p", ExecOptions{Sandbox: testSandbox, Dir: t.TempDir()})
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
	s, err := (Codex{Bin: bin}).Execute(t.Context(), "oversized input", ExecOptions{Sandbox: testSandbox, Dir: t.TempDir()})
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
	s, err := Codex{Bin: bin}.Execute(context.Background(), "scope it", ExecOptions{Sandbox: testSandbox, Dir: t.TempDir(), DoorURL: "http://127.0.0.1:1/mcp"})
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
	for _, want := range []string{"exec", "--json", "-m\ngpt-5.6-sol", "mcp_servers.lyk_door.url=", "bearer_token_env_var", "-"} {
		if !strings.Contains(string(a), want) {
			t.Errorf("codex args missing %q", want)
		}
	}
}

func TestCodexTurnFailed(t *testing.T) {
	bin := script(t, `echo '{"type":"turn.failed","error":{"message":"rate limited"}}'`)
	s, _ := Codex{Bin: bin}.Execute(context.Background(), "p", ExecOptions{Sandbox: testSandbox, Dir: t.TempDir()})
	_, res := collect(t, s)
	if res.Err == nil || !strings.Contains(res.Err.Error(), "rate limited") || res.Reason != ReasonProviderNetwork {
		t.Fatalf("%+v", res)
	}
}

// TestConfigHomeIsIsolated puts a personal skill and a sign-in file in each
// CLI's real config home. The agent run's config home must hold the sign-in file
// and nothing else, and the copy must be gone once the CLI exits.
func TestAccountSigninIsNeverCopied(t *testing.T) {
	for _, h := range []string{"claude", "codex"} {
		t.Run(h, func(t *testing.T) {
			home := t.TempDir()
			env, cred := "CODEX_HOME", "auth.json"
			terminal := `{"type":"turn.completed","usage":{}}`
			if h == "claude" {
				env, cred = "CLAUDE_CONFIG_DIR", claudeCredFile
				terminal = `{"type":"result","is_error":false}`
			}
			t.Setenv(env, home)
			if err := os.WriteFile(filepath.Join(home, cred), []byte("account-signin"), 0600); err != nil {
				t.Fatal(err)
			}
			dir := t.TempDir()
			seen := filepath.Join(t.TempDir(), "seen")
			b, _ := New(h, script(t, `printf '%s' "$`+env+`" > '`+seen+`'; echo '`+terminal+`'`))
			session, err := b.Execute(context.Background(), "p", ExecOptions{Sandbox: testSandbox, Dir: dir})
			if err != nil {
				t.Fatal(err)
			}
			if _, r := collect(t, session); r.Err != nil {
				t.Fatal(r.Err)
			}
			got, _ := os.ReadFile(seen)
			if string(got) != home {
				t.Fatalf("CLI home %q", got)
			}
			if _, err := os.Lstat(filepath.Join(dir, ".cli-home", cred)); !os.IsNotExist(err) {
				t.Fatalf("copied signin: %v", err)
			}
			if got, err := os.ReadFile(filepath.Join(home, cred)); err != nil || string(got) != "account-signin" {
				t.Fatalf("account signin changed: %q %v", got, err)
			}
		})
	}
}

func TestTheCLIGetsOnlyTheDoor(t *testing.T) {
	o := ExecOptions{Sandbox: testSandbox, Dir: t.TempDir(), MCPToken: "ltk_x", DoorURL: "http://127.0.0.1:4100/mcp", DoorToken: "door-secret"}
	var parsed struct {
		Servers map[string]struct {
			Type, URL string
			Headers   map[string]string
		} `json:"mcpServers"`
	}
	if err := json.Unmarshal(claudeMCP(o), &parsed); err != nil || len(parsed.Servers) != 1 {
		t.Fatalf("claude mcp config: %s %v", claudeMCP(o), err)
	}
	door := parsed.Servers["lyk_door"]
	if door.Type != "http" || door.URL != o.DoorURL || door.Headers["Authorization"] != "Bearer door-secret" {
		t.Fatalf("claude mcp config: %s", claudeMCP(o))
	}

	args := filepath.Join(t.TempDir(), "args")
	s, _ := Codex{Bin: script(t, `printf '%s\n' "$@" > `+args)}.Execute(context.Background(), "p", ExecOptions{Sandbox: testSandbox, Dir: t.TempDir(), Model: "gpt-5", DoorURL: o.DoorURL, DoorToken: o.DoorToken})
	collect(t, s)
	a, _ := os.ReadFile(args)
	if !strings.Contains(string(a), `mcp_servers.lyk_door.url="http://127.0.0.1:4100/mcp"`) || !strings.Contains(string(a), `mcp_servers.lyk_door.bearer_token_env_var="LYK_DOOR_TOKEN"`) ||
		strings.Count(string(a), "mcp_servers.") != 3 || strings.Contains(string(a), "door-secret") {
		t.Fatalf("codex args: %s", a)
	}
}

func TestCancelKillsTheProcessTree(t *testing.T) {
	pidfile := filepath.Join(t.TempDir(), "child")
	bin := script(t, `sleep 300 & echo $! > `+pidfile+`
wait`)
	ctx, cancel := context.WithCancel(context.Background())
	s, err := Claude{Bin: bin}.Execute(ctx, "p", ExecOptions{Sandbox: testSandbox, Dir: t.TempDir()})
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
		s, err := (Claude{Bin: bin}).Execute(context.Background(), "p", ExecOptions{Sandbox: testSandbox, Dir: t.TempDir()})
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
if [ "$1" = -p ]; then IFS= read -r frame; printf '%s' "$frame" > '`+captured+`'; else cat > '`+captured+`'; fi
echo '`+terminal+`'`)
			ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
			defer cancel()
			b, _ := New(harness, bin)
			prompt := strings.Repeat("Large prompt.\n", 200000)
			s, err := b.Execute(ctx, prompt, ExecOptions{Sandbox: testSandbox, Dir: t.TempDir()})
			if err != nil {
				t.Fatal(err)
			}
			_, r := collect(t, s)
			if r.Err != nil {
				t.Fatal(r.Err)
			}
			got, err := os.ReadFile(captured)
			if harness == "claude" {
				var frame struct{ Message struct{ Content string } }
				if e := json.Unmarshal(got, &frame); e != nil {
					t.Fatal(e)
				}
				got = []byte(frame.Message.Content)
			}
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
				s, err := b.Execute(ctx, "p", ExecOptions{Sandbox: testSandbox, Dir: t.TempDir()})
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
			s, err := b.Execute(ctx, "p", ExecOptions{Sandbox: testSandbox, Dir: dir})
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
				o := ExecOptions{Sandbox: testSandbox, Dir: dir, MCPToken: "ltk_run"}
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
				for _, want := range []string{"CLAUDE_CODE_USE_BEDROCK=1", "ANTHROPIC_API_KEY=provider-key", "LYK_TOKEN=ltk_run", "HOME=" + os.Getenv("HOME"), "XDG_CONFIG_HOME=" + os.Getenv("XDG_CONFIG_HOME")} {
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
				_, err := b.Execute(context.Background(), "p", ExecOptions{Sandbox: testSandbox, Dir: dir})
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
	s, err := b.Execute(context.Background(), "p", ExecOptions{Sandbox: testSandbox, Dir: t.TempDir(), Resume: "session", Model: "custom"})
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
		s, err := b.Execute(context.Background(), "p", ExecOptions{Sandbox: testSandbox, Dir: t.TempDir(), Model: tc.model})
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

func TestAncestorConfigAndScratchAliasUseSandbox(t *testing.T) {
	for _, artifact := range []string{".codex", ".agents", ".claude", "AGENTS.md", "CLAUDE.md", ".mcp.json"} {
		t.Run(artifact, func(t *testing.T) {
			parent := t.TempDir()
			path := filepath.Join(parent, artifact)
			if err := os.WriteFile(path, []byte("personal"), 0600); err != nil {
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
				called := false
				blocked := errors.New("test sandbox refuses launch")
				b, _ := New(h, script(t, "exit 0"))
				_, err := b.Execute(context.Background(), "p", ExecOptions{Dir: alias, Model: "gpt-5", Sandbox: func(ctx context.Context, paths CLIPaths, bin string, args ...string) (*exec.Cmd, error) {
					called = true
					return nil, blocked
				}})
				if !called || !errors.Is(err, blocked) {
					t.Fatalf("launch bypassed sandbox: %v", err)
				}
				os.Remove(filepath.Join(dir, ".agent-owned"))
			}
			if got, err := os.ReadFile(path); err != nil || string(got) != "personal" {
				t.Fatalf("ancestor changed %q %v", got, err)
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
			s, err := b.Execute(context.Background(), "p", ExecOptions{Sandbox: testSandbox, Dir: dir})
			if err != nil {
				t.Fatal(err)
			}
			collect(t, s)
			for _, p := range []string{dir, filepath.Join(dir, ".cli-home")} {
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
			if _, err := b.Execute(context.Background(), "p", ExecOptions{Sandbox: testSandbox, Dir: unsafe}); err == nil {
				t.Fatal("symlink config home accepted")
			}
		})
	}
}

func TestOwnedHistoryResumeAndConcurrentRefusal(t *testing.T) {
	t.Setenv("CODEX_HOME", t.TempDir())
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
			s, err := b.Execute(ctx, "p", ExecOptions{Sandbox: testSandbox, Dir: dir})
			if err != nil {
				t.Fatal(err)
			}
			waitFile(t, marker)
			if _, err := b.Execute(context.Background(), "p", ExecOptions{Sandbox: testSandbox, Dir: dir}); err == nil {
				t.Fatal("concurrent scratch execution accepted")
			}
			cancel()
			collect(t, s)
			b, _ = New(h, script(t, `test "$(cat "$CODEX_HOME/sessions/test")" = history || exit 9
echo '{"type":"result","is_error":false}'
echo '{"type":"turn.completed","usage":{}}'`))
			if h == "claude" {
				seedClaudeTranscript(t, "session")
			}
			s, err = b.Execute(context.Background(), "p", ExecOptions{Sandbox: testSandbox, Dir: dir, Resume: "session"})
			if h == "claude" {
				if err == nil || !strings.Contains(err.Error(), "baseline") {
					t.Fatalf("cancelled Claude history resumed without baseline: %v", err)
				}
				history, e := os.ReadFile(filepath.Join(os.Getenv("CODEX_HOME"), "sessions", "test"))
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
func TestPreexistingAccountingStateCannotBypassSandbox(t *testing.T) {
	for _, h := range []string{"claude", "codex"} {
		for _, name := range []string{"config.toml", "settings.json", "sessions/personal"} {
			t.Run(h+name, func(t *testing.T) {
				dir := t.TempDir()
				path := filepath.Join(dir, ".cli-home", name)
				os.MkdirAll(filepath.Dir(path), 0700)
				os.WriteFile(path, []byte("existing"), 0600)
				blocked := errors.New("test sandbox refuses launch")
				called := false
				b, _ := New(h, script(t, "exit 0"))
				_, err := b.Execute(context.Background(), "p", ExecOptions{Dir: dir, Model: "gpt-5", Sandbox: func(ctx context.Context, p CLIPaths, bin string, args ...string) (*exec.Cmd, error) {
					called = true
					return nil, blocked
				}})
				if !called || !errors.Is(err, blocked) {
					t.Fatalf("launch bypassed sandbox: %v", err)
				}
				if got, err := os.ReadFile(path); err != nil || string(got) != "existing" {
					t.Fatalf("state changed: %q %v", got, err)
				}
			})
		}
	}
}
func TestDiagnosticsBoundedAndRedacted(t *testing.T) {
	b := Claude{Bin: script(t, `head -c 10000 /dev/zero | tr '\000' x >&2
printf '\ngetaddrinfo ENOTFOUND api.anthropic.com\nAuthorization: Bearer secret-value\napi_key=hidden-value\nltk_secrettoken\n' >&2
exit 1`)}
	s, err := b.Execute(context.Background(), "p", ExecOptions{Sandbox: testSandbox, Dir: t.TempDir()})
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
		for _, artifact := range []string{".agent-owned"} {
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
				if _, err := b.Execute(context.Background(), "p", ExecOptions{Sandbox: testSandbox, Dir: dir}); err == nil {
					t.Fatal("unsafe current directory accepted")
				}
			})
		}
	}
}
func TestOwnerTokenCannotBePassedAsRunToken(t *testing.T) {
	for _, h := range []string{"claude", "codex"} {
		b, _ := New(h, script(t, "exit 0"))
		if _, err := b.Execute(context.Background(), "p", ExecOptions{Sandbox: testSandbox, Dir: t.TempDir(), MCPToken: "lyk_owner"}); err == nil {
			t.Fatal("owner token accepted as MCPToken")
		}
	}
}
func TestSigninNotCopiedWhileConsumerBlocked(t *testing.T) {
	for _, h := range []string{"claude", "codex"} {
		t.Run(h, func(t *testing.T) {
			home := t.TempDir()
			env, cred := "CODEX_HOME", "auth.json"
			if h == "claude" {
				env, cred = "CLAUDE_CONFIG_DIR", claudeCredFile
			}
			t.Setenv(env, home)
			original := filepath.Join(home, cred)
			os.WriteFile(original, []byte("account-signin"), 0600)
			dir := t.TempDir()
			ready := filepath.Join(t.TempDir(), "ready")
			release := filepath.Join(t.TempDir(), "release")
			bin := script(t, `echo ready > '`+ready+`'; while [ ! -f '`+release+`' ]; do sleep 0.01; done; i=0; while [ "$i" -lt 100 ]; do echo '{"type":"log"}'; i=$((i+1)); done`)
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			b, _ := New(h, bin)
			session, err := b.Execute(ctx, "p", ExecOptions{Dir: dir, Sandbox: testSandbox})
			if err != nil {
				t.Fatal(err)
			}
			waitFile(t, ready)
			if _, err := os.Lstat(filepath.Join(dir, ".cli-home", cred)); !os.IsNotExist(err) {
				t.Fatalf("copied signin %v", err)
			}
			os.WriteFile(release, nil, 0600)
			cancel()
			collect(t, session)
			if got, err := os.ReadFile(original); err != nil || string(got) != "account-signin" {
				t.Fatalf("signin changed %q %v", got, err)
			}
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
	sess, err := (Claude{Bin: bin}).Execute(ctx, "p", ExecOptions{Sandbox: testSandbox, Dir: dir})
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
	sess, err := (Claude{Bin: bin}).Execute(ctx, "p", ExecOptions{Sandbox: testSandbox, Dir: t.TempDir(), OnMessage: func(m Message) error {
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
	sess, err := (Claude{Bin: script(t, `echo '{"type":"log","message":"evidence"}'; sleep 300`)}).Execute(t.Context(), "p", ExecOptions{Sandbox: testSandbox, Dir: t.TempDir(), OnMessage: func(Message) error { return want }})
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

func TestCodexKeepsAccountConfig(t *testing.T) {
	args := filepath.Join(t.TempDir(), "args")
	bin := script(t, `printf '%s\n' "$@" > '`+args+`'; echo '{"type":"turn.completed","usage":{}}'`)
	session, err := (Codex{Bin: bin}).Execute(context.Background(), "p", ExecOptions{Dir: t.TempDir(), Sandbox: testSandbox})
	if err != nil {
		t.Fatal(err)
	}
	collect(t, session)
	got, _ := os.ReadFile(args)
	for _, flag := range []string{"--ignore-user-config", "--ignore-rules", "--disable"} {
		if strings.Contains(string(got), flag) {
			t.Fatalf("discarded personal config: %s", got)
		}
	}
}

func TestCodexAccountingNoticeUsesDurableSink(t *testing.T) {
	var messages []Message
	bin := script(t, `echo '{"type":"turn.completed","usage":{"input_tokens":1,"output_tokens":1}}'`)
	s, err := (Codex{Bin: bin}).Execute(t.Context(), "ready", ExecOptions{Sandbox: testSandbox, Dir: t.TempDir(), OnMessage: func(m Message) error { messages = append(messages, m); return nil }})
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
		{"claude", false, "--permission-mode\ndontAsk\n", "--permission-mode\nplan\n"},
		{"claude", true, "--permission-mode\ndontAsk\n", "--permission-mode\ndefault\n"},
		{"codex", false, "-s\ndanger-full-access\n", "-s\nworkspace-write\n"},
		{"codex", true, "-s\ndanger-full-access\n", "-s\nworkspace-write\n"},
		{"codex", false, "-s\ndanger-full-access\n", "-s\nread-only\n"},
		{"codex", true, "-s\ndanger-full-access\n", "network_access"},
	} {
		t.Run(fmt.Sprintf("%s inherit=%v", c.harness, c.inherit), func(t *testing.T) {
			args := filepath.Join(t.TempDir(), "args")
			b, _ := New(c.harness, script(t, `printf '%s\n' "$@" > `+args))
			s, err := b.Execute(context.Background(), "p", ExecOptions{Sandbox: testSandbox, Dir: t.TempDir(), ReadOnly: true})
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

func TestEveryHarnessHasABackend(t *testing.T) {
	for h := range Harnesses {
		if b, err := New(h, ""); err != nil || b == nil {
			t.Fatalf("%s: %v", h, err)
		}
	}
}

// ReadOnlyHarnesses must say exactly which adapters accept a read-only run.
func TestReadOnlyHarnessesMatchTheAdapters(t *testing.T) {
	for h := range ReadOnlyHarnesses {
		if _, ok := Harnesses[h]; !ok {
			t.Fatalf("unknown read-only harness %s", h)
		}
	}
	for h := range Harnesses {
		b, _ := New(h, script(t, "exit 0"))
		s, err := b.Execute(context.Background(), "p", ExecOptions{Sandbox: testSandbox, Dir: t.TempDir(), ReadOnly: true})
		if s != nil {
			collect(t, s)
		}
		if err != nil {
			t.Fatalf("%s: unexpected execution error: %v", h, err)
		}
		if !ReadOnlyHarnesses[h] {
			t.Fatalf("%s: ReadOnlyHarnesses says %v, the adapter refused: %v", h, ReadOnlyHarnesses[h], err)
		}
	}
}

// In both modes a role is pre-approved for Lykeion's own tools, the door
// among them, and none of them is denied.
func TestClaudeAllowsTheDoor(t *testing.T) {
	for _, inherit := range []bool{false, true} {
		args := filepath.Join(t.TempDir(), "args")
		s, err := Claude{Bin: script(t, `printf '%s\n' "$@" > `+args)}.Execute(context.Background(), "p",
			ExecOptions{Sandbox: testSandbox, Dir: t.TempDir(), DoorURL: "http://127.0.0.1:1/mcp", DoorToken: "d", MCPToken: "ltk_x"})
		if err != nil {
			t.Fatal(err)
		}
		collect(t, s)
		a, _ := os.ReadFile(args)
		_, denied, _ := strings.Cut(string(a), "--disallowedTools")
		if !strings.Contains(string(a), "\n"+claudeTools+"\n") || strings.Contains(denied, "lyk_door") {
			t.Fatalf("inherit=%v, tools: %s", inherit, a)
		}
	}
}

// Writable runs may edit inside their own folder, matched literally even when
// the folder's name holds glob characters, but not its .claude settings, its
// git repository or the supervisor's markers; read-only runs get no edit
// rule, and a folder name
// the CLI would split into several rules gets none either.
func TestClaudeEditsOnlyItsRunFolder(t *testing.T) {
	// The adapter resolves the run folder, so /var reads as /private/var.
	root, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	for _, c := range []struct {
		name              string
		inherit, readOnly bool
		want              string
	}{
		{"run dir [1]*", false, false, "Edit(/" + root + `/run dir \[1\]\*/**)`},
		{"run dir [1]*", true, false, "Edit(/" + root + `/run dir \[1\]\*/**)`},
		{"run dir", false, true, ""},
		{"run dir", true, true, ""},
		{"a,Write,b", false, false, ""},
		{"a) Write (b", true, false, ""},
	} {
		dir := filepath.Join(root, c.name)
		if err := os.MkdirAll(dir, 0o700); err != nil {
			t.Fatal(err)
		}
		args := filepath.Join(t.TempDir(), "args")
		s, err := Claude{Bin: script(t, `printf '%s\n' "$@" > `+args)}.Execute(context.Background(), "p",
			ExecOptions{Sandbox: testSandbox, Dir: dir, ReadOnly: c.readOnly})
		if err != nil {
			t.Fatal(err)
		}
		collect(t, s)
		a, _ := os.ReadFile(args)
		allowed, _, _ := strings.Cut(string(a), "--disallowedTools")
		got := strings.Contains(allowed, "Edit(")
		if c.want == "" && got || c.want != "" && !strings.Contains(string(a), "\n"+claudeTools+"\n"+c.want+"\n") {
			t.Fatalf("%q inherit=%v readOnly=%v, want %q, args:\n%s", c.name, c.inherit, c.readOnly, c.want, a)
		}
		if c.want == "" {
			continue
		}
		base := strings.TrimSuffix(c.want, "/**)")
		// The CLI's own homes hold the settings and hooks it loads, and git
		// runs a repository's hooks; editing them would grant anything. The
		// markers are the supervisor's alone.
		if deny := "\n--disallowedTools\n" + base + "/.claude/**)\n" + base + "/.cli-home/**)\n" + base + "/.home/**)\n" + base + "/.git/**)\n" +
			base + "/.git)\n" + base + "/.agent-exit)\n" + base + "/.agent-process)\n"; !strings.Contains(string(a), deny) {
			t.Fatalf("%q inherit=%v, want %q, args:\n%s", c.name, c.inherit, deny, a)
		}
	}
}

// Every run may read, without asking, only inside its run folder and the
// folders the daemon names (the study's add-on folder, the bases), each
// matched literally, and never the CLI's own homes in the run folder; no bare
// Read, Glob or Grep is pre-approved, since a bare search tool reads past
// every path rule. A folder whose name the CLI would split gets no rule.
// Read-only runs get the same reads in dontAsk mode and, of the door, only
// connector calls: no code runs.
func TestClaudeReadsOnlyItsFolders(t *testing.T) {
	root, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	dir := filepath.Join(root, "run dir [1]*")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	run := "/" + root + `/run dir \[1\]\*`
	reads := []string{filepath.Join(root, "study {a}"), filepath.Join(root, "a,Read,b"), filepath.Join(root, "bases", "python")}
	allow := "Read(" + run + "/**)\nRead(/" + root + `/study \{a\}/**)` + "\nRead(/" + root + "/bases/python/**)\n--disallowedTools\n"
	deny := "Read(" + run + "/.cli-home/**)\nRead(" + run + "/.home/**)\n--mcp-config\n"
	for _, c := range []struct{ inherit, readOnly bool }{{false, false}, {true, false}, {false, true}, {true, true}} {
		args := filepath.Join(t.TempDir(), "args")
		s, err := Claude{Bin: script(t, `printf '%s\n' "$@" > `+args)}.Execute(context.Background(), "p",
			ExecOptions{Sandbox: testSandbox, Dir: dir, ReadDirs: reads, ReadOnly: c.readOnly})
		if err != nil {
			t.Fatal(err)
		}
		collect(t, s)
		a, _ := os.ReadFile(args)
		want := "\n--allowedTools\n" + claudeTools + "\nEdit(" + run + "/**)\n" + allow
		deny := deny
		if c.readOnly {
			// Nothing else is pre-approved, and no account rule may edit or run
			// commands either.
			want = "\n--permission-mode\ndontAsk\n--allowedTools\nSkill,mcp__lyk_door__call_tool\n" + allow
			deny = strings.TrimSuffix(deny, "--mcp-config\n") + "Edit(//**)\nBash\n--mcp-config\n"
		}
		if !strings.Contains(string(a), want) || !strings.Contains(string(a), deny) || strings.Contains(string(a), "a,Read,b") {
			t.Fatalf("inherit=%v readOnly=%v, want %q and %q, args:\n%s", c.inherit, c.readOnly, want, deny, a)
		}
		if allowed, _, _ := strings.Cut(string(a), "--disallowedTools"); c.readOnly && strings.Contains(allowed, "Edit(") {
			t.Fatalf("inherit=%v read-only run may edit:\n%s", c.inherit, a)
		}
		for _, line := range strings.Split(string(a), "\n") {
			for _, tool := range strings.Split(line, ",") {
				if tool == "Read" || tool == "Glob" || tool == "Grep" {
					t.Fatalf("inherit=%v readOnly=%v pre-approves bare %s:\n%s", c.inherit, c.readOnly, tool, a)
				}
			}
		}
	}
}

// Claude waits on a door call a little past the door's own script limit, so a
// long script ends by the door's rule and not by Claude's request timeout.
func TestClaudeWaitsForTheDoorsScriptLimit(t *testing.T) {
	for _, inherit := range []bool{false, true} {
		dir := t.TempDir()
		s, err := Claude{Bin: script(t, "exit 0")}.Execute(context.Background(), "p",
			ExecOptions{Sandbox: testSandbox, Dir: dir, DoorURL: "http://127.0.0.1:1/mcp", DoorToken: "d"})
		if err != nil {
			t.Fatal(err)
		}
		collect(t, s)
		raw, _ := os.ReadFile(filepath.Join(dir, ".lyk-mcp.json"))
		var cfg struct {
			MCPServers map[string]struct {
				Timeout int64 `json:"timeout"`
			} `json:"mcpServers"`
		}
		if err := json.Unmarshal(raw, &cfg); err != nil {
			t.Fatal(err)
		}
		if got := cfg.MCPServers["lyk_door"].Timeout; got != 7_500_000 {
			t.Fatalf("inherit=%v, door timeout %d ms: %s", inherit, got, raw)
		}
	}
}

// Codex's per-server tool timeout defaults to a minute, so the door's own
// limit only governs a long script when Codex is told to wait past it.
func TestCodexWaitsForTheDoorsScriptLimit(t *testing.T) {
	for _, inherit := range []bool{false, true} {
		args := filepath.Join(t.TempDir(), "args")
		s, err := Codex{Bin: script(t, `printf '%s\n' "$@" > `+args)}.Execute(context.Background(), "p",
			ExecOptions{Sandbox: testSandbox, Dir: t.TempDir(), Model: "gpt-5", DoorURL: "http://127.0.0.1:1/mcp", DoorToken: "d"})
		if err != nil {
			t.Fatal(err)
		}
		collect(t, s)
		a, _ := os.ReadFile(args)
		if !strings.Contains(string(a), "-c\nmcp_servers.lyk_door.tool_timeout_sec=7500\n") {
			t.Fatalf("inherit=%v, codex args:\n%s", inherit, a)
		}
	}
}

func TestRemovedHarnessExplainsSupportedAlternatives(t *testing.T) {
	const want = "Cursor and Antigravity are no longer supported; choose Claude Code or Codex"
	for _, harness := range []string{"cursor", "antigravity"} {
		t.Run(harness, func(t *testing.T) {
			backend, err := New(harness, "unused")
			if backend != nil || !errors.Is(err, ErrHarnessRemoved) || err.Error() != want {
				t.Fatalf("removed harness: backend=%v error=%v; want %q", backend, err, want)
			}
		})
	}
}

// Codex's built-in web search answers from OpenAI's side, past the sources
// server, so every Codex run turns it off, fresh or resumed.
func TestCodexRunsWithoutWebSearch(t *testing.T) {
	for _, resume := range []string{"", "thread-1"} {
		args := filepath.Join(t.TempDir(), "args")
		b, _ := New("codex", script(t, `printf '%s\n' "$@" > `+args))
		s, err := b.Execute(context.Background(), "p", ExecOptions{Sandbox: testSandbox, Dir: t.TempDir(), Resume: resume})
		if err != nil {
			t.Fatal(err)
		}
		collect(t, s)
		a, _ := os.ReadFile(args)
		if !strings.Contains(string(a), "-c\nweb_search=\"disabled\"\n") {
			t.Fatalf("resume=%q: codex ran with\n%s", resume, a)
		}
	}
}

func TestClaudeRunsWithoutThePersonsHooks(t *testing.T) {
	args := filepath.Join(t.TempDir(), "args")
	b, _ := New("claude", script(t, `printf '%s\n' "$@" > `+args))
	s, err := b.Execute(context.Background(), "p", ExecOptions{Sandbox: testSandbox, Dir: t.TempDir()})
	if err != nil {
		t.Fatal(err)
	}
	collect(t, s)
	a, _ := os.ReadFile(args)
	if !strings.Contains(string(a), "--settings\n{\"disableAllHooks\":true}\n") {
		t.Fatalf("claude ran with\n%s", a)
	}
}
