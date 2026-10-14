//go:build !unix

package agent

import (
	"errors"
	"os/exec"
)

// Refuse to launch until this platform can own and cancel the entire tree.
func configureProcess(*exec.Cmd) error {
	return errors.New("agent process-tree isolation requires Unix")
}
func killProcessTree(*exec.Cmd) error {
	return errors.New("agent process-tree isolation requires Unix")
}
