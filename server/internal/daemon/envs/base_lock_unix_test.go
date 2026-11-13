//go:build linux || darwin

package envs

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"golang.org/x/sys/unix"
)

func TestReadBaseLockBoundsAndRejectsSpecialFiles(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "lock.txt")
	write := func(data []byte) {
		t.Helper()
		if err := os.WriteFile(path, data, 0600); err != nil {
			t.Fatal(err)
		}
	}
	write([]byte("package=1\n"))
	if got, err := readBaseLock(path); err != nil || string(got) != "package=1\n" {
		t.Fatalf("regular lock: %q, %v", got, err)
	}
	write(bytes.Repeat([]byte("x"), inventoryLimit))
	if got, err := readBaseLock(path); err != nil || len(got) != inventoryLimit {
		t.Fatalf("exact limit: length %d, %v", len(got), err)
	}
	write(bytes.Repeat([]byte("x"), inventoryLimit+1))
	if got, err := readBaseLock(path); err == nil || got != nil {
		t.Fatalf("oversized lock: length %d, %v", len(got), err)
	}
	if err := os.Remove(path); err != nil {
		t.Fatal(err)
	}
	elsewhere := filepath.Join(dir, "elsewhere")
	if err := os.WriteFile(elsewhere, []byte("secret"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(elsewhere, path); err != nil {
		t.Fatal(err)
	}
	if got, err := readBaseLock(path); err == nil || got != nil {
		t.Fatalf("symlink lock: %q, %v", got, err)
	}
	if err := os.Remove(path); err != nil {
		t.Fatal(err)
	}
	if err := unix.Mkfifo(path, 0600); err != nil {
		t.Fatal(err)
	}
	done := make(chan error, 1)
	go func() { _, err := readBaseLock(path); done <- err }()
	select {
	case err := <-done:
		if err == nil {
			t.Fatal("FIFO lock accepted")
		}
	case <-time.After(time.Second):
		t.Fatal("FIFO lock blocked")
	}
}

func TestPackagesRefusesOversizedBaseLockBeforeInventory(t *testing.T) {
	b, runner := fakeBases(t)
	if err := b.buildBaseForTest(context.Background(), Python); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(b.baseDir(Python), "lock.txt")
	if err := os.WriteFile(path, bytes.Repeat([]byte("x"), inventoryLimit+1), 0600); err != nil {
		t.Fatal(err)
	}
	packages, err := newStudy(b, t.TempDir()).Packages(context.Background(), Python)
	if err == nil || packages != "" || !strings.Contains(err.Error(), "over 4 MiB") {
		t.Fatalf("oversized packages: %q, %v", packages, err)
	}
	for _, call := range runner.calls {
		if strings.Contains(call, "freeze --local") {
			t.Fatal("ran study inventory after rejecting base lock")
		}
	}
}

func TestReadOpenedBaseLockKeepsOpenedFileAfterReplacement(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "lock.txt")
	if err := os.WriteFile(path, []byte("original"), 0600); err != nil {
		t.Fatal(err)
	}
	f, err := os.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	if err := os.Rename(path, filepath.Join(dir, "original")); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte("replacement"), 0600); err != nil {
		t.Fatal(err)
	}
	got, err := readOpenedBaseLock(f)
	if err != nil || string(got) != "original" {
		t.Fatalf("opened lock: %q, %v", got, err)
	}
}

func TestReadOpenedBaseLockRejectsReplacedFIFO(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "lock.txt")
	if err := unix.Mkfifo(path, 0600); err != nil {
		t.Fatal(err)
	}
	fd, err := unix.Open(path, unix.O_RDONLY|unix.O_NONBLOCK|unix.O_NOFOLLOW, 0)
	if err != nil {
		t.Fatal(err)
	}
	f := os.NewFile(uintptr(fd), path)
	defer f.Close()
	if err := os.Remove(path); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte("replacement"), 0600); err != nil {
		t.Fatal(err)
	}
	if got, err := readOpenedBaseLock(f); err == nil || got != nil {
		t.Fatalf("opened FIFO accepted after replacement: %q, %v", got, err)
	}
}
