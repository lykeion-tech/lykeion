package agent

import (
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
		s, err := (Cursor{Bin: script(t, `cat >/dev/null`)}).Execute(t.Context(), "p", ExecOptions{Dir: dir, ScratchReservation: r})
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
	if _, err := (Cursor{Bin: script(t, `exit 0`)}).Execute(t.Context(), "p", ExecOptions{Dir: dir, ScratchReservation: r}); err == nil {
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
	if _, err := (Cursor{Bin: script(t, `exit 0`)}).Execute(t.Context(), "p", ExecOptions{Dir: other, ScratchReservation: r}); err == nil {
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
	if _, err := (Cursor{Bin: script(t, `exit 0`)}).Execute(t.Context(), "p", ExecOptions{Dir: dir, ScratchReservation: r}); err == nil {
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
