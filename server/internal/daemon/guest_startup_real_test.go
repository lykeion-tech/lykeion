package daemon

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"lykeion/server/pkg/agent"
)

func TestRealGuestDaemonCLIStarts(t *testing.T) {
	if runtime.GOOS != "linux" {
		t.Skip("macOS guest runs fail closed until process isolation is enforced")
	}
	if os.Getenv("LYK_REAL_CLI") != "1" {
		t.Skip("set LYK_REAL_CLI=1 to run signed-in CLIs")
	}
	home, err := os.UserHomeDir()
	must(t, err)
	for _, harness := range []string{"claude", "codex"} {
		t.Run(harness, func(t *testing.T) {
			candidates := []string{}
			if bin, err := exec.LookPath(harness); err == nil {
				candidates = append(candidates, bin)
			}
			candidates = append(candidates, filepath.Join(home, ".local", "bin", harness), "/opt/homebrew/bin/"+harness, "/usr/local/bin/"+harness)
			if harness == "codex" {
				candidates = append(candidates, "/Applications/ChatGPT.app/Contents/Resources/codex-cli/CodexCLI.app/Contents/MacOS/codex")
			}
			bin := ""
			for _, candidate := range candidates {
				if st, err := os.Stat(candidate); err == nil && st.Mode().IsRegular() && st.Mode()&0111 != 0 {
					bin = candidate
					break
				}
			}
			if bin == "" {
				t.Fatal("CLI not installed; real guest acceptance cannot run")
			}
			d := &daemon{cfg: Config{Home: t.TempDir(), StateDir: t.TempDir()}}
			guest := uuid.NewString()
			run := filepath.Join(d.cfg.Home, "run")
			must(t, os.Mkdir(run, 0700))
			config := filepath.Join(d.cfg.StateDir, "guests", guest, harness)
			must(t, agent.PrepareGuestHome(harness, agent.AccountHome(harness), config))
			ctx, cancel := context.WithTimeout(t.Context(), 180*time.Second)
			defer cancel()
			auth, err := agent.GuestAuthEnv(ctx, harness)
			must(t, err)
			runCredential := "ltk_fixture_" + uuid.NewString()
			doorCredential := "door-fixture-" + uuid.NewString()
			auth = append(auth, "LYK_DOOR_TOKEN="+doorCredential)
			opts := agent.ExecOptions{Guest: true, Dir: run, ConfigHome: config, CacheDir: d.runCacheDir(guest), MCPToken: runCredential, Env: auth, Sandbox: d.cliSandbox(run, nil, false, guest)}
			var transcript strings.Builder
			helperSucceeded := false
			opts.OnMessage = func(m agent.Message) error {
				if harness == "codex" && m.Type == "tool_use" {
					var event struct {
						Item struct {
							Command  string `json:"command"`
							ExitCode *int   `json:"exit_code"`
						} `json:"item"`
					}
					if json.Unmarshal(m.Content, &event) == nil && strings.Contains(event.Item.Command, "TestGuestShellPathHelper") {
						if event.Item.ExitCode != nil {
							t.Logf("trusted PATH helper shell: %s; exit=%d", event.Item.Command, *event.Item.ExitCode)
							helperSucceeded = *event.Item.ExitCode == 0
						}
					}
				}
				if m.Type == "text" {
					transcript.Write(m.Content)
				}
				return nil
			}
			var backend agent.Backend = agent.Claude{Bin: bin}
			if harness == "codex" {
				backend = agent.Codex{Bin: bin}
			}
			marker := "guest-memory-" + uuid.NewString()
			helperPrompt := ""
			if harness == "codex" {
				self, err := os.Executable()
				must(t, err)
				// The production policy already grants the daemon executable.
				// Discover it by name, as a real run discovers lyk on trusted PATH.
				opts.Env = append(opts.Env, "PATH="+filepath.Dir(self)+":"+os.Getenv("PATH"), "GUEST_SHELL_PATH_HELPER="+marker)
				helperPrompt = " First use the shell tool to run `" + filepath.Base(self) + " -test.run=^TestGuestShellPathHelper$` exactly by its name on inherited PATH, without modifying PATH or choosing another shell or absolute executable path. Do not write files yourself."
			}
			checkHelper := func() {
				if harness == "codex" {
					if !helperSucceeded {
						t.Fatal("Codex shell did not successfully execute trusted PATH helper")
					}
					helperSucceeded = false
					proof := filepath.Join(run, "guest-helper-proof")
					data, err := os.ReadFile(proof)
					must(t, err)
					if string(data) != marker {
						t.Fatal("trusted PATH helper did not execute")
					}
					must(t, os.Remove(proof))
				}
			}
			session, err := backend.Execute(ctx, "Remember this marker: "+marker+"."+helperPrompt+" Reply only with ok.", opts)
			must(t, err)
			for range session.Messages {
			}
			result := <-session.Done
			if result.Err != nil {
				t.Fatalf("guest daemon startup: %v", result.Err)
			}
			if !strings.Contains(strings.ToLower(transcript.String()), "ok") {
				t.Fatal("reply does not contain ok")
			}
			if result.SessionID == "" || !agent.SessionReachable(harness, result.SessionID, config) {
				t.Fatal("first guest session was not persisted in guest config")
			}
			if agent.SessionReachable(harness, result.SessionID, agent.AccountHome(harness)) {
				t.Fatal("guest session opened in owner's config")
			}
			checkHelper()
			firstID := result.SessionID
			opts.Resume = firstID
			transcript.Reset()
			session, err = backend.Execute(ctx, "What marker did I ask you to remember?"+helperPrompt+" Reply only with that marker.", opts)
			must(t, err)
			for range session.Messages {
			}
			resumed := <-session.Done
			if resumed.Err != nil {
				t.Fatalf("guest resume: %v", resumed.Err)
			}
			if resumed.SessionID != firstID || !strings.Contains(transcript.String(), marker) {
				t.Fatal("second prompt did not resume guest conversation")
			}
			if agent.SessionReachable(harness, firstID, agent.AccountHome(harness)) {
				t.Fatal("resume reached owner's session store")
			}
			checkHelper()
			if harness == "claude" {
				d.cfg.Harnesses = map[string]string{harness: bin}
				c := claimed{}
				c.AgentRun.Harness = harness
				c.AgentRun.ResponsibleUserID = uuid.MustParse(guest)
				n, err := d.openToolNamer(ctx, c, opts)
				must(t, err)
				answer, err := n.Ask(ctx, "Do not use tools. Reply only: 1. Inspect process isolation")
				namingResult := n.Close()
				must(t, err)
				if (namingResult.Err != nil && !errors.Is(namingResult.Err, context.Canceled)) || !strings.Contains(answer, "Inspect process isolation") {
					t.Fatalf("real tool-free naming did not answer: %v", namingResult.Err)
				}
			}
			exposed, err := guestCredentialExposure([]string{config, d.runCacheDir(guest)}, []string{runCredential, doorCredential})
			must(t, err)
			if len(exposed) > 0 {
				t.Fatalf("private run credentials persisted in shared guest state: %v", exposed)
			}
			t.Logf("%s starts and resumes guest conversation; owner session absent; HomeRead=false; Keychain=false", harness)
		})
	}
}

