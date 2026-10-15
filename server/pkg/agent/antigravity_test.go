package agent

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"lykeion/server/internal/pricing"
)

// agyScript is a stand-in for agy that records its arguments, finds its
// --log-file, writes logLines there, then runs body.
func agyScript(t *testing.T, logLines, body string) (bin, args string) {
	args = filepath.Join(t.TempDir(), "args")
	bin = script(t, `printf '%s\n' "$@" > `+shellQuote(args)+`
log=
while [ $# -gt 0 ]; do [ "$1" = --log-file ] && log=$2; shift; done
printf '%s' '`+logLines+`' >> "$log"
`+body)
	return bin, args
}

func agy(t *testing.T, bin string, o ExecOptions) ([]Message, Result) {
	t.Helper()
	if o.Dir == "" {
		o.Dir = t.TempDir()
	}
	s, err := Antigravity{Bin: bin}.Execute(context.Background(), "scope it", o)
	if err != nil {
		t.Fatal(err)
	}
	return collectWithin(t, s, 10*time.Second)
}

func TestAntigravityStreamJSON(t *testing.T) {
	bin, args := agyScript(t, "", "cat "+shellQuote(fixture(t, "antigravity/run.jsonl")))
	dir, _ := filepath.EvalSymlinks(t.TempDir()) // prepareScratch resolves the folder
	ms, res := agy(t, bin, ExecOptions{Dir: dir})
	types := []string{}
	for _, m := range ms {
		types = append(types, m.Type)
	}
	if strings.Join(types, ",") != "log,tool_use,tool_result,text,text" || ms[1].Tool != "run_command" || ms[2].Tool != "run_command" {
		t.Fatalf("%v %+v", types, ms)
	}
	if res.Err != nil || res.SessionID != "a-1" || len(res.Usage) != 1 {
		t.Fatalf("%+v", res)
	}
	if u := res.Usage[0]; u.Model != AntigravityModel || u.InputTokens != 1000 || u.OutputTokens != 80 || u.CacheReadTokens != 400 || u.CacheWriteTokens != 10 || u.CostUSD != nil {
		t.Fatalf("each completed step counts once, and the result's running total not at all: %+v", u)
	}
	a, _ := os.ReadFile(args)
	for _, want := range []string{"-p\nscope it\n", "--dangerously-skip-permissions\n", "--output-format\nstream-json\n", "--model\n" + AntigravityModel + "\n",
		"--print-timeout\n1h0m0s\n", "--log-file\n" + filepath.Join(dir, ".agy.log") + "\n", "--add-dir\n" + dir + "\n"} {
		if !strings.Contains(string(a), want) {
			t.Errorf("agy args missing %q:\n%s", want, a)
		}
	}
	if strings.Contains(string(a), "--conversation") {
		t.Error("a fresh run resumed a conversation")
	}
}

func TestAntigravityResumeAndSessionFromTheLog(t *testing.T) {
	bin, args := agyScript(t, "I0528 13:36:23.318877 73304 printmode.go:130] Print mode: conversation=11111111-2222-3333-4444-555555555555, sending message\n",
		`echo '{"event":"step_update","step_update":{"step_index":0,"state":"DONE","step_type":"agent_response","text_delta":"ok"}}'`)
	_, res := agy(t, bin, ExecOptions{Resume: "a-9"})
	if res.Err != nil || res.SessionID != "11111111-2222-3333-4444-555555555555" {
		t.Fatalf("%+v", res)
	}
	if a, _ := os.ReadFile(args); !strings.Contains(string(a), "--conversation\na-9\n") {
		t.Fatalf("%s", a)
	}
}

