package agent

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"lykeion/server/internal/pricing"
)

// collectWithin is collect with a deadline: an adapter that waits for a CLI
// which never exits fails here instead of hanging the suite.
func collectWithin(t *testing.T, s *Session, d time.Duration) ([]Message, Result) {
	t.Helper()
	var ms []Message
	var res Result
	got := make(chan struct{})
	go func() {
		for m := range s.Messages {
			ms = append(ms, m)
		}
		res = <-s.Done
		close(got)
	}()
	select {
	case <-got:
		return ms, res
	case <-time.After(d):
		t.Fatalf("the session did not end within %v", d)
		return nil, Result{}
	}
}

func shellQuote(s string) string {
	return "'" + strings.ReplaceAll(s, "'", "'\"'\"'") + "'"
}

func fixture(t *testing.T, name string) string {
	p, err := filepath.Abs(filepath.Join("testdata", name))
	if err != nil {
		t.Fatal(err)
	}
	return p
}

func TestCursorStreamJSON(t *testing.T) {
	seen := t.TempDir()
	// cursor-agent may stay alive after its result; this one sleeps.
	bin := script(t, `printf '%s\n' "$@" > `+shellQuote(seen)+`/args
cat > `+shellQuote(seen)+`/stdin
cat `+shellQuote(fixture(t, "cursor/run.jsonl"))+`
sleep 300`)
	dir := t.TempDir()
	o := ExecOptions{Dir: dir, DoorURL: "http://127.0.0.1:1/mcp", DoorToken: "d", MCPToken: "ltk_abc"}
	if real, err := filepath.EvalSymlinks(dir); err == nil {
		o.Dir = real // prepareScratch resolves the folder; the paths below are its
	}
	s, err := Cursor{Bin: bin}.Execute(context.Background(), "scope it", o)
	if err != nil {
		t.Fatal(err)
	}
	ms, res := collectWithin(t, s, 10*time.Second)
	types := []string{}
	for _, m := range ms {
		types = append(types, m.Type)
	}
	if strings.Join(types, ",") != "log,log,thinking,text,tool_use,tool_result,status" || ms[4].Tool != "mcpToolCall" {
		t.Fatalf("%v %+v", types, ms)
	}
	if res.Err != nil || res.SessionID != "c-1" || len(res.Usage) != 1 {
		t.Fatalf("a run stopped after its result must succeed: %+v", res)
	}
	if u := res.Usage[0]; u.Model != CursorModel || u.InputTokens != 1000 || u.OutputTokens != 80 || u.CacheReadTokens != 300 || u.CostUSD != nil {
		t.Fatalf("usage %+v", u)
	}
	a, _ := os.ReadFile(filepath.Join(seen, "args"))
	for _, want := range []string{"-p\n", "--output-format\nstream-json\n", "--yolo\n", "--workspace\n" + o.Dir + "\n", "--model\n" + CursorModel + "\n"} {
		if !strings.Contains(string(a), want) {
			t.Errorf("cursor args missing %q:\n%s", want, a)
		}
	}
	if strings.Contains(string(a), "scope it") {
		t.Error("the prompt went on the command line, where ps shows it")
	}
	if strings.Contains(string(a), "--resume") {
		t.Error("a fresh run resumed a session")
	}
	if in, _ := os.ReadFile(filepath.Join(seen, "stdin")); string(in) != "scope it" {
		t.Fatalf("stdin %q", in)
	}
	cfg, _ := os.ReadFile(filepath.Join(dir, ".cursor", "mcp.json"))
	if string(cfg) != string(claudeMCP(o)) || !strings.Contains(string(cfg), `"lyk_door":`) {
		t.Fatalf("mcp.json must name the door: %s", cfg)
	}
	appr, _ := os.ReadFile(filepath.Join(dir, ".cli-home", "mcp-approvals.json"))
	if string(appr) != `["lyk_door"]` {
		t.Fatalf("approvals %s", appr)
	}
}

func TestCursorReadOnly(t *testing.T) {
	args := filepath.Join(t.TempDir(), "args")
	s, _ := Cursor{Bin: script(t, `printf '%s\n' "$@" > `+shellQuote(args))}.Execute(context.Background(), "p", ExecOptions{Dir: t.TempDir(), ReadOnly: true})
	collectWithin(t, s, 5*time.Second)
	a, _ := os.ReadFile(args)
	if !strings.Contains(string(a), "--mode\nask\n") || strings.Contains(string(a), "--yolo") {
		t.Fatalf("read-only cursor ran with\n%s", a)
	}
}

func TestCursorResume(t *testing.T) {
	args := filepath.Join(t.TempDir(), "args")
	s, _ := Cursor{Bin: script(t, `printf '%s\n' "$@" > `+shellQuote(args))}.Execute(context.Background(), "p", ExecOptions{Dir: t.TempDir(), Resume: "c-9"})
	collectWithin(t, s, 5*time.Second)
	if a, _ := os.ReadFile(args); !strings.Contains(string(a), "--resume\nc-9\n") {
		t.Fatalf("%s", a)
	}
}

