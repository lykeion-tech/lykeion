package daemon

import (
	"bufio"
	"context"
	"io"
	"lykeion/server/internal/configpath"
	"os"
	"path/filepath"
	"reflect"
	"runtime"
	"slices"
	"strings"
	"testing"

	"github.com/google/uuid"
	"lykeion/server/internal/daemon/sandbox"
	"lykeion/server/pkg/agent"
)

func guestPolicyFixture(t *testing.T) (*daemon, string, string, string, agent.CLIPaths) {
	t.Helper()
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("LYK_CONFIG", filepath.Join(home, ".config", "lyk", "config.json"))
	t.Setenv("CLAUDE_CONFIG_DIR", filepath.Join(home, "custom-claude"))
	t.Setenv("CODEX_HOME", filepath.Join(home, "custom-codex"))
	d := &daemon{cfg: Config{Home: filepath.Join(home, ".lyk", "runs"), StateDir: filepath.Join(home, ".lyk", "daemon")}}
	guest := uuid.NewString()
	config := filepath.Join(d.cfg.StateDir, "guests", guest, "codex")
	run := filepath.Join(d.cfg.Home, "run")
	for _, dir := range []string{config, run, d.cacheDir()} {
		must(t, os.MkdirAll(dir, 0700))
	}
	must(t, os.WriteFile(filepath.Join(run, ".agent-process"), []byte("test-owned-process-marker"), 0600))
	paths, err := agent.SandboxPaths("codex", config, run, "", runtime.GOOS)
	must(t, err)
	if runtime.GOOS == "linux" {
		must(t, agent.PlaceBlocked(paths))
	}
	return d, run, guest, config, paths
}

func TestGuestPolicyShape(t *testing.T) {
	d, run, guest, config, paths := guestPolicyFixture(t)
	p, err := d.cliPolicy(run, nil, false, paths, guest)
	must(t, err)
	if p.HomeRead || p.Keychain || !p.StrictRead {
		t.Fatalf("guest has ambient home or Keychain access: %+v", p)
	}
	for _, path := range []string{filepath.Join(p.Home, ".claude"), filepath.Join(p.Home, ".claude.json"), filepath.Join(p.Home, ".codex"), os.Getenv("CLAUDE_CONFIG_DIR"), os.Getenv("CODEX_HOME")} {
		if !slices.Contains(p.Hidden, path) {
			t.Errorf("owner path not hidden: %s", path)
		}
	}
	for _, root := range []string{d.cfg.StateDir, d.cacheDir()} {
		if !slices.Contains(p.RestrictedRoots, root) {
			t.Errorf("parent is not restricted: %s", root)
		}
	}
	for _, path := range []string{config, filepath.Join(d.cacheDir(), "guests", guest), run} {
		if !slices.Contains(p.Write, path) {
			t.Errorf("guest path not writable: %s", path)
		}
	}
	if slices.Contains(p.Hidden, d.cfg.StateDir) || slices.Contains(p.Hidden, d.cacheDir()) {
		t.Fatal("hidden parent would suppress guest grant")
	}
	_, err = p.Command(t.Context(), "/bin/true")
	must(t, err)
}

func TestTwoGuestsDontShare(t *testing.T) {
	d, run, guest, config, paths := guestPolicyFixture(t)
	other := uuid.NewString()
	otherConfig := filepath.Join(d.cfg.StateDir, "guests", other, "codex")
	otherCache := filepath.Join(d.cacheDir(), "guests", other)
	for _, dir := range []string{otherConfig, otherCache} {
		must(t, os.MkdirAll(dir, 0700))
		must(t, os.WriteFile(filepath.Join(dir, "secret"), []byte("other-private"), 0600))
	}
	for _, current := range []struct {
		id, home string
		paths    agent.CLIPaths
	}{{guest, config, paths}, {other, otherConfig, agent.CLIPaths{ConfigHome: otherConfig}}} {
		p, err := d.cliPolicy(run, nil, false, current.paths, current.id)
		must(t, err)
		if !slices.Contains(p.Write, current.home) || !slices.Contains(p.Write, filepath.Join(d.cacheDir(), "guests", current.id)) {
			t.Fatal("guest does not have its own state")
		}
		forbidden := otherConfig
		if current.id == other {
			forbidden = config
		}
		for _, grant := range append(slices.Clone(p.Write), p.Read...) {
			if insideStorage(grant, forbidden) {
				t.Fatal("guest granted sibling home")
			}
		}
	}
}

