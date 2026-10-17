package check

import (
	"runtime/debug"
	"syscall"
)

// MemoryLimit caps the child's heap; on Linux the kernel enforces it.
const MemoryLimit = 512 << 20

func limitMemory() error {
	debug.SetMemoryLimit(MemoryLimit)
	return syscall.Setrlimit(syscall.RLIMIT_DATA, &syscall.Rlimit{Cur: MemoryLimit, Max: MemoryLimit})
}
