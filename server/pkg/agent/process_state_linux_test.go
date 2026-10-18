//go:build linux

package agent

import (
	"errors"
	"os"
	"path/filepath"
	"testing"
)

func TestLinuxGroupObservationRejectsVanishedCapturedPID(t *testing.T) {
	root := t.TempDir()
	if err := os.Mkdir(filepath.Join(root, "102"), 0700); err != nil {
		t.Fatal(err)
	}
	members, err := observeLinuxGroup(101, root)
	if !errors.Is(err, errProcessObservationIncomplete) || len(members) != 0 {
		t.Fatalf("vanished PID silently omitted: %+v %v", members, err)
	}
}
