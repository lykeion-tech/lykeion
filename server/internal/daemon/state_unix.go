//go:build unix

package daemon

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"syscall"
)

// The advisory lock survives for the complete daemon lifetime, including child
// shutdown; kernel release after a crash permits safe outbox recovery.
func ownState(dir string) (*os.File, error) {
	if err := privateDir(dir); err != nil {
		return nil, err
	}
	p := filepath.Join(dir, "daemon.lock")
	if fi, err := os.Lstat(p); err == nil && !fi.Mode().IsRegular() {
		return nil, errors.New("daemon lock is not a regular file")
	} else if err != nil && !errors.Is(err, os.ErrNotExist) {
		return nil, err
	}
	f, err := os.OpenFile(p, os.O_CREATE|os.O_RDWR, 0600)
	if err != nil {
		return nil, err
	}
	if err = f.Chmod(0600); err != nil {
		f.Close()
		return nil, err
	}
	if err = syscall.Flock(int(f.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); err != nil {
		f.Close()
		return nil, fmt.Errorf("state directory already owned by a daemon: %w", err)
	}
	return f, nil
}
