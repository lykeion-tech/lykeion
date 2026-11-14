package daemon

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"github.com/google/uuid"
	"github.com/modelcontextprotocol/go-sdk/mcp"
	"lykeion/server/internal/daemon/envs"
	"lykeion/server/pkg/agent"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"
)

func TestAGuestCannotReadTheOwnersHome(t *testing.T) { testGuestSandbox(t, false) }
func TestAGuestCanUseItsOwnHome(t *testing.T)        { testGuestSandbox(t, true) }

func TestGuestCannotReadOtherProcessEnvironment(t *testing.T) {
	for _, name := range []string{"AWS_SECRET_ACCESS_KEY", "OPENAI_API_KEY", "ANTHROPIC_API_KEY", "GITHUB_TOKEN", "CUSTOM_PERSONAL_VALUE", "SSH_AUTH_SOCK", "NODE_OPTIONS", "HTTP_PROXY", "CLAUDE_CODE_OAUTH_TOKEN"} {
		t.Setenv(name, "ambient-owner-fixture")
	}
	for _, samePerson := range []bool{false, true} {
		t.Run(fmt.Sprintf("same_person_%t", samePerson), func(t *testing.T) {
			d, run, guest, config, paths := guestPolicyFixture(t)
			ctx, cancel := context.WithTimeout(t.Context(), 90*time.Second)
			defer cancel()
			external := exec.CommandContext(ctx, "/bin/sh", "-c", "echo READY; read release")
			external.Env = []string{"PATH=/usr/bin:/bin", "LYK_PROCESS_FIXTURE=external-isolation-fixture"}
			startGuestProcessFixture(t, external)
			other := uuid.NewString()
			if samePerson {
				other = guest
			}
			siblingRun := filepath.Join(d.cfg.Home, "sibling-run")
			siblingConfig := filepath.Join(d.cfg.StateDir, "guests", other, "codex")
			for _, dir := range []string{siblingRun, siblingConfig} {
				must(t, os.MkdirAll(dir, 0700))
			}
			must(t, os.WriteFile(filepath.Join(siblingRun, ".agent-process"), []byte("test-owned-process-marker"), 0600))
			credential := filepath.Join(siblingRun, ".lyk-config.json")
			must(t, os.WriteFile(credential, []byte("private-run-credential-fixture"), 0600))
			siblingPaths, err := agent.SandboxPaths("codex", siblingConfig, siblingRun, "", "linux")
			must(t, err)
			must(t, agent.PlaceBlocked(siblingPaths))
			sibling, err := d.cliSandbox(siblingRun, nil, false, other)(ctx, siblingPaths, "/bin/sh", "-c", "echo READY; read release")
			must(t, err)
			sibling.Env = append(agent.ChildEnv(agent.ExecOptions{Guest: true, Dir: siblingRun, ConfigHome: siblingConfig, CacheDir: d.runCacheDir(other)}), "LYK_PROCESS_FIXTURE=sibling-isolation-fixture")
			startGuestProcessFixture(t, sibling)
			for _, cmd := range []*exec.Cmd{external, sibling} {
				data, err := os.ReadFile(fmt.Sprintf("/proc/%d/environ", cmd.Process.Pid))
				must(t, err)
				if !bytes.Contains(data, []byte("LYK_PROCESS_FIXTURE=")) {
					t.Fatal("unsandboxed environment control cannot observe fixture")
				}
				data, err = os.ReadFile(fmt.Sprintf("/proc/%d/root%s", cmd.Process.Pid, credential))
				must(t, err)
				if string(data) != "private-run-credential-fixture" {
					t.Fatal("unsandboxed process-root control cannot observe fixture")
				}
			}
			externalDir, err := os.MkdirTemp("/var/tmp", "lyk-guest-files-")
			must(t, err)
			defer os.RemoveAll(externalDir)
			externalFile := filepath.Join(externalDir, "initial")
			lateFile := filepath.Join(externalDir, "late")
			must(t, os.WriteFile(externalFile, []byte("external-private-file-fixture"), 0600))
			started := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if err := os.WriteFile(lateFile, []byte("late-external-private-file-fixture"), 0600); err != nil {
					http.Error(w, "fixture", 500)
					return
				}
				w.WriteHeader(204)
			}))
			defer started.Close()
			denied := []string{credential, externalFile, lateFile}
			for _, person := range []string{uuid.NewString(), uuid.NewString()} {
				for _, path := range []string{filepath.Join(d.cfg.StateDir, "guests", person, "codex", "auth.json"), filepath.Join(d.cfg.StateDir, "guests", person, "claude", "projects", "session.jsonl"), filepath.Join(d.cacheDir(), "guests", person, "private")} {
					must(t, os.MkdirAll(filepath.Dir(path), 0700))
					must(t, os.WriteFile(path, []byte("other-person-state-fixture"), 0600))
					denied = append(denied, path)
				}
			}
			ownerFile := filepath.Join(os.Getenv("HOME"), "Documents", "owner-only")
			must(t, os.MkdirAll(filepath.Dir(ownerFile), 0700))
			must(t, os.WriteFile(ownerFile, []byte("owner-file-fixture"), 0600))
			denied = append(denied, ownerFile)
			for _, path := range denied {
				if path == lateFile {
					continue
				}
				data, err := os.ReadFile(path)
				must(t, err)
				if len(data) == 0 {
					t.Fatal("empty file control")
				}
			}
			code := guestProcessProbe(denied)
			code = strings.Replace(code, "import glob, pathlib, os", "import glob, pathlib, os, urllib.request\nurllib.request.build_opener(urllib.request.ProxyHandler({})).open("+fmt.Sprintf("%q", started.URL)+").read()", 1)
			output, err := exec.CommandContext(ctx, "/usr/bin/python3", "-c", code).CombinedOutput()
			if err == nil || (!bytes.Contains(output, []byte("EXPOSED_ENV")) && !bytes.Contains(output, []byte("EXPOSED_FILE"))) {
				t.Fatalf("unisolated regression control did not expose planted fixtures: %v %s", err, output)
			}
			t.Log("unisolated control exposes planted fixture; real sandbox launchers must refuse it")
			for _, surface := range []string{"cli_bash", "naming", "door_script", "persistent_kernel", "command_connector", "service"} {
				t.Run(surface, func(t *testing.T) {
					must(t, os.Remove(lateFile))
					runGuestProcessProbe(t, ctx, d, run, guest, config, paths, code, surface)
					data, err := os.ReadFile(lateFile)
					must(t, err)
					if string(data) != "late-external-private-file-fixture" {
						t.Fatal("late file control unavailable after sandbox started")
					}
				})
			}
		})
	}
}

