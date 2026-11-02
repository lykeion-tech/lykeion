package sandbox

import (
	"errors"
	"os"
	"path/filepath"
	"testing"
)

func TestSQLiteAndExactFilesConfineForReal(t *testing.T) {
	h := tempDir(t)
	dir := mkdir(t, filepath.Join(h, `codex.[x]+(state)with\slash`))
	p := Policy{Home: h, HomeRead: true, WriteSQLite: []string{dir}, WriteFiles: []string{filepath.Join(dir, "auth.json")}}
	for _, name := range []string{"auth.json", "state_5.sqlite", "state_5.sqlite-wal", "state_5.sqlite-shm", "state_5.sqlite-journal", "new_store_921.sqlite-wal"} {
		target := filepath.Join(dir, name)
		script := "printf x > " + shq(target) + " && rm " + shq(target) + " && printf y > " + shq(target)
		if out, err := sandboxed(t, p, script); err != nil {
			t.Fatalf("allowed %s: %v %s", name, err, out)
		}
		if b, err := os.ReadFile(target); err != nil || string(b) != "y" {
			t.Fatal(name, string(b), err)
		}
	}
	for _, name := range []string{"config.toml", "other.json", "state.sqlite", "state_x.sqlite", "state_5.sqlite.bak", "state_5.sqlite-wal-extra", "AGENTS.md"} {
		if out, err := sandboxed(t, p, "printf x > "+shq(filepath.Join(dir, name))); err == nil {
			t.Fatalf("unexpected write %s: %s", name, out)
		}
	}
	other := mkdir(t, filepath.Join(h, "codexX[x]+(state)"))
	if out, err := sandboxed(t, p, "printf x > "+shq(filepath.Join(other, "state_5.sqlite"))); err == nil {
		t.Fatalf("regex metachar escaped incorrectly: %s", out)
	}
	nested := mkdir(t, filepath.Join(dir, "nested"))
	if out, err := sandboxed(t, p, "printf x > "+shq(filepath.Join(nested, "state_5.sqlite"))); err == nil {
		t.Fatalf("nested match: %s", out)
	}
}

func TestLockDirCanBeCreatedRemovedAndRecreated(t *testing.T) {
	h := tempDir(t)
	lock := filepath.Join(h, "x.lock")
	p := Policy{Home: h, HomeRead: true, LockDirs: []string{lock}}
	script := "mkdir " + shq(lock) + " && touch " + shq(filepath.Join(lock, "pid")) + " && rm -r " + shq(lock) + " && mkdir " + shq(lock)
	if out, err := sandboxed(t, p, script); err != nil {
		t.Fatalf("lock lifecycle denied: %v %s", err, out)
	}
	if fi, err := os.Stat(lock); err != nil || !fi.IsDir() {
		t.Fatal("lock not re-created", err)
	}
	// A lock another process holds at launch is still granted.
	if out, err := sandboxed(t, p, "rmdir "+shq(lock)+" && mkdir "+shq(lock)); err != nil {
		t.Fatalf("held lock denied: %v %s", err, out)
	}
	if out, err := sandboxed(t, p, "mkdir "+shq(filepath.Join(h, "other.lock"))); err == nil {
		t.Fatalf("sibling lock allowed: %s", out)
	}
	if out, err := sandboxed(t, p, "touch "+shq(lock+".owner")); err == nil {
		t.Fatalf("sibling file allowed: %s", out)
	}
}

// An agent can leave a file or a link where a lock folder belongs; the next
// launch clears it instead of refusing every later run.
func TestPlantedLockPathIsClearedAtLaunch(t *testing.T) {
	h := tempDir(t)
	target := mkdir(t, filepath.Join(h, "target"))
	link := filepath.Join(h, "link.lock")
	if err := os.Symlink(target, link); err != nil {
		t.Fatal(err)
	}
	file := filepath.Join(h, "file.lock")
	if err := os.WriteFile(file, []byte("x"), 0600); err != nil {
		t.Fatal(err)
	}
	for _, lock := range []string{link, file} {
		p := Policy{Home: h, HomeRead: true, LockDirs: []string{lock}}
		if out, err := sandboxed(t, p, "mkdir "+shq(lock)+" && rmdir "+shq(lock)); err != nil {
			t.Fatalf("planted %s blocked the lock: %v %s", lock, err, out)
		}
		if _, err := os.Lstat(lock); !errors.Is(err, os.ErrNotExist) {
			t.Fatalf("planted %s not cleared: %v", lock, err)
		}
	}
	if fi, err := os.Stat(target); err != nil || !fi.IsDir() {
		t.Fatal("link target removed", err)
	}
}

func TestLockDirRejectsUnsafePaths(t *testing.T) {
	h := tempDir(t)
	for _, lock := range []string{filepath.Join(h, "absent", "x.lock"), filepath.Join(h, "bad\nname.lock"), h} {
		p := Policy{Home: h, HomeRead: true, LockDirs: []string{lock}}
		if _, err := p.seatbeltProfile(); err == nil {
			t.Errorf("accepted unsafe lock %q", lock)
		}
	}
	if _, err := (Policy{Home: h, HomeRead: true, LockDirs: []string{filepath.Join(h, "x.lock")}}).bwrapArgs("true"); err == nil {
		t.Error("Linux accepted a lock grant")
	}
}
