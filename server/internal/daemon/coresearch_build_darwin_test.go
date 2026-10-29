//go:build darwin

package daemon

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
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
	if os.Getenv("LYK_NO_TEX") == "1" {
		t.Skip("explicit LYK_NO_TEX=1")
	}
	_, _, root, e := buildTools(ctx)
	if e != nil {
		t.Fatal(e)
	}
	p, e := buildTestDaemon(t).buildPolicy(dir, tmp, root)
	if e != nil {
		t.Fatal(e)
	}
	// Reads and writes are probed separately: a combined script fails on the
	// write alone and would hide a leaked read.
	for _, probe := range []string{`cat "$1"`, `echo bad > "$1"`} {
		cmd, e := p.Command(ctx, "/bin/sh", "-c", probe, "probe", outside)
		if e != nil {
			t.Fatal(e)
		}
		cmd.Env = []string{"PATH=/usr/bin:/bin"}
		output, e := cmd.CombinedOutput()
		sandbox.CloseCommand(cmd)
		if e == nil || strings.Contains(string(output), "private") {
			t.Fatalf("policy escape by %q: %s", probe, output)
		}
	}
	b, e := os.ReadFile(outside)
	if e != nil || string(b) != "private" {
		t.Fatal("outside modified", e)
	}
	if out, e := exec.Command("sw_vers", "-productVersion").Output(); e == nil {
		t.Logf("macOS %s", out)
	}
}
