package daemon

import (
	"bufio"
	"bytes"
	"fmt"
	"io"
	"lykeion/server/internal/daemon/envs"
	"lykeion/server/internal/daemon/sandbox"
	"lykeion/server/pkg/agent"
	"os"
	"path/filepath"
	"slices"
	"testing"
)

func TestPrivatePathsIncludeTokenConfigurations(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	external := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(external, "xdg"))
	actual := filepath.Join(external, "actual.json")
	t.Setenv("LYK_CONFIG", actual)
	config, err := os.UserConfigDir()
	must(t, err)
	d := &daemon{cfg: Config{StateDir: filepath.Join(external, "daemon")}}
	paths, err := d.privatePaths()
	must(t, err)
	for _, path := range []string{actual, filepath.Join(config, "lyk", "config.json"), filepath.Join(external, "xdg", "lyk", "config.json"), d.cfg.StateDir} {
		if !slices.Contains(paths, path) {
			t.Errorf("missing private path %s: %v", path, paths)
		}
	}
}

func TestServicePolicyRestrictsPrivateState(t *testing.T) {
	root := t.TempDir()
	t.Setenv("HOME", t.TempDir())
	actual := filepath.Join(root, "config.json")
	t.Setenv("LYK_CONFIG", actual)
	stateRoot := filepath.Join(root, "daemon")
	code := filepath.Join(stateRoot, "services", "study", "evaluator", "code")
	state := filepath.Join(stateRoot, "services", "study", "evaluator", "state")
	p, err := servicePolicy(stateRoot, filepath.Join(root, "runs"), code, state, t.TempDir(), &envs.Bases{Dir: filepath.Join(root, "bases")})
	must(t, err)
	if !slices.Contains(p.Hidden, actual) {
		t.Errorf("config not hidden")
	}
	if !slices.Contains(p.RestrictedRoots, stateRoot) {
		t.Errorf("state not restricted")
	}
	if !slices.Contains(p.Hidden, filepath.Join(root, "daemon.log")) {
		t.Error("log not hidden")
	}
	if !slices.Contains(p.Read, code) || !slices.Contains(p.Write, state) {
		t.Error("own grants missing")
	}
}

