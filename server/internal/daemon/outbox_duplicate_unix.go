//go:build darwin || linux

package daemon

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"

	"golang.org/x/sys/unix"
)

// syncExistingReport retries the local file and directory sync after a
// potentially ambiguous earlier publication. It does not prove parent-chain
// durability or defend against arbitrary same-user ancestor replacement.
func syncExistingReport(path, queueDir string, want Report, syncFile func(*os.File) error) (result error) {
	dir, name := filepath.Dir(path), filepath.Base(path)
	fd, err := unix.Open(dir, unix.O_RDONLY|unix.O_DIRECTORY|unix.O_NOFOLLOW|unix.O_CLOEXEC, 0)
	if err != nil {
		return err
	}
	parent := os.NewFile(uintptr(fd), dir)
	defer parent.Close()
	n, err := unix.Openat(fd, name, unix.O_RDONLY|unix.O_NONBLOCK|unix.O_NOFOLLOW|unix.O_CLOEXEC, 0)
	if err != nil {
		return err
	}
	f := os.NewFile(uintptr(n), path)
	defer f.Close()
	// After open, ENOENT means the observed record or directory vanished;
	// callers must never mistake that uncertainty for an initially absent key.
	defer func() {
		if errors.Is(result, os.ErrNotExist) {
			result = fmt.Errorf("existing queue record disappeared: %v", result)
		}
	}()
	got, err := decodeReportFile(f)
	if err != nil {
		return err
	}
	if !sameReport(got, want) {
		return errors.New("conflicting durable report")
	}
	if err = syncFile(f); err != nil {
		return err
	}
	var opened, entry unix.Stat_t
	if err = unix.Fstat(n, &opened); err != nil {
		return err
	}
	if err = unix.Fstatat(fd, name, &entry, unix.AT_SYMLINK_NOFOLLOW); err != nil {
		return err
	}
	if opened.Dev != entry.Dev || opened.Ino != entry.Ino || uint32(entry.Mode)&unix.S_IFMT != unix.S_IFREG {
		return errors.New("queue record replaced during sync")
	}
	if err = syncFile(parent); err != nil {
		return err
	}
	ancestor, err := os.Open(filepath.Dir(dir))
	if err != nil {
		return err
	}
	defer ancestor.Close()
	if err = syncFile(ancestor); err != nil {
		return fmt.Errorf("queue parent sync: %w", err)
	}
	if filepath.Clean(dir) != filepath.Clean(queueDir) {
		// A quarantined record's containing directory is nested beneath the
		// queue; also persist the queue directory's own parent entry.
		queueParent, e := os.Open(filepath.Dir(queueDir))
		if e != nil {
			return e
		}
		defer queueParent.Close()
		if e = syncFile(queueParent); e != nil {
			return fmt.Errorf("queue ancestor sync: %w", e)
		}
	}
	return nil
}