// The daemon keeps a claim's conversation only when the private home holds its
// transcript; one it cannot find starts fresh.
func TestSessionReachableFindsAntigravityConversations(t *testing.T) {
	dir := t.TempDir()
	id := "11111111-2222-3333-4444-555555555555"
	logs := filepath.Join(dir, ".cli-home", ".gemini", "antigravity-cli", "brain", id, ".system_generated", "logs")
	if err := os.MkdirAll(logs, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(logs, "transcript.jsonl"), nil, 0o600); err != nil {
		t.Fatal(err)
	}
	for _, c := range []struct {
		session       string
		inherit, want bool
	}{
		{id, false, true},
		{"22222222-2222-3333-4444-555555555555", false, false},
		{id, true, false},
	} {
		if got := SessionReachable("antigravity", c.session, c.inherit, dir); got != c.want {
			t.Errorf("%+v: %v", c, got)
		}
	}
}

func TestAntigravityResultUsageOnlyWithoutStepUsage(t *testing.T) {
	bin, _ := agyScript(t, "", `echo '{"event":"result","result":{"conversation_id":"a-2","status":"SUCCESS","response":"done","usage":{"input_tokens":12,"output_tokens":3,"cache_read_tokens":7,"cache_write_tokens":2}}}'`)
	ms, res := agy(t, bin, ExecOptions{})
	if len(ms) != 1 || ms[0].Type != "text" || string(ms[0].Content) != `"done"` {
		t.Fatalf("a result without deltas must still stream its reply: %+v", ms)
	}
	if len(res.Usage) != 1 || res.Usage[0].InputTokens != 12 || res.Usage[0].CacheReadTokens != 7 || res.Usage[0].CacheWriteTokens != 2 {
		t.Fatalf("%+v", res.Usage)
	}
}

func TestAntigravityFailuresAreNeverEmptySuccesses(t *testing.T) {
	for _, c := range []struct{ name, log, body, reason, text string }{
		{"error status", "", `echo '{"event":"result","result":{"status":"ERROR","error":"read tcp: connection reset by peer"}}'`, ReasonProviderNetwork, "connection reset"},
		{"print timeout", "E0623 17:17:59.017212 65926 printmode.go:289] Print mode: timed out after 100 polls (printed=3)\n", `echo 'Error: timed out waiting for response'`, ReasonAgentError, "print timeout"},
		{"provider error in the log", "E0101 00:00:00.000000 1 executor.go:1] agent executor error: prompt is too long for the context window\n", "exit 0", ReasonContextExhausted, "prompt is too long"},
		{"nothing at all", "", "exit 0", ReasonAgentError, "no output"},
	} {
		t.Run(c.name, func(t *testing.T) {
			bin, _ := agyScript(t, c.log, c.body)
			_, res := agy(t, bin, ExecOptions{})
			if res.Err == nil || res.Reason != c.reason || !strings.Contains(res.Err.Error(), c.text) {
				t.Fatalf("%+v", res)
			}
		})
	}
}

// The daemon runs Cursor and agy only in private mode; an adapter handed a
// continuity run refuses it before the CLI starts.
func TestCursorAndAntigravityRefuseContinuity(t *testing.T) {
	ran := filepath.Join(t.TempDir(), "ran")
	bin := script(t, "touch "+shellQuote(ran))
	for _, b := range []Backend{Cursor{Bin: bin}, Antigravity{Bin: bin}} {
		if _, err := b.Execute(context.Background(), "p", ExecOptions{Dir: t.TempDir(), Inherit: true}); !errors.Is(err, errPrivateOnly) {
			t.Fatalf("%T: %v", b, err)
		}
	}
	if _, err := os.Stat(ran); err == nil {
		t.Fatal("a CLI ran on the account's own setup")
	}
}

