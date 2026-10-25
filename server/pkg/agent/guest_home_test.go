package agent

import (
	"os"
	"path/filepath"
	"slices"
	"testing"
)

func TestConfigHomeOverridesTheAccountHome(t *testing.T) {
	t.Setenv("CODEX_HOME", "/y")
	t.Setenv("CLAUDE_CONFIG_DIR", "/y")
	env := childEnv(ExecOptions{ConfigHome: "/x"}, "")
	for _, key := range []string{"CODEX_HOME", "CLAUDE_CONFIG_DIR"} {
		if !slices.Contains(env, key+"=/x") {
			t.Errorf("%s did not use run config home", key)
		}
	}
}
func TestConfigHomeDrivesSandboxPaths(t *testing.T) {
	home := t.TempDir()
	o := ExecOptions{ConfigHome: home, Dir: t.TempDir(), Sandbox: testSandbox}
	_, cleanup, err := prepareScratch(&o, "claude")
	if err != nil {
		t.Fatal(err)
	}
	defer cleanup(true)
	if !slices.Equal(o.CLIPaths.Write, []string{ClaudeProjectDir(home, o.Dir)}) {
		t.Fatalf("write paths = %v", o.CLIPaths.Write)
	}
}
func TestNoConfigHomeKeepsTodaysBehaviour(t *testing.T) {
	home := t.TempDir()
	t.Setenv("CLAUDE_CONFIG_DIR", home)
	if !slices.Contains(childEnv(ExecOptions{}, ""), "CLAUDE_CONFIG_DIR="+home) {
		t.Fatal("account config home lost")
	}
	o := ExecOptions{Dir: t.TempDir(), Sandbox: testSandbox}
	_, cleanup, err := prepareScratch(&o, "claude")
	if err != nil {
		t.Fatal(err)
	}
	defer cleanup(true)
	if !slices.Equal(o.CLIPaths.Write, []string{ClaudeProjectDir(home, o.Dir)}) {
		t.Fatalf("write paths = %v", o.CLIPaths.Write)
	}
}
func TestGuestHomeHoldsOnlyTheSignIn(t *testing.T) {
	owner := t.TempDir()
	for _, name := range []string{"auth.json", "config.toml", "sessions/a", "skills/s", "hooks.json"} {
		p := filepath.Join(owner, name)
		if err := os.MkdirAll(filepath.Dir(p), 0700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte("fixture"), 0644); err != nil {
			t.Fatal(err)
		}
	}
	guest := filepath.Join(t.TempDir(), "guest")
	if err := PrepareGuestHome("codex", owner, guest); err != nil {
		t.Fatal(err)
	}
	entries, err := os.ReadDir(guest)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 1 || entries[0].Name() != "auth.json" {
		t.Fatalf("unexpected guest entries: %v", entries)
	}
	for name, mode := range map[string]os.FileMode{"": 0700, "auth.json": 0600} {
		st, err := os.Stat(filepath.Join(guest, name))
		if err != nil {
			t.Fatal(err)
		}
		if st.Mode().Perm() != mode {
			t.Fatalf("%s mode = %o", name, st.Mode().Perm())
		}
	}
}
func TestGuestHomeRefreshesTheSignIn(t *testing.T) {
	owner, guest := t.TempDir(), filepath.Join(t.TempDir(), "guest")
	for _, want := range []string{"first", "second"} {
		if err := os.WriteFile(filepath.Join(owner, "auth.json"), []byte(want), 0600); err != nil {
			t.Fatal(err)
		}
		if err := PrepareGuestHome("codex", owner, guest); err != nil {
			t.Fatal(err)
		}
		got, err := os.ReadFile(filepath.Join(guest, "auth.json"))
		if err != nil {
			t.Fatal(err)
		}
		if string(got) != want {
			t.Fatal("stale sign-in")
		}
	}
}
func TestConfigHomeSessionLookup(t *testing.T) {
	owner, guest := t.TempDir(), t.TempDir()
	t.Setenv("CLAUDE_CONFIG_DIR", owner)
	p := filepath.Join(owner, "projects", "p")
	if err := os.MkdirAll(p, 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(p, "s.jsonl"), []byte("{}"), 0600); err != nil {
		t.Fatal(err)
	}
	if !SessionReachable("claude", "s") {
		t.Fatal("owner transcript missing")
	}
	if SessionReachable("claude", "s", guest) {
		t.Fatal("guest saw owner transcript")
	}
	p = filepath.Join(guest, "projects", "p")
	if err := os.MkdirAll(p, 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(p, "s.jsonl"), []byte("{}"), 0600); err != nil {
		t.Fatal(err)
	}
	if !SessionReachable("claude", "s", guest) {
		t.Fatal("guest transcript missing")
	}
}
