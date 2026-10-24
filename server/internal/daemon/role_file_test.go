package daemon

import (
	"context"
	"lykeion/server/pkg/agent"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestRoleInstructionsArriveInTheCLIsOwnFile(t *testing.T) {
	f := fixture(t)
	var prompt, before string
	f.cfg.NewBackend = func(h, b string) (agent.Backend, error) {
		real, err := agent.New(h, b)
		if err != nil {
			return nil, err
		}
		return backendFunc(func(ctx context.Context, p string, o agent.ExecOptions) (*agent.Session, error) {
			prompt = p
			md, _ := os.ReadFile(filepath.Join(o.Dir, "CLAUDE.md"))
			before = string(md)
			return real.Execute(ctx, p, o)
		}), nil
	}
	cancel, done := startDaemon(t, f.cfg)
	waitFor(t, func() bool { return f.status(t) == "completed" })
	stopDaemon(t, cancel, done)
	if before != "# Scoper" {
		t.Fatalf("CLAUDE.md before launch %q", before)
	}
	if strings.Contains(prompt, "SKILL.md") || strings.Contains(prompt, "CLAUDE.md") {
		t.Fatalf("prompt still points at a file: %q", prompt)
	}
	if _, err := os.Stat(filepath.Join(f.cfg.Home, f.id.String()+"-1", "SKILL.md")); !os.IsNotExist(err) {
		t.Fatalf("role instructions still in SKILL.md: %v", err)
	}
	if roleFile("codex") != "AGENTS.md" || roleFile("claude") != "CLAUDE.md" {
		t.Fatal(roleFile("codex"), roleFile("claude"))
	}
}
