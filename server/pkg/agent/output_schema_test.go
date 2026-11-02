package agent

import (
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

const testSchema = `{"type":"object","properties":{"ok":{"type":"boolean"}},"required":["ok"],"additionalProperties":false}`

func argvOf(t *testing.T, path string) []string {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return strings.Split(strings.TrimSuffix(string(b), "\n"), "\n")
}

// Claude takes the schema inline and its structured output is the result's
// answer.
func TestClaudeOutputSchema(t *testing.T) {
	args := filepath.Join(t.TempDir(), "args")
	bin := script(t, `printf '%s\n' "$@" > `+args+`
echo '{"type":"result","subtype":"success","is_error":false,"result":"done","structured_output":{"ok":true}}'`)
	s, err := Claude{Bin: bin}.Execute(t.Context(), "p", ExecOptions{Sandbox: testSandbox, Dir: t.TempDir(), OutputSchema: testSchema})
	if err != nil {
		t.Fatal(err)
	}
	_, res := collect(t, s)
	if res.Err != nil {
		t.Fatal(res.Err)
	}
	argv := argvOf(t, args)
	if i := slices.Index(argv, "--json-schema"); i < 0 || i+1 >= len(argv) || argv[i+1] != testSchema {
		t.Fatalf("claude ran with %q", argv)
	}
	if res.Output != `{"ok":true}` {
		t.Fatalf("output %q", res.Output)
	}

	// No schema, no flag.
	s, err = Claude{Bin: bin}.Execute(t.Context(), "p", ExecOptions{Sandbox: testSandbox, Dir: t.TempDir()})
	if err != nil {
		t.Fatal(err)
	}
	collect(t, s)
	if slices.Contains(argvOf(t, args), "--json-schema") {
		t.Fatal("schema flag without a schema")
	}
}

// Codex reads the schema from a file in the run folder and its last message
// is the answer.
func TestCodexOutputSchema(t *testing.T) {
	args := filepath.Join(t.TempDir(), "args")
	seen := filepath.Join(t.TempDir(), "schema")
	bin := script(t, `printf '%s\n' "$@" > `+args+`
prev=; for a in "$@"; do [ "$prev" = --output-schema ] && cat "$a" > `+seen+`; prev=$a; done
echo '{"type":"item.completed","item":{"type":"agent_message","text":"thinking aloud"}}'
echo '{"type":"item.completed","item":{"type":"agent_message","text":"{\"ok\":true}"}}'
echo '{"type":"turn.completed","usage":{"input_tokens":0,"output_tokens":0}}'`)
	dir := t.TempDir()
	s, err := Codex{Bin: bin}.Execute(t.Context(), "p", ExecOptions{Sandbox: testSandbox, Dir: dir, Model: "gpt-5.6-sol", OutputSchema: testSchema})
	if err != nil {
		t.Fatal(err)
	}
	_, res := collect(t, s)
	if res.Err != nil {
		t.Fatal(res.Err)
	}
	argv := argvOf(t, args)
	real, _ := filepath.EvalSymlinks(dir)
	i := slices.Index(argv, "--output-schema")
	if i < 0 || i+1 >= len(argv) || filepath.Dir(argv[i+1]) != real || i > slices.Index(argv, "-") {
		t.Fatalf("codex ran with %q", argv)
	}
	if b, _ := os.ReadFile(seen); string(b) != testSchema {
		t.Fatalf("schema file %q", b)
	}
	if res.Output != `{"ok":true}` {
		t.Fatalf("output %q", res.Output)
	}

	// A second run in the same folder replaces the file.
	s, err = Codex{Bin: bin}.Execute(t.Context(), "p", ExecOptions{Sandbox: testSandbox, Dir: dir, Model: "gpt-5.6-sol", OutputSchema: testSchema})
	if err != nil {
		t.Fatal(err)
	}
	if _, res = collect(t, s); res.Err != nil {
		t.Fatal(res.Err)
	}

	s, err = Codex{Bin: bin}.Execute(t.Context(), "p", ExecOptions{Sandbox: testSandbox, Dir: t.TempDir(), Model: "gpt-5.6-sol"})
	if err != nil {
		t.Fatal(err)
	}
	collect(t, s)
	if slices.Contains(argvOf(t, args), "--output-schema") {
		t.Fatal("schema flag without a schema")
	}
}
