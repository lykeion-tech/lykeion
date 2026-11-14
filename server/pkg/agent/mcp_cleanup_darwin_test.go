//go:build darwin

package agent

import (
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

func TestClaudeMCPReportsReplacementWithoutRemovingIt(t *testing.T) {
	captured := filepath.Join(t.TempDir(), "path")
	bin := script(t, `while [ "$#" -gt 0 ]; do
 if [ "$1" = --mcp-config ]; then shift; printf '%s' "$1" > `+strconv.Quote(captured)+`; rm "$1"; printf replacement > "$1"; break; fi
 shift
done
echo '{"type":"result","is_error":false}'`)
	s, err := (Claude{Bin: bin}).Execute(t.Context(), "p", ExecOptions{Sandbox: testSandbox, Dir: t.TempDir(), DoorURL: "http://127.0.0.1:1/mcp", DoorToken: "synthetic"})
	if err != nil {
		t.Fatal(err)
	}
	_, result := collect(t, s)
	if result.Err == nil || !strings.Contains(result.Err.Error(), "private MCP configuration changed") {
		t.Fatalf("replacement not reported: %v", result.Err)
	}
	p, err := os.ReadFile(captured)
	if err != nil {
		t.Fatal(err)
	}
	path := string(p)
	t.Cleanup(func() { _ = os.RemoveAll(filepath.Dir(path)) })
	if got, err := os.ReadFile(path); err != nil || string(got) != "replacement" {
		t.Fatalf("replacement lost: %q %v", got, err)
	}
}

func TestMCPSetupCleanupPreservesReplacedFile(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "owned")
	if err := os.Mkdir(dir, 0700); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, "config.json")
	if err := os.WriteFile(path, []byte("original"), 0600); err != nil {
		t.Fatal(err)
	}
	info, err := os.Lstat(path)
	if err != nil {
		t.Fatal(err)
	}
	replacement := filepath.Join(dir, "replacement")
	if err := os.WriteFile(replacement, []byte("keep"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.Rename(replacement, path); err != nil {
		t.Fatal(err)
	}
	if err := removeCreatedMCP(path, info); err == nil {
		t.Fatal("replacement was not reported")
	}
	if got, err := os.ReadFile(path); err != nil || string(got) != "keep" {
		t.Fatalf("replacement lost: %q %v", got, err)
	}
}
