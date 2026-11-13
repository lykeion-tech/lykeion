//go:build darwin || linux

package daemon

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"

	"github.com/google/uuid"
	"golang.org/x/sys/unix"
)

// syncExistingReport retries the local file and directory sync after a
// potentially ambiguous earlier publication. It does not prove parent-chain
// durability or defend against arbitrary same-user ancestor replacement.
func syncExistingReport(path string, want Report, syncFile func(*os.File) error) (result error) {
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
	info, err := f.Stat()
	if err != nil {
		return err
	}
	if !info.Mode().IsRegular() || info.Size() < 1 || info.Size() == int64(^uint64(0)>>1) {
		return errors.New("invalid existing queue record")
	}
	b, err := io.ReadAll(io.LimitReader(f, info.Size()+1))
	if err != nil || int64(len(b)) != info.Size() {
		return errors.New("queue record changed while reading")
	}
	var got Report
	if err = json.Unmarshal(b, &got); err != nil || got.AgentRunID == uuid.Nil || got.Attempt <= 0 || (got.Kind != "complete" && got.Kind != "fail" && got.Kind != "messages" && (got.Kind != "rerun" || got.Rerun == nil)) {
		return errors.New("invalid existing queue record")
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
	return nil
}
