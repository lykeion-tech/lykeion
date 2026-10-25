//go:build !unix

package daemon

import "os/exec"

func ownGroup(*exec.Cmd) {}

func killGroup(cmd *exec.Cmd) {
	if cmd.Process != nil {
		cmd.Process.Kill()
	}
}

// scriptOpen opens a script without following a link in its last place
// or waiting on a pipe.
const scriptOpen = 0

func terminateService(cmd *exec.Cmd)       { killGroup(cmd) }
func serviceGroupAlive(cmd *exec.Cmd) bool { return false }
