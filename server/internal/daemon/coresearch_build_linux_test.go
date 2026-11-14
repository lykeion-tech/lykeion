//go:build linux

package daemon

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"lykeion/server/internal/daemon/sandbox"
)

func TestBuildLinuxSandboxPolicy(t *testing.T) {
	dir, tmp := t.TempDir(), t.TempDir()
	outside := filepath.Join(t.TempDir(), "protected")
	if e := os.WriteFile(outside, []byte("private"), 0600); e != nil {
		t.Fatal(e)
	}
	if os.Getenv("LYK_NO_TEX") == "1" {
		t.Skip("explicit LYK_NO_TEX=1")
	}
	_, _, root, e := buildTools(t.Context())
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
		cmd, e := p.Command(t.Context(), "/bin/sh", "-c", probe, "probe", outside)
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
}
