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
func TestCodexSandboxHidesPersonalFiles(t *testing.T) {
	personal := []string{"memories", "AGENTS.md", "plugins", "skills", "rules", "hooks", "agents", "memories.lock", "memories_v2.lock", "history.jsonl", "archived_sessions", "prompts", "transcription-history.jsonl"}
	for _, goos := range []string{"darwin", "linux"} {
		h := t.TempDir()
		for _, dir := range []string{"memories", "plugins/cache", "skills", "rules", "hooks", "agents", "archived_sessions", "prompts"} {
			if err := os.MkdirAll(filepath.Join(h, dir), 0700); err != nil {
				t.Fatal(err)
			}
		}
		for _, file := range []string{"AGENTS.md", "memories_1.sqlite", "memories_1.sqlite-wal", "memories.lock", "memories_v2.lock", "history.jsonl", "transcription-history.jsonl", "state_5.sqlite"} {
			if err := os.WriteFile(filepath.Join(h, file), []byte("personal"), 0600); err != nil {
				t.Fatal(err)
			}
		}
		p, err := SandboxPaths("codex", h, "/r", "", goos)
		if err != nil {
			t.Fatal(err)
		}
		for _, name := range personal {
			if !slices.Contains(p.Hidden, filepath.Join(h, name)) {
				t.Errorf("%s: %s is not hidden", goos, name)
			}
		}
		for _, kept := range []string{"memories_v2", "memories_1.sqlite", "memories_1.sqlite-wal", "state_5.sqlite", "sessions"} {
			if slices.Contains(p.Hidden, filepath.Join(h, kept)) {
				t.Errorf("%s: %s is hidden", goos, kept)
			}
		}
		if slices.Contains(p.Write, filepath.Join(h, "plugins", "cache")) {
			t.Errorf("%s: plugin cache writable: %v", goos, p.Write)
		}
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
	if err := os.Symlink(other, filepath.Join(h, "sessions")); err != nil {
		t.Fatal(err)
	}
	if _, err := SandboxPaths("codex", h, "/run", "", "darwin"); err == nil {
		t.Fatal("accepted symlinked state")
	}
	if entries, _ := os.ReadDir(other); len(entries) != 0 {
		t.Fatal("created directory through a link")
	}
}
func TestClaudeSandboxHidesPersonalFiles(t *testing.T) {
	for _, goos := range []string{"darwin", "linux"} {
		h := t.TempDir()
		for _, dir := range []string{"plugins/cache", "skills", "agents", "commands", "rules", "output-styles", "workflows", "routines", "sessions"} {
			if err := os.MkdirAll(filepath.Join(h, dir), 0700); err != nil {
				t.Fatal(err)
			}
		}
		for _, file := range []string{"CLAUDE.md", "history.jsonl", ".credentials.json", "settings.json"} {
			if err := os.WriteFile(filepath.Join(h, file), []byte("personal"), 0600); err != nil {
				t.Fatal(err)
			}
		}
		p, err := SandboxPaths("claude", h, "/r", "", goos)
		if err != nil {
			t.Fatal(err)
		}
		for _, name := range []string{"CLAUDE.md", "plugins", "skills", "agents", "commands", "rules", "output-styles", "workflows", "routines", "history.jsonl"} {
			if !slices.Contains(p.Hidden, filepath.Join(h, name)) {
				t.Errorf("%s: %s is not hidden", goos, name)
			}
		}
		for _, kept := range []string{".credentials.json", "settings.json", "sessions"} {
			if slices.Contains(p.Hidden, filepath.Join(h, kept)) {
				t.Errorf("%s: %s is hidden", goos, kept)
			}
		}
		if goos == "linux" && !slices.Equal(p.WriteData, []string{filepath.Join(h, ".credentials.json")}) {
			t.Errorf("sign-in not writable: %v", p.WriteData)
		}
		if len(p.Blocked) != 0 {
			t.Errorf("%s: blocked %v", goos, p.Blocked)
		}
	}
}

func TestClaudeGetsRefreshLocks(t *testing.T) {
	for _, goos := range []string{"darwin", "linux"} {
		h := filepath.Join(t.TempDir(), ".claude")
		p, err := SandboxPaths("claude", h, "/r", "", goos)
		if err != nil {
			t.Fatal(err)
		}
		want := []string{filepath.Join(h, ".oauth_refresh.lock"), filepath.Join(filepath.Dir(h), ".claude.lock")}
		if !slices.Equal(p.LockDirs, want) {
			t.Errorf("%s: lock dirs %v, want %v", goos, p.LockDirs, want)
		}
		if _, err := os.Lstat(want[0]); !os.IsNotExist(err) {
			t.Errorf("%s: lock pre-created", goos)
		}
	}
}
