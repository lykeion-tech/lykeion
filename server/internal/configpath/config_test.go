package configpath

import (
	"os"
	"path/filepath"
	"slices"
	"testing"
)

func TestPrivateCoversDefaultXDGAndActualWithoutReadingFiles(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(t.TempDir(), "external-xdg"))
	for _, actual := range []string{"", filepath.Join(t.TempDir(), "custom.json"), "relative-personal-config.json"} {
		t.Setenv("LYK_CONFIG", actual)
		selected, err := Actual()
		if err != nil {
			t.Fatal(err)
		}
		paths, err := Private()
		if err != nil {
			t.Fatal(err)
		}
		selected, err = filepath.Abs(selected)
		if err != nil {
			t.Fatal(err)
		}
		def, err := Default()
		if err != nil {
			t.Fatal(err)
		}
		for _, expected := range []string{selected, def, filepath.Join(os.Getenv("XDG_CONFIG_HOME"), "lyk", "config.json")} {
			if !slices.Contains(paths, expected) {
				t.Fatalf("missing %s in %v", expected, paths)
			}
			if _, err := os.Stat(expected); !os.IsNotExist(err) {
				t.Fatalf("resolver created or read an existing fixture: %v", err)
			}
		}
	}
}
