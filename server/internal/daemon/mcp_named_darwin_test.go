//go:build darwin

package daemon

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"lykeion/server/internal/daemon/sandbox"
	"lykeion/server/pkg/agent"
)

func TestNamedClaudeMCPRealCLIPolicyAndDoorIsolation(t *testing.T) {
	if err := sandbox.Check(t.Context()); err != nil {
		t.Skipf("sandbox unavailable: %v", err)
	}
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("LYK_CONFIG", filepath.Join(t.TempDir(), "config.json"))
	account := filepath.Join(home, "account")
	must(t, os.Mkdir(account, 0700))
	t.Setenv("CLAUDE_CONFIG_DIR", account)
	scratch := t.TempDir()
	run := filepath.Join(scratch, "run")
	must(t, os.Mkdir(run, 0700))
	bin := fakeCLI(t, "claude", `while [ "$#" -gt 0 ]; do
  if [ "$1" = --mcp-config ]; then shift; cfg="$1"; break; fi; shift
done
grep -q SYNTHETIC_DOOR_TOKEN "$cfg" || exit 31
grep -q SYNTHETIC_DOOR_TOKEN "$cfg" || exit 32
printf '%s' "$cfg" > mcp-path
while [ ! -f release ]; do sleep .05; done
echo '{"type":"result","is_error":false}'
`)
	d := &daemon{cfg: Config{Home: scratch, StateDir: t.TempDir()}}
	ctx, cancel := context.WithTimeout(t.Context(), 15*time.Second)
	defer cancel()
	s, err := (agent.Claude{Bin: bin}).Execute(ctx, "fixture", agent.ExecOptions{Dir: run, ConfigHome: account, DoorURL: "http://127.0.0.1:1/mcp", DoorToken: "SYNTHETIC_DOOR_TOKEN", Sandbox: d.cliSandbox(run, nil, false)})
	must(t, err)
	pathFile := filepath.Join(run, "mcp-path")
	var raw []byte
	for ctx.Err() == nil {
		raw, err = os.ReadFile(pathFile)
		if err == nil {
			break
		}
		time.Sleep(20 * time.Millisecond)
	}
	if err != nil {
		t.Fatalf("CLI did not publish its config path: %v", err)
	}
	path := strings.TrimSpace(string(raw))
	policy := sandbox.Policy{Home: home, Write: []string{run}}
	var placeholders []placeholder
	must(t, protectRunFolder(&policy, run, true, &placeholders))
	cmd, err := policy.Command(ctx, "/bin/cat", path)
	must(t, err)
	if out, err := cmd.CombinedOutput(); err == nil || strings.Contains(string(out), "SYNTHETIC_DOOR_TOKEN") {
		t.Fatalf("Door could read the private MCP configuration: %v", err)
	}
	must(t, os.WriteFile(filepath.Join(run, "release"), nil, 0600))
	for range s.Messages {
	}
	if result := <-s.Done; result.Err != nil {
		t.Fatalf("Claude: %v", result.Err)
	}
	if _, err := os.Lstat(path); !os.IsNotExist(err) {
		t.Fatalf("configuration not retired: %v", err)
	}
}

func TestNamedMCPReadGrantForGuestAndReadOnly(t *testing.T) {
	if err := sandbox.Check(t.Context()); err != nil {
		t.Skipf("sandbox unavailable: %v", err)
	}
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("LYK_CONFIG", filepath.Join(t.TempDir(), "config.json"))
	root, err := os.MkdirTemp("/private/tmp", "lyk-mcp-")
	must(t, err)
	t.Cleanup(func() { _ = os.RemoveAll(root) })
	path := filepath.Join(root, "config.json")
	must(t, os.WriteFile(path, []byte("synthetic"), 0400))
	scratch, state := t.TempDir(), t.TempDir()
	run := filepath.Join(scratch, "run")
	must(t, os.Mkdir(run, 0700))
	guestHome := filepath.Join(state, "guests", "test-guest", "claude")
	must(t, os.MkdirAll(guestHome, 0700))
	d := &daemon{cfg: Config{Home: scratch, StateDir: state}}
	for _, tc := range []struct {
		name          string
		readOnly      bool
		guest, config string
	}{
		{"read-only", true, "", t.TempDir()},
		{"guest", false, "test-guest", guestHome},
	} {
		t.Run(tc.name, func(t *testing.T) {
			p, err := d.cliPolicy(run, nil, tc.readOnly, agent.CLIPaths{MCPConfig: path, ConfigHome: tc.config}, tc.guest)
			must(t, err)
			cmd, err := p.Command(t.Context(), "/bin/cat", path)
			must(t, err)
			out, err := cmd.CombinedOutput()
			if err != nil || string(out) != "synthetic" {
				t.Fatalf("CLI could not read config: %q %v", out, err)
			}
		})
	}
}
