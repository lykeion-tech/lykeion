package agent

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// A running Claude turn takes each message on Input as a further stream-json
// user message after the prompt, and its stdin closes on the result.
func TestClaudeInputSendsMessages(t *testing.T) {
	seen := filepath.Join(t.TempDir(), "stdin")
	bin := script(t, `read -r first; printf '%s\n' "$first" > `+seen+`
echo '{"type":"system","subtype":"init","session_id":"s1"}'
read -r second; printf '%s\n' "$second" >> `+seen+`
echo '{"type":"result","subtype":"success","is_error":false,"result":"done","structured_output":{"ok":true}}'
cat >/dev/null; echo closed >> `+seen)
	input := make(chan string, 1)
	opts := ExecOptions{Sandbox: testSandbox, Dir: t.TempDir(), OutputSchema: testSchema, Input: input}
	opts.OnMessage = func(m Message) error {
		if m.Type == "log" && strings.Contains(string(m.Content), `"init"`) {
			input <- "A researcher commented on the study: the answer must be 7"
		}
		return nil
	}
	s, err := Claude{Bin: bin}.Execute(t.Context(), "the prompt", opts)
	if err != nil {
		t.Fatal(err)
	}
	_, res := collectWithin(t, s, 10*time.Second)
	if res.Err != nil || res.Output != `{"ok":true}` {
		t.Fatalf("result %+v", res)
	}
	var lines []string
	for deadline := time.Now().Add(5 * time.Second); time.Now().Before(deadline); time.Sleep(10 * time.Millisecond) {
		b, _ := os.ReadFile(seen)
		if lines = strings.Split(strings.TrimSpace(string(b)), "\n"); len(lines) == 3 {
			break
		}
	}
	if len(lines) != 3 || lines[2] != "closed" {
		t.Fatalf("stdin saw %q", lines)
	}
	for i, want := range []string{"the prompt", "A researcher commented on the study: the answer must be 7"} {
		var frame struct {
			Type    string
			Message struct{ Role, Content string }
		}
		if err := json.Unmarshal([]byte(lines[i]), &frame); err != nil || frame.Type != "user" || frame.Message.Role != "user" || frame.Message.Content != want {
			t.Fatalf("line %d: %q", i+1, lines[i])
		}
	}
}

// An installer may switch its entry-point symlink immediately after --version.
// The private turn must still execute the exact target that answered the probe.
func TestLabClaudeUpdateBetweenProbeAndLaunch(t *testing.T) {
	for _, viaPath := range []bool{false, true} {
		name := "configured symlink"
		if viaPath {
			name = "PATH lookup"
		}
		t.Run(name, func(t *testing.T) {
			dir, err := filepath.EvalSymlinks(t.TempDir())
			if err != nil {
				t.Fatal(err)
			}
			old := filepath.Join(dir, "2.1.317")
			next := filepath.Join(dir, "2.1.318")
			link := filepath.Join(dir, "claude")
			if err := os.WriteFile(old, []byte(`#!/bin/sh
if [ "$1" = "--version" ]; then
  dir=${0%/*}
  /bin/ln -sf "$dir/2.1.318" "$dir/claude" || exit 2
  echo '2.1.317 (Claude Code)'
  exit 0
fi
read -r prompt
echo '{"type":"result","subtype":"success","is_error":false,"result":"verified target"}'
`), 0700); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(next, []byte("#!/bin/sh\nread -r prompt\necho '{\"type\":\"result\",\"subtype\":\"success\",\"is_error\":false,\"result\":\"unverified target\"}'\n"), 0700); err != nil {
				t.Fatal(err)
			}
			if err := os.Symlink(old, link); err != nil {
				t.Fatal(err)
			}
			c := Claude{Bin: link}
			if viaPath {
				c.Bin = ""
				t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))
			}
			session, err := c.execute(t.Context(), "synthetic lab prompt", ExecOptions{Lab: true, ReadOnly: true, Dir: t.TempDir(), Sandbox: testSandbox})
			if err != nil {
				t.Fatal(err)
			}
			_, res := collectWithin(t, session, 10*time.Second)
			if target, err := os.Readlink(link); err != nil || target != next {
				t.Fatalf("update did not occur: %s %v", target, err)
			}
			if res.Err != nil || res.Output != "verified target" {
				t.Fatalf("unexpected launch: %q, %v", res.Output, res.Err)
			}
		})
	}
}
