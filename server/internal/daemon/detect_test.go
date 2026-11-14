package daemon

import (
	"os"
	"path/filepath"
	"testing"
)

func TestDetectFindsOnlySupportedCLIs(t *testing.T) {
	bin := t.TempDir()
	for _, name := range []string{"claude", "codex", "cursor-agent", "agy"} {
		if err := os.WriteFile(filepath.Join(bin, name), []byte("#!/bin/sh\n"), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	t.Setenv("PATH", bin)
	got := Detect()
	if len(got) != 2 || got["claude"] != filepath.Join(bin, "claude") || got["codex"] != filepath.Join(bin, "codex") {
		t.Fatalf("%v", got)
	}
}
