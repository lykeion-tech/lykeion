package sandbox

import (
	"bufio"
	"context"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"
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
		"blocked outside":    {Home: home, Write: []string{w}, Blocked: []string{f}},
		"blocked link":       {Home: home, Write: []string{w}, Blocked: []string{link}},
		"blocked relative":   {Home: home, Write: []string{w}, Blocked: []string{"relative"}},
		"data folder":        {Home: home, WriteData: []string{w}},
		"data missing":       {Home: home, WriteData: []string{filepath.Join(home, "missing")}},
		"data link":          {Home: home, WriteData: []string{link}},
		"hidden home itself": {Home: home, HomeRead: true, Hidden: []string{home}},
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

func TestHomeReadRejectsHiddenUnderWritableAncestor(t *testing.T) {
	home := tempDir(t)
	w := mkdir(t, filepath.Join(home, "w"))
	parent := mkdir(t, filepath.Join(w, "parent"))
	secret := filepath.Join(parent, "secret")
	if err := os.WriteFile(secret, []byte("secret"), 0600); err != nil {
		t.Fatal(err)
	}
	p := Policy{Home: home, HomeRead: true, Write: []string{w}, Hidden: []string{secret}}
	if _, err := p.seatbeltProfile(); err == nil {
		t.Error("accepted hidden path below movable writable ancestor")
	}
	if _, err := p.bwrapArgs("true"); err == nil {
		t.Error("accepted hidden path below movable writable ancestor")
	}
}

func TestWriteDataCannotReopenBlockedSubtree(t *testing.T) {
	home := tempDir(t)
	w := mkdir(t, filepath.Join(home, "w"))
	config := mkdir(t, filepath.Join(w, "config"))
	f := filepath.Join(config, "file")
	if err := os.WriteFile(f, nil, 0600); err != nil {
		t.Fatal(err)
	}
	p := Policy{Home: home, Write: []string{w}, Blocked: []string{config}, WriteData: []string{f}}
	if _, err := p.seatbeltProfile(); err == nil {
		t.Error("accepted write-data under blocked path")
	}
	if _, err := p.bwrapArgs("true"); err == nil {
		t.Error("accepted write-data under blocked path")
	}
}

func TestExplicitHiddenOutsideHome(t *testing.T) {
	home := tempDir(t)
	outside := tempDir(t)
	secret := mkdir(t, filepath.Join(outside, "state"))
	write := mkdir(t, filepath.Join(outside, "run"))
	p := Policy{Home: home, HomeRead: true, Write: []string{write}, Hidden: []string{secret, filepath.Join(outside, "missing")}}
	if _, err := p.resolve(); err != nil {
		t.Fatal(err)
	}
	nested := mkdir(t, filepath.Join(write, "nested"))
	deep := mkdir(t, filepath.Join(nested, "secret"))
	p.Hidden = []string{deep}
	if _, err := p.resolve(); err == nil {
		t.Fatal("accepted movable hidden ancestor")
	}
	link := filepath.Join(outside, "link")
	if err := os.Symlink(secret, link); err != nil {
		t.Fatal(err)
	}
	p.Hidden = []string{link}
	p.Write = []string{outside}
	if _, err := p.resolve(); err == nil {
		t.Fatal("accepted link under writable parent")
	}
}

func TestRestrictedRootsRejectAncestorGrants(t *testing.T) {
	home := tempDir(t)
	parent := mkdir(t, filepath.Join(home, "shared"))
	root := mkdir(t, filepath.Join(parent, "root"))
	own := mkdir(t, filepath.Join(root, "own"))
	for _, p := range []Policy{{Home: home, HomeRead: true, Read: []string{parent}, RestrictedRoots: []string{root}}, {Home: home, HomeRead: true, Write: []string{parent}, RestrictedRoots: []string{root}}, {Home: home, HomeRead: true, Read: []string{root}, RestrictedRoots: []string{root}}, {Home: home, HomeRead: true, WriteSQLite: []string{parent}, RestrictedRoots: []string{root}}} {
		if _, err := p.resolve(); err == nil {
			t.Fatalf("accepted reopening restricted root: %+v", p)
		}
	}
	if _, err := (Policy{Home: home, HomeRead: true, RestrictedRoots: []string{root}, Write: []string{own}}).resolve(); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(home, "link")
	if err := os.Symlink(root, link); err != nil {
		t.Fatal(err)
	}
	if _, err := (Policy{Home: home, HomeRead: true, RestrictedRoots: []string{link}}).resolve(); err == nil {
		t.Fatal("accepted linked root")
	}
}

func TestPrepareRestrictedRootPreservesExistingContent(t *testing.T) {
	home := tempDir(t)
	root := filepath.Join(home, ".lyk")
	if err := PrepareRestrictedRoot(home, root); err != nil {
		t.Fatal(err)
	}
	fi, err := os.Stat(root)
	if err != nil || fi.Mode().Perm() != 0700 {
		t.Fatalf("root mode: %v %v", fi, err)
	}
	file := filepath.Join(root, "existing")
	if err := os.WriteFile(file, []byte("kept"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := PrepareRestrictedRoot(home, root); err != nil {
		t.Fatal(err)
	}
	b, err := os.ReadFile(file)
	if err != nil || string(b) != "kept" {
		t.Fatalf("changed existing content: %q %v", b, err)
	}
	link := filepath.Join(home, "alias")
	if err := os.Symlink(root, link); err != nil {
		t.Fatal(err)
	}
	if err := PrepareRestrictedRoot(home, link); err == nil {
		t.Fatal("followed linked root")
	}
	if err := PrepareRestrictedRoot(home, file); err == nil {
		t.Fatal("replaced existing file")
	}
}

func TestProtectedWriteRootCannotMoveIntoAnotherGrant(t *testing.T) {
	requireSandbox(t)
	home := tempDir(t)
	run := mkdir(t, filepath.Join(home, "run"))
	study := mkdir(t, filepath.Join(home, "study"))
	config := filepath.Join(run, "config.json")
	if err := os.WriteFile(config, []byte("protected"), 0600); err != nil {
		t.Fatal(err)
	}
	p := Policy{Home: home, HomeRead: true, Write: []string{run, study}, ReadOnly: []string{config}}
	moved := filepath.Join(study, "moved")
	out, err := sandboxed(t, p, "mv "+shq(run)+" "+shq(moved)+" && echo changed > "+shq(filepath.Join(moved, "config.json")))
	if err == nil {
		t.Fatalf("moved protected run root and changed config: %s", out)
	}
	if b, err := os.ReadFile(config); err != nil || string(b) != "protected" {
		t.Fatalf("protected root moved or content changed: %q %v", b, err)
	}
	if out, err := sandboxed(t, p, "echo own > "+shq(filepath.Join(run, "own"))+" && echo own > "+shq(filepath.Join(study, "own"))); err != nil {
		t.Fatalf("ordinary writes broken: %v %s", err, out)
	}
}

func TestLateReadOnlyRejectsUnsafeParents(t *testing.T) {
	home := tempDir(t)
	write := mkdir(t, filepath.Join(home, "write"))
	nested := mkdir(t, filepath.Join(write, "nested"))
	target := filepath.Join(home, "target")
	if err := os.WriteFile(target, nil, 0600); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(write, "link")
	if err := os.Symlink(target, link); err != nil {
		t.Fatal(err)
	}
	for _, path := range []string{filepath.Join(nested, "marker"), target, link, "relative", nested} {
		if _, err := (Policy{Home: home, Write: []string{write}, LateReadOnly: []string{path}}).resolve(); err == nil {
			t.Errorf("accepted unsafe late marker %s", path)
		}
	}
	marker := filepath.Join(write, "future")
	if _, err := (Policy{Home: home, Write: []string{write}, LateReadOnly: []string{marker}}).resolve(); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(marker); !os.IsNotExist(err) {
		t.Fatalf("late marker created during resolve: %v", err)
	}
}

func TestHiddenCredentialsCreatedAfterLaunchStayHidden(t *testing.T) {
	requireSandbox(t)
	for _, name := range []string{".netrc", ".config/gh/hosts.yml", ".docker/config.json"} {
		t.Run(name, func(t *testing.T) {
			home := tempDir(t)
			ordinary := filepath.Join(home, "ordinary")
			if err := os.WriteFile(ordinary, []byte("ordinary-visible\n"), 0600); err != nil {
				t.Fatal(err)
			}
			target := filepath.Join(home, name)
			hidden := target
			if strings.HasPrefix(name, ".config/") {
				hidden = filepath.Join(home, ".config/gh")
			}
			ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
			defer cancel()
			cmd, err := (Policy{Home: home, HomeRead: true, Hidden: []string{hidden}}).Command(ctx, "sh", "-c", "cat "+shq(ordinary)+"; echo READY; read go; cat "+shq(target)+" 2>/dev/null; cat "+shq(ordinary))
			if err != nil {
				t.Fatal(err)
			}
			stdout, err := cmd.StdoutPipe()
			if err != nil {
				t.Fatal(err)
			}
			stdin, err := cmd.StdinPipe()
			if err != nil {
				t.Fatal(err)
			}
			if err := cmd.Start(); err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { cancel(); _ = cmd.Wait() })
			reader := bufio.NewReader(stdout)
			for _, want := range []string{"ordinary-visible\n", "READY\n"} {
				line, err := reader.ReadString('\n')
				if err != nil || line != want {
					t.Fatalf("startup read = %q, %v; want %q", line, err, want)
				}
			}
			// The host creates the credential while the sandboxed child waits.
			if err := os.MkdirAll(filepath.Dir(target), 0700); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(target, []byte("inert-credential-marker\n"), 0600); err != nil {
				t.Fatal(err)
			}
			if _, err := io.WriteString(stdin, "continue\n"); err != nil {
				t.Fatal(err)
			}
			_ = stdin.Close()
			out, err := io.ReadAll(reader)
			if err != nil {
				t.Fatal(err)
			}
			if err := cmd.Wait(); err != nil {
				t.Fatal(err)
			}
			if string(out) != "ordinary-visible\n" {
				t.Fatalf("late credential escaped or ordinary home read lost: %q", out)
			}
		})
	}
}
