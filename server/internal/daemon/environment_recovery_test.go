package daemon

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"lykeion/server/internal/daemon/envs"
)

func TestEnvironmentFailureClearsAfterInterpreterRuns(t *testing.T) {
	for _, tc := range []struct {
		name    string
		code    string
		cell    bool
		restart bool
	}{
		{name: "script error", code: "exit 9"},
		{name: "cell error", code: "fail", cell: true},
		{name: "empty restart", cell: true, restart: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if tc.cell {
				t.Setenv("LYK_KERNEL_STUB", "1")
			}
			l := newLykeion(t)
			dir := t.TempDir()
			d := runDoor(t, l, dir, envs.Python)
			ready := d.interp
			unavailable := true
			d.interp = func(lang string) (string, []string, error) {
				if unavailable {
					return "", nil, envs.ErrNotReady
				}
				return ready(lang)
			}
			if _, _, err := d.runCode(context.Background(), nil, runCodeIn{Language: envs.Python, Code: "x"}); !errors.Is(err, envs.ErrNotReady) {
				t.Fatalf("initial failure = %v", err)
			}
			unavailable = false
			if tc.cell {
				if _, _, err := d.runCode(context.Background(), nil, runCodeIn{Language: envs.Python, Code: tc.code, Restart: tc.restart}); err != nil {
					t.Fatalf("run code: %v", err)
				}
			} else {
				must(t, os.WriteFile(filepath.Join(dir, "fail.py"), []byte(tc.code+"\n"), 0600))
				if _, _, err := d.runScript(context.Background(), nil, runScriptIn{Path: "fail.py"}); err != nil {
					t.Fatalf("run script: %v", err)
				}
			}
			if got := d.EnvironmentFailures(); len(got) != 0 {
				t.Fatalf("successful interpreter launch left failure: %v", got)
			}
		})
	}
}

func TestSuccessfulRScriptClearsVerifiedPythonPrerequisite(t *testing.T) {
	l := newLykeion(t)
	dir := t.TempDir()
	d := runDoor(t, l, dir, envs.Python, envs.R)
	rbin := filepath.Join(d.Study.Bases.Dir, "r", "bin")
	must(t, os.MkdirAll(rbin, 0755))
	must(t, os.WriteFile(filepath.Join(rbin, "Rscript"), []byte("#!/bin/sh\nexit 0\n"), 0755))
	must(t, os.WriteFile(filepath.Join(dir, "check.R"), []byte("exit 0\n"), 0600))
	ready := d.interp
	unavailable := true
	d.interp = func(lang string) (string, []string, error) {
		if lang == envs.Python && unavailable {
			return "", nil, envs.ErrNotReady
		}
		return ready(lang)
	}
	if _, _, err := d.runCode(context.Background(), nil, runCodeIn{Language: envs.Python, Code: "x"}); !errors.Is(err, envs.ErrNotReady) {
		t.Fatalf("initial Python failure = %v", err)
	}
	unavailable = false
	if _, _, err := d.runScript(context.Background(), nil, runScriptIn{Path: "check.R"}); err != nil {
		t.Fatalf("successful R script: %v", err)
	}
	if err := d.Study.Ready(envs.Python); err != nil {
		t.Fatalf("fixture did not restore Python readiness: %v", err)
	}
	if got := d.EnvironmentFailures(); len(got) != 0 {
		t.Fatalf("verified-ready Python failure retained after R run: %v", got)
	}
}

func TestSuccessfulRScriptKeepsUnavailablePythonFailure(t *testing.T) {
	l := newLykeion(t)
	dir := t.TempDir()
	d := runDoor(t, l, dir, envs.R)
	rbin := filepath.Join(d.Study.Bases.Dir, "r", "bin")
	must(t, os.MkdirAll(rbin, 0755))
	must(t, os.WriteFile(filepath.Join(rbin, "Rscript"), []byte("#!/bin/sh\nexit 0\n"), 0755))
	must(t, os.WriteFile(filepath.Join(dir, "check.R"), []byte("exit 0\n"), 0600))
	if _, _, err := d.runCode(context.Background(), nil, runCodeIn{Language: envs.Python, Code: "x"}); !errors.Is(err, envs.ErrNotReady) {
		t.Fatalf("initial Python failure = %v", err)
	}
	if _, _, err := d.runScript(context.Background(), nil, runScriptIn{Path: "check.R"}); err != nil {
		t.Fatalf("successful R script: %v", err)
	}
	if got := d.EnvironmentFailures(); len(got) != 1 || got[0] != envs.Python {
		t.Fatalf("unavailable Python failure after R run = %v", got)
	}
}
