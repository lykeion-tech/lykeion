//go:build !unix

package envs

import "os/exec"

// Language bases are unavailable here; keep the runner buildable for CLI
// targets that import the environment descriptions.
func cancelBuildTree(cmd *exec.Cmd) {}
