package daemon

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"github.com/google/uuid"
	"lykeion/server/internal/apiclient"
	"lykeion/server/internal/daemon/envs"
	"lykeion/server/internal/daemon/sandbox"
	"lykeion/server/pkg/agent"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"slices"
	"strconv"
	"strings"
	"testing"
	"time"
)

func TestCLIPolicyShape(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	scratch := t.TempDir()
	run := filepath.Join(scratch, "run")
	sibling := filepath.Join(scratch, "other")
	for _, p := range []string{run, sibling, filepath.Join(home, ".ssh")} {
		if err := os.MkdirAll(p, 0700); err != nil {
			t.Fatal(err)
		}
	}
	d := &daemon{cfg: Config{Home: scratch, StateDir: filepath.Join(home, "state")}}
	p, err := d.cliPolicy(run, nil, false, agent.CLIPaths{Keychain: true})
	if err != nil {
		t.Fatal(err)
	}
	if !p.HomeRead || !p.Keychain || !slices.Contains(p.Write, run) || !slices.Contains(p.RestrictedRoots, scratch) || !slices.Contains(p.Hidden, filepath.Join(home, ".ssh")) {
		t.Fatalf("policy %+v", p)
	}
	p, err = d.cliPolicy(run, nil, true, agent.CLIPaths{})
	if err != nil {
		t.Fatal(err)
	}
	if slices.Contains(p.Write, run) || !slices.Contains(p.Read, run) {
		t.Fatalf("read-only %+v", p)
	}
}

func TestCLISandboxDenies(t *testing.T) {
	if err := sandbox.Check(context.Background()); err != nil {
		t.Skipf("sandbox unavailable: %v", err)
	}
	for _, harness := range []string{"claude", "codex"} {
		t.Run(harness, func(t *testing.T) {
			home := t.TempDir()
			t.Setenv("HOME", home)
			t.Setenv("LYK_CONFIG", filepath.Join(t.TempDir(), "lyk-config.json"))
			stateDir := t.TempDir()
			scratch := t.TempDir()
			run := filepath.Join(scratch, "run")
			other := filepath.Join(scratch, "other")
			for _, dir := range []string{run, other, filepath.Join(home, ".ssh"), filepath.Join(home, ".claude", "hooks"), filepath.Join(home, ".codex")} {
				must(t, os.MkdirAll(dir, 0700))
			}
			for _, name := range []string{".agent-owned", ".agent-lock", ".agent-process"} {
				must(t, os.WriteFile(filepath.Join(run, name), []byte("daemon-owned"), 0600))
			}
			secrets := []string{filepath.Join(home, ".ssh", "id_x"), os.Getenv("LYK_CONFIG"), filepath.Join(other, "secret"), filepath.Join(stateDir, "secret")}
			for _, path := range secrets {
				must(t, os.WriteFile(path, []byte("secret"), 0600))
			}
			cliHome := filepath.Join(home, "."+harness)
			paths, err := agent.SandboxPaths(harness, cliHome, run, "", runtime.GOOS)
			must(t, err)
			if runtime.GOOS == "linux" {
				must(t, agent.PlaceBlocked(paths))
				if harness == "claude" {
					must(t, os.WriteFile(filepath.Join(cliHome, ".credentials.json"), nil, 0600))
				}
			}
			runConfig := []string{filepath.Join(run, ".lyk-mcp.json"), filepath.Join(run, ".lyk-config.json"), filepath.Join(run, ".cli-home", "state")}
			for _, path := range runConfig {
				must(t, os.MkdirAll(filepath.Dir(path), 0700))
				must(t, os.WriteFile(path, []byte("run-readable"), 0600))
			}
			config := filepath.Join(home, ".claude.json")
			must(t, os.WriteFile(config, []byte("protected"), 0600))
			// Keychain is tested separately; these fixtures exercise only fake CLI files.
			paths.Keychain = false
			d := &daemon{cfg: Config{Home: scratch, StateDir: stateDir}}
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			wrap := d.cliSandbox(run, nil, false)
			probe := func(script string) error {
				cmd, err := wrap(ctx, paths, "/bin/sh", "-c", script)
				if err != nil {
					t.Fatal(err)
				}
				cmd.Dir = run
				out, err := cmd.CombinedOutput()
				if err != nil {
					t.Logf("probe denied: %s", out)
				}
				return err
			}
			for _, path := range runConfig {
				if err := probe("cat " + strconv.Quote(path) + " >/dev/null"); err != nil {
					t.Errorf("CLI cannot read run config %s", path)
				}
				if probe("echo changed > "+strconv.Quote(path)) == nil {
					t.Errorf("CLI changed run config %s", path)
				}
			}
			for _, path := range []string{filepath.Join(home, ".claude.json"), filepath.Join(home, ".claude", "settings.json"), filepath.Join(home, ".claude", "hooks", "x"), filepath.Join(home, ".codex", "config.toml"), filepath.Join(home, ".zshrc")} {
				if probe("echo x > "+strconv.Quote(path)) == nil {
					t.Errorf("wrote protected %s", path)
				}
			}
			for _, marker := range []string{".agent-process", ".agent-owned", ".agent-lock"} {
				if probe("echo forged > "+strconv.Quote(filepath.Join(run, marker))) == nil {
					t.Errorf("CLI could forge daemon marker %s", marker)
				}
			}
			alias := filepath.Join(run, "config-hardlink")
			if probe("ln "+strconv.Quote(config)+" "+strconv.Quote(alias)+" && echo changed > "+strconv.Quote(alias)) == nil {
				t.Error("changed personal config through hardlink")
			}
			b, err := os.ReadFile(config)
			must(t, err)
			if string(b) != "protected" {
				t.Fatalf("personal config changed through alias: %q", b)
			}
			for _, path := range secrets {
				if probe("cat "+strconv.Quote(path)) == nil {
					t.Errorf("read secret %s", path)
				}
			}
			if err := probe("echo x > " + strconv.Quote(filepath.Join(run, "ok"))); err != nil {
				t.Fatal(err)
			}
			state := paths.Write[0]
			if harness == "codex" {
				state = filepath.Join(cliHome, "sessions")
				must(t, os.MkdirAll(state, 0700))
			}
			if err := probe("echo x > " + strconv.Quote(filepath.Join(state, "ok"))); err != nil {
				t.Fatal(err)
			}
			wrap = d.cliSandbox(run, nil, true)
			if probe("echo x > "+strconv.Quote(filepath.Join(run, "read-only-denied"))) == nil {
				t.Error("read-only run wrote run folder")
			}
			if err := probe("echo x > \"$TMPDIR/probe\" && echo x > " + strconv.Quote(filepath.Join(d.cacheDir(), "cache-probe")) + " && echo x > " + strconv.Quote(filepath.Join(state, "read-only-state"))); err != nil {
				t.Fatal(err)
			}

		})
	}
}

