package daemon

import (
	"bufio"
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"lykeion/server/internal/daemon/sandbox"
	"lykeion/server/pkg/agent"
)

// A door command may still be active when Claude is invoked again to repair a hand-in.
func TestDoorGuardSurvivesClaudeMCPReplacement(t *testing.T) {
	if err := sandbox.Check(context.Background()); err != nil {
		t.Skipf("sandbox unavailable: %v", err)
	}
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("LYK_CONFIG", filepath.Join(t.TempDir(), "config.json"))
	run := filepath.Join(t.TempDir(), "run")
	must(t, os.Mkdir(run, 0700))
	config := filepath.Join(home, "account")
	must(t, os.Mkdir(config, 0700))
	policy := sandbox.Policy{Home: home, Write: []string{run}}
	var placeholders []placeholder
	must(t, protectRunFolder(&policy, run, true, &placeholders))
	guard := filepath.Join(run, ".lyk-mcp.json")
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	cmd, err := policy.Command(ctx, "/bin/sh", "-c", `printf 'ready\n'; read ignored; if grep -q SYNTHETIC_DOOR_TOKEN .lyk-mcp.json 2>/dev/null; then echo exposed; exit 1; fi; echo hidden`)
	must(t, err)
	cmd.Dir = run
	in, err := cmd.StdinPipe()
	must(t, err)
	out, err := cmd.StdoutPipe()
	must(t, err)
	var stderr strings.Builder
	cmd.Stderr = &stderr
	must(t, cmd.Start())
	t.Cleanup(func() { cancel(); _ = in.Close(); _ = cmd.Wait() })
	reader := bufio.NewReader(out)
	line, err := reader.ReadString('\n')
	if err != nil || line != "ready\n" {
		t.Fatalf("door command did not start: %q %v (%s)", line, err, &stderr)
	}
	fake := fakeCLI(t, "claude", `
while [ "$#" -gt 0 ]; do
  if [ "$1" = --mcp-config ]; then shift; grep -q SYNTHETIC_DOOR_TOKEN "$1" || exit 3; break; fi
  shift
done
echo '{"type":"result","is_error":false}'
`)
	d := &daemon{cfg: Config{Home: filepath.Dir(run), StateDir: t.TempDir()}}
	session, err := (agent.Claude{Bin: fake}).Execute(ctx, "fixture", agent.ExecOptions{
		Dir: run, ConfigHome: config, DoorURL: "http://127.0.0.1:1/mcp", DoorToken: "SYNTHETIC_DOOR_TOKEN",
		Sandbox: d.cliSandbox(run, nil, false),
	})
	if err != nil {
		t.Fatal(err)
	}
	for range session.Messages {
	}
	if result := <-session.Done; result.Err != nil {
		t.Fatalf("Claude did not read its MCP configuration: %v", result.Err)
	}
	contents, err := os.ReadFile(guard)
	if err != nil || strings.Contains(string(contents), "SYNTHETIC_DOOR_TOKEN") {
		t.Fatalf("token published in Door run folder: %v", err)
	}
	_, err = fmt.Fprintln(in, "continue")
	must(t, err)
	line, err = reader.ReadString('\n')
	if err != nil || line != "hidden\n" {
		t.Fatalf("door observed replaced configuration: %q %v (%s)", line, err, &stderr)
	}
	if err := cmd.Wait(); err != nil {
		t.Fatalf("door command: %v (%s)", err, &stderr)
	}
}
