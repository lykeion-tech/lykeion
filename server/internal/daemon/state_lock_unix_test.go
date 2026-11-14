//go:build darwin || linux

package daemon

import (
	"os"
	"path/filepath"
	"syscall"
	"testing"
)

func TestOwnStateRejectsLinkedAndSpecialFiles(t *testing.T) {
	for _, kind := range []string{"symlink", "hardlink", "fifo"} {
		t.Run(kind, func(t *testing.T) {
			dir := t.TempDir()
			target := filepath.Join(t.TempDir(), "target")
			if err := os.WriteFile(target, []byte("keep"), 0644); err != nil {
				t.Fatal(err)
			}
			if err := os.Chmod(target, 0644); err != nil {
				t.Fatal(err)
			}
			path := filepath.Join(dir, "daemon.lock")
			var err error
			switch kind {
			case "symlink":
				err = os.Symlink(target, path)
			case "hardlink":
				err = os.Link(target, path)
			case "fifo":
				err = syscall.Mkfifo(path, 0600)
			}
			if err != nil {
				t.Fatal(err)
			}
			if f, err := ownState(dir); err == nil {
				f.Close()
				t.Fatal("accepted unsafe lock")
			}
			info, err := os.Stat(target)
			if err != nil {
				t.Fatal(err)
			}
			if info.Mode().Perm() != 0644 {
				t.Fatalf("target mode changed to %o", info.Mode().Perm())
			}
			b, err := os.ReadFile(target)
			if err != nil || string(b) != "keep" {
				t.Fatalf("target changed: %q %v", b, err)
			}
		})
	}
}