func TestCLISandboxClaudeTemp(t *testing.T) {
	if err := sandbox.Check(context.Background()); err != nil {
		t.Skipf("sandbox unavailable: %v", err)
	}
	for _, readOnly := range []bool{false, true} {
		t.Run(fmt.Sprintf("readOnly=%t", readOnly), func(t *testing.T) {
			t.Setenv("HOME", t.TempDir())
			hostile := t.TempDir()
			t.Setenv("CLAUDE_CODE_TMPDIR", hostile)
			scratch := t.TempDir()
			run := filepath.Join(scratch, "run")
			must(t, os.MkdirAll(run, 0700))
			for _, name := range []string{".agent-owned", ".agent-lock", ".agent-process"} {
				must(t, os.WriteFile(filepath.Join(run, name), []byte("daemon-owned"), 0600))
			}
			d := &daemon{cfg: Config{Home: scratch, StateDir: t.TempDir()}}
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			cmd, err := d.cliSandbox(run, nil, readOnly)(ctx, agent.CLIPaths{}, "/bin/sh", "-c", `test "$CLAUDE_CODE_TMPDIR" = "$TMPDIR" || exit 21
root="$CLAUDE_CODE_TMPDIR/claude-$(id -u)"
mkdir -m 700 "$root" && echo private > "$root/probe" && printf '%s' "$TMPDIR"`)
			must(t, err)
			cmd.Dir = run
			out, err := cmd.CombinedOutput()
			if err != nil {
				t.Fatalf("vendor temp selection failed: %v: %s", err, out)
			}
			tmp := string(out)
			if tmp == hostile || tmp == "" {
				t.Fatalf("temp path not isolated: %q", tmp)
			}
			_, err = os.Stat(filepath.Join(tmp, fmt.Sprintf("claude-%d", os.Getuid()), "probe"))
			must(t, err)
			cancel()
			deadline := time.Now().Add(time.Second)
			for time.Now().Before(deadline) {
				if _, err := os.Stat(tmp); os.IsNotExist(err) {
					return
				}
				time.Sleep(time.Millisecond)
			}
			t.Fatal("per-run temp survived lifecycle cleanup")
		})
	}
}

