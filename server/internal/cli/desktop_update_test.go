package cli

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestDesktopUpdateCheckIsDisabledAndDoesNotLoadConfig(t *testing.T) {
	path := filepath.Join(t.TempDir(), "malformed.json")
	if err := os.WriteFile(path, []byte("not valid json"), 0600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("LYK_CONFIG", path)
	var out bytes.Buffer
	command := Root(strings.NewReader(""), &out)
	command.SetArgs([]string{"desktop-update-check"})
	err := command.Execute()
	if err == nil || err.Error() != "update check unavailable" {
		t.Fatalf("unexpected error %v", err)
	}
	if out.Len() != 0 {
		t.Fatalf("unexpected output %q", out.String())
	}
}

func TestDesktopUpdateBuildInfoIsLocalAndDoesNotLoadConfig(t *testing.T) {
	path := filepath.Join(t.TempDir(), "malformed.json")
	if err := os.WriteFile(path, []byte("not valid json"), 0600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("LYK_CONFIG", path)
	var out bytes.Buffer
	command := Root(strings.NewReader(""), &out)
	command.SetArgs([]string{"desktop-update-build-info"})
	err := command.Execute()
	if err == nil || err.Error() != "update trust unavailable" {
		t.Fatalf("unexpected error %v", err)
	}
	if out.Len() != 0 {
		t.Fatalf("unexpected output %q", out.String())
	}
}
