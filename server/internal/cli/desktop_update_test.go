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
	if err != nil {
		t.Fatalf("unexpected error %v", err)
	}
	if strings.TrimSpace(out.String()) != `{"state":"error","category":"unsupported"}` {
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

func TestDesktopUpdateStreamDisabledWithoutConfiguration(t *testing.T) {
	var out bytes.Buffer
	command := Root(strings.NewReader("STREAM\n"), &out)
	if found, _, err := command.Find([]string{"desktop-update-stream"}); err != nil || found.Name() != "desktop-update-stream" {
		t.Fatal("stream command not registered")
	}
	command.SetArgs([]string{"desktop-update-stream"})
	if err := command.Execute(); err == nil || out.Len() != 0 {
		t.Fatalf("disabled stream wrote output or succeeded: %v %q", err, out.String())
	}
}
