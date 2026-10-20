package sandbox

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func TestDoorPolicyUnchanged(t *testing.T) {
	want, err := os.ReadFile("door_profile.golden")
	if err != nil {
		t.Fatal(err)
	}
	got, err := (Policy{Home: "/"}).seatbeltProfile()
	if err != nil {
		t.Fatal(err)
	}
	// Homebrew is optional on both platforms.
	normalize := func(s string) string { return strings.ReplaceAll(s, ` (subpath "/opt/homebrew")`, "") }
	if normalize(got) != normalize(string(want)) {
		t.Fatalf("door profile changed:\n%s", got)
	}
}

func TestHomeReadExposesHomeButNotHidden(t *testing.T) {
	requireSandbox(t)
	home := tempDir(t)
	a := mkdir(t, filepath.Join(home, "a"))
	secret := mkdir(t, filepath.Join(home, "secret"))
	for _, p := range []string{filepath.Join(a, "file"), filepath.Join(secret, "key")} {
		if err := os.WriteFile(p, []byte("visible"), 0600); err != nil {
			t.Fatal(err)
		}
	}
	p := Policy{Home: home, HomeRead: true, Hidden: []string{secret, filepath.Join(home, "missing")}}
	if out, err := sandboxed(t, p, "cat "+shq(filepath.Join(a, "file"))); err != nil || out != "visible" {
		t.Fatalf("read: %v %q", err, out)
	}
	out, err := sandboxed(t, p, "cat "+shq(filepath.Join(secret, "key")))
	if err == nil || strings.Contains(out, "visible") {
		t.Fatalf("hidden read: %v %q", err, out)
	}
	refused(t, p, "echo x > "+shq(filepath.Join(a, "new")))
}

func TestBlockedPathCannotBeCreatedOrReplaced(t *testing.T) {
	requireSandbox(t)
	home := tempDir(t)
	w := mkdir(t, filepath.Join(home, "w"))
	sessions := mkdir(t, filepath.Join(w, "sessions"))
	config, hooks := filepath.Join(w, "config.toml"), filepath.Join(w, "hooks")
	if runtime.GOOS == "linux" {
		if err := os.WriteFile(config, nil, 0600); err != nil {
			t.Fatal(err)
		}
		mkdir(t, hooks)
	}
	other := filepath.Join(w, "other")
	if err := os.WriteFile(other, []byte("other"), 0600); err != nil {
		t.Fatal(err)
	}
	p := Policy{Home: home, Write: []string{w}, Blocked: []string{config, hooks}}
	for _, script := range []string{"echo x > " + shq(config), "mv " + shq(other) + " " + shq(config)} {
		refused(t, p, script)
	}
	if runtime.GOOS == "linux" {
		refused(t, p, "rmdir "+shq(hooks))
		refused(t, p, "echo x > "+shq(filepath.Join(hooks, "new")))
		if _, err := sandboxed(t, p, "mkdir "+shq(hooks)); err == nil {
			t.Fatal("created existing placeholder")
		}
	} else {
		refused(t, p, "mkdir "+shq(hooks))
	}
	if out, err := sandboxed(t, p, "echo x > "+shq(filepath.Join(sessions, "x"))); err != nil {
		t.Fatalf("session write: %v %s", err, out)
	}
}

func TestUnifiedPolicyRejectsUnsafePaths(t *testing.T) {
	home := tempDir(t)
	w := mkdir(t, filepath.Join(home, "w"))
	f := filepath.Join(home, "file")
	if err := os.WriteFile(f, nil, 0600); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(w, "link")
	if err := os.Symlink(f, link); err != nil {
		t.Fatal(err)
	}
	for name, p := range map[string]Policy{
		"blocked outside":        {Home: home, Write: []string{w}, Blocked: []string{f}},
		"blocked link":           {Home: home, Write: []string{w}, Blocked: []string{link}},
		"blocked relative":       {Home: home, Write: []string{w}, Blocked: []string{"relative"}},
		"data folder":            {Home: home, WriteData: []string{w}},
		"data missing":           {Home: home, WriteData: []string{filepath.Join(home, "missing")}},
		"data link":              {Home: home, WriteData: []string{link}},
		"hidden missing outside": {Home: home, HomeRead: true, Hidden: []string{filepath.Join(tempDir(t), "missing")}},
		"hidden home itself":     {Home: home, HomeRead: true, Hidden: []string{home}},
	} {
		t.Run(name, func(t *testing.T) {
			if _, err := p.seatbeltProfile(); err == nil {
				t.Fatal("accepted unsafe policy")
			}
		})
	}
}

func TestBlockedNestedPathCannotBeMovedWithParent(t *testing.T) {
	requireSandbox(t)
	home := tempDir(t)
	w := mkdir(t, filepath.Join(home, "w"))
	parent := mkdir(t, filepath.Join(w, "config"))
	f := filepath.Join(parent, "settings")
	if err := os.WriteFile(f, nil, 0600); err != nil {
		t.Fatal(err)
	}
	p := Policy{Home: home, Write: []string{w}, Blocked: []string{f}}
	if runtime.GOOS == "linux" {
		if _, err := p.bwrapArgs("true"); err == nil {
			t.Fatal("accepted nested blocked path")
		}
		return
	}
	refused(t, p, "mv "+shq(parent)+" "+shq(filepath.Join(w, "moved")))
	p.Write = append(p.Write, parent)
	refused(t, p, "mv "+shq(parent)+" "+shq(filepath.Join(w, "moved")))
}

func TestNewPathsRejectLinksInWriteFolders(t *testing.T) {
	home := tempDir(t)
	w := mkdir(t, filepath.Join(tempDir(t), "w"))
	f := filepath.Join(w, "file")
	if err := os.WriteFile(f, nil, 0600); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(w, "link")
	if err := os.Symlink(f, link); err != nil {
		t.Fatal(err)
	}
	for name, p := range map[string]Policy{
		"blocked":    {Home: home, Write: []string{w}, Blocked: []string{link}},
		"write data": {Home: home, Write: []string{w}, WriteData: []string{link}},
	} {
		t.Run(name, func(t *testing.T) {
			if _, err := p.seatbeltProfile(); err == nil {
				t.Fatal("accepted path through writable link")
			}
		})
	}
}
