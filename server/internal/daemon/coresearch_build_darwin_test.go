//go:build darwin

package daemon

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"

	"lykeion/server/internal/daemon/sandbox"
)

func TestBuildDarwinSandboxPolicy(t *testing.T) {
	dir, tmp := t.TempDir(), t.TempDir()
	outside := filepath.Join(t.TempDir(), "protected")
	if e := os.WriteFile(outside, []byte("private"), 0600); e != nil {
		t.Fatal(e)
	}
	ctx, cancel := context.WithTimeout(t.Context(), 10*time.Second)
	defer cancel()
	_, root, e := buildTools(ctx)
	if e != nil {
		t.Fatal(e)
	}
	p, e := buildTestDaemon(t).buildPolicy(dir, tmp, root)
	if e != nil {
		t.Fatal(e)
	}
	cmd, e := p.Command(ctx, "/bin/sh", "-c", `cat "$1"; echo bad > "$1"`, "probe", outside)
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
	if out, e := exec.Command("sw_vers", "-productVersion").Output(); e == nil {
		t.Logf("macOS %s", out)
	}
}
