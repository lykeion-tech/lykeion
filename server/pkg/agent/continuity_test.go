package agent

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"syscall"
	"testing"
	"time"
)

func TestInheritRunsAsTheAccount(t *testing.T) {
	real := t.TempDir()
	t.Setenv("HOME", real)
	t.Setenv("CLAUDE_CONFIG_DIR", "")
	os.Unsetenv("CLAUDE_CONFIG_DIR")
	for _, c := range []struct{ harness, env, home, cred, terminal string }{
		{"claude", "CLAUDE_CONFIG_DIR", ".claude", claudeCredFile, `{"type":"result","is_error":false}`},
		{"codex", "CODEX_HOME", ".codex", codexCredFile, `{"type":"turn.completed","usage":{}}`},
	} {
		t.Run(c.harness, func(t *testing.T) {
			os.MkdirAll(filepath.Join(real, c.home, "skills", "personal"), 0o700)
			os.WriteFile(filepath.Join(real, c.home, c.cred), []byte("secret"), 0o600)
			seen, args := filepath.Join(t.TempDir(), "seen"), filepath.Join(t.TempDir(), "args")
			bin := script(t, `printf '%s\n' "$@" > `+args+`
echo "HOME=$HOME CFG=$`+c.env+`" > `+seen+`
ls -A .cli-home >> `+seen+`
echo '`+c.terminal+`'`)
			b, _ := New(c.harness, bin)
			s, err := b.Execute(context.Background(), "p", ExecOptions{Sandbox: testSandbox, Dir: t.TempDir()})
			if err != nil {
				t.Fatal(err)
			}
			if _, r := collect(t, s); r.Err != nil {
				t.Fatal(r.Err)
			}
			got, _ := os.ReadFile(seen)
			// The account's own HOME applies; no config home is forced, and no
			// sign-in is copied into scratch.
			if want := "HOME=" + real + " CFG="; strings.TrimSpace(string(got)) != want {
				t.Fatalf("the CLI saw %q, want %q", got, want)
			}
			argv, _ := os.ReadFile(args)
			for _, isolation := range []string{"--setting-sources", "dontAsk", "--ignore-user-config"} {
				if strings.Contains(string(argv), isolation) {
					t.Errorf("continuity run kept isolation flag %q", isolation)
				}
			}
			want := []string{"-s\ndanger-full-access" + "\n"}
			if c.harness == "claude" {
				want = []string{"--input-format\nstream-json", "--permission-mode\ndefault\n--allowedTools\n" + claudeTools + "\n"}
			}
			for _, w := range want {
				if !strings.Contains(string(argv), w) {
					t.Errorf("continuity args lack %q: %s", w, argv)
				}
			}
		})
	}
}

func TestSingleModeAllowsAccountDiscovery(t *testing.T) {
	parent := t.TempDir()
	os.WriteFile(filepath.Join(parent, "CLAUDE.md"), []byte("project"), 0600)
	dir := filepath.Join(parent, "scratch")
	os.Mkdir(dir, 0700)
	bin := script(t, `IFS= read -r first; echo '{"type":"result","is_error":false}'; cat >/dev/null`)
	s, err := (Claude{Bin: bin}).Execute(context.Background(), "p", ExecOptions{Dir: dir, Sandbox: testSandbox})
	if err != nil {
		t.Fatal(err)
	}
	if _, r := collect(t, s); r.Err != nil {
		t.Fatal(r.Err)
	}
}

