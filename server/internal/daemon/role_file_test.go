package daemon

import (
	"context"
	"lykeion/server/pkg/agent"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// A Claude run reads no CLAUDE.md, its own included, so a study role run's
// role reaches the CLI through its options, once, and never through a file
// in the run folder or the prompt.
func TestStudyRoleReachesClaudeOptions(t *testing.T) {
	f := fixture(t)
	var prompt, instructions string
	var md error
	f.cfg.NewBackend = func(h, b string) (agent.Backend, error) {
		real, err := agent.New(h, b)
		if err != nil {
			return nil, err
		}
		return backendFunc(func(ctx context.Context, p string, o agent.ExecOptions) (*agent.Session, error) {
			prompt, instructions = p, o.Instructions
			_, md = os.Stat(filepath.Join(o.Dir, "CLAUDE.md"))
			return real.Execute(ctx, p, o)
		}), nil
	}
	cancel, done := startDaemon(t, f.cfg)
	waitFor(t, func() bool { return f.status(t) == "completed" })
	stopDaemon(t, cancel, done)
	if instructions != "# Scoper" {
		t.Fatalf("role in the CLI's options %q", instructions)
	}
	if !os.IsNotExist(md) {
		t.Fatalf("a CLAUDE.md nothing reads was written: %v", md)
	}
	if strings.Contains(prompt, "Scoper") || strings.Contains(prompt, "SKILL.md") || strings.Contains(prompt, "CLAUDE.md") {
		t.Fatalf("prompt carries or points at the role: %q", prompt)
	}
}
