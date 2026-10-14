//go:build linux

package cli

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
)

func processStart(pid int) (string, error) {
	if pid <= 0 {
		return "", errors.New("invalid process ID")
	}
	b, err := os.ReadFile(filepath.Join("/proc", strconv.Itoa(pid), "stat"))
	if err != nil {
		return "", err
	}
	i := strings.LastIndexByte(string(b), ')')
	if i < 0 {
		return "", errors.New("invalid process stat")
	}
	fields := strings.Fields(string(b[i+1:]))
	if len(fields) < 20 {
		return "", errors.New("short process stat")
	}
	return fields[19], nil
}

func detach(c *exec.Cmd) error {
	c.SysProcAttr = &syscall.SysProcAttr{Setsid: true}
	return nil
}

func terminate(pid int) error { return syscall.Kill(pid, syscall.SIGTERM) }

func clearStoppedOwner(dir, path string, owner daemonOwner) error {
	lockPath := filepath.Join(dir, "daemon.lock")
	if err := checkStateLock(lockPath); err != nil {
		return err
	}
	f, err := os.OpenFile(lockPath, os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		return err
	}
	defer f.Close()
	if err := syscall.Flock(int(f.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); errors.Is(err, syscall.EWOULDBLOCK) {
		return nil
	} else if err != nil {
		return err
	}
	defer syscall.Flock(int(f.Fd()), syscall.LOCK_UN)
	return clearOwner(path, owner)
}

func ownedPrivateDir(path string) error {
	fi, err := os.Lstat(path)
	if err != nil {
		return err
	}
	stat, ok := fi.Sys().(*syscall.Stat_t)
	if !ok || !fi.IsDir() || fi.Mode()&os.ModeSymlink != 0 || stat.Uid != uint32(os.Getuid()) || fi.Mode().Perm() != 0o700 {
		return fmt.Errorf("scratch root must be an owned private directory: %s", path)
	}
	return nil
}
