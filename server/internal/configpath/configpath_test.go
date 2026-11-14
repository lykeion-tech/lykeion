package configpath

import (
	"os"
	"path/filepath"
	"slices"
	"testing"
)

func TestPrivateProtectsAbsentAndSiblingProfileTrees(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(t.TempDir(), "xdg"))
	def, err := Default()
	if err != nil {
		t.Fatal(err)
	}
	selected := filepath.Join(filepath.Dir(def), "profiles", "desktop-one", "config.json")
	t.Setenv("LYK_CONFIG", selected)
	paths, err := Private()
	if err != nil {
		t.Fatal(err)
	}
	for _, root := range []string{filepath.Join(filepath.Dir(def), "profiles"), filepath.Join(os.Getenv("XDG_CONFIG_HOME"), "lyk", "profiles")} {
		if !slices.Contains(paths, root) {
			t.Fatalf("future profile root %s is visible: %v", root, paths)
		}
		if _, err := os.Stat(root); !os.IsNotExist(err) {
			t.Fatalf("resolver created root: %v", err)
		}
	}
}
