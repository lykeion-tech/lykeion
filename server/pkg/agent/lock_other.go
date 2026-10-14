//go:build !darwin && !linux

package agent

import (
	"fmt"
	"os"
)

// lockScratch keeps F2's create-exclusive lock off macOS and Linux: a crashed
// holder's lock file must be removed before the scratch can run again.
// ponytail: LockFileEx on Windows when agent runs are supported there.
func lockScratch(path string) (unlock func(), err error) {
	f, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600)
	if err != nil {
		return nil, fmt.Errorf("scratch is already in use or unavailable: %w", err)
	}
	f.Close()
	return func() { os.Remove(path) }, nil
}
