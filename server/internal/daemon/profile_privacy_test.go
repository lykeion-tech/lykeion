package daemon

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"testing"

	"lykeion/server/internal/configpath"
	"lykeion/server/internal/daemon/envs"
	"lykeion/server/internal/daemon/sandbox"
	"lykeion/server/pkg/agent"
)

func TestPoliciesHideWholeProfileRoots(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(home, "external-xdg"))
	config, err := configpath.Default()
	must(t, err)
	t.Setenv("LYK_CONFIG", filepath.Join(filepath.Dir(config), "profiles", "selected", "config.json"))
	scratch := filepath.Join(home, "scratch")
	run := filepath.Join(scratch, "current")
	state := filepath.Join(home, "daemon-state")
	bases := envs.NewBases(filepath.Join(home, "bases"))
	for _, dir := range []string{run, state} {
		must(t, os.MkdirAll(dir, 0700))
	}
	d := &daemon{cfg: Config{Home: scratch, StateDir: state}}
	cli, err := d.cliPolicy(run, nil, false, agent.CLIPaths{})
	must(t, err)
	service, err := servicePolicy(state, scratch, t.TempDir(), t.TempDir(), t.TempDir(), bases)
	must(t, err)
	for _, root := range []string{filepath.Join(filepath.Dir(config), "profiles"), filepath.Join(home, ".config", "lyk", "profiles"), filepath.Join(os.Getenv("XDG_CONFIG_HOME"), "lyk", "profiles")} {
		for _, p := range []sandbox.Policy{cli, service} {
			if !slices.Contains(p.Hidden, root) {
				t.Fatalf("profile root missing: %s %+v", root, p)
			}
		}
	}
}

func TestCLISandboxHidesProfilesCreatedAfterLaunch(t *testing.T) {
	if err := sandbox.Check(t.Context()); err != nil {
		t.Skipf("sandbox unavailable: %v", err)
	}
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(home, "external-xdg"))
	config, err := configpath.Default()
	must(t, err)
	t.Setenv("LYK_CONFIG", filepath.Join(filepath.Dir(config), "profiles", "selected", "config.json"))
	scratch := filepath.Join(home, "scratch")
	run := filepath.Join(scratch, "current")
	must(t, os.MkdirAll(run, 0700))
	d := &daemon{cfg: Config{Home: scratch, StateDir: t.TempDir()}}
	p, err := d.cliPolicy(run, nil, false, agent.CLIPaths{})
	must(t, err)
	future := []string{os.Getenv("LYK_CONFIG"), filepath.Join(filepath.Dir(config), "profiles", "future-sibling", "config.json"), filepath.Join(os.Getenv("XDG_CONFIG_HOME"), "lyk", "profiles", "late", "config.json"), filepath.Join(home, ".config", "lyk", "profiles", "late", "config.json")}
	ready, goFile := filepath.Join(run, "ready"), filepath.Join(run, "go")
	script := "echo ready > " + strconv.Quote(ready) + "; while [ ! -f " + strconv.Quote(goFile) + " ]; do sleep 0.01; done; "
	for _, path := range future {
		script += "if cat " + strconv.Quote(path) + " >/dev/null 2>&1; then echo exposed-profile; exit 17; fi; "
	}
	script += "echo own > " + strconv.Quote(filepath.Join(run, "own"))
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	cmd, err := p.Command(ctx, "/bin/sh", "-c", script)
	must(t, err)
	var output bytes.Buffer
	cmd.Stdout = &output
	cmd.Stderr = &output
	must(t, cmd.Start())
	t.Cleanup(func() {
		cancel()
		if cmd.ProcessState == nil {
			cmd.Wait()
		}
	})
	waitFor(t, func() bool { _, err := os.Stat(ready); return err == nil })
	for _, path := range future {
		must(t, os.MkdirAll(filepath.Dir(path), 0700))
		must(t, os.WriteFile(path, []byte("lyk_private_profile"), 0600))
	}
	must(t, os.WriteFile(goFile, nil, 0600))
	if err := cmd.Wait(); err != nil {
		t.Fatal(fmt.Errorf("late profiles exposed or own writes failed: %w %s", err, output.String()))
	}
}
