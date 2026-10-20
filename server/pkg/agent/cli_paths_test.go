package agent

import (
	"os"
	"path/filepath"
	"slices"
	"testing"
)

func TestClaudeProjectDirMatchesClaude(t *testing.T) {
	for in, want := range map[string]string{"/Users/x/Library/Application Support/lyk/run 1": "-Users-x-Library-Application-Support-lyk-run-1", "/private/tmp/claude-501/a.b_c": "-private-tmp-claude-501-a-b-c", "/tmp/é": "-tmp--"} {
		if got := ClaudeProjectDir("/h/.claude", in); got != filepath.Join("/h/.claude/projects", want) {
			t.Fatal(got, want)
		}
	}
}
func TestClaudePathsGrantSessionFolderAndResumedOne(t *testing.T) {
	h := t.TempDir()
	old := filepath.Join(h, "projects", "-old")
	os.MkdirAll(old, 0700)
	os.WriteFile(filepath.Join(old, "abc.jsonl"), nil, 0600)
	for _, platform := range []string{"darwin", "linux"} {
		p, err := SandboxPaths("claude", h, "/r/new", "abc", platform)
		if err != nil {
			t.Fatal(err)
		}
		if !slices.Equal(p.Write, []string{filepath.Join(h, "projects", "-r-new"), old}) || len(p.Blocked) != 0 || p.Keychain != (platform == "darwin") {
			t.Fatal(p)
		}
		if platform == "linux" && !slices.Equal(p.WriteData, []string{filepath.Join(h, ".credentials.json")}) {
			t.Fatal(p)
		}
	}
}
func TestCodexPaths(t *testing.T) {
	h := t.TempDir()
	os.WriteFile(filepath.Join(h, "state_5.sqlite"), nil, 0600)
	p, err := SandboxPaths("codex", h, "/r", "", "darwin")
	if err != nil {
		t.Fatal(err)
	}
	if slices.Contains(p.Write, h) || !slices.Equal(p.WriteSQLite, []string{h}) || !p.Keychain {
		t.Fatal(p)
	}
	if _, err := os.Stat(filepath.Join(h, "auth.json")); !os.IsNotExist(err) {
		t.Fatal("must not precreate JSON")
	}
	p, err = SandboxPaths("codex", h, "/r", "", "linux")
	if err != nil || !slices.Equal(p.Write, []string{h}) || len(p.Blocked) != 6 || len(p.WriteFiles) != 0 {
		t.Fatal(p, err)
	}
}
func TestPlaceBlockedNeverOverwrites(t *testing.T) {
	h := t.TempDir()
	config := filepath.Join(h, "config.toml")
	os.WriteFile(config, []byte("keep"), 0600)
	p := CLIPaths{Blocked: []string{config, filepath.Join(h, "hooks"), filepath.Join(h, "AGENTS.md")}}
	if err := PlaceBlocked(p); err != nil {
		t.Fatal(err)
	}
	b, _ := os.ReadFile(config)
	if string(b) != "keep" {
		t.Fatal(string(b))
	}
	if fi, err := os.Stat(filepath.Join(h, "AGENTS.md")); err != nil || !fi.Mode().IsRegular() || fi.Size() != 0 {
		t.Fatal(fi, err)
	}
	fi, err := os.Stat(filepath.Join(h, "hooks"))
	if err != nil || !fi.IsDir() {
		t.Fatal(fi, err)
	}
}

func TestClaudeBlockedConfigOutsideWrite(t *testing.T) {
	h := t.TempDir()
	p, err := SandboxPaths("claude", h, "/run", "", "darwin")
	if err != nil {
		t.Fatal(err)
	}
	if len(p.Blocked) != 0 || len(p.WriteData) != 0 || len(p.WriteFiles) != 0 || len(p.WriteSQLite) != 0 || !slices.Equal(p.Write, []string{filepath.Join(h, "projects", "-run")}) {
		t.Fatal(p)
	}
}
func TestCLIPathsRejectRedirectedState(t *testing.T) {
	h := t.TempDir()
	other := t.TempDir()
	if err := os.Symlink(other, filepath.Join(h, "plugins")); err != nil {
		t.Fatal(err)
	}
	if _, err := SandboxPaths("codex", h, "/run", "", "darwin"); err == nil {
		t.Fatal("accepted symlinked state")
	}
	if _, err := os.Stat(filepath.Join(other, "cache")); !os.IsNotExist(err) {
		t.Fatal("created directory through a link")
	}
}