func startGuestProcessFixture(t *testing.T, cmd *exec.Cmd) {
	t.Helper()
	stdout, err := cmd.StdoutPipe()
	must(t, err)
	stdin, err := cmd.StdinPipe()
	must(t, err)
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	must(t, cmd.Start())
	t.Cleanup(func() { stdin.Close(); cmd.Process.Kill(); cmd.Wait() })
	line, err := bufio.NewReader(stdout).ReadString('\n')
	if err != nil || line != "READY\n" {
		t.Fatalf("process fixture failed readiness: %v %q %s", err, line, stderr.String())
	}
}

func guestProcessProbe(denied []string) string {
	encoded, _ := json.Marshal(denied)
	return `import glob, pathlib, os
for name in ["AWS_SECRET_ACCESS_KEY","OPENAI_API_KEY","ANTHROPIC_API_KEY","GITHUB_TOKEN","CUSTOM_PERSONAL_VALUE","SSH_AUTH_SOCK","NODE_OPTIONS","HTTP_PROXY","CLAUDE_CODE_OAUTH_TOKEN"]:
    assert not os.environ.get(name), "EXPOSED_ENV ambient " + name
paths = ` + string(encoded) + `
seen = glob.glob('/proc/[0-9]*')
assert seen, 'sandbox proc view is unavailable'
for proc in seen:
    try:
        data = pathlib.Path(proc + '/environ').read_bytes()
    except OSError:
        continue
    assert b'LYK_PROCESS_FIXTURE=external-isolation-fixture' not in data, 'EXPOSED_ENV external'
    assert b'LYK_PROCESS_FIXTURE=sibling-isolation-fixture' not in data, 'EXPOSED_ENV sibling'
    assert b'LYK_VM_HOST_FIXTURE=task-owned-host-sentinel' not in data, 'EXPOSED_ENV Mac host'
for path in paths + ['/tmp/lyk-sm-resume/host-only-planted']:
    for candidate in [path] + [proc + '/root' + path for proc in seen]:
        try:
            data = pathlib.Path(candidate).read_bytes()
        except OSError:
            continue
        assert not data, 'EXPOSED_FILE ' + candidate
print('PROBE_OK')
`
}

