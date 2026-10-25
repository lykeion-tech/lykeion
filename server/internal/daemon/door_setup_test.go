package daemon

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"lykeion/server/internal/daemon/envs"
)

func TestAFirstInstallRecordsThirteen(t *testing.T) {
	l := newLykeion(t)
	dir := t.TempDir()
	d := runDoor(t, l, dir, envs.Python)
	d.Study.Env()
	inventory := filepath.Join(d.Study.Dir, "installed.txt")
	must(t, os.WriteFile(filepath.Join(d.Study.Dir, "python", "bin", "python"), []byte("#!/bin/sh\n[ ! -e '"+inventory+"' ] || cat '"+inventory+"'\n"), 0755))
	must(t, os.WriteFile(filepath.Join(dir, "install.py"), []byte("printf 'example==1.0\\n' > '"+inventory+"'"), 0600))
	if _, err := useTool(t, d, "run_script", map[string]any{"path": "install.py"}); err != nil {
		t.Fatal(err)
	}
	l.mu.Lock()
	got := l.pythonVersion
	l.mu.Unlock()
	if got != "3.13" {
		t.Fatalf("recorded %q", got)
	}
	_, _, err := d.setUp(context.Background(), nil, setupIn{Python: "3.11"})
	if err == nil || !strings.Contains(err.Error(), "This study uses Python 3.13; a study keeps one Python version.") {
		t.Fatal(err)
	}
}
func TestTheVersionSurvivesALostFolder(t *testing.T) {
	l := newLykeion(t)
	d := runDoor(t, l, t.TempDir(), envs.Python)
	must(t, d.setPython(context.Background(), "3.13"))
	must(t, os.RemoveAll(d.Study.Dir))
	must(t, os.MkdirAll(d.Study.Dir, 0700))
	_, _, err := d.setUp(context.Background(), nil, setupIn{Python: "3.11"})
	if err == nil || !strings.Contains(err.Error(), "This study uses Python 3.13;") {
		t.Fatal(err)
	}
}
func TestNoCheckOnceRecorded(t *testing.T) {
	l := newLykeion(t)
	dir := t.TempDir()
	d := runDoor(t, l, dir, envs.Python)
	d.Study.Env()
	must(t, d.setPython(context.Background(), "3.13"))
	calls := 0
	original := d.Study.Run
	d.Study.Run = func(ctx context.Context, e []string, dir, name string, a ...string) ([]byte, error) {
		if strings.Contains(strings.Join(a, " "), "freeze --local") {
			calls++
		}
		return original(ctx, e, dir, name, a...)
	}
	must(t, os.WriteFile(filepath.Join(dir, "a.py"), []byte("echo ok"), 0600))
	if _, err := useTool(t, d, "run_script", map[string]any{"path": "a.py"}); err != nil {
		t.Fatal(err)
	}
	if calls != 1 {
		t.Fatalf("expected only mandatory inventory; pip freeze calls=%d", calls)
	}
}
func TestSetUpRejectsEscapingLockPaths(t *testing.T) {
	l := newLykeion(t)
	dir := t.TempDir()
	d := runDoor(t, l, dir, envs.Python)
	outside := filepath.Join(t.TempDir(), "lock.yml")
	must(t, os.WriteFile(outside, []byte("dependencies: []"), 0600))
	must(t, os.Symlink(outside, filepath.Join(dir, "escape.yml")))
	for _, path := range []string{outside, "../lock.yml", "escape.yml"} {
		if _, _, err := d.setUp(context.Background(), nil, setupIn{Lock: path}); err == nil {
			t.Fatalf("accepted %s", path)
		}
	}
}
func TestSetUpRejectsInvalidPythonAndPackageOptions(t *testing.T) {
	l := newLykeion(t)
	d := runDoor(t, l, t.TempDir(), envs.Python)
	for _, in := range []setupIn{{Python: "3.15"}, {Python: "3.11.1"}, {Conda: []string{"--prefix=/outside"}}, {Conda: []string{"evil::python"}}} {
		if _, _, err := d.setUp(context.Background(), nil, in); err == nil {
			t.Fatalf("accepted %+v", in)
		}
	}
}

func TestSetUpWaitHonorsTheCallDeadline(t *testing.T) {
	l := newLykeion(t)
	d := runDoor(t, l, t.TempDir(), envs.Python)
	d.executionMu.Lock()
	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()
	done := make(chan error, 1)
	go func() { _, _, err := d.setUp(ctx, nil, setupIn{Python: "3.11"}); done <- err }()
	select {
	case err := <-done:
		d.executionMu.Unlock()
		if err == nil {
			t.Fatal("cancelled setup succeeded")
		}
	case <-time.After(300 * time.Millisecond):
		d.executionMu.Unlock()
		<-done
		t.Fatal("setup waited past its context")
	}
}