func TestClaudeStreamInputIsEscapedNDJSON(t *testing.T) {
	captured := filepath.Join(t.TempDir(), "stdin")
	bin := script(t, `i=0; while [ "$i" -lt 3000 ]; do echo '{"type":"log","message":"startup"}'; i=$((i+1)); done
IFS= read -r first; printf '%s' "$first" > '`+captured+`'
echo '{"type":"result","is_error":false}'
cat >/dev/null`)
	prompt := strings.Repeat("Line with \"quotes\", \\ slashes,\nnewlines and   separators.\n", 80000)
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	s, err := (Claude{Bin: bin}).Execute(ctx, prompt, ExecOptions{Sandbox: testSandbox, Dir: t.TempDir()})
	if err != nil {
		t.Fatal(err)
	}
	if _, r := collect(t, s); r.Err != nil {
		t.Fatal(r.Err)
	}
	b, _ := os.ReadFile(captured)
	var frame struct {
		Type    string
		Message struct{ Role, Content string }
	}
	if err := json.Unmarshal(b, &frame); err != nil || frame.Type != "user" || frame.Message.Role != "user" || frame.Message.Content != prompt {
		t.Fatalf("first frame (%d bytes) did not round-trip: %v", len(b), err)
	}
}

func TestClaudeControlRequestIsDeniedAndRecorded(t *testing.T) {
	reply := filepath.Join(t.TempDir(), "reply")
	// The trailing cat only ends once Lykeion closes stdin after the result.
	bin := script(t, `IFS= read -r first
echo '{"type":"control_request","request_id":"req-7","request":{"subtype":"can_use_tool","tool_name":"Bash","input":{"command":"touch x","token":"ltk_secret"}}}'
IFS= read -r answer; printf '%s' "$answer" > '`+reply+`'
echo '{"type":"control_request","request_id":"req-8","request":{"subtype":"elicitation"}}'
IFS= read -r answer2; printf '\n%s' "$answer2" >> '`+reply+`'
echo '{"type":"result","is_error":false}'
cat >/dev/null`)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	s, err := (Claude{Bin: bin}).Execute(ctx, "p", ExecOptions{Sandbox: testSandbox, Dir: t.TempDir()})
	if err != nil {
		t.Fatal(err)
	}
	ms, r := collect(t, s)
	if r.Err != nil {
		t.Fatal(r.Err)
	}
	b, _ := os.ReadFile(reply)
	lines := strings.Split(string(b), "\n")
	if len(lines) != 2 || !strings.Contains(lines[0], `"request_id":"req-7"`) || !strings.Contains(lines[0], `"behavior":"deny"`) ||
		!strings.Contains(lines[1], `"request_id":"req-8"`) || !strings.Contains(lines[1], `"subtype":"error"`) {
		t.Fatalf("replies: %s", b)
	}
	var records []string
	for _, m := range ms {
		if m.Type == "status" {
			records = append(records, string(m.Content))
		}
		if strings.Contains(string(m.Content), "ltk_secret") || strings.Contains(string(m.Content), "touch x") {
			t.Fatalf("decision record carries tool input: %s", m.Content)
		}
	}
	if len(records) != 2 || !strings.Contains(records[0], "req-7") || !strings.Contains(records[1], "elicitation") {
		t.Fatalf("records: %v", records)
	}
}

func TestMalformedClaudeControlRequestFails(t *testing.T) {
	for _, frame := range []string{
		`{"type":"control_request","request":{"subtype":"can_use_tool"}}`,
		`{"type":"control_request","request_id":"r1","request":{}}`,
	} {
		bin := script(t, `IFS= read -r first; echo '`+frame+`'; sleep 5`)
		s, err := (Claude{Bin: bin}).Execute(context.Background(), "p", ExecOptions{Sandbox: testSandbox, Dir: t.TempDir()})
		if err != nil {
			t.Fatal(err)
		}
		if _, r := collect(t, s); r.Err == nil || !strings.Contains(r.Err.Error(), "malformed Claude control request") {
			t.Fatalf("%s: %v", frame, r.Err)
		}
	}
}