func testGuestSandbox(t *testing.T, own bool) {
	t.Helper()
	if err := sandbox.Check(t.Context()); err != nil {
		t.Skipf("sandbox unavailable: %v", err)
	}
	d, run, guest, config, paths := guestPolicyFixture(t)
	owner := os.Getenv("HOME")
	denied := []string{filepath.Join(owner, "Documents", "planted"), filepath.Join(owner, ".claude", "projects", "session"), filepath.Join(owner, ".codex", "config.toml"), filepath.Join(os.Getenv("CODEX_HOME"), "auth.json"), filepath.Join(os.Getenv("CLAUDE_CONFIG_DIR"), "settings.json"), filepath.Join(d.cfg.StateDir, "private"), filepath.Join(d.cacheDir(), "owner"), filepath.Join(d.cfg.StateDir, "guests", uuid.NewString(), "codex", "auth.json"), filepath.Join(d.cacheDir(), "guests", uuid.NewString(), "private")}
	for _, path := range denied {
		must(t, os.MkdirAll(filepath.Dir(path), 0700))
		must(t, os.WriteFile(path, []byte("owner-or-other-guest"), 0600))
	}
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	script := `if ls "$HOME/.claude/projects" 2>/dev/null; then exit 70; fi; for path do if cat "$path" 2>/dev/null; then echo exposed; exit 71; fi; done`
	args := append([]string{"-c", script, "sh"}, denied...)
	if own {
		script = `test "$CODEX_HOME" = "$1" && test "$npm_config_cache" = "$2/npm" && test "$CLAUDE_CODE_TMPDIR" = "$TMPDIR" && case "$TMPDIR" in "$3"/*) ;; *) exit 72;; esac && echo own > "$1/probe" && mkdir -p "$2/npm" && echo cache > "$2/npm/probe"`
		args = []string{"-c", script, "sh", config, filepath.Join(d.cacheDir(), "guests", guest), run}
	}
	cmd, err := d.cliSandbox(run, nil, false, guest)(ctx, paths, "/bin/sh", args...)
	must(t, err)
	cmd.Dir = run
	cmd.Env = agent.ChildEnv(agent.ExecOptions{Guest: true, Dir: run, ConfigHome: config, CacheDir: filepath.Join(d.cacheDir(), "guests", guest)})
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("guest sandbox probe: %v %s", err, out)
	}
	if strings.Contains(string(out), "owner-or-other-guest") {
		t.Fatal("guest read private data")
	}
}

func TestTheOwnersOwnRunsAreUnchanged(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	d := &daemon{cfg: Config{Home: t.TempDir(), StateDir: t.TempDir()}}
	run, cliHome := t.TempDir(), t.TempDir()
	cli := agent.CLIPaths{ConfigHome: cliHome, Write: []string{filepath.Join(cliHome, "sessions")}, WriteData: []string{filepath.Join(cliHome, "credential")}, WriteFiles: []string{filepath.Join(cliHome, "auth.json")}, WriteSQLite: []string{cliHome}, Blocked: []string{filepath.Join(cliHome, "settings")}}
	hidden := []string{}
	for _, name := range []string{".ssh", ".aws", ".gnupg", ".config/gh", ".netrc", ".docker/config.json", ".kube", ".npmrc", ".pypirc", ".git-credentials"} {
		hidden = append(hidden, filepath.Join(home, name))
	}
	hidden = append(hidden, filepath.Join(d.cfg.StateDir, "services"), filepath.Join(filepath.Dir(d.cfg.StateDir), "daemon.log"), d.cfg.StateDir)
	personal, err := configpath.Private()
	must(t, err)
	hidden = append(hidden, personal...)
	self, err := os.Executable()
	must(t, err)
	self, err = filepath.EvalSymlinks(self)
	must(t, err)
	for _, readOnly := range []bool{false, true} {
		expected := sandbox.Policy{Home: home, HomeRead: true, Write: append([]string{d.cacheDir()}, cli.Write...), WriteData: cli.WriteData, WriteFiles: cli.WriteFiles, WriteSQLite: cli.WriteSQLite, Blocked: cli.Blocked, Hidden: hidden, RestrictedRoots: []string{d.cfg.Home, filepath.Join(home, ".lyk")}}
		if readOnly {
			expected.Read = []string{run, self}
		} else {
			expected.Write = append(expected.Write, run)
			expected.Read = []string{self}
		}
		actual, err := d.cliPolicy(run, nil, readOnly, cli, "")
		must(t, err)
		if !reflect.DeepEqual(actual, expected) {
			t.Fatalf("owner policy changed: actual=%+v expected=%+v", actual, expected)
		}
	}
}

