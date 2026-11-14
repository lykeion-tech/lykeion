//go:build unix

package envs

import (
	"os"
	"os/exec"
	"syscall"
)

// killGroupOnCancel ends the command's whole process group when its context
// ends. The sandbox starts each command in a new session, so its pid is the
// group's.
func killGroupOnCancel(cmd *exec.Cmd) {
	cmd.Cancel = func() error {
		err := syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL)
		if err == syscall.ESRCH {
			return os.ErrProcessDone
		}
		return err
	}
}
