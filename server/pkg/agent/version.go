package agent

import (
	"context"
	"os/exec"
	"strings"
	"time"
)

// VersionTimeout bounds a CLI's --version probe.
var VersionTimeout = 10 * time.Second

// DetectVersion runs bin --version and returns VersionLine of what it
// printed. Any failure (missing binary, non-zero exit, timeout) gives "": a
// version is shown to people, never required.
func DetectVersion(ctx context.Context, bin string) string {
	return probe(ctx, bin, "--version")
}

// probe runs name with args and returns VersionLine of what it printed, or ""
// on any failure.
func probe(ctx context.Context, name string, args ...string) string {
	ctx, cancel := context.WithTimeout(ctx, VersionTimeout)
	defer cancel()
	cmd := exec.CommandContext(ctx, name, args...)
	// A descendant holding stdout open must not outlast the deadline.
	cmd.WaitDelay = time.Second
	out, err := cmd.Output()
	if err != nil {
		return ""
	}
	return VersionLine(string(out))
}

// VersionLine keeps the first line of a version report, trimmed and capped at
// 64 characters.
func VersionLine(s string) string {
	line, _, _ := strings.Cut(s, "\n")
	line = strings.TrimSpace(line)
	if r := []rune(line); len(r) > 64 {
		line = strings.TrimSpace(string(r[:64]))
	}
	return line
}