func TestGuestNamingUsesTheGuestSandbox(t *testing.T) {
	d, _, guest, _, _ := guestPolicyFixture(t)
	config := filepath.Join(d.cfg.StateDir, "guests", guest, "claude")
	must(t, os.MkdirAll(config, 0700))
	secret := filepath.Join(os.Getenv("HOME"), "Documents", "private")
	must(t, os.MkdirAll(filepath.Dir(secret), 0700))
	must(t, os.WriteFile(secret, []byte("private"), 0600))
	bin := filepath.Join(os.Getenv("HOME"), "installation", "claude")
	must(t, os.MkdirAll(filepath.Dir(bin), 0700))
	script := `#!/bin/sh
[ "$CLAUDE_CODE_OAUTH_TOKEN" = "guest-fixture" ] || exit 61
if cat "$HOME/Documents/private" 2>/dev/null; then exit 62; fi
while IFS= read -r prompt; do
 printf '{"type":"assistant","message":{"content":[{"type":"text","text":"%s"}]}}\n' "$CLAUDE_CONFIG_DIR"
 echo '{"type":"result","subtype":"success","session_id":"naming-only","modelUsage":{"m":{"inputTokens":1,"outputTokens":1}}}'
done`
	script = strings.ReplaceAll(script, "$HOME/Documents/private", secret)
	must(t, os.WriteFile(bin, []byte(script), 0700))
	d.cfg.Harnesses = map[string]string{"claude": bin}
	c := claimed{}
	c.AgentRun.Harness = "claude"
	c.AgentRun.ResponsibleUserID = uuid.MustParse(guest)
	opts := agent.ExecOptions{Guest: true, ConfigHome: config, CacheDir: d.runCacheDir(guest), Env: []string{"CLAUDE_CODE_OAUTH_TOKEN=guest-fixture"}}
	n, err := d.openToolNamer(t.Context(), c, opts)
	must(t, err)
	defer n.Close()
	answer, err := n.Ask(t.Context(), "Name the step")
	must(t, err)
	if answer != config {
		t.Fatal("naming used owner's config")
	}
}

func TestGuestCannotReadFutureGuestState(t *testing.T) {
	d, run, guest, _, paths := guestPolicyFixture(t)
	p, err := d.cliPolicy(run, nil, false, paths, guest)
	must(t, err)
	late := []string{filepath.Join(d.cfg.StateDir, "guests", uuid.NewString(), "codex", "secret"), filepath.Join(d.cacheDir(), "guests", uuid.NewString(), "secret")}
	cmd, err := p.Command(t.Context(), "/bin/sh", "-c", `echo READY; read go; for path do if cat "$path" 2>/dev/null; then exit 63; fi; done`, "sh", late[0], late[1])
	must(t, err)
	stdout, err := cmd.StdoutPipe()
	must(t, err)
	stdin, err := cmd.StdinPipe()
	must(t, err)
	must(t, cmd.Start())
	reader := bufio.NewReader(stdout)
	line, err := reader.ReadString('\n')
	must(t, err)
	if line != "READY\n" {
		t.Fatal("sandbox did not reach rendezvous")
	}
	for _, path := range late {
		must(t, os.MkdirAll(filepath.Dir(path), 0700))
		must(t, os.WriteFile(path, []byte("private"), 0600))
	}
	_, err = io.WriteString(stdin, "go\n")
	must(t, err)
	must(t, stdin.Close())
	output, err := io.ReadAll(reader)
	must(t, err)
	must(t, cmd.Wait())
	if len(output) != 0 {
		t.Fatal("late guest state became readable")
	}
}

