//go:build unix

package envs

import (
	"os"
	"os/exec"
	"syscall"

	"lykeion/server/internal/daemon/sandbox"
)

// killGroupOnCancel ends the command's whole process group when its context
// ends. The sandbox starts each command in a new session, so its pid is the
// group's.
func killGroupOnCancel(cmd *exec.Cmd) {
	cmd.Cancel = func() error {
		err := sandbox.KillGroup(cmd.Process.Pid)
		if err == syscall.ESRCH {
			return os.ErrProcessDone
		}
		return err
	}
}