func TestSetupRunnerStopsInstallerChildren(t *testing.T) {
	l := newLykeion(t)
	dir := t.TempDir()
	d := runDoor(t, l, dir, envs.Python)
	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()
	start := time.Now()
	_, err := d.runSandboxed(ctx, nil, dir, "/bin/sh", "-c", "echo $$ > pgid; sleep 2 & wait")
	if err == nil || time.Since(start) > time.Second {
		t.Fatalf("installer cancellation: %v after %s", err, time.Since(start))
	}
	if !groupGone(t, dir) {
		t.Fatal("installer children still running")
	}
}

func TestPythonReplacementPreservesOtherSessions(t *testing.T) {
	t.Setenv("LYK_KERNEL_STUB", "1")
	d := runDoor(t, newLykeion(t), t.TempDir(), envs.Python)
	sessions, err := d.liveSessions(context.Background())
	must(t, err)
	for _, lang := range []string{"python", "r", "julia"} {
		result, err := sessions.Exec(context.Background(), lang, "before")
		if err != nil || result.Stdout != "1 ran before" {
			t.Fatalf("%s: %+v %v", lang, result, err)
		}
	}
	must(t, d.refreshPythonSession(context.Background()))
	sessions, err = d.liveSessions(context.Background())
	must(t, err)
	for _, lang := range []string{"r", "julia"} {
		result, err := sessions.Exec(context.Background(), lang, "after")
		if err != nil || result.Stdout != "2 ran after" {
			t.Fatalf("%s state was cleared: %+v %v", lang, result, err)
		}
	}
	result, err := sessions.Exec(context.Background(), "python", "after")
	if err != nil || result.Stdout != "1 ran after" {
		t.Fatalf("Python did not restart: %+v %v", result, err)
	}
}

func TestCondaPackageInventoryCannotReadOutsideTheStudy(t *testing.T) {
	home, run := t.TempDir(), t.TempDir()
	const sentinel = "harmless-private-study-sentinel"
	secret := filepath.Join(home, "private.txt")
	must(t, os.WriteFile(secret, []byte(sentinel), 0600))
	l := newLykeion(t)
	d := newRunDoor(t, l, run, func(d *Door) { d.home = home }, envs.Python)
	d.Study.Env()
	conda := filepath.Join(d.Study.Dir, "conda")
	must(t, os.MkdirAll(filepath.Join(conda, "bin"), 0755))
	must(t, os.WriteFile(filepath.Join(conda, "bin", "python"), nil, 0755))
	must(t, os.WriteFile(filepath.Join(conda, ".lyk-ready"), nil, 0600))
	must(t, os.Symlink(secret, filepath.Join(conda, "lock.txt")))
	must(t, os.WriteFile(filepath.Join(run, "a.py"), []byte("echo safe"), 0600))
	got, err := useTool(t, d, "run_script", map[string]any{"path": "a.py"})
	if err == nil {
		t.Errorf("outside-study lock was accepted: %s", got)
	}
	runs, _ := l.recorded()
	for _, record := range runs {
		if strings.Contains(fmt.Sprint(record["packages"]), sentinel) {
			t.Fatal("private sentinel escaped into recorded package inventory")
		}
	}
	if len(runs) != 0 {
		t.Fatalf("untrusted inventory produced %d records", len(runs))
	}
}

func TestScriptsDoNotLaunchAfterCondaRecoveryFails(t *testing.T) {
	for _, extension := range []string{"R", "jl"} {
		t.Run(extension, func(t *testing.T) {
			l := newLykeion(t)
			dir := t.TempDir()
			d := runDoor(t, l, dir, envs.Python, envs.R, envs.Julia)
			d.Study.Env()
			must(t, d.Study.RememberPython("3.11"))
			original := d.Study.Run
			d.Study.Run = func(c context.Context, e []string, dir, n string, a ...string) ([]byte, error) {
				if filepath.Base(n) == "micromamba" {
					return nil, errors.New("recovery unavailable")
				}
				if filepath.Base(n) == "Rscript" {
					return []byte("package 1.0"), nil
				}
				return original(c, e, dir, n, a...)
			}
			script := "a." + extension
			must(t, os.WriteFile(filepath.Join(dir, script), []byte("touch launched"), 0600))
			_, err := useTool(t, d, "run_script", map[string]any{"path": script})
			if err == nil {
				t.Error("script launched after recovery failed")
			}
			if _, err := os.Stat(filepath.Join(dir, "launched")); !os.IsNotExist(err) {
				t.Errorf("script executed: %v", err)
			}
			if _, err := useTool(t, d, "run_code", map[string]any{"language": scriptLanguages["."+extension], "code": "1 + 1"}); err == nil {
				t.Error("code accepted after recovery failed")
			}
			runs, _ := l.recorded()
			if len(runs) != 0 {
				t.Errorf("recorded %d runs after recovery failure", len(runs))
			}
		})
	}
}
