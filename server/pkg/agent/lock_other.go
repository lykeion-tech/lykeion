//go:build !darwin && !linux

package agent

import (
	"fmt"
	"os"
)

// lockScratch keeps F2's create-exclusive lock off macOS and Linux: a crashed
// holder's lock file must be removed before the scratch can run again.
// ponytail: LockFileEx on Windows when agent runs are supported there.
func lockScratch(root *os.Root) (file *os.File, unlock func(), err error) {
	f, err := root.OpenFile(".agent-lock", os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600)
	if err != nil {
		return nil, nil, fmt.Errorf("%w or unavailable: %v", ErrScratchBusy, err)
	}
	return f, func() { f.Close(); root.Remove(".agent-lock") }, nil
}
