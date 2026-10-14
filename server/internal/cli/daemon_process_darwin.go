//go:build darwin

package cli

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"syscall"

	"golang.org/x/sys/unix"
)

func processStart(pid int) (string, error) {
	if pid <= 0 {
		return "", errors.New("invalid process ID")
	}
	p, err := unix.SysctlKinfoProc("kern.proc.pid", pid)
	if err != nil {
		return "", err
	}
	if int(p.Proc.P_pid) != pid {
		return "", errors.New("process ID changed")
	}
	t := p.Proc.P_starttime
	return fmt.Sprintf("%d:%d", t.Sec, t.Usec), nil
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
