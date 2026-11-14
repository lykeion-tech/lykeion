//go:build linux || darwin

package envs

import (
	"fmt"
	"os"
	"path/filepath"

	"golang.org/x/sys/unix"
)

// mountRoot reports whether dir is the root of a mounted volume.
func mountRoot(dir string) bool {
	var self, parent unix.Stat_t
	if unix.Lstat(dir, &self) != nil || unix.Lstat(filepath.Dir(dir), &parent) != nil {
		return false
	}
	return self.Dev != parent.Dev
}

// reserve refuses an install when the disk holding dir has less than need free.
func reserve(dir string, need int64) error {
	var fs unix.Statfs_t
	if err := unix.Statfs(dir, &fs); err != nil {
		return &os.PathError{Op: "statfs", Path: dir, Err: err}
	}
	free := uint64(fs.Bavail) * uint64(fs.Bsize)
	if free < uint64(need) {
		return fmt.Errorf("this install needs %d GB of free disk space and %d GB is free", need>>30, free>>30)
	}
	return nil
}
