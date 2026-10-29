//go:build darwin

package daemon

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"lykeion/server/pkg/agent"
)

// A sandboxed CLI that refreshes its sign-in must be able to save it: Claude
// Code keeps its OAuth tokens in the login keychain and rewrites them there.
func TestCLISandboxSavesKeychainSignIn(t *testing.T) {
	const service = "lyk-sandbox-keychain-probe"
	security := func(args ...string) ([]byte, error) {
		return exec.Command("/usr/bin/security", args...).CombinedOutput()
	}
	security("delete-generic-password", "-s", service)
	if out, err := security("add-generic-password", "-s", service, "-a", "probe", "-w", "before"); err != nil {
		t.Skipf("no writable login keychain: %v %s", err, out)
	}
	t.Cleanup(func() { security("delete-generic-password", "-s", service) })
	run := filepath.Join(t.TempDir(), ".lyk", "coresearch", "chat")
	must(t, os.MkdirAll(run, 0700))
	home, err := os.UserHomeDir()
	must(t, err)
	paths, err := agent.SandboxPaths("claude", filepath.Join(home, ".claude"), run, "", "darwin")
	must(t, err)
	d := &daemon{cfg: Config{Home: t.TempDir()}}
	cmd, err := d.cliSandbox(run, nil, false)(t.Context(), paths, "/usr/bin/security", "add-generic-password", "-U", "-s", service, "-a", "probe", "-w", "after")
	must(t, err)
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("sandboxed CLI cannot save its sign-in: %v %s", err, out)
	}
	if out, err := security("find-generic-password", "-s", service, "-w"); err != nil || string(out) != "after\n" {
		t.Fatalf("saved sign-in not persisted: %v %q", err, out)
	}
}
