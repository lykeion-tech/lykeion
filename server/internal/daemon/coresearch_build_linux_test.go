//go:build linux

package daemon

import (
	"os"
	"path/filepath"
	"testing"

	"lykeion/server/internal/daemon/sandbox"
)

func TestBuildLinuxSandboxPolicy(t *testing.T) {
	dir, tmp := t.TempDir(), t.TempDir()
	outside := filepath.Join(t.TempDir(), "protected")
	if e := os.WriteFile(outside, []byte("private"), 0600); e != nil {
		t.Fatal(e)
	}
	_, root, e := buildTools(t.Context())
	if e != nil {
		t.Fatal(e)
	}
	p, e := buildTestDaemon(t).buildPolicy(dir, tmp, root)
	if e != nil {
		t.Fatal(e)
	}
	cmd, e := p.Command(t.Context(), "/bin/sh", "-c", `cat "$1"; echo bad > "$1"`, "probe", outside)
	if e != nil {
		t.Fatal(e)
	}
	defer sandbox.CloseCommand(cmd)
	cmd.Env = []string{"PATH=/usr/bin:/bin"}
	if output, e := cmd.CombinedOutput(); e == nil {
		t.Fatalf("policy escape: %s", output)
	}
	b, e := os.ReadFile(outside)
	if e != nil || string(b) != "private" {
		t.Fatal("outside modified", e)
	}
}