func TestPrivatePoliciesDenyExternalConfigsAndSiblingState(t *testing.T) {
	for _, late := range []bool{false, true} {
		t.Run(fmt.Sprintf("late=%v", late), func(t *testing.T) {
			raw, err := os.MkdirTemp(".", ".private-policy-")
			must(t, err)
			t.Cleanup(func() { os.RemoveAll(raw) })
			root, err := filepath.Abs(raw)
			must(t, err)
			root, err = filepath.EvalSymlinks(root)
			must(t, err)
			home := t.TempDir()
			t.Setenv("HOME", home)
			t.Setenv("XDG_CONFIG_HOME", filepath.Join(root, "xdg"))
			actual := filepath.Join(root, "actual.json")
			t.Setenv("LYK_CONFIG", actual)
			cfg, err := os.UserConfigDir()
			must(t, err)
			stateRoot := filepath.Join(root, "daemon")
			code := filepath.Join(stateRoot, "services", "mine", "code")
			state := filepath.Join(stateRoot, "services", "mine", "state")
			run := filepath.Join(root, "runs", "run")
			inputs := filepath.Join(run, "attachments")
			for _, p := range []string{code, state, inputs, filepath.Join(root, "xdg", "lyk"), filepath.Join(cfg, "lyk"), filepath.Join(stateRoot, "cache")} {
				must(t, os.MkdirAll(p, 0700))
			}
			input := filepath.Join(inputs, "input.txt")
			must(t, os.WriteFile(input, []byte("OWN_INPUT"), 0444))
			serviceInput := filepath.Join(code, "code.txt")
			must(t, os.WriteFile(serviceInput, []byte("OWN_CODE"), 0444))
			hidden := []string{actual, filepath.Join(root, "xdg", "lyk", "config.json"), filepath.Join(cfg, "lyk", "config.json"), filepath.Join(root, "daemon.log"), filepath.Join(stateRoot, "cache", "other-study"), filepath.Join(stateRoot, "metadata"), filepath.Join(stateRoot, "services", "other", "state", "answer"), filepath.Join(stateRoot, "services", "other", "code", "source"), filepath.Join(root, "runs", "other", "secret"), filepath.Join(root, "bases", "studies", "other", "secret")}
			create := func() {
				for _, p := range hidden {
					must(t, os.MkdirAll(filepath.Dir(p), 0700))
					must(t, os.WriteFile(p, []byte("PRIVATE_SENTINEL"), 0600))
				}
			}
			if !late {
				create()
			}
			bases := &envs.Bases{Dir: filepath.Join(root, "bases")}
			baseFile := filepath.Join(bases.Dir, "python", "trusted")
			must(t, os.MkdirAll(filepath.Dir(baseFile), 0700))
			must(t, os.WriteFile(baseFile, []byte("ALLOWED"), 0444))
			study := envs.ForStudy(bases, filepath.Join(bases.Dir, "studies", "own"))
			must(t, os.MkdirAll(study.Dir, 0700))
			studyFile := filepath.Join(study.Dir, "own-data")
			must(t, os.WriteFile(studyFile, []byte("ALLOWED"), 0600))
			d := &daemon{cfg: Config{Home: filepath.Join(root, "runs"), StateDir: stateRoot}, bases: bases}
			cli, err := d.cliPolicy(run, study, false, agent.CLIPaths{})
			must(t, err)
			private, err := d.privatePaths()
			must(t, err)
			door := &Door{ctx: t.Context(), home: home, Study: study, PrivatePaths: private, PrivateRoots: []string{filepath.Join(root, "runs")}, Child: func() agent.ExecOptions { return agent.ExecOptions{Dir: run} }}
			must(t, door.sandboxReady())
			t.Cleanup(func() { os.RemoveAll(door.tmp); os.RemoveAll(door.bridgeDir) })
			servicePolicy, err := servicePolicy(stateRoot, filepath.Join(root, "runs"), code, state, t.TempDir(), bases)
			must(t, err)
			// Exercise filesystem enforcement only. Production macOS service startup
			// remains blocked by the strict networking requirement.
			servicePolicy.LoopbackOnly = false
			for _, tc := range []struct {
				name            string
				p               sandbox.Policy
				input, positive string
			}{{"cli", cli, input, "OWN_INPUT"}, {"door", door.policy, input, "OWN_INPUT"}, {"service", servicePolicy, serviceInput, "OWN_CODE"}} {
				t.Run(tc.name, func(t *testing.T) {
					if late {
						for _, path := range hidden {
							err := os.Remove(path)
							if err != nil && !os.IsNotExist(err) {
								t.Fatal(err)
							}
						}
					}
					script := `cat "$1"; echo READY; read go; shift; for file do cat "$file"; done`
					args := append([]string{"-c", script, "sh", tc.input}, hidden...)
					cmd, err := tc.p.Command(t.Context(), "/bin/sh", args...)
					must(t, err)
					stdout, err := cmd.StdoutPipe()
					must(t, err)
					stdin, err := cmd.StdinPipe()
					must(t, err)
					var stderr bytes.Buffer
					cmd.Stderr = &stderr
					must(t, cmd.Start())
					reader := bufio.NewReader(stdout)
					line, err := reader.ReadString('\n')
					must(t, err)
					if line != tc.positive+"READY\n" {
						t.Fatalf("own read failed: %q", line)
					}
					if late {
						create()
					}
					_, err = io.WriteString(stdin, "continue\n")
					must(t, err)
					must(t, stdin.Close())
					output, err := io.ReadAll(reader)
					must(t, err)
					_ = cmd.Wait()
					if len(output) != 0 {
						t.Fatalf("private read: %s", output)
					}
					allowed := []string{baseFile}
					if tc.name != "service" {
						allowed = append(allowed, studyFile)
					}
					for _, path := range allowed {
						cmd, err := tc.p.Command(t.Context(), "/bin/cat", path)
						must(t, err)
						output, err := cmd.CombinedOutput()
						if err != nil || string(output) != "ALLOWED" {
							t.Fatalf("permitted read %s: %s %v", path, output, err)
						}
					}
					if tc.name == "service" {
						cmd, err := tc.p.Command(t.Context(), "/bin/sh", "-c", `printf own > "$1"`, "sh", filepath.Join(state, "allowed"))
						must(t, err)
						output, err := cmd.CombinedOutput()
						if err != nil {
							t.Fatalf("own state write: %s %v", output, err)
						}
					}
				})
			}
		})
	}
}

func TestDoorAndServiceRestrictExternalBaseRoot(t *testing.T) {
	root := t.TempDir()
	home := t.TempDir()
	t.Setenv("HOME", home)
	bases := &envs.Bases{Dir: filepath.Join(root, "bases")}
	study := envs.ForStudy(bases, filepath.Join(bases.Dir, "studies", "own"))
	run := filepath.Join(root, "runs", "own")
	for _, p := range []string{run, study.Dir} {
		must(t, os.MkdirAll(p, 0700))
	}
	door := &Door{ctx: t.Context(), home: home, Study: study, PrivateRoots: []string{filepath.Join(root, "runs")}, Child: func() agent.ExecOptions { return agent.ExecOptions{Dir: run} }}
	must(t, door.sandboxReady())
	t.Cleanup(func() { os.RemoveAll(door.tmp); os.RemoveAll(door.bridgeDir) })
	if !slices.Contains(door.policy.RestrictedRoots, filepath.Join(root, "runs")) {
		t.Error("Door scratch root missing")
	}
	if !slices.Contains(door.policy.RestrictedRoots, bases.Dir) {
		t.Error("Door exposes other studies in external base root")
	}
	stateRoot := filepath.Join(root, "daemon")
	p, err := servicePolicy(stateRoot, filepath.Join(root, "runs"), filepath.Join(stateRoot, "code"), filepath.Join(stateRoot, "state"), t.TempDir(), bases)
	must(t, err)
	if !slices.Contains(p.RestrictedRoots, filepath.Join(root, "runs")) {
		t.Error("service scratch root missing")
	}
	if !slices.Contains(p.RestrictedRoots, bases.Dir) {
		t.Error("service exposes other studies in external base root")
	}
}
