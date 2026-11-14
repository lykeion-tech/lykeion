package daemon

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"testing"

	"lykeion/server/internal/daemon/envs"
)

// environmentFailureSnapshot is the daemon-owned evidence the hand-in can
// use; it must not depend on the text the model writes in its hand-in.
type environmentFailureSnapshot interface {
	EnvironmentFailures() []string
}

func recordedEnvironmentFailures(t *testing.T, d *Door) []string {
	t.Helper()
	reporter, ok := any(d).(environmentFailureSnapshot)
	if !ok {
		t.Fatal("Door does not expose trusted environment failure evidence")
	}
	return reporter.EnvironmentFailures()
}

func TestDoorEnvironmentFailuresTrackLanguageAndSuccessfulRecovery(t *testing.T) {
	l := newLykeion(t)
	dir := t.TempDir()
	d := runDoor(t, l, dir, envs.Python)
	must(t, os.WriteFile(filepath.Join(dir, "check.py"), []byte("echo ready\n"), 0600))
	must(t, os.WriteFile(filepath.Join(dir, "check.R"), []byte("echo ready\n"), 0600))
	ready := d.interp
	unavailable := map[string]bool{envs.Python: true, envs.R: true}
	d.interp = func(lang string) (string, []string, error) {
		if unavailable[lang] {
			return "", nil, envs.ErrNotReady
		}
		return ready(lang)
	}
	if _, _, err := d.runCode(context.Background(), nil, runCodeIn{Language: envs.Python, Code: "print(1)"}); !errors.Is(err, envs.ErrNotReady) {
		t.Fatalf("run_code failure = %v, want environment not ready", err)
	}
	if _, _, err := d.runScript(context.Background(), nil, runScriptIn{Path: "check.R"}); !errors.Is(err, envs.ErrNotReady) {
		t.Fatalf("run_script failure = %v, want environment not ready", err)
	}
	got := recordedEnvironmentFailures(t, d)
	if !reflect.DeepEqual(got, []string{envs.Python, envs.R}) {
		t.Fatalf("unavailable languages = %v, want [python r]", got)
	}
	// Returning a valid interpreter alone is not evidence that a run worked.
	unavailable[envs.Python] = false
	if _, _, err := d.runScript(context.Background(), nil, runScriptIn{Path: "check.py"}); err != nil {
		t.Fatalf("successful Python retry: %v", err)
	}
	got = recordedEnvironmentFailures(t, d)
	if !reflect.DeepEqual(got, []string{envs.R}) {
		t.Fatalf("unavailable languages after Python recovered = %v, want [r]", got)
	}
	if other := recordedEnvironmentFailures(t, runDoor(t, l, t.TempDir(), envs.Python)); len(other) != 0 {
		t.Fatalf("new attempt inherited prior failures: %v", other)
	}
}

func TestDoorSetupRecordsTypedEnvironmentFailure(t *testing.T) {
	l := newLykeion(t)
	d := runDoor(t, l, t.TempDir()) // no Python base
	if _, _, err := d.setUp(context.Background(), nil, setupIn{Python: "3.11"}); !errors.Is(err, envs.ErrNotReady) {
		t.Fatalf("setup failure = %v, want environment not ready", err)
	}
	if got := recordedEnvironmentFailures(t, d); !reflect.DeepEqual(got, []string{envs.Python}) {
		t.Fatalf("unavailable languages after setup = %v, want [python]", got)
	}
}

func TestDoorRunCodeSuccessClearsOnlyItsOwnEnvironmentFailure(t *testing.T) {
	t.Setenv("LYK_KERNEL_STUB", "1")
	l := newLykeion(t)
	d := runDoor(t, l, t.TempDir(), envs.Python)
	ready := d.interp
	unavailable := true
	d.interp = func(lang string) (string, []string, error) {
		if unavailable {
			return "", nil, envs.ErrNotReady
		}
		return ready(lang)
	}
	if _, _, err := d.runCode(context.Background(), nil, runCodeIn{Language: envs.Python, Code: "x = 1"}); !errors.Is(err, envs.ErrNotReady) {
		t.Fatalf("initial run_code failure = %v", err)
	}
	if got := recordedEnvironmentFailures(t, d); !reflect.DeepEqual(got, []string{envs.Python}) {
		t.Fatalf("initial unavailable languages = %v", got)
	}
	unavailable = false
	if _, _, err := d.runCode(context.Background(), nil, runCodeIn{Language: envs.Python, Code: "x = 1"}); err != nil {
		t.Fatalf("successful run_code retry: %v", err)
	}
	if got := recordedEnvironmentFailures(t, d); len(got) != 0 {
		t.Fatalf("run_code success left stale environment failure: %v", got)
	}
}

func TestDoorDoesNotRecordUnrelatedToolErrorAsEnvironmentFailure(t *testing.T) {
	l := newLykeion(t)
	d := runDoor(t, l, t.TempDir(), envs.Python)
	if _, _, err := d.runScript(context.Background(), nil, runScriptIn{Path: "missing.py"}); err == nil {
		t.Fatal("expected missing script error")
	}
	if got := recordedEnvironmentFailures(t, d); len(got) != 0 {
		t.Fatalf("missing script was classified as environment failure: %v", got)
	}
}

func TestDoorRecordsEnvironmentFailureDuringPackageLookup(t *testing.T) {
	l := newLykeion(t)
	dir := t.TempDir()
	d := runDoor(t, l, dir, envs.Python)
	must(t, os.WriteFile(filepath.Join(dir, "check.py"), []byte("echo ready\n"), 0600))
	ready := d.interp
	d.interp = func(lang string) (string, []string, error) {
		name, args, err := ready(lang)
		if err == nil {
			if removeErr := os.Remove(filepath.Join(d.Study.Bases.Dir, lang, "ready")); removeErr != nil {
				return "", nil, removeErr
			}
		}
		return name, args, err
	}
	if _, _, err := d.runScript(context.Background(), nil, runScriptIn{Path: "check.py"}); !errors.Is(err, envs.ErrNotReady) {
		t.Fatalf("package lookup failure = %v, want environment not ready", err)
	}
	if got := recordedEnvironmentFailures(t, d); !reflect.DeepEqual(got, []string{envs.Python}) {
		t.Fatalf("unavailable languages after package lookup = %v, want [python]", got)
	}
}

func TestDoorRecordsMissingPythonPrerequisiteForR(t *testing.T) {
	l := newLykeion(t)
	dir := t.TempDir()
	d := runDoor(t, l, dir, envs.R)
	d.Study.ReadOnly = true
	must(t, os.WriteFile(filepath.Join(dir, "check.R"), []byte("print(1)\n"), 0600))
	if _, _, err := d.runScript(context.Background(), nil, runScriptIn{Path: "check.R"}); !errors.Is(err, envs.ErrNotReady) {
		t.Fatalf("R prerequisite failure = %v, want environment not ready", err)
	}
	if got := recordedEnvironmentFailures(t, d); !reflect.DeepEqual(got, []string{envs.Python}) {
		t.Fatalf("unavailable languages after R prerequisite = %v, want [python]", got)
	}
}
