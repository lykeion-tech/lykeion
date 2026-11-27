package agent

import (
	"archive/tar"
	"bytes"
	"context"
	"io"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

func TestSkillsDirIsTheProjectFolderEachCLIReads(t *testing.T) {
	dir := t.TempDir()
	for h, rel := range map[string]string{"claude": ".claude/skills", "codex": ".agents/skills"} {
		if got, err := SkillsDir(h, dir); err != nil || got != filepath.Join(dir, filepath.FromSlash(rel)) {
			t.Errorf("%s: %s %v", h, got, err)
		}
	}
	if _, err := SkillsDir("nope", dir); err == nil {
		t.Error("an unknown harness has a skills folder")
	}
}

// A link anywhere on the way to the skills folder, planted by code the run
// started, is refused rather than followed out of the run folder.
func TestSkillsDirRefusesALinkOnTheWay(t *testing.T) {
	for _, planted := range []string{".claude", filepath.Join(".claude", "skills")} {
		dir, outside := t.TempDir(), t.TempDir()
		if err := os.MkdirAll(filepath.Dir(filepath.Join(dir, planted)), 0o700); err != nil {
			t.Fatal(err)
		}
		if err := os.Symlink(outside, filepath.Join(dir, planted)); err != nil {
			t.Fatal(err)
		}
		if got, err := SkillsDir("claude", dir); err == nil {
			t.Errorf("%s: link followed to %s", planted, got)
		}
	}
	// A folder that doesn't exist yet is fine: the caller makes it.
	if _, err := SkillsDir("claude", t.TempDir()); err != nil {
		t.Fatal(err)
	}
}

// stage writes what the daemon stages for a run: two pinned skills.
func stage(t *testing.T) string {
	t.Helper()
	s := filepath.Join(t.TempDir(), "stage")
	for _, n := range []string{"scanpy", "rdkit"} {
		name := PinnedName(n)
		os.MkdirAll(filepath.Join(s, name, "references"), 0o700)
		os.WriteFile(filepath.Join(s, name, "SKILL.md"), []byte("---\nname: "+name+"\ndescription: d\n---\n"), 0o600)
		os.WriteFile(filepath.Join(s, name, "references", "a.md"), []byte("a"), 0o600)
	}
	return s
}

// The fake CLI lists the folder the real CLI reads skills from.
func TestStagedSkillsReachTheFolderTheCLIReadsAndNeverTheAccountsHome(t *testing.T) {
	real := t.TempDir()
	t.Setenv("HOME", real)
	os.Unsetenv("CLAUDE_CONFIG_DIR")
	for _, c := range []struct{ harness, home, cred, terminal, read string }{
		{"claude", ".claude", claudeCredFile, `{"type":"result","is_error":false}`, ".claude/skills"},
		{"codex", ".codex", codexCredFile, `{"type":"turn.completed","usage":{}}`, ".agents/skills"},
	} {
		t.Run(c.harness, func(t *testing.T) {
			own := filepath.Join(real, c.home, "skills")
			os.MkdirAll(filepath.Join(own, "scanpy"), 0o700)
			os.WriteFile(filepath.Join(own, "scanpy", "SKILL.md"), []byte("mine"), 0o600)
			os.WriteFile(filepath.Join(real, c.home, c.cred), []byte("secret"), 0o600)
			seen := filepath.Join(t.TempDir(), "seen")
			bin := script(t, `ls -A `+c.read+` > `+seen+"\necho '"+c.terminal+"'")
			b, _ := New(c.harness, bin)
			dir := t.TempDir()
			staged := stage(t)
			for i := range 2 {
				s, err := b.Execute(context.Background(), "p", ExecOptions{Sandbox: testSandbox, Dir: dir, SkillsFrom: staged})
				if err != nil {
					t.Fatalf("run %d: %v", i+1, err)
				}
				if _, r := collect(t, s); r.Err != nil {
					t.Fatalf("run %d: %v", i+1, r.Err)
				}
				got, _ := os.ReadFile(seen)
				if names := strings.Fields(string(got)); !slices.Equal(names, []string{PinnedName("rdkit"), PinnedName("scanpy")}) {
					t.Fatalf("run %d: the CLI read %q from %s", i+1, got, c.read)
				}
				// What an agent leaves in the placed folder is gone next time.
				placed, _ := SkillsDir(c.harness, dir)
				os.MkdirAll(filepath.Join(placed, "lyk-stale"), 0o700)
			}
			es, _ := os.ReadDir(own)
			if b, _ := os.ReadFile(filepath.Join(own, "scanpy", "SKILL.md")); len(es) != 1 || string(b) != "mine" {
				t.Fatalf("the account's own skills changed: %v %q", es, b)
			}
		})
	}
}

func TestRemovedHarnessHasNoSkillsFolder(t *testing.T) {
	for _, h := range []string{"cursor", "antigravity"} {
		if _, err := SkillsDir(h, t.TempDir()); err == nil {
			t.Fatalf("removed harness %s accepted", h)
		}
	}
}

func TestAPinIsRenamedInItsFrontmatter(t *testing.T) {
	dir := filepath.Join(t.TempDir(), PinnedName("scanpy"))
	os.MkdirAll(dir, 0o700)
	os.WriteFile(filepath.Join(dir, "SKILL.md"), []byte("---\nname: scanpy\ndescription: \"Single-cell: name: scanpy\"\n---\n\n# name: scanpy\n"), 0o600)
	if err := RenameSkill(dir, "scanpy", PinnedName("scanpy")); err != nil {
		t.Fatal(err)
	}
	b, _ := os.ReadFile(filepath.Join(dir, "SKILL.md"))
	if want := "---\nname: lyk-scanpy\ndescription: \"Single-cell: name: scanpy\"\n---\n\n# name: scanpy\n"; string(b) != want {
		t.Fatalf("SKILL.md:\n%s", b)
	}
	os.WriteFile(filepath.Join(dir, "SKILL.md"), []byte("---\nname: other\n---\n"), 0o600)
	if err := RenameSkill(dir, "scanpy", PinnedName("scanpy")); err == nil {
		t.Fatal("a skill that names something else was renamed")
	}
}

func tarOf(t *testing.T, hs ...*tar.Header) io.Reader {
	t.Helper()
	var buf bytes.Buffer
	tw := tar.NewWriter(&buf)
	for _, h := range hs {
		if err := tw.WriteHeader(h); err != nil {
			t.Fatal(err)
		}
		if h.Typeflag == tar.TypeReg {
			tw.Write(bytes.Repeat([]byte("x"), int(h.Size)))
		}
	}
	tw.Close()
	return &buf
}

func TestUntarRefusesAnythingButFilesInsideTheFolder(t *testing.T) {
	root := t.TempDir()
	dst := filepath.Join(root, "s")
	if err := Untar(tarOf(t, &tar.Header{Typeflag: tar.TypeDir, Name: "references/", Mode: 0o755},
		&tar.Header{Typeflag: tar.TypeReg, Name: "references/a.md", Size: 3, Mode: 0o644}), dst); err != nil {
		t.Fatal(err)
	}
	if b, _ := os.ReadFile(filepath.Join(dst, "references", "a.md")); string(b) != "xxx" {
		t.Fatalf("unpacked %q", b)
	}
	for _, bad := range []*tar.Header{
		{Typeflag: tar.TypeReg, Name: "../escape", Size: 1},
		{Typeflag: tar.TypeReg, Name: "/tmp/escape", Size: 1},
		{Typeflag: tar.TypeReg, Name: "a/../../escape", Size: 1},
		{Typeflag: tar.TypeSymlink, Name: "link", Linkname: "/etc/hosts"},
		{Typeflag: tar.TypeLink, Name: "hard", Linkname: "SKILL.md"},
	} {
		if err := Untar(tarOf(t, bad), filepath.Join(t.TempDir(), "s")); err == nil {
			t.Errorf("%s (%c) was unpacked", bad.Name, bad.Typeflag)
		}
	}
	if _, err := os.Stat(filepath.Join(root, "escape")); err == nil {
		t.Fatal("a file landed outside the skill's folder")
	}
	if !ValidSkillName("13c-metabolic-flux") || ValidSkillName("../x") || ValidSkillName("") || ValidSkillName("A") || ValidSkillName(strings.Repeat("a", 61)) {
		t.Fatal("ValidSkillName")
	}
}

func TestRoleRunsMayCallTheDoorAndNotSources(t *testing.T) {
	role := strings.Split(claudeTools, ",")
	if !slices.Contains(role, "Skill") || !slices.Contains(role, "mcp__lyk_door") || slices.Contains(role, "mcp__lyk_sources") {
		t.Fatal(claudeTools)
	}
}

func RenameSkill(folder, from, to string) error {
	root, err := os.OpenRoot(folder)
	if err != nil {
		return err
	}
	defer root.Close()
	return RenameSkillRoot(root, from, to)
}
func Untar(r io.Reader, dst string) error {
	if err := os.MkdirAll(dst, 0755); err != nil {
		return err
	}
	root, err := os.OpenRoot(dst)
	if err != nil {
		return err
	}
	defer root.Close()
	return UntarRoot(r, root)
}