func TestNoClaimWithoutSandbox(t *testing.T) {
	claims := 0
	var heartbeat map[string]json.RawMessage
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.Contains(r.URL.Path, "claim") {
			claims++
		}
		if strings.HasSuffix(r.URL.Path, "heartbeat") {
			if err := json.NewDecoder(r.Body).Decode(&heartbeat); err != nil {
				t.Error(err)
			}
		}
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprint(w, "[]")
	}))
	defer srv.Close()
	d := &daemon{sandboxErr: errors.New("broken sandbox"), api: &apiclient.Client{Server: srv.URL, HTTP: srv.Client()}, refs: []runtimeRef{{WorkspaceID: uuid.New(), RuntimeID: uuid.New()}}}
	d.claim(t.Context())
	d.heartbeat(t.Context())
	if claims != 0 {
		t.Fatalf("made %d claims", claims)
	}
	var bases map[string]string
	if err := json.Unmarshal(heartbeat["bases"], &bases); err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(bases["sandbox"], "failed") {
		t.Fatalf("bases %v", bases)
	}
}

func TestNamingSessionSandboxed(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	t.Setenv("CLAUDE_CONFIG_DIR", t.TempDir())
	d := &daemon{cfg: Config{Home: t.TempDir(), StateDir: t.TempDir(), Harnesses: map[string]string{"claude": "/bin/false"}}, sandboxErr: errors.New("naming sandbox probe")}
	c := claimed{}
	c.AgentRun.Harness = "claude"
	_, err := d.openToolNamer(t.Context(), c)
	if err == nil || !strings.Contains(err.Error(), "naming sandbox probe") {
		t.Fatalf("naming did not use daemon sandbox: %v", err)
	}
}

func TestCLIPolicyHidesOtherStudiesAndWorkspaces(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	scratch := t.TempDir()
	run := filepath.Join(scratch, "run")
	bases := envs.NewBases(t.TempDir())
	studyDir := filepath.Join(bases.Dir, "studies", "workspace", "study")
	sibling := filepath.Join(filepath.Dir(studyDir), "other")
	otherWorkspace := filepath.Join(bases.Dir, "studies", "other-workspace")
	for _, path := range []string{run, studyDir, sibling, otherWorkspace} {
		must(t, os.MkdirAll(path, 0700))
	}
	for _, name := range []string{".agent-owned", ".agent-lock", ".agent-process"} {
		must(t, os.WriteFile(filepath.Join(run, name), []byte("daemon-owned"), 0600))
	}
	d := &daemon{cfg: Config{Home: scratch, StateDir: t.TempDir()}, bases: bases}
	study := envs.ForStudy(bases, studyDir)
	p, err := d.cliPolicy(run, study, false, agent.CLIPaths{})
	must(t, err)
	for _, path := range []string{sibling, otherWorkspace} {
		if !slices.Contains(p.RestrictedRoots, bases.Dir) || slices.Contains(p.Read, path) || slices.Contains(p.Write, path) {
			t.Errorf("not masked by restricted base root: %s", path)
		}
	}
	if !slices.Contains(p.Write, studyDir) {
		t.Fatal("study not writable")
	}
	p, err = d.cliPolicy(run, study, true, agent.CLIPaths{})
	must(t, err)
	if slices.Contains(p.Write, studyDir) || !slices.Contains(p.Read, studyDir) {
		t.Fatal("read-only study writable")
	}
}

