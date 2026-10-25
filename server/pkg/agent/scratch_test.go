package agent

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestScratchReservationBorrowKeepsParentLock(t *testing.T) {
	dir, _ := filepath.EvalSymlinks(t.TempDir())
	r, err := ReserveScratch(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer r.Release()
	for i := 0; i < 2; i++ {
		s, err := (Codex{Bin: script(t, `cat >/dev/null`)}).Execute(t.Context(), "p", ExecOptions{Sandbox: testSandbox, Dir: dir, ScratchReservation: r})
		if err != nil {
			t.Fatal(err)
		}
		collectWithin(t, s, 5*time.Second)
		if other, err := ReserveScratch(dir); err == nil {
			other.Release()
			t.Fatal("adapter released the parent reservation")
		}
	}
	r.Release()
	if _, err := (Codex{Bin: script(t, `exit 0`)}).Execute(t.Context(), "p", ExecOptions{Sandbox: testSandbox, Dir: dir, ScratchReservation: r}); err == nil {
		t.Fatal("accepted released reservation")
	}
	next, err := ReserveScratch(dir)
	if err != nil {
		t.Fatal(err)
	}
	next.Release()
}

func TestScratchReservationRejectsMismatchAndChangedIdentity(t *testing.T) {
	dir, _ := filepath.EvalSymlinks(t.TempDir())
	r, err := ReserveScratch(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer r.Release()
	other := t.TempDir()
	if _, err := (Codex{Bin: script(t, `exit 0`)}).Execute(t.Context(), "p", ExecOptions{Sandbox: testSandbox, Dir: other, ScratchReservation: r}); err == nil {
		t.Fatal("accepted another scratch's reservation")
	}
	if _, err := os.Stat(filepath.Join(other, ".agent-owned")); !os.IsNotExist(err) {
		t.Fatal("mismatched reservation mutated scratch", err)
	}
	if err := os.Rename(filepath.Join(dir, ".agent-lock"), filepath.Join(dir, "old-lock")); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, ".agent-lock"), nil, 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := (Codex{Bin: script(t, `exit 0`)}).Execute(t.Context(), "p", ExecOptions{Sandbox: testSandbox, Dir: dir, ScratchReservation: r}); err == nil {
		t.Fatal("accepted replaced reservation lock inode")
	}
}

func TestScratchReservationRetiresFolderAtomically(t *testing.T) {
	parent, _ := filepath.EvalSymlinks(t.TempDir())
	dir := filepath.Join(parent, "scope-test")
	if err := os.Mkdir(dir, 0700); err != nil {
		t.Fatal(err)
	}
	r, err := ReserveScratch(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer r.Release()
	if err := os.WriteFile(filepath.Join(dir, "secret"), []byte("synthetic"), 0600); err != nil {
		t.Fatal(err)
	}
	pinned, err := os.OpenRoot(dir)
	if err != nil {
		t.Fatal(err)
	}
	if err := r.Remove(); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(dir); !os.IsNotExist(err) {
		t.Fatal("retired scratch still present", err)
	}
	if err := os.Mkdir(dir, 0700); err != nil {
		t.Fatal(err)
	}
	if stale, err := reservePinnedScratch(dir, pinned); err == nil {
		stale.Release()
		t.Fatal("stale pinned directory acquired replacement scratch")
	}
	next, err := ReserveScratch(dir)
	if err != nil {
		t.Fatal(err)
	}
	next.Release()
	entries, err := os.ReadDir(parent)
	if err != nil {
		t.Fatal(err)
	}
	for _, entry := range entries {
		if strings.HasPrefix(entry.Name(), RetiredScratchPrefix) {
			t.Fatal("successful removal retained secrets", entry.Name())
		}
	}
}

func TestScratchReservationOwnerReleaseWaitsForBorrow(t *testing.T) {
	dir, _ := filepath.EvalSymlinks(t.TempDir())
	r, err := ReserveScratch(dir)
	if err != nil {
		t.Fatal(err)
	}
	release, err := r.borrow(dir)
	if err != nil {
		t.Fatal(err)
	}
	r.Release()
	if other, err := ReserveScratch(dir); err == nil {
		other.Release()
		t.Fatal("parent release unlocked a borrowed execution")
	}
	if _, err := r.borrow(dir); err == nil {
		t.Fatal("accepted borrow after parent release")
	}
	release()
	next, err := ReserveScratch(dir)
	if err != nil {
		t.Fatal(err)
	}
	next.Release()
}

func TestScratchReservationFailsClosedOnInvalidProcessProof(t *testing.T) {
	dir := t.TempDir()
	held, err := ReserveScratch(dir)
	if err != nil {
		t.Fatal(err)
	}
	proof := filepath.Join(dir, ".agent-process")
	if err := os.WriteFile(proof, []byte("invalid proof"), 0600); err != nil {
		t.Fatal(err)
	}
	credential := filepath.Join(dir, ".cli-home", "auth.json")
	if err := os.Mkdir(filepath.Dir(credential), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(credential, []byte("synthetic credential"), 0600); err != nil {
		t.Fatal(err)
	}
	if next, err := ReserveScratch(dir); !errors.Is(err, ErrScratchBusy) {
		if next != nil {
			next.Release()
		}
		t.Fatalf("live holder was not authoritative: %v", err)
	}
	held.Release()
	for range 2 {
		if next, err := ReserveScratch(dir); err == nil || !strings.Contains(err.Error(), "invalid agent process ownership") {
			if next != nil {
				next.Release()
			}
			t.Fatalf("invalid proof did not fail closed or release failed reservation: %v", err)
		}
		if b, err := os.ReadFile(credential); err != nil || string(b) != "synthetic credential" {
			t.Fatalf("failed reconciliation mutated credentials: %q %v", b, err)
		}
	}
	if err := os.Remove(proof); err != nil {
		t.Fatal(err)
	}
	next, err := ReserveScratch(dir)
	if err != nil {
		t.Fatal(err)
	}
	next.Release()
}

// A run's code can leave folders it made read-only, as Go's module cache
// does; removing the run's folder still removes them, and a link out of the
// folder is never followed to change what it points at.
func TestScratchRemovalClearsReadOnlyFolders(t *testing.T) {
	parent, _ := filepath.EvalSymlinks(t.TempDir())
	outside := filepath.Join(t.TempDir(), "outside")
	if err := os.Mkdir(outside, 0o500); err != nil {
		t.Fatal(err)
	}
	defer os.Chmod(outside, 0o700)
	dir := filepath.Join(parent, "run-1")
	locked := filepath.Join(dir, "pkg", "mod", "x@v1")
	if err := os.MkdirAll(locked, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(locked, "go.mod"), []byte("module x"), 0o444); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outside, filepath.Join(dir, "pkg", "out")); err != nil {
		t.Fatal(err)
	}
	for _, p := range []string{locked, filepath.Dir(locked)} {
		if err := os.Chmod(p, 0o555); err != nil {
			t.Fatal(err)
		}
	}
	r, err := ReserveScratch(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer r.Release()
	if err := r.Remove(); err != nil {
		t.Fatal(err)
	}
	entries, _ := os.ReadDir(parent)
	if len(entries) != 0 {
		t.Fatalf("left behind: %v", entries)
	}
	if fi, err := os.Stat(outside); err != nil || fi.Mode().Perm() != 0o500 {
		t.Fatalf("a folder outside the run was changed: %v %v", fi.Mode(), err)
	}
}
