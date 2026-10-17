//go:build unix

package daemon

import (
	"os/exec"
	"syscall"
)

// A local command runs in its own process group, as the agent CLI does, so
// what it starts stops with it.
func ownGroup(cmd *exec.Cmd) { cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true} }

func killGroup(cmd *exec.Cmd) {
	if cmd.Process != nil {
		syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL)
	}
}