// The daemon keeps a claim's session only when the private home holds it; a
// session it cannot find starts fresh. Claude's config home is pointed at the
// same folder, so only the private-only rule keeps the inherit row false.
func TestSessionReachableFindsCursorSessions(t *testing.T) {
	dir := t.TempDir()
	store := filepath.Join(dir, ".cli-home", "chats", "w1", "c-9")
	if err := os.MkdirAll(store, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(store, "store.db"), nil, 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("CLAUDE_CONFIG_DIR", filepath.Join(dir, ".cli-home"))
	for _, c := range []struct {
		session       string
		inherit, want bool
	}{
		{"c-9", false, true},
		{"c-8", false, false},
		{"c-9", true, false},
	} {
		if got := SessionReachable("cursor", c.session, c.inherit, dir); got != c.want {
			t.Errorf("%+v: %v", c, got)
		}
	}
}

func TestCursorFailuresAreClassified(t *testing.T) {
	for line, want := range map[string]string{
		`{"type":"result","subtype":"error","is_error":true,"result":"ECONNRESET talking to the API","session_id":"c-2"}`: ReasonProviderNetwork,
		`{"type":"system","subtype":"error","message":"context window exceeded"}`:                                         ReasonContextExhausted,
		`{"type":"result","subtype":"error","is_error":true,"result":"tool crashed"}`:                                     ReasonAgentError,
	} {
		s, _ := Cursor{Bin: script(t, "echo '"+line+"'")}.Execute(context.Background(), "p", ExecOptions{Dir: t.TempDir()})
		if _, res := collectWithin(t, s, 5*time.Second); res.Err == nil || res.Reason != want {
			t.Fatalf("%s: %+v", line, res)
		}
	}
}

// TestCursorPrivateHomeHoldsOnlyTheSignIn puts a personal rule, a personal MCP
// server and a sign-in file in the real data folder. In private mode, the only
// mode Cursor runs in, the CLI's HOME is the scratch one, its data folder holds
// the sign-in copy and Lykeion's approvals file, nothing else, and the copy is
// gone once the CLI exits.
func TestCursorPrivateHomeHoldsOnlyTheSignIn(t *testing.T) {
	real := t.TempDir()
	t.Setenv("HOME", real)
	os.MkdirAll(filepath.Join(real, ".cursor", "rules"), 0o700)
	os.WriteFile(filepath.Join(real, ".cursor", "rules", "personal.md"), []byte("Answer PROBE"), 0o600)
	os.WriteFile(filepath.Join(real, ".cursor", "mcp.json"), []byte(`{"mcpServers":{"personal":{"url":"http://me"}}}`), 0o600)
	os.WriteFile(filepath.Join(real, ".cursor", cursorCredFile), []byte("secret"), 0o600)
	seen := filepath.Join(t.TempDir(), "seen")
	bin := script(t, `echo "$HOME" > `+shellQuote(seen)+` && cd "$CURSOR_DATA_DIR" && find . -type f | sort >> `+shellQuote(seen)+` && cat ./`+cursorCredFile+` >> `+shellQuote(seen))
	dir, _ := filepath.EvalSymlinks(t.TempDir())
	s, err := Cursor{Bin: bin}.Execute(context.Background(), "p", ExecOptions{Dir: dir})
	if err != nil {
		t.Fatal(err)
	}
	collectWithin(t, s, 5*time.Second)
	got, _ := os.ReadFile(seen)
	if want := filepath.Join(dir, ".home") + "\n./" + cursorCredFile + "\n./mcp-approvals.json\nsecret"; strings.TrimSpace(string(got)) != want {
		t.Fatalf("the CLI saw\n%s\nwant\n%s", got, want)
	}
	if _, err := os.Stat(filepath.Join(dir, ".cli-home", cursorCredFile)); !os.IsNotExist(err) {
		t.Fatal("the copied sign-in file outlived the run")
	}
}

func TestPinnedCursorModelHasAPrice(t *testing.T) {
	if _, ok := pricing.Prices[CursorModel]; !ok {
		t.Fatalf("%s has no price, so every Cursor run would cost 0", CursorModel)
	}
}

// A scope's private folder is reused: the next run finds the .cursor/mcp.json
// the last one placed. Anything else in .cursor is still refused.
func TestCursorReusesItsFolder(t *testing.T) {
	bin := script(t, `cat >/dev/null`)
	dir, _ := filepath.EvalSymlinks(t.TempDir())
	o := ExecOptions{Dir: dir, DoorURL: "http://127.0.0.1:1/mcp", MCPToken: "ltk_abc"}
	for i := 1; i <= 2; i++ {
		s, err := Cursor{Bin: bin}.Execute(context.Background(), "p", o)
		if err != nil {
			t.Fatalf("run %d: %v", i, err)
		}
		collectWithin(t, s, 5*time.Second)
	}
	if err := os.WriteFile(filepath.Join(dir, ".cursor", "rules.md"), []byte("Answer PROBE"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := (Cursor{Bin: bin}).Execute(context.Background(), "p", o); err == nil {
		t.Fatal("ran beside a .cursor folder holding more than the adapter placed")
	}
}