func TestClaudeResumeRefusedBeforeWork(t *testing.T) {
	seedClaudeTranscript(t, "gone")
	for _, c := range []struct {
		name, out string
		refused   bool
	}{
		{"missing", `{"type":"result","subtype":"error_during_execution","is_error":true,"num_turns":0,"total_cost_usd":0,"session_id":"gone"}`, true},
		{"worked", `{"type":"assistant","message":{"content":[{"type":"text","text":"hi"}]}}
{"type":"result","subtype":"error_during_execution","is_error":true,"num_turns":1,"total_cost_usd":0.1,"session_id":"gone"}`, false},
	} {
		t.Run(c.name, func(t *testing.T) {
			dir := t.TempDir()
			baseline := []byte(`{"version":1,"session":"gone","models":{"m":{"inputTokens":1,"outputTokens":1,"costUSD":0.1}}}`)
			bin := script(t, `IFS= read -r first; cat <<'EOF'
`+c.out+`
EOF
cat >/dev/null`)
			s, err := (Claude{Bin: bin}).Execute(context.Background(), "p", ExecOptions{Sandbox: testSandbox, Dir: dir, Resume: "gone", ClaudeBaseline: baseline})
			if err != nil {
				t.Fatal(err)
			}
			_, r := collect(t, s)
			if r.Err == nil || r.ResumeRefused != c.refused || len(r.Usage) != 0 {
				t.Fatalf("%+v", r)
			}
		})
	}
}

type blockedWriter struct{ closed chan struct{} }

func (w blockedWriter) Write([]byte) (int, error) { <-w.closed; return 0, os.ErrClosed }
func (w blockedWriter) Close() error              { close(w.closed); return nil }

func TestClaudeInputIsBoundedAndCloseUnblocks(t *testing.T) {
	in := newClaudeInput(blockedWriter{make(chan struct{})})
	var err error
	for i := 0; i <= claudeInputQueue+1 && err == nil; i++ {
		err = in.send(map[string]int{"i": i})
	}
	if err == nil || !strings.Contains(err.Error(), "queue is full") {
		t.Fatalf("a stuck child did not bound the queue: %v", err)
	}
	in.close()
	in.close()
	if err := in.send("x"); err != errClaudeInputClosed {
		t.Fatalf("send after close: %v", err)
	}
}

