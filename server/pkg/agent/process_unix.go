//go:build unix

package agent

import (
	"errors"
	"os"
	"os/exec"
	"syscall"
)

func configureProcess(cmd *exec.Cmd) error {
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	cmd.Cancel = func() error { return killProcessTree(cmd) }
	return nil
}
func killProcessTree(cmd *exec.Cmd) error {
	if cmd.Process == nil {
		return os.ErrProcessDone
	}
	owner, err := readProcess(cmd.Dir)
	if errors.Is(err, os.ErrNotExist) || (err == nil && owner.PID != cmd.Process.Pid) {
		// Before ownership is committed the launch gate is closed: only the
		// supervisor exists, and the exec-owned process handle can stop it.
		return cmd.Process.Kill()
	}
	if err != nil {
		return err
	}
	killed, err := signalOwned(owner)
	if err == nil && !killed {
		return os.ErrProcessDone
	}
	return err
}
