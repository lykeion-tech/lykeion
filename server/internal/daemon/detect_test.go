package daemon

import (
	"os"
	"path/filepath"
	"testing"
)

func TestDetectFindsTheNewCLIs(t *testing.T) {
	bin := t.TempDir()
	for _, name := range []string{"cursor-agent", "agy"} {
		if err := os.WriteFile(filepath.Join(bin, name), []byte("#!/bin/sh\n"), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	t.Setenv("PATH", bin)
	got := Detect()
	if got["cursor"] != filepath.Join(bin, "cursor-agent") || got["antigravity"] != filepath.Join(bin, "agy") || len(got) != 2 {
		t.Fatalf("%v", got)
	}
}