func TestCancelWhileClaudeIgnoresStdin(t *testing.T) {
	bin := script(t, `sleep 30`)
	ctx, cancel := context.WithCancel(context.Background())
	s, err := (Claude{Bin: bin}).Execute(ctx, strings.Repeat("x", 4<<20), ExecOptions{Sandbox: testSandbox, Dir: t.TempDir()})
	if err != nil {
		t.Fatal(err)
	}
	time.AfterFunc(300*time.Millisecond, cancel)
	done := make(chan Result, 1)
	go func() { _, r := collect(t, s); done <- r }()
	select {
	case r := <-done:
		if r.Reason != ReasonRuntimeOffline {
			t.Fatalf("%+v", r)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("a blocked stdin write outlived cancellation")
	}
}

func TestClaudeBaselineRoundTripsThroughResult(t *testing.T) {
	seedClaudeTranscript(t, "s1")
	result := `{"type":"result","is_error":false,"session_id":"s1","modelUsage":{"m":{"inputTokens":%d,"outputTokens":%d,"cacheReadInputTokens":0,"cacheCreationInputTokens":0,"costUSD":%s}}}`
	run := func(out string, o ExecOptions) Result {
		bin := script(t, "IFS= read -r first; echo '"+out+"'; cat >/dev/null")
		o.Dir = t.TempDir()
		s, err := (Claude{Bin: bin}).Execute(context.Background(), "p", o)
		if err != nil {
			t.Fatal(err)
		}
		_, r := collect(t, s)
		return r
	}
	first := run(fmt.Sprintf(result, 10, 5, "0.5"), ExecOptions{Sandbox: testSandbox})
	if first.Err != nil || len(first.ClaudeSnapshot) == 0 {
		t.Fatalf("%+v", first)
	}
	// A fresh scratch resumes from the returned snapshot and is charged only
	// for the work after it, never twice.
	second := run(fmt.Sprintf(result, 14, 9, "0.75"), ExecOptions{Sandbox: testSandbox, Resume: "s1", ClaudeBaseline: first.ClaudeSnapshot})
	if second.Err != nil || len(second.Usage) != 1 {
		t.Fatalf("%+v", second)
	}
	u := second.Usage[0]
	if u.InputTokens != 4 || u.OutputTokens != 4 || u.CostUSD == nil || *u.CostUSD != 0.25 {
		t.Fatalf("resumed usage %+v cost %v", u, *u.CostUSD)
	}
	if _, err := (Claude{Bin: "/bin/false"}).Execute(context.Background(), "p", ExecOptions{Sandbox: testSandbox, Dir: t.TempDir(), Resume: "s1"}); err == nil {
		t.Fatal("resume without a trustworthy baseline started")
	}
}

// catalogScript fakes a Codex whose `debug models` prints live (or fails with
// "fail"), whose `--bundled` prints bundled, and which records its exec args.
func catalogScript(t *testing.T, live, bundled, args string) string {
	p := filepath.Join(t.TempDir(), "codex")
	body := `#!/bin/sh
if [ "$1" = debug ]; then
	if [ "$3" = --bundled ]; then echo '` + bundled + `'; exit 0; fi
	[ '` + live + `' = fail ] && exit 1
	echo '` + live + `'; exit 0
fi
printf '%s\n' "$@" > '` + args + `'
echo '{"type":"turn.completed","usage":{"input_tokens":10,"output_tokens":1}}'
`
	if err := os.WriteFile(p, []byte(body), 0o755); err != nil {
		t.Fatal(err)
	}
	return p
}

func TestCodexModelComesFromTheAccount(t *testing.T) {
	const live = `{"models":[{"slug":"hidden-x","visibility":"hide"},{"slug":"gpt-6.1-sol","visibility":"list"},{"slug":"gpt-5.6-sol","visibility":"list"}]}`
	const bundled = `{"models":[{"slug":"gpt-5.6-luna","visibility":"list"}]}`
	for _, c := range []struct {
		name, live, configured, explicit, want string
		inherit                                bool
	}{
		{"configured default the account can see", live, "gpt-5.6-sol", "", "gpt-5.6-sol", true},
		{"configured default the account cannot see", live, "gpt-retired", "", "gpt-6.1-sol", true},
		{"no configured default", live, "", "", "gpt-6.1-sol", true},
		{"offline falls back to the bundled catalog", "fail", "", "", "gpt-5.6-luna", true},
		{"explicit model wins", live, "gpt-5.6-sol", "gpt-x", "gpt-x", true},
	} {
		t.Run(c.name, func(t *testing.T) {
			home := t.TempDir()
			t.Setenv("CODEX_HOME", home)
			if c.configured != "" {
				os.WriteFile(filepath.Join(home, "config.toml"), []byte("# personal\nmodel = \""+c.configured+"\"\nmodel_reasoning_effort = \"medium\"\n[profiles.fast]\nmodel = \"other\"\n"), 0o600)
			}
			args := filepath.Join(t.TempDir(), "args")
			s, err := (Codex{Bin: catalogScript(t, c.live, bundled, args)}).Execute(context.Background(), "p", ExecOptions{Sandbox: testSandbox, Dir: t.TempDir(), Model: c.explicit})
			if err != nil {
				t.Fatal(err)
			}
			_, r := collect(t, s)
			argv, _ := os.ReadFile(args)
			if r.Err != nil || r.Model != c.want || len(r.Usage) != 1 || r.Usage[0].Model != c.want || !strings.Contains(string(argv), "-m\n"+c.want+"\n") {
				t.Fatalf("model %q usage %+v err %v args %s", r.Model, r.Usage, r.Err, argv)
			}
		})
	}
	t.Run("no catalog and no default", func(t *testing.T) {
		t.Setenv("CODEX_HOME", t.TempDir())
		bin := catalogScript(t, "fail", "not json", filepath.Join(t.TempDir(), "args"))
		if _, err := (Codex{Bin: bin}).Execute(context.Background(), "p", ExecOptions{Sandbox: testSandbox, Dir: t.TempDir()}); err == nil || !strings.Contains(err.Error(), "cannot determine the Codex model") {
			t.Fatal(err)
		}
	})
}

func TestUnpricedCodexModelRunsWithAVisibleWarning(t *testing.T) {
	t.Setenv("CODEX_HOME", t.TempDir())
	bin := catalogScript(t, `{"models":[{"slug":"gpt-unpriced","visibility":"list"}]}`, "", filepath.Join(t.TempDir(), "args"))
	s, err := (Codex{Bin: bin}).Execute(context.Background(), "p", ExecOptions{Sandbox: testSandbox, Dir: t.TempDir()})
	if err != nil {
		t.Fatal(err)
	}
	ms, r := collect(t, s)
	if r.Err != nil || r.Model != "gpt-unpriced" || r.Usage[0].CostUSD != nil {
		t.Fatalf("%+v", r)
	}
	warned := false
	for _, m := range ms {
		warned = warned || (m.Type == "status" && strings.Contains(string(m.Content), `"priced":false`) && strings.Contains(string(m.Content), "count $0"))
	}
	if !warned {
		t.Fatal("an unpriced model ran without a visible warning")
	}
}

func TestClaudeReportsItsModel(t *testing.T) {
	bin := script(t, `IFS= read -r prompt
echo '{"type":"system","subtype":"init","model":"claude-haiku-5-5","session_id":"s"}'
echo '{"type":"result","is_error":false,"session_id":"s"}'`)
	s, err := (Claude{Bin: bin}).Execute(context.Background(), "p", ExecOptions{Sandbox: testSandbox, Dir: t.TempDir()})
	if err != nil {
		t.Fatal(err)
	}
	if _, r := collect(t, s); r.Model != "claude-haiku-5-5" {
		t.Fatalf("%+v", r)
	}
}

func TestCodexAlwaysDangerFullAccess(t *testing.T) {
	for _, ro := range []bool{false, true} {
		args := filepath.Join(t.TempDir(), "args")
		bin := script(t, `printf '%s\n' "$@" > '`+args+`'; echo '{"type":"turn.completed","usage":{}}'`)
		session, err := (Codex{Bin: bin}).Execute(context.Background(), "p", ExecOptions{Dir: t.TempDir(), Sandbox: testSandbox, ReadOnly: ro})
		if err != nil {
			t.Fatal(err)
		}
		collect(t, session)
		got, _ := os.ReadFile(args)
		if !strings.Contains(string(got), "-s\ndanger-full-access\n") || strings.Contains(string(got), "--ignore-user-config") {
			t.Fatalf("args %s", got)
		}
	}
}

func TestScratchLockSurvivesAKilledHolder(t *testing.T) {
	if runtime.GOOS != "darwin" && runtime.GOOS != "linux" {
		t.Skip("advisory scratch locks are macOS and Linux only")
	}
	dir := t.TempDir()
	resolved, _ := filepath.EvalSymlinks(dir)
	lock := filepath.Join(resolved, ".agent-lock")
	ready := filepath.Join(t.TempDir(), "ready")
	holder := exec.Command("perl", "-e", `use Fcntl ":flock"; open(F, ">>", $ARGV[0]) or die; flock(F, LOCK_EX) or die; open(R, ">", $ARGV[1]); close(R); sleep 60`, lock, ready)
	if err := holder.Start(); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 200; i++ {
		if _, err := os.Stat(ready); err == nil {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	bin := script(t, `IFS= read -r prompt; echo '{"type":"result","is_error":false}'`)
	if _, err := (Claude{Bin: bin}).Execute(context.Background(), "p", ExecOptions{Sandbox: testSandbox, Dir: dir}); err == nil || !strings.Contains(err.Error(), "already in use") {
		t.Fatalf("a held scratch was entered: %v", err)
	}
	holder.Process.Signal(syscall.SIGKILL)
	holder.Wait()
	s, err := (Claude{Bin: bin}).Execute(context.Background(), "p", ExecOptions{Sandbox: testSandbox, Dir: dir})
	if err != nil {
		t.Fatalf("a killed holder still blocks the scratch: %v", err)
	}
	if _, r := collect(t, s); r.Err != nil {
		t.Fatal(r.Err)
	}
	if _, err := os.Stat(lock); err != nil {
		t.Fatal("the lock file was deleted:", err)
	}
}

func TestAccountingStateIsNotADiscoveryHome(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, ".cli-home", "nested", "skills")
	os.MkdirAll(path, 0700)
	bin := script(t, `echo '{"type":"result","is_error":false}'`)
	s, err := (Claude{Bin: bin}).Execute(context.Background(), "p", ExecOptions{Dir: dir, Sandbox: testSandbox})
	if err != nil {
		t.Fatal(err)
	}
	if _, r := collect(t, s); r.Err != nil {
		t.Fatal(r.Err)
	}
	if _, err := os.Stat(path); err != nil {
		t.Fatal(err)
	}
}

func TestSessionReachableLooksInTheCLIStore(t *testing.T) {
	claudeHome, codexHome := t.TempDir(), t.TempDir()
	t.Setenv("CLAUDE_CONFIG_DIR", claudeHome)
	t.Setenv("CODEX_HOME", codexHome)
	os.MkdirAll(filepath.Join(claudeHome, "projects", "-old-scratch"), 0o700)
	os.WriteFile(filepath.Join(claudeHome, "projects", "-old-scratch", "s-1.jsonl"), nil, 0o600)
	os.MkdirAll(filepath.Join(codexHome, "sessions", "2026", "10", "14"), 0o700)
	os.WriteFile(filepath.Join(codexHome, "sessions", "2026", "10", "14", "rollout-2026-10-14T19-05-49-t-1.jsonl"), nil, 0o600)
	for _, c := range []struct {
		harness, session string
		inherit, want    bool
	}{
		{"claude", "s-1", true, true},
		{"claude", "s-2", true, false},
		{"claude", "../s-1", true, false},
		{"codex", "t-1", true, true},
		{"codex", "t-2", true, false},
		{"unknown", "s-1", true, false},
	} {
		if got := SessionReachable(c.harness, c.session); got != c.want {
			t.Errorf("%+v: %v", c, got)
		}
	}
}

func TestPersonalLykeionNamedCodexServerCannotMerge(t *testing.T) {
	for _, clash := range []bool{false, true} {
		home := t.TempDir()
		t.Setenv("CODEX_HOME", home)
		name := "personal_tool"
		if clash {
			name = "lyk_door"
		}
		os.WriteFile(filepath.Join(home, "config.toml"), []byte("model = \"gpt-5.6-sol\"\n[mcp_servers."+name+"]\nurl = \"http://personal\"\nhttp_headers = { \"X-Personal\" = \"1\" }\n"), 0o600)
		args := filepath.Join(t.TempDir(), "args")
		bin := script(t, `printf '%s\n' "$@" > `+args+`; echo '{"type":"turn.completed","usage":{}}'`)
		var notes []string
		o := ExecOptions{Sandbox: testSandbox, Dir: t.TempDir(), DoorURL: "http://127.0.0.1:1/mcp", DoorToken: "d", MCPToken: "ltk_x", OnMessage: func(m Message) error {
			if m.Type == "status" && strings.Contains(string(m.Content), "continuity") {
				notes = append(notes, string(m.Content))
			}
			return nil
		}}
		s, err := (Codex{Bin: bin}).Execute(context.Background(), "p", o)
		if err != nil {
			t.Fatal(err)
		}
		_, r := collect(t, s)
		argv, _ := os.ReadFile(args)
		strict := strings.Contains(string(argv), "--ignore-user-config")
		if strict != clash || (len(notes) == 1) != clash || r.Model != "gpt-5.6-sol" {
			t.Fatalf("clash=%v strict=%v notes=%v model=%q args=%s", clash, strict, notes, r.Model, argv)
		}
		if clash && (strings.Contains(string(argv), "personal") || !strings.Contains(notes[0], "lyk_door")) {
			t.Fatalf("personal values reached argv or the note is unclear: %s %v", argv, notes)
		}
	}
}
