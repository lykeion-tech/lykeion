package agent

import (
	"crypto/rand"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sync"
)

// ErrScratchBusy means another execution owns the scratch's advisory lock.
var ErrScratchBusy = errors.New("scratch is already in use")

// RetiredScratchPrefix marks folders atomically detached for deletion. A later
// collector may finish an interrupted removal without applying retention again.
const RetiredScratchPrefix = ".scratch-retired-"

// ScratchReservation owns a scratch's cross-process lock for the parent run.
// An adapter may borrow it, but only its owner releases the reservation.
type ScratchReservation struct {
	mu               sync.Mutex
	dir              string
	root             *os.Root
	info             os.FileInfo
	lock             *os.File
	unlock           func()
	closed, borrowed bool
}

// ReserveScratch reserves an existing real directory and reconciles any prior
// supervised process before its contents may change. Contention is nonblocking.
func ReserveScratch(dir string) (*ScratchReservation, error) {
	path, err := filepath.Abs(dir)
	if err != nil {
		return nil, err
	}
	parent, err := filepath.EvalSymlinks(filepath.Dir(path))
	if err != nil {
		return nil, err
	}
	path = filepath.Join(parent, filepath.Base(path))
	info, err := os.Lstat(path)
	if err != nil {
		return nil, err
	}
	if !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
		return nil, fmt.Errorf("scratch is not a real directory: %s", path)
	}
	root, err := os.OpenRoot(path)
	if err != nil {
		return nil, err
	}
	return reservePinnedScratch(path, root)
}

func reservePinnedScratch(dir string, root *os.Root) (*ScratchReservation, error) {
	info, err := root.Stat(".")
	if err != nil {
		root.Close()
		return nil, err
	}
	lock, unlock, err := lockScratch(root)
	if err != nil {
		root.Close()
		return nil, err
	}
	r := &ScratchReservation{dir: dir, root: root, info: info, lock: lock, unlock: unlock}
	if err := r.validate(dir); err != nil {
		r.Release()
		return nil, err
	}
	// Parent death releases the lock before its supervised group necessarily
	// exits. Reconcile that identity before exposing the directory to reuse or
	// deletion, including callers with no journal for the previous attempt.
	if _, err := r.root.Lstat(".agent-process"); err == nil {
		if err := RecoverScratch(dir); err != nil {
			r.Release()
			return nil, fmt.Errorf("reconcile scratch process: %w", err)
		}
	} else if !errors.Is(err, os.ErrNotExist) {
		r.Release()
		return nil, err
	}
	return r, nil
}

func (r *ScratchReservation) validate(dir string) error {
	if r.closed || r.lock == nil {
		return errors.New("scratch reservation is closed")
	}
	path, err := filepath.Abs(dir)
	if err != nil {
		return err
	}
	if path != r.dir {
		return errors.New("scratch reservation belongs to a different directory")
	}
	current, err := os.Lstat(path)
	if err != nil {
		return err
	}
	if !current.IsDir() || current.Mode()&os.ModeSymlink != 0 || !os.SameFile(current, r.info) {
		return errors.New("scratch reservation directory identity changed")
	}
	locked, err := r.lock.Stat()
	if err != nil {
		return err
	}
	current, err = r.root.Lstat(".agent-lock")
	if err != nil {
		return err
	}
	if !current.Mode().IsRegular() || !os.SameFile(current, locked) {
		return errors.New("scratch reservation lock identity changed")
	}
	return nil
}

func (r *ScratchReservation) borrow(dir string) (func(), error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if err := r.validate(dir); err != nil {
		return nil, err
	}
	if r.borrowed {
		return nil, errors.New("scratch reservation is already borrowed")
	}
	r.borrowed = true
	return func() {
		r.mu.Lock()
		defer r.mu.Unlock()
		r.borrowed = false
		if r.closed {
			r.release()
		}
	}, nil
}

// Release closes the parent's reservation. An adapter still draining output
// retains the lock until its borrow ends; new borrows are rejected immediately.
func (r *ScratchReservation) Release() {
	if r == nil {
		return
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	r.closed = true
	if !r.borrowed {
		r.release()
	}
}
func (r *ScratchReservation) release() {
	if r.unlock != nil {
		r.unlock()
		r.unlock = nil
		r.lock = nil
		r.root.Close()
		r.root = nil
	}
}

// Remove atomically retires the reserved directory before deleting it. A new
// run can create a fresh canonical directory; no run can enter the old tree
// halfway through deletion. A failed removal names its safely retired path.
func (r *ScratchReservation) Remove() error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if err := r.validate(r.dir); err != nil {
		return err
	}
	if r.borrowed {
		return errors.New("cannot remove a borrowed scratch reservation")
	}
	retired := filepath.Join(filepath.Dir(r.dir), RetiredScratchPrefix+rand.Text())
	if err := os.Rename(r.dir, retired); err != nil {
		return err
	}
	if err := removeTree(retired); err != nil {
		return fmt.Errorf("remove retired scratch %s: %w", retired, err)
	}
	return nil
}

// removeTree deletes a folder even where code left read-only folders in it,
// as Go's module cache does, by making each folder writable first. Links are
// not folders here, so nothing outside is changed.
func removeTree(dir string) error {
	if os.RemoveAll(dir) == nil {
		return nil
	}
	filepath.WalkDir(dir, func(p string, d fs.DirEntry, err error) error {
		if err == nil && d.IsDir() {
			os.Chmod(p, 0o700)
		}
		return nil
	})
	return os.RemoveAll(dir)
}
