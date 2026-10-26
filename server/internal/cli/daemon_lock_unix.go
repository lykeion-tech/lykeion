//go:build darwin || linux

package cli

import (
	"errors"
	"os"
	"path/filepath"

	"golang.org/x/sys/unix"
)

// stateLockIdle observes existing state without creating or replacing lock files.
func stateLockIdle(dir string) bool {
	info, err := os.Lstat(dir)
	if errors.Is(err, os.ErrNotExist) {
		return true
	}
	if err != nil || !info.IsDir() {
		return false
	}
	fd, err := unix.Open(dir, unix.O_RDONLY|unix.O_DIRECTORY|unix.O_NOFOLLOW|unix.O_CLOEXEC, 0)
	if err != nil {
		return false
	}
	directory := os.NewFile(uintptr(fd), dir)
	defer directory.Close()
	opened, err := directory.Stat()
	if err != nil || !os.SameFile(info, opened) {
		return false
	}
	path := filepath.Join(dir, "daemon.lock")
	lockFD, err := unix.Openat(fd, "daemon.lock", unix.O_RDONLY|unix.O_NOFOLLOW|unix.O_NONBLOCK|unix.O_CLOEXEC, 0)
	if errors.Is(err, unix.ENOENT) {
		_, missing := os.Lstat(path)
		return errors.Is(missing, os.ErrNotExist) && sameStatePath(dir, info)
	}
	if err != nil {
		return false
	}
	lock := os.NewFile(uintptr(lockFD), path)
	defer lock.Close()
	lockInfo, err := lock.Stat()
	if err != nil || !lockInfo.Mode().IsRegular() || !sameStatePath(path, lockInfo) {
		return false
	}
	if err := unix.Flock(lockFD, unix.LOCK_EX|unix.LOCK_NB); err != nil {
		return false
	}
	defer unix.Flock(lockFD, unix.LOCK_UN)
	return sameStatePath(path, lockInfo) && sameStatePath(dir, info)
}

func sameStatePath(path string, expected os.FileInfo) bool {
	current, err := os.Lstat(path)
	return err == nil && os.SameFile(expected, current)
}
