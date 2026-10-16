package agent

import (
	"fmt"
	"os"
	"path/filepath"
	"testing"
)

func TestDiscoveryRefusalHasPrelaunchType(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, ".claude.json"), []byte(`{}`), 0600); err != nil {
		t.Fatal(err)
	}
	err := checkOwnedState(root)
	if fmt.Sprintf("%T", err) != "*agent.PrelaunchDiscoveryError" {
		t.Fatalf("discovery refusal is not typed: %T: %v", err, err)
	}
}

func TestDiscoveryClassificationPreservesSymlinkRefusal(t *testing.T) {
	root := t.TempDir()
	if err := os.Symlink("missing", filepath.Join(root, ".claude.json")); err != nil {
		t.Fatal(err)
	}
	err := checkOwnedState(root)
	if err == nil || fmt.Sprintf("%T", err) == "*agent.PrelaunchDiscoveryError" {
		t.Fatalf("symlink classified as discovery: %v", err)
	}
}

func TestDiscoveryCannotHideLaterSymlink(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, ".claude.json"), []byte(`{}`), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink("missing", filepath.Join(root, "z-link")); err != nil {
		t.Fatal(err)
	}
	err := checkOwnedState(root)
	if err == nil || fmt.Sprintf("%T", err) == "*agent.PrelaunchDiscoveryError" {
		t.Fatalf("discovery hid symlink: %v", err)
	}
}