func runGuestProcessProbe(t *testing.T, ctx context.Context, d *daemon, run, guest, config string, paths agent.CLIPaths, code, surface string) {
	t.Helper()
	expect := func(output []byte, err error) {
		t.Helper()
		if err != nil || !bytes.Contains(output, []byte("PROBE_OK")) {
			t.Fatalf("%s probe did not execute safely: %v %s", surface, err, output)
		}
	}
	switch surface {
	case "cli_bash":
		cmd, err := d.cliSandbox(run, nil, false, guest)(ctx, paths, "/bin/bash", "-c", "exec /usr/bin/python3 -c "+shellQuoteProbe(code))
		must(t, err)
		cmd.Dir = run
		cmd.Env = agent.ChildEnv(agent.ExecOptions{Guest: true, Dir: run, ConfigHome: config, CacheDir: d.runCacheDir(guest)})
		output, err := cmd.CombinedOutput()
		expect(output, err)
	case "naming":
		claudeConfig := filepath.Join(d.cfg.StateDir, "guests", guest, "claude")
		must(t, os.MkdirAll(claudeConfig, 0700))
		bin := filepath.Join(os.Getenv("HOME"), "installation", "claude-probe")
		must(t, os.MkdirAll(filepath.Dir(bin), 0700))
		script := "#!/bin/sh\n/usr/bin/python3 -c " + shellQuoteProbe(strings.ReplaceAll(code, "print('PROBE_OK')", "")) + " || exit 41\n" + `while IFS= read -r prompt; do
printf '%s\n' '{"type":"assistant","message":{"content":[{"type":"text","text":"PROBE_OK"}]}}' '{"type":"result","subtype":"success","session_id":"naming-fixture","modelUsage":{"m":{"inputTokens":1,"outputTokens":1}}}'
done
`
		must(t, os.WriteFile(bin, []byte(script), 0700))
		d.cfg.Harnesses = map[string]string{"claude": bin}
		c := claimed{}
		c.AgentRun.Harness = "claude"
		c.AgentRun.ResponsibleUserID = uuid.MustParse(guest)
		n, err := d.openToolNamer(ctx, c, agent.ExecOptions{Guest: true, ConfigHome: claudeConfig, CacheDir: d.runCacheDir(guest)})
		must(t, err)
		defer n.Close()
		answer, err := n.Ask(ctx, "Name this probe")
		if err != nil {
			t.Fatalf("naming probe failed: %v; %v", err, n.Close().Err)
		}
		expect([]byte(answer), err)
	case "door_script", "persistent_kernel", "command_connector":
		door := newRunDoor(t, newLykeion(t), run, func(door *Door) {
			door.Guest = true
			door.home = os.Getenv("HOME")
			door.PrivateRoots = []string{d.cfg.Home, d.cfg.StateDir}
			door.Child = func() agent.ExecOptions {
				return guestDoorOptions(agent.ExecOptions{Guest: true, Dir: run, ConfigHome: config, CacheDir: d.runCacheDir(guest)})
			}
			door.interp = func(string) (string, []string, error) { return "/usr/bin/python3", nil, nil }
		}, envs.Python)
		if surface == "command_connector" {
			cmd, err := door.guestConnectorCommand(ctx, "/usr/bin/python3", "-c", code)
			must(t, err)
			cmd.Dir = door.connectorDir
			cmd.Env = door.guestConnectorEnv(nil)
			output, err := cmd.CombinedOutput()
			expect(output, err)
		} else if surface == "door_script" {
			must(t, os.WriteFile(filepath.Join(run, "probe.py"), []byte(code), 0600))
			answer, _, err := door.runScript(ctx, nil, runScriptIn{Path: "probe.py"})
			must(t, err)
			expect([]byte(answer.Content[0].(*mcp.TextContent).Text), nil)
		} else {
			specDir := filepath.Join(door.Study.Bases.Dir, "python", "share", "jupyter", "kernels", "python3")
			must(t, os.MkdirAll(specDir, 0700))
			must(t, os.WriteFile(filepath.Join(specDir, "kernel.json"), []byte(`{"argv":["/usr/bin/python3","-m","ipykernel_launcher","-f","{connection_file}"],"display_name":"Python","language":"python"}`), 0600))
			sessions, err := door.liveSessions(ctx)
			must(t, err)
			first, err := sessions.Exec(ctx, "python", "retained_fixture = 37\n"+code)
			must(t, err)
			if !first.OK {
				t.Fatal(first.Error)
			}
			expect([]byte(first.Stdout), nil)
			second, err := sessions.Exec(ctx, "python", "assert retained_fixture == 37\n"+code)
			must(t, err)
			if !second.OK {
				t.Fatal(second.Error)
			}
			expect([]byte(second.Stdout), nil)
		}
	case "service":
		serviceDaemon, c, study := serviceFixture(t)
		serviceDaemon.serviceCommand = nil
		c.Services[0].Start = "/usr/bin/python3 -c " + shellQuoteProbe(code) + " && exec ./helper"
		services, err := serviceDaemon.startServices(ctx, c, study, true)
		must(t, err)
		defer services.Stop()
		output, err := os.ReadFile(filepath.Join(filepath.Dir(serviceState(serviceDaemon, c)), "log"))
		expect(output, err)
	}
	t.Logf("%s executed environment, process-root and sibling-file probe", surface)
}

