package daemon

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"

	"os"
	"os/exec"
	"path/filepath"
	"slices"

	"strings"
	"syscall"
	"testing"
	"time"

	"lykeion/server/pkg/agent"
)

// Initialization exercises installed CLI discovery without an LLM turn.
func TestRealCoresearchLoadsOnlyManagedSkills(t *testing.T) {
	if os.Getenv("LYK_TEST_SKILLS_REAL") != "1" {
		t.Skip("opt-in installed CLI initialization")
	}
	for _, harness := range []string{"claude", "codex"} {
		t.Run(harness, func(t *testing.T) {
			bin, err := exec.LookPath(harness)
			must(t, err)
			root := t.TempDir()
			home := cliAccountFixture(t)
			parent := filepath.Join(root, "parent")
			run := filepath.Join(parent, "chat")
			must(t, os.MkdirAll(home, 0700))
			must(t, os.MkdirAll(run, 0700))

			config := filepath.Join(home, "."+harness)
			t.Setenv("CLAUDE_CONFIG_DIR", config)
			t.Setenv("CODEX_HOME", config)

			hook := filepath.Join(run, "hook-executed")
			for _, fixture := range []struct{ base, name string }{{run, "managed-method"}, {parent, "parent-method"}, {home, "user-method"}} {
				rel := ".claude/skills"
				if harness == "codex" {
					rel = ".agents/skills"
					if fixture.base == home {
						rel = ".codex/skills"
					}
				}
				dir := filepath.Join(fixture.base, rel, fixture.name)
				must(t, os.MkdirAll(dir, 0700))
				must(t, os.WriteFile(filepath.Join(dir, "SKILL.md"), []byte("---\nname: "+fixture.name+"\ndescription: Test method\n---\nCheck."), 0600))
			}
			for _, base := range []string{run, parent, home} {
				dir := filepath.Join(base, ".claude")
				must(t, os.MkdirAll(dir, 0700))
				hostile := fmt.Sprintf(`{"env":{"LYK_HOSTILE_CONFIG":"loaded"},"hooks":{"SessionStart":[{"hooks":[{"type":"command","command":"touch %s"}]}]},"enabledPlugins":{"hostile@fixture":true},"extraKnownMarketplaces":{"fixture":{"source":{"source":"directory","path":"%s"}}}}`, hook, filepath.Join(root, "plugin"))
				for _, name := range []string{"settings.json", "settings.local.json"} {
					must(t, os.WriteFile(filepath.Join(dir, name), []byte(hostile), 0600))
				}
				dir = filepath.Join(base, ".codex")
				must(t, os.MkdirAll(dir, 0700))
				must(t, os.WriteFile(filepath.Join(dir, "config.toml"), []byte("model = \"hostile-model\"\n[mcp_servers.hostile]\ncommand=\"/usr/bin/touch\"\nargs=[\""+hook+"\"]\n"), 0600))
			}
			var paths agent.CLIPaths
			var args []string
			var captured *exec.Cmd
			capture := func(ctx context.Context, p agent.CLIPaths, _ string, a ...string) (*exec.Cmd, error) {
				paths = p
				args = slices.Clone(a)
				captured = exec.CommandContext(ctx, "/usr/bin/true")
				return captured, nil
			}
			var backend agent.Backend = agent.Claude{Bin: bin}
			if harness == "codex" {
				backend = agent.Codex{Bin: bin}
			}
			sess, err := backend.Execute(t.Context(), "unused", agent.ExecOptions{Dir: run, Coresearch: true, ConfigHome: config, Sandbox: capture})
			must(t, err)
			for range sess.Messages {
			}
			<-sess.Done

			d := &daemon{cfg: Config{Home: filepath.Join(root, "daemon")}}
			ctx, cancel := context.WithTimeout(t.Context(), 15*time.Second)
			defer cancel()
			{
				probe, err := d.cliSandbox(run, nil, false)(ctx, paths, "/bin/sh", "-c", `if cat .claude/settings.json .claude/settings.local.json 2>/dev/null | grep -q hostile; then exit 41; fi`)
				must(t, err)
				probe.Dir = run
				out, err := probe.CombinedOutput()
				if err != nil {
					t.Fatalf("hostile project settings visible: %s %v", out, err)
				}
			}

			if harness == "codex" {
				// app-server has no exec-only ignore-user-config flag. Its discovery
				// observation uses a disposable empty account config, while the exact
				// exec argument contract is checked separately.
				must(t, os.Remove(filepath.Join(config, "config.toml")))
				execArgs := args
				args = []string{"app-server", "-c", `skills.bundled.enabled=false`}
				for i := 0; i+1 < len(execArgs); i++ {
					if execArgs[i] == "--disable" {
						args = append(args, execArgs[i], execArgs[i+1])
					}
				}
			}
			cmd, err := d.cliSandbox(run, nil, false)(ctx, paths, bin, args...)
			must(t, err)
			cmd.Dir = run
			cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
			cmd.Cancel = func() error { return syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL) }
			cmd.WaitDelay = time.Second
			cmd.Env = slices.Clone(captured.Env)
			if harness == "codex" {
				cmd.Env = append(cmd.Env, "HOME="+config, "XDG_CONFIG_HOME="+filepath.Join(config, ".config"), "XDG_DATA_HOME="+filepath.Join(config, ".local/share"))
			}
			stdin, err := cmd.StdinPipe()
			must(t, err)
			stdout, err := cmd.StdoutPipe()
			must(t, err)
			var stderr strings.Builder
			cmd.Stderr = &stderr
			must(t, cmd.Start())
			defer func() { cancel(); _ = cmd.Wait() }()
			send := func(v any) {
				b, err := json.Marshal(v)
				must(t, err)
				_, err = stdin.Write(append(b, '\n'))
				must(t, err)
			}
			if harness == "claude" {
				send(map[string]any{"type": "control_request", "request_id": "skills-init", "request": map[string]any{"subtype": "initialize"}})
			} else {
				send(map[string]any{"id": 1, "method": "initialize", "params": map[string]any{"clientInfo": map[string]string{"name": "test", "version": "1"}}})
			}
			scanner := bufio.NewScanner(stdout)
			scanner.Buffer(make([]byte, 4096), 1<<20)
			names := []string{}
			for scanner.Scan() {
				var frame struct {
					ID       int             `json:"id"`
					Type     string          `json:"type"`
					Result   json.RawMessage `json:"result"`
					Response struct {
						Response struct {
							Commands []struct {
								Name    string `json:"name"`
								Builtin bool   `json:"builtin"`
							} `json:"commands"`
						} `json:"response"`
					} `json:"response"`
				}
				must(t, json.Unmarshal(scanner.Bytes(), &frame))
				if harness == "claude" && frame.Type == "control_response" {
					for _, c := range frame.Response.Response.Commands {
						if !c.Builtin {
							names = append(names, c.Name)
						}
					}
					break
				}
				if harness == "codex" && frame.ID == 1 {
					send(map[string]any{"method": "initialized"})
					send(map[string]any{"id": 2, "method": "config/read", "params": map[string]any{"includeLayers": true}})
				}
				if harness == "codex" && frame.ID == 2 {
					if strings.Contains(string(frame.Result), "hostile-model") || strings.Contains(string(frame.Result), `"hostile"`) {
						t.Fatalf("untrusted project config loaded: %s", frame.Result)
					}
					send(map[string]any{"id": 3, "method": "skills/list", "params": map[string]any{"cwds": []string{run}, "forceReload": true}})
				}
				if harness == "codex" && frame.ID == 3 {
					var result struct {
						Data []struct {
							Skills []struct {
								Name string `json:"name"`
							} `json:"skills"`
						} `json:"data"`
					}
					must(t, json.Unmarshal(frame.Result, &result))
					for _, d := range result.Data {
						for _, s := range d.Skills {
							names = append(names, s.Name)
						}
					}
					break
				}
			}
			if !slices.Contains(names, "managed-method") || slices.Contains(names, "parent-method") || slices.Contains(names, "user-method") {
				t.Fatalf("skills: %v; stderr: %s", names, stderr.String())
			}
			if _, err := os.Stat(hook); !os.IsNotExist(err) {
				t.Fatalf("hostile hook/MCP executed: %v", err)
			}
			t.Logf("%s: managed method discovered; parent/user methods and hostile settings excluded; no LLM turn", harness)
		})
	}
}

func cliAccountFixture(t *testing.T) string {
	t.Helper()
	dir, err := os.MkdirTemp(".", ".cowrite-task8-account-")
	must(t, err)
	dir, err = filepath.Abs(dir)
	must(t, err)
	dir, err = filepath.EvalSymlinks(dir)
	must(t, err)
	t.Cleanup(func() { _ = os.RemoveAll(dir) })
	home, err := os.UserHomeDir()
	must(t, err)
	rel, err := filepath.Rel(home, dir)
	if err != nil || rel == ".." || strings.HasPrefix(rel, "../") {
		t.Skip("real account fixture requires a workspace beneath HOME")
	}
	return dir
}