func TestCLISandboxHidesFutureSiblings(t *testing.T) {
	if err := sandbox.Check(t.Context()); err != nil {
		t.Skipf("sandbox unavailable: %v", err)
	}
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("LYK_CONFIG", filepath.Join(home, "sign-in.json"))
	scratch := filepath.Join(home, ".lyk", "runs")
	run := filepath.Join(scratch, "current")
	bases := envs.NewBases(filepath.Join(home, ".lyk", "envs"))
	studyDir := filepath.Join(bases.Dir, "studies", "workspace", "current")
	for _, dir := range []string{run, studyDir} {
		must(t, os.MkdirAll(dir, 0700))
	}
	for _, name := range []string{".agent-owned", ".agent-lock", ".agent-process"} {
		must(t, os.WriteFile(filepath.Join(run, name), []byte("daemon-owned"), 0600))
	}
	d := &daemon{cfg: Config{Home: scratch, StateDir: t.TempDir()}, bases: bases}
	study := envs.ForStudy(bases, studyDir)
	future := []string{filepath.Join(scratch, "future", "secret"), filepath.Join(filepath.Dir(studyDir), "future-study", "secret"), filepath.Join(bases.Dir, "studies", "future-workspace", "secret"), filepath.Join(home, ".lyk", "future-data", "secret")}
	ready, goFile := filepath.Join(run, "ready"), filepath.Join(run, "go")
	script := "echo ready > " + strconv.Quote(ready) + "; while [ ! -f " + strconv.Quote(goFile) + " ]; do sleep 0.01; done; "
	for _, path := range future {
		script += "if cat " + strconv.Quote(path) + " >/dev/null 2>&1; then echo exposed-future-path; exit 17; fi; "
	}
	script += "echo own > " + strconv.Quote(filepath.Join(run, "own")) + " && echo own > " + strconv.Quote(filepath.Join(studyDir, "own")) + " && echo cache > " + strconv.Quote(filepath.Join(d.cacheDir(), "own"))
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	cmd, err := d.cliSandbox(run, study, false)(ctx, agent.CLIPaths{}, "/bin/sh", "-c", script)
	must(t, err)
	cmd.Dir = run
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
		must(t, os.WriteFile(path, []byte("secret"), 0600))
	}
	must(t, os.WriteFile(goFile, nil, 0600))
	if err := cmd.Wait(); err != nil {
		t.Fatalf("future siblings became readable or own grants failed: %v %s", err, output.String())
	}
}

func TestCLIExecutableCannotBecomeWritableState(t *testing.T) {
	home := t.TempDir()
	codeHome := filepath.Join(home, ".codex")
	bin := filepath.Join(codeHome, "packages", "standalone", "bin", "codex")
	must(t, os.MkdirAll(filepath.Dir(bin), 0700))
	must(t, os.WriteFile(bin, []byte("#!/bin/sh\n"), 0700))
	alias := filepath.Join(home, "codex")
	must(t, os.Symlink(bin, alias))
	paths := agent.CLIPaths{Write: []string{codeHome}}
	p := sandbox.Policy{Home: home, HomeRead: true, Write: paths.Write}
	must(t, protectCLIExecutable(&p, paths, alias))
	protected, err := filepath.EvalSymlinks(filepath.Join(codeHome, "packages"))
	must(t, err)
	if !slices.Contains(p.Blocked, protected) {
		t.Fatalf("CLI code remains writable: %+v", p)
	}
	stateBin := filepath.Join(codeHome, "sessions", "codex")
	must(t, os.MkdirAll(filepath.Dir(stateBin), 0700))
	must(t, os.WriteFile(stateBin, []byte("#!/bin/sh\n"), 0700))
	if err := protectCLIExecutable(&p, paths, stateBin); err == nil {
		t.Fatal("executable inside required mutable state accepted")
	}
}

func TestCLISandboxProtectsInstalledExecutable(t *testing.T) {
	if runtime.GOOS != "linux" {
		t.Skip("Linux whole config-home grant")
	}
	if err := sandbox.Check(t.Context()); err != nil {
		t.Skipf("sandbox unavailable: %v", err)
	}
	home := t.TempDir()
	t.Setenv("HOME", home)
	codeHome := filepath.Join(home, ".codex")
	scratch := t.TempDir()
	run := filepath.Join(scratch, "run")
	bin := filepath.Join(codeHome, "packages", "standalone", "bin", "codex")
	alias := filepath.Join(home, ".local", "bin", "codex")
	for _, dir := range []string{filepath.Dir(bin), filepath.Dir(alias), run, filepath.Join(codeHome, "sessions")} {
		must(t, os.MkdirAll(dir, 0700))
	}
	script := "#!/bin/sh\nif echo changed > " + strconv.Quote(bin) + "; then exit 17; fi\nif echo changed > " + strconv.Quote(filepath.Join(codeHome, "packages", "new-code")) + "; then exit 18; fi\necho state > " + strconv.Quote(filepath.Join(codeHome, "sessions", "ok")) + "\n"
	must(t, os.WriteFile(bin, []byte(script), 0700))
	must(t, os.Symlink(bin, alias))
	paths, err := agent.SandboxPaths("codex", codeHome, run, "", runtime.GOOS)
	must(t, err)
	must(t, agent.PlaceBlocked(paths))
	for _, name := range []string{".agent-owned", ".agent-lock", ".agent-process"} {
		must(t, os.WriteFile(filepath.Join(run, name), []byte("daemon-owned"), 0600))
	}
	d := &daemon{cfg: Config{Home: scratch, StateDir: t.TempDir()}}
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	cmd, err := d.cliSandbox(run, nil, false)(ctx, paths, alias)
	must(t, err)
	cmd.Dir = run
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("installed-code protection or writable state failed: %v %s", err, out)
	}
	after, err := os.ReadFile(bin)
	must(t, err)
	if string(after) != script {
		t.Fatal("installed code changed")
	}
	if _, err := os.Stat(filepath.Join(codeHome, "sessions", "ok")); err != nil {
		t.Fatal(err)
	}
}

