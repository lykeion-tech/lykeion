package daemon

import (
	"context"
	"lykeion/server/internal/daemon/sandbox"
	"lykeion/server/pkg/agent"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestCoresearchCLIPathProvidesPrivateNamedExecutable(t *testing.T) {
	source := filepath.Join(t.TempDir(), "daemon-renamed")
	if err := os.WriteFile(source, []byte("#!/bin/sh\nprintf trusted"), 0700); err != nil {
		t.Fatal(err)
	}
	dir, cleanup, err := coresearchCLIPath(source)
	if err != nil {
		t.Fatal(err)
	}
	defer cleanup()
	second, cleanup2, err := coresearchCLIPath(source)
	if err != nil {
		t.Fatal(err)
	}
	defer cleanup2()
	if dir == second {
		t.Fatal("concurrent runs share alias directory")
	}
	cmd := exec.Command("/bin/sh", "-c", "command -v lyk >/dev/null && lyk")
	cmd.Env = []string{"PATH=" + dir + ":/usr/bin:/bin"}
	b, err := cmd.CombinedOutput()
	if err != nil || strings.TrimSpace(string(b)) != "trusted" {
		t.Fatalf("lookup failed: %s %v", b, err)
	}
	cleanup()
	if _, err := os.Stat(dir); !os.IsNotExist(err) {
		t.Fatal("alias not retired")
	}
	if _, err := os.Stat(filepath.Join(second, "lyk")); err != nil {
		t.Fatal("other run's alias removed", err)
	}
}

func TestCoresearchCLIPathCopiesExecutable(t *testing.T) {
	source := filepath.Join(t.TempDir(), "daemon")
	original := []byte("#!/bin/sh\nprintf trusted")
	if err := os.WriteFile(source, original, 0700); err != nil {
		t.Fatal(err)
	}
	dir, cleanup, err := coresearchCLIPath(source)
	if err != nil {
		t.Fatal(err)
	}
	defer cleanup()
	projection := filepath.Join(dir, "lyk")
	sourceInfo, err := os.Stat(source)
	if err != nil {
		t.Fatal(err)
	}
	projectionInfo, err := os.Stat(projection)
	if err != nil {
		t.Fatal(err)
	}
	if os.SameFile(sourceInfo, projectionInfo) {
		t.Fatal("projection must not be a hard link to the daemon executable")
	}
	if projectionInfo.Mode().Perm() != 0500 {
		t.Fatalf("projection permissions = %o, want 0500", projectionInfo.Mode().Perm())
	}
	if err := os.Chmod(projection, 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(projection, []byte("changed"), 0700); err != nil {
		t.Fatal(err)
	}
	got, err := os.ReadFile(source)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != string(original) {
		t.Fatalf("source changed through projection: %q", got)
	}
}

func TestCoresearchCLIAliasUnderSandbox(t *testing.T) {
	if err := sandbox.Check(context.Background()); err != nil {
		t.Skipf("sandbox unavailable: %v", err)
	}
	t.Setenv("HOME", t.TempDir())
	scratch := t.TempDir()
	run := filepath.Join(scratch, "run")
	must(t, os.MkdirAll(run, 0700))
	self, err := os.Executable()
	must(t, err)
	dir, cleanup, err := coresearchCLIPath(self)
	must(t, err)
	defer cleanup()
	d := &daemon{cfg: Config{Home: scratch, StateDir: t.TempDir()}}
	command := d.cliSandboxWithRetention(run, nil, false, "", nil, dir)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	cmd, err := command(ctx, agent.CLIPaths{}, "/bin/sh", "-c", "command -v lyk && lyk -test.list '^$' && if rm \"$LYK_ALIAS\" 2>/dev/null; then exit 7; fi && if printf x > \"$LYK_ALIAS\" 2>/dev/null; then exit 8; fi")
	must(t, err)
	cmd.Dir = run
	cmd.Env = []string{"PATH=" + dir + ":/usr/bin:/bin", "LYK_ALIAS=" + filepath.Join(dir, "lyk")}
	must(t, os.WriteFile(filepath.Join(run, ".agent-process"), []byte("test"), 0600))
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("sandboxed alias: %v %s", err, out)
	}
}