func TestGuestExecutableBesideOwnerFilesDoesNotExposeThem(t *testing.T) {
	d, run, guest, _, paths := guestPolicyFixture(t)
	home := os.Getenv("HOME")
	secret := filepath.Join(home, "Documents", "planted")
	must(t, os.MkdirAll(filepath.Dir(secret), 0700))
	must(t, os.WriteFile(secret, []byte("private"), 0600))
	bin := filepath.Join(home, "standalone-cli")
	must(t, os.WriteFile(bin, []byte("#!/bin/sh\nif cat \""+secret+"\" 2>/dev/null; then exit 81; fi\n"), 0700))
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	cmd, err := d.cliSandbox(run, nil, false, guest)(ctx, paths, bin)
	must(t, err)
	cmd.Env = agent.ChildEnv(agent.ExecOptions{Guest: true})
	if output, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("standalone CLI home boundary: %v %s", err, output)
	}
}

func TestGuestExecutableCannotReadAdjacentOwnerFile(t *testing.T) {
	d, run, guest, _, paths := guestPolicyFixture(t)
	install := filepath.Join(os.Getenv("HOME"), "installation")
	must(t, os.MkdirAll(install, 0700))
	secret := filepath.Join(install, "private-owner-file")
	must(t, os.WriteFile(secret, []byte("adjacent-owner-fixture"), 0600))
	bin := filepath.Join(install, "codex")
	must(t, os.WriteFile(bin, []byte("#!/bin/sh\nif cat '"+secret+"' 2>/dev/null; then exit 77; fi\n"), 0700))
	control, err := os.ReadFile(secret)
	must(t, err)
	if string(control) != "adjacent-owner-fixture" {
		t.Fatal("unobservable fixture")
	}
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	cmd, err := d.cliSandbox(run, nil, false, guest)(ctx, paths, bin)
	must(t, err)
	cmd.Dir = run
	cmd.Env = agent.ChildEnv(agent.ExecOptions{Guest: true})
	if output, err := cmd.CombinedOutput(); err != nil || strings.Contains(string(output), "adjacent-owner-fixture") {
		t.Fatalf("executable parent exposed adjacent owner data: %v %s", err, output)
	}
}

func TestGuestCLIHelperCleanupKeepsRunProtections(t *testing.T) {
	d, run, guest, config, paths := guestPolicyFixture(t)
	wrap := d.cliSandbox(run, nil, false, guest)
	helperCtx, cancelHelper := context.WithCancel(t.Context())
	defer cancelHelper()
	helper, err := wrap(helperCtx, paths, "/usr/bin/true")
	must(t, err)
	helper.Dir = run
	helper.Env = agent.ChildEnv(agent.ExecOptions{Guest: true, Dir: run, ConfigHome: config})
	if output, err := helper.CombinedOutput(); err != nil {
		t.Fatalf("helper: %v %s", err, output)
	}
	tmp := ""
	for _, arg := range helper.Args {
		if value, ok := strings.CutPrefix(arg, "TMPDIR="); ok {
			tmp = value
		}
	}
	if tmp == "" {
		t.Fatal("helper private temp not observable")
	}
	nextCtx, cancelNext := context.WithCancel(t.Context())
	defer cancelNext()
	next, err := wrap(nextCtx, paths, "/bin/sh", "-c", `test -d "$1/.claude" && echo PROTECTED`, "sh", run)
	must(t, err)
	next.Dir = run
	next.Env = agent.ChildEnv(agent.ExecOptions{Guest: true, Dir: run, ConfigHome: config})
	cancelHelper()
	waitFor(t, func() bool { _, err := os.Stat(tmp); return os.IsNotExist(err) })
	if output, err := next.CombinedOutput(); err != nil || strings.TrimSpace(string(output)) != "PROTECTED" {
		t.Fatalf("helper cleanup removed next launch protections: %v %s", err, output)
	}
}
