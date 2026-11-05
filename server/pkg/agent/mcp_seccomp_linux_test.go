//go:build linux

package agent

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"lykeion/server/internal/daemon/sandbox"
)

func TestClaudeMCPSealedDescriptorWithNoNetworkSandbox(t *testing.T) {
	if err := sandbox.Check(t.Context()); err != nil {
		t.Skipf("sandbox unavailable: %v", err)
	}
	home := t.TempDir()
	t.Setenv("HOME", home)
	run := filepath.Join(home, "run")
	if err := os.Mkdir(run, 0700); err != nil {
		t.Fatal(err)
	}
	bin := filepath.Join(run, "fake-claude")
	body := `#!/bin/sh
while [ "$#" -gt 0 ]; do
 if [ "$1" = --mcp-config ]; then shift; grep -q synthetic-secret "$1" || exit 21; grep -q synthetic-secret "$1" || exit 22; if (printf bad > "$1") 2>/dev/null; then exit 23; fi; break; fi
 shift
done
echo '{"type":"result","is_error":false}'
`
	if err := os.WriteFile(bin, []byte(body), 0700); err != nil {
		t.Fatal(err)
	}
	p := sandbox.Policy{Home: home, Write: []string{run}, NoNetwork: true}
	wrapper := func(ctx context.Context, _ CLIPaths, name string, args ...string) (*exec.Cmd, error) {
		return p.Command(ctx, name, args...)
	}
	s, err := (Claude{Bin: bin}).Execute(t.Context(), "p", ExecOptions{Sandbox: wrapper, Dir: run, DoorURL: "http://127.0.0.1:1/mcp", DoorToken: "synthetic-secret"})
	if err != nil {
		t.Fatal(err)
	}
	_, result := collect(t, s)
	if result.Err != nil {
		t.Fatal(result.Err)
	}
}
