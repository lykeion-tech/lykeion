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
	if runtime.GOOS == "linux" {
		if entries, err := os.ReadDir("/proc/self/fd"); err == nil {
			for _, entry := range entries {
				if fd, err := strconv.Atoi(entry.Name()); err == nil && fd > 2 {
					syscall.CloseOnExec(fd)
				}
			}
			return
		}
	}
	// macOS's /dev/fd does not reliably list every open descriptor, so mark
	// each possible one; unused numbers just fail.
	limit := uint64(65536)
	var rl syscall.Rlimit
	if syscall.Getrlimit(syscall.RLIMIT_NOFILE, &rl) == nil && rl.Cur < limit {
		limit = rl.Cur
	}
	for fd := 3; uint64(fd) < limit; fd++ {
		syscall.CloseOnExec(fd)
	}
}
