//go:build linux

package check

import (
	"os"
	"testing"
)

func TestRunMemoryBomb(t *testing.T) {
	_, err := run(t, "def check(records):\n    x = [0] * ((1<<30)-1)\n    return []\n", Default)
	if err == nil {
		t.Fatal("memory bomb accepted")
	}
	if p, perr := os.FindProcess(os.Getpid()); perr != nil || p == nil {
		t.Fatal("test process gone")
	}
	if _, err := run(t, "def check(records):\n    return []\n", Default); err != nil {
		t.Fatalf("server unusable after bomb: %v", err)
	}
}
