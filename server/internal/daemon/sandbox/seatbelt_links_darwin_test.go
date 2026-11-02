package sandbox

import (
	"os"
	"path/filepath"
	"testing"
)

// A hidden folder's child links lead out of it; reading through one reaches
// the target, so the target has to be hidden as well.
func TestHiddenFolderLinkChildrenStayHidden(t *testing.T) {
	h := tempDir(t)
	elsewhere := mkdir(t, filepath.Join(h, "elsewhere", "foo"))
	if err := os.WriteFile(filepath.Join(elsewhere, "SKILL.md"), []byte("PRIVATE SKILL"), 0o600); err != nil {
		t.Fatal(err)
	}
	skills := mkdir(t, filepath.Join(h, ".claude", "skills"))
	link := filepath.Join(skills, "foo")
	if err := os.Symlink(filepath.Join(elsewhere, "SKILL.md"), link); err != nil {
		t.Fatal(err)
	}
	// Without the hiding the same read works, so the failure below is the hiding.
	if out, err := sandboxed(t, Policy{Home: h, HomeRead: true}, "cat "+shq(link)); err != nil || out != "PRIVATE SKILL" {
		t.Fatalf("control read: %q %v", out, err)
	}
	p := Policy{Home: h, HomeRead: true, Hidden: []string{skills}}
	if out, err := sandboxed(t, p, "cat "+shq(link)); err == nil {
		t.Fatalf("hidden folder's link child readable: %s", out)
	}
}