func TestCLISandboxCleansTemporaryState(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	scratch := t.TempDir()
	run := filepath.Join(scratch, "run")
	must(t, os.Mkdir(run, 0700))
	for _, name := range []string{".agent-owned", ".agent-lock", ".agent-process"} {
		must(t, os.WriteFile(filepath.Join(run, name), []byte("daemon-owned"), 0600))
	}
	d := &daemon{cfg: Config{Home: scratch, StateDir: t.TempDir()}}
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	cmd, err := d.cliSandbox(run, nil, false)(ctx, agent.CLIPaths{}, "/bin/sh", "-c", "true")
	must(t, err)
	tmp := ""
	for _, arg := range cmd.Args {
		if value, ok := strings.CutPrefix(arg, "TMPDIR="); ok {
			tmp = value
		}
	}
	if tmp == "" {
		t.Fatal("no private temp folder")
	}
	if _, err := os.Stat(tmp); err != nil {
		t.Fatal(err)
	}
	config := filepath.Join(run, ".lyk-config.json")
	if _, err := os.Stat(config); err != nil {
		t.Fatal(err)
	}
	cancel()
	waitFor(t, func() bool {
		_, tmpErr := os.Stat(tmp)
		_, configErr := os.Stat(config)
		return errors.Is(tmpErr, os.ErrNotExist) && errors.Is(configErr, os.ErrNotExist)
	})
}

func TestCLISandboxExecuteProtectsSupervisorMarkers(t *testing.T) {
	if err := sandbox.Check(t.Context()); err != nil {
		t.Skipf("sandbox unavailable: %v", err)
	}
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("CLAUDE_CONFIG_DIR", filepath.Join(home, ".claude"))
	if runtime.GOOS == "linux" {
		must(t, os.MkdirAll(filepath.Join(home, ".claude"), 0700))
		must(t, os.WriteFile(filepath.Join(home, ".claude", ".credentials.json"), []byte("{}"), 0600))
	}
	scratch := t.TempDir()
	run := filepath.Join(scratch, "run")
	must(t, os.Mkdir(run, 0700))

	bin := filepath.Join(home, "claude")
	body := `#!/bin/sh
IFS= read -r first || exit 46
if [ -e /dev/fd/3 ] || [ -e /dev/fd/4 ]; then exit 50; fi
for marker in .agent-owned .agent-lock .agent-process .agent-exit .agent-exit.tmp; do
 [ -f "$PWD/$marker" ] || exit 49
 if echo forged > "$PWD/$marker"; then exit 48; fi
done
echo '{"type":"result","is_error":false,"session_id":"marker-session","modelUsage":{"m":{"inputTokens":1,"outputTokens":1,"costUSD":0.01}}}'
`
	must(t, os.WriteFile(bin, []byte(body), 0700))
	d := &daemon{cfg: Config{Home: scratch, StateDir: t.TempDir()}}
	ctx, cancel := context.WithTimeout(t.Context(), 15*time.Second)
	defer cancel()
	wrapped := d.cliSandbox(run, nil, false)
	builtBeforeRecord := false
	opts := agent.ExecOptions{Dir: run, CacheDir: d.cacheDir(), Sandbox: func(ctx context.Context, paths agent.CLIPaths, name string, args ...string) (*exec.Cmd, error) {
		_, err := os.Stat(filepath.Join(run, ".agent-process"))
		builtBeforeRecord = errors.Is(err, os.ErrNotExist)
		return wrapped(ctx, paths, name, args...)
	}}
	sess, err := (agent.Claude{Bin: bin}).Execute(ctx, "probe", opts)
	must(t, err)
	for range sess.Messages {
	}
	result := <-sess.Done
	if result.Err != nil || result.SessionID != "marker-session" {
		t.Fatalf("real supervised sandbox failed marker protection: %+v", result)
	}
	if !builtBeforeRecord {
		t.Fatal("test did not build sandbox before process record was published")
	}
}

