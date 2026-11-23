//go:build unix

package sandbox

import (
	"syscall"
	"time"
)

// KillGroup ends every process in group pgid. A child being forked as the
// signal is sent can miss it on macOS, so the kill repeats for a moment until
// nothing is left to signal. It returns the first kill's error.
func KillGroup(pgid int) error {
	err := syscall.Kill(-pgid, syscall.SIGKILL)
	for i, again := 0, err; again == nil && i < 9; i++ {
		time.Sleep(5 * time.Millisecond)
		again = syscall.Kill(-pgid, syscall.SIGKILL)
	}
	return err
}
