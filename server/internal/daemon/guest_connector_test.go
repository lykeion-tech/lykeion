package daemon

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"lykeion/server/internal/pack"
	"lykeion/server/pkg/agent"
)

func TestGuestCommandConnectorIsConfined(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("CLAUDE_CODE_OAUTH_TOKEN", "owner-cli-fixture")
	t.Setenv("CODEX_HOME", filepath.Join(t.TempDir(), "custom-codex"))
	t.Setenv("CLAUDE_CONFIG_DIR", filepath.Join(t.TempDir(), "custom-claude"))
	runRoot := t.TempDir()
	run := filepath.Join(runRoot, "own")
	must(t, os.MkdirAll(run, 0700))
	private := []string{filepath.Join(home, "Documents", "secret"), filepath.Join(home, ".codex", "auth.json"), filepath.Join(home, ".claude", "projects", "secret"), filepath.Join(os.Getenv("CODEX_HOME"), "auth.json"), filepath.Join(os.Getenv("CLAUDE_CONFIG_DIR"), ".credentials.json"), filepath.Join(runRoot, "other", "secret")}
	for _, path := range private {
		must(t, os.MkdirAll(filepath.Dir(path), 0700))
		must(t, os.WriteFile(path, []byte("private-fixture"), 0600))
	}
	self, err := os.Executable()
	must(t, err)
	l := newLykeion(t)
	d := newRunDoor(t, l, run, func(d *Door) {
		d.Guest = true
		d.home = home
		d.PrivateRoots = []string{runRoot}
		d.Connectors = []pack.Connector{{Name: "stub", Command: []string{self}, Env: map[string]string{"LYK_DOOR_STUB": "1", "GUEST_CONNECTOR_PROBE": "1", "GUEST_PROBE_PATHS": strings.Join(private, string(os.PathListSeparator))}}}
		d.Child = func() agent.ExecOptions {
			return agent.ExecOptions{Guest: true, Dir: run, ConfigHome: filepath.Join(home, "guest"), Env: []string{"CLAUDE_CODE_OAUTH_TOKEN=guest-cli-fixture", "LYK_TOKEN=ltk_run"}}
		}
	})
	got, err := callTool(t, d, "stub", "echo", map[string]any{"q": "guest"})
	must(t, err)
	if got != "call:c1\necho guest" {
		t.Fatal(got)
	}
	data, err := os.ReadFile(filepath.Join(d.connectorDir, "stub.guest"))
	must(t, err)
	var result struct {
		Readable      []string
		HasCLIAuth    bool
		HasCLIConfig  bool
		HomeIsPrivate bool
	}
	must(t, json.Unmarshal(data, &result))
	if len(result.Readable) != 0 || result.HasCLIAuth || result.HasCLIConfig || !result.HomeIsPrivate {
		t.Fatalf("guest connector escaped: %+v", result)
	}
	if _, err := os.Stat(filepath.Join(d.connectorDir, "stub.pid")); err != nil {
		t.Fatal("connector cannot use its private cwd")
	}
}

func TestGuestCommandConnectorFailsWithoutSandbox(t *testing.T) {
	self, err := os.Executable()
	must(t, err)
	d := newRunDoor(t, newLykeion(t), t.TempDir(), func(d *Door) {
		d.Guest = true
		d.SandboxErr = errors.New("fixture unavailable")
		d.Connectors = []pack.Connector{{Name: "stub", Command: []string{self}, Env: map[string]string{"LYK_DOOR_STUB": "1"}}}
	})
	if _, err := callTool(t, d, "stub", "echo", nil); err == nil {
		t.Fatal("guest connector ran without a sandbox")
	}
	if _, err := os.Stat(filepath.Join(d.connectorDir, "stub.pid")); !os.IsNotExist(err) {
		t.Fatal("guest connector launched before sandbox check")
	}
}

func TestGuestCommandConnectorRejectsWritableExecutableSymlinkEscape(t *testing.T) {
	run := t.TempDir()
	alias := filepath.Join(run, "connector")
	must(t, os.Symlink("/bin/sh", alias))
	d := newRunDoor(t, newLykeion(t), run, func(d *Door) { d.Guest = true })
	if _, err := d.guestConnectorCommand(t.Context(), alias, "-c", "true"); err == nil {
		t.Fatal("writable command symlink escaped its grant")
	}
}
