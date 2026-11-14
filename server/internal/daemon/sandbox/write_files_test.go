package sandbox

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestWriteFilesExactLifecycle(t *testing.T) {
	h := t.TempDir()
	dir := filepath.Join(h, ".codex")
	if err := os.Mkdir(dir, 0700); err != nil {
		t.Fatal(err)
	}
	target := filepath.Join(dir, "state_5.sqlite-wal")
	p := Policy{Home: h, HomeRead: true, WriteFiles: []string{target}}
	prof, err := p.seatbeltProfile()
	if err != nil {
		t.Fatal(err)
	}
	parent, err := filepath.EvalSymlinks(dir)
	if err != nil {
		t.Fatal(err)
	}
	exact := filepath.Join(parent, filepath.Base(target))
	if !strings.Contains(prof, "allow file-read* file-write* (literal "+sbplString(exact)+")") {
		t.Fatal(prof)
	}
	if strings.Contains(prof, "file-write* (subpath "+sbplString(parent)+")") {
		t.Fatal("broad parent grant")
	}
	if _, err := p.bwrapArgs("true"); err == nil {
		t.Fatal("Linux must reject lifecycle grants")
	}
	if _, err := os.Stat(target); !os.IsNotExist(err) {
		t.Fatal("policy precreated state")
	}
}

func TestWriteFilesRejectUnsafePaths(t *testing.T) {
	for _, kind := range []string{"link", "directory", "control", "hidden", "readonly", "missing-parent", "blocked", "missing-hidden"} {
		t.Run(kind, func(t *testing.T) {
			h := t.TempDir()
			dir := filepath.Join(h, "state")
			os.Mkdir(dir, 0700)
			target := filepath.Join(dir, "db")
			p := Policy{Home: h, HomeRead: true, WriteFiles: []string{target}}
			switch kind {
			case "link":
				os.Symlink(filepath.Join(h, "other"), target)
			case "directory":
				os.Mkdir(target, 0700)
			case "control":
				p.WriteFiles = []string{filepath.Join(dir, "bad\nname")}
			case "hidden":
				os.WriteFile(target, nil, 0600)
				p.Hidden = []string{dir}
			case "readonly":
				os.WriteFile(target, nil, 0600)
				p.Write = []string{dir}
				p.ReadOnly = []string{target}
			case "missing-parent":
				p.WriteFiles = []string{filepath.Join(dir, "absent", "db")}
			case "missing-hidden":
				p.Hidden = []string{target}
			case "blocked":
				os.WriteFile(target, nil, 0600)
				p.Write = []string{dir}
				p.Blocked = []string{target}
			}
			if _, err := p.seatbeltProfile(); err == nil {
				t.Fatal("accepted unsafe file grant")
			}
		})
	}
}

func TestSQLiteRejectsUnsafeParents(t *testing.T) {
	h := tempDir(t)
	for _, name := range []string{"link", "quote\"name", "control\nname"} {
		path := filepath.Join(h, name)
		if name == "link" {
			os.Symlink(h, path)
		} else {
			os.Mkdir(path, 0700)
		}
		p := Policy{Home: h, HomeRead: true, WriteSQLite: []string{path}}
		if _, err := p.seatbeltProfile(); err == nil {
			t.Fatalf("unsafe SQLite parent: %q", name)
		}
	}
	p := Policy{Home: h, HomeRead: true, WriteSQLite: []string{h}}
	if _, err := p.bwrapArgs("true"); err == nil {
		t.Fatal("Linux accepted SQLite grants")
	}
	p.Hidden = []string{filepath.Join(h, "state_1.sqlite")}
	if _, err := p.seatbeltProfile(); err == nil {
		t.Fatal("missing hidden SQLite filename accepted")
	}
}
