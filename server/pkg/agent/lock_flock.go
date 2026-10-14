//go:build darwin || linux

package agent

import (
	"fmt"
	"os"
	"syscall"
)

// lockScratch holds an advisory exclusive lock on the scratch's lock file. The
// file is never deleted: a dead holder's lock is released with its process, so
// a crash leaves nothing to clean up.
func lockScratch(path string) (unlock func(), err error) {
	f, err := os.OpenFile(path, os.O_RDWR|os.O_CREATE|syscall.O_NOFOLLOW, 0600)
	if err != nil {
		return nil, fmt.Errorf("scratch lock is unavailable: %w", err)
	}
	if err := syscall.Flock(int(f.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); err != nil {
		f.Close()
		return nil, fmt.Errorf("scratch is already in use: %w", err)
	}
	return func() { f.Close() }, nil
}
