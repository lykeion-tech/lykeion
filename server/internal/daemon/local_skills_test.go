package daemon

import (
	"bytes"
	"os"
	"path/filepath"
	"testing"

	"lykeion/server/internal/skill"
)

func localSkillFixture(t *testing.T, dir, name string) {
	t.Helper()
	if err := os.MkdirAll(dir, 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "SKILL.md"), []byte("---\nname: "+name+"\ndescription: Local research\n---\n"), 0600); err != nil {
		t.Fatal(err)
	}
}
func TestRuntimeSkillRootsAndDeduplication(t *testing.T) {
	home := t.TempDir()
	codex := t.TempDir()
	for harness, want := range map[string]string{"claude": filepath.Join(home, ".claude", "skills"), "codex": filepath.Join(codex, "skills")} {
		roots, err := localSkillRoots(harness, home, codex)
		if err != nil || len(roots) != 2 || roots[0].path != want || roots[1].path != filepath.Join(home, ".agents", "skills") {
			t.Fatalf("%s roots: %+v %v", harness, roots, err)
		}
	}
	provider := filepath.Join(home, ".claude", "skills", "same")
	universal := filepath.Join(home, ".agents", "skills", "same")
	localSkillFixture(t, provider, "provider-wins")
	localSkillFixture(t, universal, "universal-loses")
	localSkillFixture(t, filepath.Join(home, ".agents", "skills", "nested", "other"), "other-method")
	raw := []byte{0, 255, 128, 42}
	if err := os.WriteFile(filepath.Join(provider, ".hidden"), raw, 0600); err != nil {
		t.Fatal(err)
	}
	list, err := listLocalSkills("claude", home, "")
	if err != nil || len(list) != 2 {
		t.Fatalf("inventory: %+v %v", list, err)
	}
	var key string
	for _, s := range list {
		if s.Name == "provider-wins" {
			key = s.Key
			if s.FileCount != 2 {
				t.Fatal("hidden file skipped")
			}
		}
	}
	if key == "" {
		t.Fatal("provider did not win")
	}
	b, _, err := loadLocalSkill("claude", home, "", key)
	if err != nil || !bytes.Equal(b.Files[".hidden"], raw) {
		t.Fatalf("bytes changed: %+v %v", b, err)
	}
	for _, bad := range []string{"../secret", "/tmp/secret", "same/../../secret"} {
		if _, _, err := loadLocalSkill("claude", home, "", bad); err == nil {
			t.Fatalf("accepted path %q", bad)
		}
	}
}
func TestRuntimeSkillPinnedRootAndUnsafeChildren(t *testing.T) {
	home := t.TempDir()
	target := t.TempDir()
	localSkillFixture(t, target, "linked-method")
	root := filepath.Join(home, ".claude", "skills")
	if err := os.MkdirAll(root, 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(target, filepath.Join(root, "linked")); err != nil {
		t.Fatal(err)
	}
	b, _, err := loadLocalSkill("claude", home, "", "linked")
	if err != nil || len(b.Files) != 1 {
		t.Fatalf("selected symlink root: %v", err)
	}
	outside := filepath.Join(t.TempDir(), "secret")
	if err := os.WriteFile(outside, []byte("secret"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outside, filepath.Join(target, "escape")); err != nil {
		t.Fatal(err)
	}
	if _, _, err := loadLocalSkill("claude", home, "", "linked"); err == nil {
		t.Fatal("escaping child accepted")
	}
	if err := os.Remove(filepath.Join(target, "escape")); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(target, "oversized"), make([]byte, skill.FileBytes+1), 0600); err != nil {
		t.Fatal(err)
	}
	if _, _, err := loadLocalSkill("claude", home, "", "linked"); err == nil {
		t.Fatal("oversized file silently skipped")
	}
}

func TestRuntimeSkillCodexHomeFollowsProviderEnvironment(t *testing.T) {
	roots, err := localSkillRoots("codex", t.TempDir(), "  relative-config  ")
	if err != nil || roots[0].path != filepath.Join("relative-config", "skills") {
		t.Fatalf("CODEX_HOME source semantics: %+v %v", roots, err)
	}
}
