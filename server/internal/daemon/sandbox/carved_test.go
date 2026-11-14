package sandbox

import (
	"context"
	"os"
	"path/filepath"
	"runtime"
	"testing"
)

func TestCarvedSkillsExposeSystemStateAndHideLateUserFolders(t *testing.T) {
	requireSandbox(t)
	for _, grant := range []string{"write", "sqlite"} {
		t.Run(grant, func(t *testing.T) {
			if grant == "sqlite" && runtime.GOOS != "darwin" {
				t.Skip("numeric SQLite lifecycle grants are macOS-specific; Linux uses the carved account write case")
			}
			home := tempDir(t)
			account := mkdir(t, filepath.Join(home, "account"))
			skills := mkdir(t, filepath.Join(account, "skills"))
			system := mkdir(t, filepath.Join(skills, ".system"))
			user := mkdir(t, filepath.Join(skills, "user-method"))
			if err := os.WriteFile(filepath.Join(system, "seed"), []byte("CLI state"), 0600); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(filepath.Join(user, "SKILL.md"), []byte("unmanaged"), 0600); err != nil {
				t.Fatal(err)
			}
			p := Policy{Home: home, HomeRead: true, CarvedRoots: []string{skills}, Write: []string{system}}
			if grant == "write" {
				p.Write = append(p.Write, account)
			} else {
				p.WriteSQLite = []string{account}
			}
			late := filepath.Join(skills, "late-method", "SKILL.md")
			script := "cat " + shq(filepath.Join(system, "seed")) + " >/dev/null || exit 11\necho refreshed > " + shq(filepath.Join(system, "refreshed")) + " || exit 12\nif cat " + shq(filepath.Join(user, "SKILL.md")) + " 2>/dev/null; then exit 41; fi\nif cat " + shq(late) + " 2>/dev/null; then exit 42; fi\nif echo changed > " + shq(filepath.Join(user, "SKILL.md")) + " 2>/dev/null; then exit 43; fi\n"
			if grant == "write" {
				script += "echo refreshed > " + shq(filepath.Join(account, "auth.json")) + " || exit 14\n"
			} else {
				script += "echo sqlite > " + shq(filepath.Join(account, "state_5.sqlite")) + " || exit 15\n"
			}
			cmd, err := p.Command(context.Background(), "/bin/sh", "-c", script)
			if err != nil {
				t.Fatalf("account skills need an explicit nested projection: %v", err)
			}
			if err := os.MkdirAll(filepath.Dir(late), 0700); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(late, []byte("late unmanaged"), 0600); err != nil {
				t.Fatal(err)
			}
			out, err := cmd.CombinedOutput()
			if err != nil {
				t.Fatalf("projection escaped or lost CLI state: %s %v", out, err)
			}
			data, err := os.ReadFile(filepath.Join(user, "SKILL.md"))
			if err != nil || string(data) != "unmanaged" {
				t.Fatalf("user skill mutated: %q %v", data, err)
			}
		})
	}
}

func TestCarvedRootsPreserveExplicitBoundaryValidation(t *testing.T) {
	home := tempDir(t)
	account := mkdir(t, filepath.Join(home, "account"))
	source := mkdir(t, filepath.Join(account, "skills"))
	own := mkdir(t, filepath.Join(source, ".system"))
	link := filepath.Join(account, "linked")
	if err := os.Symlink(source, link); err != nil {
		t.Fatal(err)
	}
	for _, p := range []Policy{{Home: home, CarvedRoots: []string{link}}, {Home: home, CarvedRoots: []string{home}}, {Home: home, CarvedRoots: []string{source}, Write: []string{source}}, {Home: home, CarvedRoots: []string{source}, Read: []string{source}}} {
		if _, err := p.resolve(); err == nil {
			t.Fatalf("accepted an unsafe carve: %+v", p)
		}
	}
	if _, err := (Policy{Home: home, Read: []string{account}, Write: []string{own}, CarvedRoots: []string{source}}).resolve(); err != nil {
		t.Fatalf("explicit descendant behind carve rejected: %v", err)
	}
}
