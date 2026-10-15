package agent

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func versionTestCLI(t *testing.T, script string) string {
	t.Helper()
	p := filepath.Join(t.TempDir(), "cli")
	if err := os.WriteFile(p, []byte("#!/bin/sh\n"+script+"\n"), 0o700); err != nil {
		t.Fatal(err)
	}
	return p
}

func TestDetectVersionKeepsTheFirstLine(t *testing.T) {
	ok := versionTestCLI(t, `[ "$1" = --version ] || exit 9
echo "  2.1.7 (Claude Code)  "
echo "second line"`)
	if got := DetectVersion(context.Background(), ok); got != "2.1.7 (Claude Code)" {
		t.Fatalf("version %q", got)
	}
	long := versionTestCLI(t, `printf '%s\n' "`+strings.Repeat("v", 100)+`"`)
	if got := DetectVersion(context.Background(), long); got != strings.Repeat("v", 64) {
		t.Fatalf("not capped: %d chars", len(got))
	}
	for name, bin := range map[string]string{
		"failing": versionTestCLI(t, `echo "1.0.0"; exit 3`),
		"missing": filepath.Join(t.TempDir(), "absent"),
	} {
		if got := DetectVersion(context.Background(), bin); got != "" {
			t.Fatalf("%s CLI: %q", name, got)
		}
	}
}

func TestDetectVersionGivesUpAfterItsTimeout(t *testing.T) {
	prev := VersionTimeout
	VersionTimeout = 200 * time.Millisecond
	t.Cleanup(func() { VersionTimeout = prev })
	slow := versionTestCLI(t, `exec sleep 5`)
	start := time.Now()
	if got := DetectVersion(context.Background(), slow); got != "" || time.Since(start) > 3*time.Second {
		t.Fatalf("slow CLI: %q after %s", got, time.Since(start))
	}
}
