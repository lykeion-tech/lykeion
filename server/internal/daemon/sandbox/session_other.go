//go:build !unix

package sandbox

import "os/exec"

func newSession(*exec.Cmd) {}
