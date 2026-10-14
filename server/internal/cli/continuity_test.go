package cli

import (
	"bytes"
	"strings"
	"testing"

	"github.com/google/uuid"
)

func TestWorkspaceContinuityCommands(t *testing.T) {
	t.Setenv("LYK_CONFIG", t.TempDir()+"/config.json")
	ws := uuid.NewString()
	run := func(args ...string) string {
		t.Helper()
		var out bytes.Buffer
		root := Root(strings.NewReader(""), &out)
		root.SetArgs(append([]string{"workspace", "continuity"}, append(args, "--workspace", ws)...))
		if err := root.Execute(); err != nil {
			t.Fatalf("%v: %v", args, err)
		}
		return out.String()
	}
	if got := run("status"); !strings.Contains(got, "mode workspace_continuity (default)") {
		t.Fatal(got)
	}
	run("disable")
	if got := run("status"); !strings.Contains(got, "mode private (policy ") {
		t.Fatal(got)
	}
	run("reset")
	if got := run("status"); !strings.Contains(got, "(default)") {
		t.Fatal(got)
	}
	var out bytes.Buffer
	root := Root(strings.NewReader(""), &out)
	root.SetArgs([]string{"workspace", "continuity", "status", "--workspace", "../x"})
	if root.Execute() == nil {
		t.Fatal("accepted a non-UUID workspace")
	}
}
