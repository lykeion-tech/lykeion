//go:build !unix

package daemon

import (
	"errors"
	"os"
)

func ownState(string) (*os.File, error) {
	return nil, errors.New("daemon process ownership requires Unix")
}

// linkCount is how many names the file has; unknown here, so one.
func linkCount(os.FileInfo) uint64 { return 1 }
