//go:build darwin || linux

package daemon

import (
	"bytes"
	"errors"
	"fmt"
	"golang.org/x/sys/unix"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestOutboxExistingRecordSyncFailures(t *testing.T) {
	dir := t.TempDir()
	r := reportFixture()
	ob := &Outbox{Dir: dir}
	must(t, ob.Put(r))
	path := ob.file(r)
	for _, failAt := range []int{1, 2, 3} {
		calls := 0
		err := syncExistingReport(path, r, func(f *os.File) error {
			calls++
			if calls == failAt {
				return errors.New("injected sync failure")
			}
			return f.Sync()
		})
		if err == nil {
			t.Fatalf("sync %d failure ignored", failAt)
		}
	}
	if err := syncExistingReport(path, r, func(f *os.File) error { return f.Sync() }); err != nil {
		t.Fatal(err)
	}
	changed := r
	changed.Error = "different"
	if err := syncExistingReport(path, changed, func(f *os.File) error { return f.Sync() }); err == nil {
		t.Fatal("conflict accepted")
	}
}
func TestOutboxExistingRecordRejectsLinksAndFIFO(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "entry")
	r := reportFixture()
	if err := os.Symlink("target", path); err != nil {
		t.Fatal(err)
	}
	if err := syncExistingReport(path, r, func(f *os.File) error { return f.Sync() }); err == nil {
		t.Fatal("symlink accepted")
	}
	must(t, os.Remove(path))
	if err := unix.Mkfifo(path, 0600); err != nil {
		t.Fatal(err)
	}
	if err := syncExistingReport(path, r, func(f *os.File) error { return f.Sync() }); err == nil {
		t.Fatal("FIFO accepted")
	}
}

func TestOutboxDuplicatePreservesExistingEvidence(t *testing.T) {
	for _, quarantined := range []bool{false, true} {
		t.Run(fmt.Sprint(quarantined), func(t *testing.T) {
			dir := t.TempDir()
			r := reportFixture()
			saved := r
			saved.Rejections = []time.Time{time.Now().UTC()}
			target := dir
			if quarantined {
				target = filepath.Join(dir, "quarantine")
			}
			must(t, atomicJSON(target, filepath.Base((&Outbox{Dir: dir}).file(r)), saved))
			path := filepath.Join(target, filepath.Base((&Outbox{Dir: dir}).file(r)))
			before, err := os.ReadFile(path)
			must(t, err)
			must(t, (&Outbox{Dir: dir}).Put(r))
			after, err := os.ReadFile(path)
			must(t, err)
			if !bytes.Equal(before, after) {
				t.Fatal("duplicate rewrote existing rejection evidence")
			}
		})
	}
}
func TestOutboxObservedRecordDisappearanceIsNotAbsent(t *testing.T) {
	dir := t.TempDir()
	r := reportFixture()
	ob := &Outbox{Dir: dir}
	must(t, ob.Put(r))
	path := ob.file(r)
	err := syncExistingReport(path, r, func(f *os.File) error {
		if f.Name() == path {
			return os.Remove(path)
		}
		return f.Sync()
	})
	if err == nil || errors.Is(err, os.ErrNotExist) {
		t.Fatalf("disappearance misclassified: %v", err)
	}
}