func shellQuoteProbe(s string) string { return "'" + strings.ReplaceAll(s, "'", "'\"'\"'") + "'" }

func TestGuestFailsClosedWithoutBubblewrap(t *testing.T) {
	d, run, guest, _, paths := guestPolicyFixture(t)
	t.Setenv("PATH", t.TempDir())
	cmd, err := d.cliSandbox(run, nil, false, guest)(t.Context(), paths, "/bin/sh", "-c", "exit 0")
	if err == nil || !strings.Contains(err.Error(), "install bubblewrap") || cmd != nil {
		t.Fatalf("missing namespace launcher must refuse CLI: %v", err)
	}
}

func TestGuestNamingCancellationStopsEveryProcess(t *testing.T) {
	d, _, guest, _, _ := guestPolicyFixture(t)
	config := filepath.Join(d.cfg.StateDir, "guests", guest, "claude")
	must(t, os.MkdirAll(config, 0700))
	bin := filepath.Join(os.Getenv("HOME"), "installation", "claude-cancel-fixture")
	must(t, os.MkdirAll(filepath.Dir(bin), 0700))
	body := `#!/bin/sh
sleep 300 &
while IFS= read -r prompt; do
printf '%s\n' '{"type":"assistant","message":{"content":[{"type":"text","text":"READY"}]}}' '{"type":"result","subtype":"success","session_id":"fixture","modelUsage":{"m":{"inputTokens":1,"outputTokens":1}}}'
done
`
	must(t, os.WriteFile(bin, []byte(body), 0700))
	for i := 0; i < 8; i++ {
		dir := filepath.Join(d.cfg.Home, fmt.Sprintf("naming-cancel-%d", i))
		must(t, os.MkdirAll(dir, 0700))
		ctx, cancel := context.WithTimeout(t.Context(), 10*time.Second)
		n, err := agent.OpenNamingSession(ctx, "claude", bin, dir, "", d.cliSandbox(dir, nil, false, guest), agent.ExecOptions{Guest: true, ConfigHome: config, CacheDir: d.runCacheDir(guest)})
		must(t, err)
		answer, err := n.Ask(ctx, "Probe")
		must(t, err)
		if answer != "READY" {
			t.Fatal("fixture did not start")
		}
		marker, err := os.ReadFile(filepath.Join(dir, ".agent-process"))
		must(t, err)
		var owner struct {
			PID int `json:"pid"`
		}
		must(t, json.Unmarshal(marker, &owner))
		if owner.PID <= 1 {
			t.Fatal("missing verified process group")
		}
		closed := make(chan struct{})
		go func() { n.Close(); close(closed) }()
		select {
		case <-closed:
		case <-time.After(5 * time.Second):
			cancel()
			t.Fatal("immediate naming cancellation failed to close")
		}
		cancel()
		waitFor(t, func() bool { return errors.Is(syscall.Kill(-owner.PID, 0), syscall.ESRCH) })
	}
}
