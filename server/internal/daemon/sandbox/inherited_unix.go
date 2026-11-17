//go:build unix

package sandbox

import (
	"os"
	"runtime"
	"strconv"
	"syscall"
)

// A descriptor this process inherited without close-on-exec, such as a pipe
// left open by whatever started the daemon, would pass into every sandboxed
// child. Agents receive only the descriptors a launch hands them on purpose.
func init() { closeInheritedOnExec() }

func closeInheritedOnExec() {
	dir := "/dev/fd"
	if runtime.GOOS == "linux" {
		dir = "/proc/self/fd"
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		return
	}
	for _, entry := range entries {
		if fd, err := strconv.Atoi(entry.Name()); err == nil && fd > 2 {
			syscall.CloseOnExec(fd)
		}
	}
}
