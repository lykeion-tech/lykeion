//go:build !linux

package check

import "runtime/debug"

// MemoryLimit is a soft target for the collector here; only Linux enforces it.
const MemoryLimit = 512 << 20

func limitMemory() error {
	debug.SetMemoryLimit(MemoryLimit)
	return nil
}