func TestRealGuestCLIVersions(t *testing.T) {
	if runtime.GOOS != "linux" || os.Getenv("LYK_REAL_CLI") != "1" {
		t.Skip("set LYK_REAL_CLI=1 inside Linux to check installed CLIs")
	}
	for _, harness := range []string{"claude", "codex"} {
		t.Run(harness, func(t *testing.T) {
			bin, err := exec.LookPath(harness)
			must(t, err)
			d := &daemon{cfg: Config{Home: t.TempDir(), StateDir: t.TempDir()}}
			run := filepath.Join(d.cfg.Home, "run")
			must(t, os.Mkdir(run, 0700))
			must(t, os.WriteFile(filepath.Join(run, ".agent-process"), []byte("test-owned-process-marker"), 0600))
			guest := uuid.NewString()
			config := filepath.Join(d.cfg.StateDir, "guests", guest, harness)
			must(t, os.MkdirAll(config, 0700))
			paths, err := agent.SandboxPaths(harness, config, run, "", "linux")
			must(t, err)
			must(t, agent.PlaceBlocked(paths))
			ctx, cancel := context.WithTimeout(t.Context(), 20*time.Second)
			defer cancel()
			cmd, err := d.cliSandbox(run, nil, false, guest)(ctx, paths, bin, "--version")
			must(t, err)
			cmd.Dir = run
			cmd.Env = agent.ChildEnv(agent.ExecOptions{Guest: true, Dir: run, ConfigHome: config, CacheDir: d.runCacheDir(guest)})
			output, err := cmd.CombinedOutput()
			if err != nil {
				t.Fatalf("installed guest CLI: %v %s", err, output)
			}
			if len(output) == 0 {
				t.Fatal("CLI version did not execute")
			}
			t.Logf("guest %s --version: %s", harness, output)
		})
	}
}

func guestCredentialExposure(roots, needles []string) ([]string, error) {
	var exposed []string
	for _, root := range roots {
		err := filepath.WalkDir(root, func(path string, entry fs.DirEntry, err error) error {
			if err != nil {
				return err
			}
			if !entry.Type().IsRegular() {
				return nil
			}
			data, err := os.ReadFile(path)
			if err != nil {
				return err
			}
			for _, needle := range needles {
				if bytes.Contains(data, []byte(needle)) {
					exposed = append(exposed, path)
					break
				}
			}
			return nil
		})
		if err != nil {
			return nil, err
		}
	}
	return exposed, nil
}
func TestGuestPersistedCredentialProbeHasPositiveControl(t *testing.T) {
	root := t.TempDir()
	path := filepath.Join(root, "fixture")
	must(t, os.WriteFile(path, []byte("non-secret-credential-fixture"), 0600))
	exposed, err := guestCredentialExposure([]string{root}, []string{"non-secret-credential-fixture"})
	must(t, err)
	if len(exposed) != 1 || exposed[0] != path {
		t.Fatal("credential persistence control was not observable")
	}
	must(t, os.Remove(path))
	exposed, err = guestCredentialExposure([]string{root}, []string{"non-secret-credential-fixture"})
	must(t, err)
	if len(exposed) != 0 {
		t.Fatal("removed control is still exposed")
	}
}

func TestGuestShellPathHelper(t *testing.T) {
	marker := os.Getenv("GUEST_SHELL_PATH_HELPER")
	if marker == "" {
		return
	}
	must(t, os.WriteFile("guest-helper-proof", []byte(marker), 0600))
}