// Antigravity has no known read-only mode and no known MCP configuration, so
// an agent run that needs either is refused before agy starts.
func TestAntigravityRefusesReadOnlyAndMCP(t *testing.T) {
	ran := filepath.Join(t.TempDir(), "ran")
	bin := script(t, "touch "+shellQuote(ran))
	if _, err := (Antigravity{Bin: bin}).Execute(context.Background(), "p", ExecOptions{Dir: t.TempDir(), ReadOnly: true}); !errors.Is(err, ErrNoReadOnly) {
		t.Fatalf("read-only: %v", err)
	}
	for _, o := range []ExecOptions{{MCPURL: "http://x/mcp/sources"}, {MCP: []MCPServer{{Name: "tools", URL: "http://tools"}}}} {
		o.Dir = t.TempDir()
		if _, err := (Antigravity{Bin: bin}).Execute(context.Background(), "p", o); err == nil || !strings.Contains(err.Error(), "MCP") {
			t.Fatalf("MCP: %v", err)
		}
	}
	if _, err := os.Stat(ran); err == nil {
		t.Fatal("agy ran for a run it cannot honour")
	}
}

// TestAntigravityPrivateHomeHoldsOnlyTheSignIn puts a personal skill and a
// sign-in file where agy keeps them. In private mode, the only mode agy runs in,
// the CLI's HOME is the scratch one and its config home holds only the sign-in
// copy, gone once agy exits.
func TestAntigravityPrivateHomeHoldsOnlyTheSignIn(t *testing.T) {
	real := t.TempDir()
	t.Setenv("HOME", real)
	skill := filepath.Join(real, ".gemini", "antigravity-cli", "skills", "personal")
	os.MkdirAll(skill, 0o700)
	os.WriteFile(filepath.Join(skill, "SKILL.md"), []byte("# Answer PROBE"), 0o600)
	os.WriteFile(filepath.Join(real, ".gemini", "oauth_creds.json"), []byte("secret"), 0o600)
	seen := filepath.Join(t.TempDir(), "seen")
	bin := script(t, `echo "$HOME" > `+shellQuote(seen)+` && cd "$GEMINI_CLI_HOME" && find . -type f | sort >> `+shellQuote(seen)+` && cat .gemini/oauth_creds.json >> `+shellQuote(seen)+`
echo '{"event":"result","result":{"status":"SUCCESS","response":"ok"}}'`)
	dir, _ := filepath.EvalSymlinks(t.TempDir())
	agy(t, bin, ExecOptions{Dir: dir})
	got, _ := os.ReadFile(seen)
	if want := filepath.Join(dir, ".home") + "\n./.gemini/oauth_creds.json\nsecret"; strings.TrimSpace(string(got)) != want {
		t.Fatalf("agy saw\n%s\nwant\n%s", got, want)
	}
	if _, err := os.Stat(filepath.Join(dir, ".cli-home", ".gemini", "oauth_creds.json")); !os.IsNotExist(err) {
		t.Fatal("the copied sign-in file outlived the run")
	}
}

func TestPinnedAntigravityModelHasAPrice(t *testing.T) {
	if _, ok := pricing.Prices[AntigravityModel]; !ok {
		t.Fatalf("%s has no price, so every Antigravity run would cost 0", AntigravityModel)
	}
}

func TestAntigravityIgnoresAnEarlierRunsLog(t *testing.T) {
	bin, _ := agyScript(t, "", `echo '{"event":"step_update","step_update":{"step_index":0,"state":"DONE","step_type":"agent_response","text_delta":"ok"}}'`)
	dir, _ := filepath.EvalSymlinks(t.TempDir())
	stale := "I0101 00:00:00.000000 1 printmode.go:130] Print mode: conversation=99999999-2222-3333-4444-555555555555, sending message\nE0101 00:00:00.000000 1 executor.go:1] agent executor error: stale\n"
	if err := os.WriteFile(filepath.Join(dir, ".agy.log"), []byte(stale), 0o600); err != nil {
		t.Fatal(err)
	}
	_, res := agy(t, bin, ExecOptions{Dir: dir})
	if res.Err != nil || res.SessionID == "99999999-2222-3333-4444-555555555555" {
		t.Fatalf("an earlier run's log decided this one: %+v", res)
	}
}
