//go:build unix

package sandbox

import (
	"os/exec"
	"syscall"
)

// newSession detaches the command from the daemon's terminal, so code cannot
// push input into it (TIOCSTI), and makes it the leader of its own process
// group.
func newSession(cmd *exec.Cmd) { cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true} }
