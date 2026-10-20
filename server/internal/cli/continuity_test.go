package cli

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/google/uuid"
)

func TestWorkspaceContinuityCommands(t *testing.T) {
	configDir := t.TempDir()
	t.Setenv("LYK_CONFIG", filepath.Join(configDir, "config.json"))
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
	if got := run("status"); !strings.Contains(got, "sessions kept 30 days (default)") {
		t.Fatal(got)
	}
	for _, removed := range []string{"enable", "disable"} {
		var out bytes.Buffer
		root := Root(strings.NewReader(""), &out)
		root.SetArgs([]string{"workspace", "continuity", removed, "--workspace", ws})
		if err := root.Execute(); err == nil || !strings.Contains(err.Error(), "unknown command") {
			t.Fatalf("%s: %v", removed, err)
		}
	}
	policyDir := filepath.Join(configDir, "daemon", "workspace-profiles", ws)
	if err := os.MkdirAll(policyDir, 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(policyDir, "policy.json"), []byte(`{"version":1,"mode":"private","session_retention_days":7}`), 0600); err != nil {
		t.Fatal(err)
	}
	if got := run("status"); !strings.Contains(got, "sessions kept 7 days (policy ") {
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
