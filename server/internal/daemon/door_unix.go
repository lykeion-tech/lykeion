//go:build unix

package daemon

import (
	"os/exec"
	"syscall"

	"lykeion/server/internal/daemon/sandbox"
)

// A local command runs in its own process group, as the agent CLI does, so
// what it starts stops with it.
func ownGroup(cmd *exec.Cmd) { cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true} }

func killGroup(cmd *exec.Cmd) {
	if cmd.Process != nil {
		sandbox.KillGroup(cmd.Process.Pid)
	}
}

// scriptOpen opens a script without following a link in its last place
// or waiting on a pipe.
const scriptOpen = syscall.O_NOFOLLOW | syscall.O_NONBLOCK

func terminateService(cmd *exec.Cmd) {
	if cmd.Process != nil {
		syscall.Kill(-cmd.Process.Pid, syscall.SIGTERM)
	}
}
func serviceGroupAlive(cmd *exec.Cmd) bool {
	return cmd.Process != nil && syscall.Kill(-cmd.Process.Pid, 0) == nil
}