func TestCLISandboxFindsOwnExecutable(t *testing.T) {
	if err := sandbox.Check(context.Background()); err != nil {
		t.Skipf("sandbox unavailable: %v", err)
	}
	t.Setenv("HOME", t.TempDir())
	scratch := t.TempDir()
	run := filepath.Join(scratch, "run")
	must(t, os.MkdirAll(run, 0700))
	self, err := os.Executable()
	must(t, err)
	d := &daemon{cfg: Config{Home: scratch, StateDir: t.TempDir()}}
	cmd, err := d.cliSandbox(run, nil, false)(context.Background(), agent.CLIPaths{}, "/bin/sh", "-c", "command -v "+filepath.Base(self))
	must(t, err)
	cmd.Dir = run
	cmd.Env = []string{"PATH=" + filepath.Dir(self) + ":/usr/bin:/bin"}
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("the agent's shell cannot find the daemon's executable on PATH: %v %s", err, out)
	}
}

func TestCLISandboxZshHeredoc(t *testing.T) {
	if err := sandbox.Check(context.Background()); err != nil {
		t.Skipf("sandbox unavailable: %v", err)
	}
	if _, err := os.Stat("/bin/zsh"); err != nil {
		t.Skip("no zsh")
	}
	t.Setenv("HOME", t.TempDir())
	scratch := t.TempDir()
	run := filepath.Join(scratch, "run")
	must(t, os.MkdirAll(run, 0700))
	d := &daemon{cfg: Config{Home: scratch, StateDir: t.TempDir()}}
	cmd, err := d.cliSandbox(run, nil, false)(context.Background(), agent.CLIPaths{}, "/bin/zsh", "-fc", "cat <<'EOF'\nsummary\nEOF")
	must(t, err)
	cmd.Dir = run
	cmd.Env = []string{"PATH=/usr/bin:/bin"}
	if out, err := cmd.CombinedOutput(); err != nil || string(out) != "summary\n" {
		t.Fatalf("zsh heredoc in the sandbox: %v %q", err, out)
	}
}

func TestCLIReadsLeanBuiltAfterPolicyStarts(t *testing.T) {
	if err := sandbox.Check(t.Context()); err != nil {
		t.Skipf("sandbox unavailable: %v", err)
	}
	home := t.TempDir()
	t.Setenv("HOME", home)
	bases := envs.NewBases(filepath.Join(home, "bases"))
	run, study := filepath.Join(home, "run"), filepath.Join(bases.Dir, "studies", "mine")
	for _, dir := range []string{run, study} {
		must(t, os.MkdirAll(dir, 0700))
	}
	d := &daemon{cfg: Config{Home: filepath.Join(home, "daemon"), StateDir: filepath.Join(home, "state")}, bases: bases}
	p, err := d.cliPolicy(run, envs.ForStudy(bases, study), false, agent.CLIPaths{})
	must(t, err)
	root := filepath.Join(bases.Dir, "lean")
	if !slices.Contains(p.Read, root) || slices.Contains(p.Write, root) || slices.Contains(p.Read, bases.Dir) {
		t.Fatalf("Lean policy: %+v", p)
	}
	ready, goFile := filepath.Join(run, "waiting"), filepath.Join(run, "go")
	pair := filepath.Join(root, "pair")
	cmd, err := p.Command(t.Context(), "/bin/sh", "-c", `touch "$1"; while [ ! -f "$2" ]; do sleep 0.01; done; cat "$3/ready" && ! touch "$3/mutated"`, "sh", ready, goFile, pair)
	must(t, err)
	must(t, cmd.Start())
	for i := 0; i < 500; i++ {
		if _, err := os.Stat(ready); err == nil {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	must(t, os.MkdirAll(pair, 0755))
	must(t, os.WriteFile(filepath.Join(pair, "ready"), nil, 0644))
	must(t, os.WriteFile(goFile, nil, 0600))
	must(t, cmd.Wait())
}
