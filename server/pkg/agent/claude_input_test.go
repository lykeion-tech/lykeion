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
