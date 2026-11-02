package daemon

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestRunEnvPutsResolvedLykFirstWithoutStudyPaths(t *testing.T) {
	self, _ := os.Executable()
	resolved, err := filepath.EvalSymlinks(self)
	if err != nil {
		t.Fatal(err)
	}
	got, err := runPath()
	if err != nil {
		t.Fatal(err)
	}
	first, rest, _ := strings.Cut(got, string(os.PathListSeparator))
	if first != filepath.Dir(resolved) {
		t.Fatalf("first PATH entry %q, want %q", first, filepath.Dir(resolved))
	}
	if rest != os.Getenv("PATH") {
		t.Fatalf("the person's PATH was altered: %q", rest)
	}
}

func TestLykDirFollowsSymlinks(t *testing.T) {
	real, links := t.TempDir(), t.TempDir()
	must(t, os.WriteFile(filepath.Join(real, "lyk"), nil, 0o755))
	must(t, os.Symlink(filepath.Join(real, "lyk"), filepath.Join(links, "lyk")))
	want, _ := filepath.EvalSymlinks(real)
	if got, err := resolvedDir(filepath.Join(links, "lyk")); err != nil || got != want {
		t.Fatalf("%q %v, want %q", got, err, want)
	}
}
