//go:build darwin || linux

package agent

import (
	"errors"
	"fmt"
	"os"
	"syscall"
)

// lockScratch holds an advisory exclusive lock on the scratch's lock file. The
// file stays in its live directory; deletion first retires that directory.
// A dead holder releases its lock with its process, leaving no stale claim.
func lockScratch(root *os.Root) (file *os.File, unlock func(), err error) {
	f, err := root.OpenFile(".agent-lock", os.O_RDWR|os.O_CREATE|syscall.O_NOFOLLOW, 0600)
	if err != nil {
		return nil, nil, fmt.Errorf("scratch lock is unavailable: %w", err)
	}
	if err := syscall.Flock(int(f.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); err != nil {
		f.Close()
		if errors.Is(err, syscall.EWOULDBLOCK) || errors.Is(err, syscall.EAGAIN) {
			return nil, nil, fmt.Errorf("%w: %v", ErrScratchBusy, err)
		}
		return nil, nil, fmt.Errorf("scratch lock is unavailable: %w", err)
	}
	return f, func() { f.Close() }, nil
}
