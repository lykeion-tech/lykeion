package sandbox

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"testing"
)

// tempDir returns a fresh temp dir with symlinks resolved, as the profile
// sees it (/var/folders is /private/var/folders on macOS).
func tempDir(t *testing.T) string {
	t.Helper()
	d, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	return d
}

// shortDir is a resolved temp dir under /tmp, short enough to hold a unix
// socket path.
func shortDir(t *testing.T) string {
	t.Helper()
	d, err := os.MkdirTemp("/tmp", "lyk")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.RemoveAll(d) })
	if d, err = filepath.EvalSymlinks(d); err != nil {
		t.Fatal(err)
	}
	return d
}

func mkdir(t *testing.T, p string) string {
	t.Helper()
	if err := os.MkdirAll(p, 0o755); err != nil {
		t.Fatal(err)
	}
	return p
}

func TestSeatbeltProfileRules(t *testing.T) {
	home := tempDir(t)
	run := mkdir(t, filepath.Join(home, "run"))
	base := mkdir(t, filepath.Join(home, ".lyk", "envs", "python"))
	prof, err := Policy{Home: home, Write: []string{run}, Read: []string{base}}.seatbeltProfile()
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{
		"(version 1)\n(deny default)\n",
		`(subpath "/usr")`,
		`(subpath "/System")`,
		fmt.Sprintf("(allow file-read* (subpath %q))", base),
		fmt.Sprintf("(allow file-read* file-write* (subpath %q))", run),
		fmt.Sprintf("(literal %q)", filepath.Join(home, ".lyk")),
		fmt.Sprintf("(allow network-outbound (remote unix-socket (subpath %q)))", run),
		`(allow network-outbound (remote unix-socket (path-literal "/private/var/run/mDNSResponder")))`,
		`(allow network-outbound (remote ip))`,
		`(allow file-write* (literal "/dev/null") (literal "/dev/tty") (subpath "/dev/fd")`,
		`(allow signal (target same-sandbox))`,
	} {
		if !strings.Contains(prof, want) {
			t.Errorf("profile lacks %s\n%s", want, prof)
		}
	}
	for _, never := range []string{
		"(allow default)", "regex", "lsopen", "appleevent", "pasteboard", "securityd", "SecurityServer",
		"com.apple.lsd", "coreservices", "file-read* (subpath " + fmt.Sprintf("%q", home) + ")",
	} {
		if strings.Contains(prof, never) {
			t.Errorf("profile holds %s\n%s", never, prof)
		}
	}
}

func TestSeatbeltQuotesHostilePaths(t *testing.T) {
	home := tempDir(t)
	run := mkdir(t, filepath.Join(home, `a"b) (allow file-read* (subpath "/")) \c d`))
	prof, err := Policy{Home: home, Write: []string{run}}.seatbeltProfile()
	if err != nil {
		t.Fatal(err)
	}
	want := `(subpath "` + strings.NewReplacer(`\`, `\\`, `"`, `\"`).Replace(run) + `")`
	if !strings.Contains(prof, want) {
		t.Fatalf("path not escaped as %s:\n%s", want, prof)
	}
	if strings.Contains(prof, `(allow file-read* (subpath "/"))`) {
		t.Fatalf("injected rule reached the profile:\n%s", prof)
	}
}

func TestRefusesPathsThatCannotBeExpressed(t *testing.T) {
	home := tempDir(t)
	nl := mkdir(t, filepath.Join(home, "a\nb"))
	run := mkdir(t, filepath.Join(home, "run"))
	envs := mkdir(t, filepath.Join(home, ".lyk", "envs"))
	study := mkdir(t, filepath.Join(envs, "studies", "w", "s"))
	ssh := mkdir(t, filepath.Join(home, ".ssh"))
	// A link planted below the home folder must not carry a policy path to
	// what it points at, nor may a link outside the home reach into it.
	planted := filepath.Join(home, ".lyk-envs")
	outside := filepath.Join(tempDir(t), "into-home")
	for link, target := range map[string]string{planted: ssh, outside: ssh} {
		if err := os.Symlink(target, link); err != nil {
			t.Fatal(err)
		}
	}
	// A link below the home folder pointing out of it is refused as well:
	// the home folder's contents are the person's, not the policy's.
	away := filepath.Join(study, "tmp-away")
	if err := os.Symlink(tempDir(t), away); err != nil {
		t.Fatal(err)
	}
	linkedParent := filepath.Join(home, "linked")
	if err := os.Symlink(home, linkedParent); err != nil {
		t.Fatal(err)
	}
	cases := map[string]Policy{
		"newline":                 {Home: home, Write: []string{nl}},
		"relative":                {Home: home, Write: []string{"run"}},
		"missing":                 {Home: home, Write: []string{filepath.Join(home, "nope")}},
		"no home":                 {Write: []string{home}},
		"home itself":             {Home: home, Write: []string{home}},
		"above home":              {Home: home, Read: []string{filepath.Dir(home)}},
		"nul":                     {Home: home, Read: []string{home + "/x\x00y"}},
		"control char":            {Home: home, Read: []string{home + "/x\ty"}},
		"link below home":         {Home: home, Read: []string{planted}},
		"link into home":          {Home: home, Read: []string{outside}},
		"link in a parent":        {Home: home, Write: []string{filepath.Join(linkedParent, "run")}},
		"link out of home":        {Home: home, Write: []string{away}},
		"link out, home as given": {Home: home + "/", Write: []string{away}},
		"write under read":        {Home: home, Read: []string{envs}, Write: []string{study}},
		"read under write":        {Home: home, Write: []string{envs}, Read: []string{study}},
		"read equal to write":     {Home: home, Write: []string{run}, Read: []string{run}},
	}
	// The same link spelled through an alias of the home folder: the
	// person's own link to it, and on macOS /var and /tmp for /private/....
	alias := filepath.Join(tempDir(t), "my-home")
	if err := os.Symlink(home, alias); err != nil {
		t.Fatal(err)
	}
	cases["link out, via a link to home"] = Policy{Home: home, Write: []string{filepath.Join(alias, ".lyk", "envs", "studies", "w", "s", "tmp-away")}}
	cases["dotdot"] = Policy{Home: home, Write: []string{run + "/../run"}}
	if raw, ok := strings.CutPrefix(away, "/private"); ok {
		cases["link out, /var for /private/var"] = Policy{Home: home, Write: []string{raw}}
	}
	if runtime.GOOS == "darwin" {
		short := shortDir(t)
		out := filepath.Join(short, "away")
		if err := os.Symlink(tempDir(t), out); err != nil {
			t.Fatal(err)
		}
		cases["link out, /tmp for /private/tmp"] = Policy{Home: short, Write: []string{strings.TrimPrefix(out, "/private")}}
	}
	for name, p := range cases {
		if _, err := p.seatbeltProfile(); err == nil {
			t.Errorf("%s: seatbelt profile accepted %q", name, append(p.Write, p.Read...))
		}
		if _, err := p.bwrapArgs("true"); err == nil {
			t.Errorf("%s: bwrap args accepted %q", name, append(p.Write, p.Read...))
		}
	}
	// Two write folders may nest: a run's temp folder can sit in its study.
	if _, err := (Policy{Home: home, Write: []string{study, mkdir(t, filepath.Join(study, "tmp"))}}).seatbeltProfile(); err != nil {
		t.Errorf("nested write folders refused: %v", err)
	}
}

// spellingDir returns a fresh folder holding a folder named name, plus that
// folder spelled as other, or skips when the file system tells the two
// spellings apart.
func spellingDir(t *testing.T, name, other string) (string, string) {
	t.Helper()
	root := tempDir(t)
	dir := mkdir(t, filepath.Join(root, name))
	if _, err := os.Stat(filepath.Join(root, other)); err != nil {
		t.Skipf("the file system tells %q from %q", name, other)
	}
	return dir, filepath.Join(root, other)
}

// The home folder is recognised by what it is, not by how it is spelled: a
// case-insensitive or normalisation-insensitive file system answers to other
// spellings of the same folder.
func TestHomeIsKnownByIdentity(t *testing.T) {
	for _, sp := range []struct{ kind, name, other string }{
		{"case", "Home", "home"},
		{"normalisation", "Caf\u00e9", "Cafe\u0301"},
	} {
		t.Run(sp.kind, func(t *testing.T) {
			home, other := spellingDir(t, sp.name, sp.other)
			run := mkdir(t, filepath.Join(home, "run"))
			mkdir(t, filepath.Join(home, ".ssh"))
			study := mkdir(t, filepath.Join(home, ".lyk", "envs", "studies", "w", "s"))
			outside := tempDir(t)
			if err := os.Symlink(outside, filepath.Join(study, "tmp-away")); err != nil {
				t.Fatal(err)
			}
			if err := os.Symlink(".ssh", filepath.Join(home, ".lyk-envs")); err != nil {
				t.Fatal(err)
			}
			awayRel := filepath.Join(".lyk", "envs", "studies", "w", "s", "tmp-away")
			cases := map[string]Policy{
				"link out, other spelling":      {Home: home, Write: []string{filepath.Join(other, awayRel)}},
				"link to .ssh, other spelling":  {Home: home, Read: []string{filepath.Join(other, ".lyk-envs")}},
				"home itself, other spelling":   {Home: home, Write: []string{other}},
				"home field in other spelling":  {Home: other, Write: []string{filepath.Join(home, awayRel)}},
				"home itself, home field other": {Home: other, Write: []string{home}},
			}
			for name, p := range cases {
				if _, err := p.seatbeltProfile(); err == nil {
					t.Errorf("%s: seatbelt profile accepted %q", name, append(p.Write, p.Read...))
				}
				if _, err := p.bwrapArgs("true"); err == nil {
					t.Errorf("%s: bwrap args accepted %q", name, append(p.Write, p.Read...))
				}
			}
			// A folder spelled the other way is granted under the spelling the
			// file system keeps, which is the one the kernel matches against.
			p := Policy{Home: other, Write: []string{filepath.Join(other, "run")}}
			prof, err := p.seatbeltProfile()
			if err != nil {
				t.Fatal(err)
			}
			if !strings.Contains(prof, fmt.Sprintf("(subpath %q)", run)) {
				t.Fatalf("run folder not granted as %s:\n%s", run, prof)
			}
		})
	}
}

func TestPathsOutsideHomeAreResolved(t *testing.T) {
	home := tempDir(t)
	elsewhere := tempDir(t)
	data := mkdir(t, filepath.Join(elsewhere, "data"))
	link := filepath.Join(elsewhere, "link")
	if err := os.Symlink(data, link); err != nil {
		t.Fatal(err)
	}
	prof, err := Policy{Home: home, Write: []string{link}}.seatbeltProfile()
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(prof, fmt.Sprintf("(subpath %q)", data)) || strings.Contains(prof, link+`"`) {
		t.Fatalf("symlink not resolved:\n%s", prof)
	}
}

func TestBwrapArgs(t *testing.T) {
	home := tempDir(t)
	study := mkdir(t, filepath.Join(home, ".lyk", "envs", "studies", "w", "s"))
	tmp := mkdir(t, filepath.Join(study, "tmp"))
	base := mkdir(t, filepath.Join(home, ".lyk", "envs", "python"))
	run := mkdir(t, filepath.Join(home, "run"))
	args, err := Policy{Home: home, Write: []string{tmp, run, study}, Read: []string{base}}.bwrapArgs("python3", "-c", "print(1)")
	if err != nil {
		t.Fatal(err)
	}
	got := strings.Join(args, " ")
	// Shallower binds go first so a deeper one lands on top of them.
	want := "--ro-bind / / --dev /dev --proc /proc --tmpfs /tmp --tmpfs /run" +
		" --ro-bind-try /run/systemd/resolve /run/systemd/resolve --tmpfs " + home +
		" --bind " + run + " " + run +
		" --ro-bind " + base + " " + base +
		" --bind " + study + " " + study +
		" --bind " + tmp + " " + tmp +
		" --remount-ro " + home +
		" --unshare-pid --unshare-ipc --cap-drop ALL --share-net --die-with-parent -- python3 -c print(1)"
	if got != want {
		t.Fatalf("bwrap args\n got %s\nwant %s", got, want)
	}
	if slices.Contains(args, "--new-session") {
		t.Fatal("--new-session is left to Command's own setsid")
	}
}

// policyFixture plants a secret under a temp HOME and returns a policy that
// may write only the run folder.
func policyFixture(t *testing.T) (Policy, string, string) {
	t.Helper()
	return fixtureIn(t, tempDir(t))
}

func fixtureIn(t *testing.T, home string) (Policy, string, string) {
	t.Helper()
	secret := filepath.Join(mkdir(t, filepath.Join(home, ".ssh")), "id_rsa")
	if err := os.WriteFile(secret, []byte("PRIVATE KEY"), 0o600); err != nil {
		t.Fatal(err)
	}
	run := mkdir(t, filepath.Join(home, "studies", `run "1") x\y`))
	return Policy{Home: home, Write: []string{run}}, secret, run
}

func sandboxed(t *testing.T, p Policy, script string) (string, error) {
	t.Helper()
	cmd, err := p.Command(context.Background(), "/bin/sh", "-c", script)
	if err != nil {
		t.Fatal(err)
	}
	cmd.Env = append(os.Environ(), "HOME="+p.Home)
	cmd.Dir = p.Home
	if len(p.Write) > 0 {
		cmd.Dir = p.Write[0]
	}
	out, err := cmd.CombinedOutput()
	return string(out), err
}

func requireSandbox(t *testing.T) {
	t.Helper()
	switch runtime.GOOS {
	case "darwin":
	case "linux":
		if _, err := exec.LookPath("bwrap"); err != nil {
			t.Skip("bwrap is not installed")
		}
	default:
		t.Skip("no sandbox on " + runtime.GOOS)
	}
}

func TestSandboxConfinesForReal(t *testing.T) {
	requireSandbox(t)
	p, secret, run := policyFixture(t)

	refused(t, p, "cat "+shq(secret))
	refused(t, p, "sh -c 'cat \"$0\"' "+shq(secret))
	probe := filepath.Join(p.Home, "lyk-sandbox-probe")
	refused(t, p, "echo x > "+shq(probe))
	if _, err := os.Stat(probe); err == nil {
		t.Fatal("probe file exists in HOME")
	}
	refused(t, p, "sh -c 'echo x > \"$0\"' "+shq(probe))
	outside := filepath.Join(tempDir(t), "outside")
	refused(t, p, "echo x > "+shq(outside))
	if _, err := os.Stat(outside); err == nil {
		t.Fatal("wrote outside the write folders")
	}
	if out, err := sandboxed(t, p, "echo ok > result.txt && sh -c 'cat result.txt' && echo quiet > /dev/null"); err != nil || out != "ok\n" {
		t.Fatalf("run folder not writable: err=%v out=%q", err, out)
	}
	if b, err := os.ReadFile(filepath.Join(run, "result.txt")); err != nil || string(b) != "ok\n" {
		t.Fatalf("result.txt: %q %v", b, err)
	}
}

func TestSandboxAllowsNetwork(t *testing.T) {
	requireSandbox(t)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		fmt.Fprint(w, "pong")
	}))
	defer srv.Close()
	tls := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		fmt.Fprint(w, "secure pong")
	}))
	defer tls.Close()
	p, _, _ := policyFixture(t)
	if out, err := sandboxed(t, p, "curl -q -sS "+srv.URL); err != nil || out != "pong" {
		t.Fatalf("network: err=%v out=%q", err, out)
	}
	if out, err := sandboxed(t, p, "curl -q -sS -k "+tls.URL); err != nil || out != "secure pong" {
		t.Fatalf("https: err=%v out=%q", err, out)
	}
}

// Name lookups go through the system resolver; skipped when this machine
// cannot resolve names at all.
func TestSandboxResolvesNames(t *testing.T) {
	requireSandbox(t)
	lookup := `getent hosts example.com > /dev/null && echo resolved`
	if runtime.GOOS == "darwin" {
		lookup = `/usr/bin/python3 -c 'import socket; socket.getaddrinfo("example.com", 443); print("resolved")'`
	}
	if out, err := exec.Command("/bin/sh", "-c", lookup).CombinedOutput(); err != nil {
		t.Skipf("no name resolution outside the sandbox: %s", out)
	}
	p, _, _ := policyFixture(t)
	if out, err := sandboxed(t, p, lookup); err != nil || !strings.HasSuffix(out, "resolved\n") {
		t.Fatalf("lookup: err=%v out=%q", err, out)
	}
}

func TestCheck(t *testing.T) {
	requireSandbox(t)
	if err := Check(context.Background()); err != nil {
		t.Fatal(err)
	}
}

// Check's probe lies where only the home rule hides it: a sandbox that
// shows the person's home folder fails the check.
func TestCheckFailsASandboxThatShowsTheHomeFolder(t *testing.T) {
	requireSandbox(t)
	if err := check(context.Background(), showsThePersonsHome); err == nil {
		t.Fatal("a sandbox that shows the person's home folder passed the check")
	}
}

func shq(s string) string { return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'" }

func TestReadFolderUnderHomeResolves(t *testing.T) {
	requireSandbox(t)
	p, _, _ := policyFixture(t)
	base := mkdir(t, filepath.Join(p.Home, ".lyk", "envs", "python", "bin"))
	if err := os.Symlink("/bin/sh", filepath.Join(base, "python")); err != nil {
		t.Fatal(err)
	}
	p.Read = []string{filepath.Dir(base)}
	sh, err := filepath.EvalSymlinks("/bin/sh")
	if err != nil {
		t.Fatal(err)
	}
	// A venv's interpreter is a link that is resolved through every folder
	// above it, all of them under the hidden home.
	if out, err := sandboxed(t, p, "realpath "+shq(filepath.Join(base, "python"))); err != nil || out != sh+"\n" {
		t.Fatalf("realpath under a read folder: err=%v out=%q", err, out)
	}
	// Seatbelt refuses the listing; bwrap shows an empty home holding only the
	// folders it bound in.
	if runtime.GOOS == "darwin" {
		refused(t, p, "ls "+shq(p.Home))
	} else if out, err := sandboxed(t, p, "ls -A "+shq(p.Home)); err != nil || strings.Contains(out, ".ssh") || !strings.Contains(out, ".lyk") {
		t.Fatalf("home listing: err=%v out=%q", err, out)
	}
}

// refused runs script and requires the OS to have refused it, so a profile
// that fails to load (and so runs nothing) does not pass for confinement.
func refused(t *testing.T, p Policy, script string) {
	t.Helper()
	out, err := sandboxed(t, p, script)
	markers := []string{"Operation not permitted"}
	if runtime.GOOS == "linux" {
		markers = append(markers, "Permission denied", "Read-only file system", "No such file or directory", "Directory nonexistent", "Device or resource busy")
	}
	denied := slices.ContainsFunc(markers, func(m string) bool { return strings.Contains(out, m) })
	// A sandbox that could not start says so in its own name.
	started := !strings.Contains(out, "bwrap: ") && !strings.Contains(out, "sandbox-exec: ")
	if err == nil || !denied || !started || strings.Contains(out, "PRIVATE KEY") {
		t.Fatalf("%s: not refused by the sandbox: err=%v out=%q", script, err, out)
	}
}

// Python's multiprocessing needs POSIX semaphores and shared memory.
func TestMultiprocessingWorks(t *testing.T) {
	requireSandbox(t)
	py := "/usr/bin/python3"
	if _, err := os.Stat(py); err != nil {
		t.Skip("no " + py)
	}
	p, _, run := policyFixture(t)
	script := `
from concurrent.futures import ProcessPoolExecutor
from multiprocessing import shared_memory, Lock
def square(x): return x * x
if __name__ == "__main__":
    Lock()
    with ProcessPoolExecutor(2) as ex:
        print(sum(ex.map(square, range(5))))
    shm = shared_memory.SharedMemory(create=True, size=4)
    shm.buf[:2] = b"ok"
    other = shared_memory.SharedMemory(name=shm.name)
    print(bytes(other.buf[:2]).decode())
    other.close(); shm.close(); shm.unlink()
`
	if err := os.WriteFile(filepath.Join(run, "mp.py"), []byte(script), 0o644); err != nil {
		t.Fatal(err)
	}
	if out, err := sandboxed(t, p, "TMPDIR=$PWD "+py+" mp.py 2>&1 | grep -v xcrun"); err != nil || !strings.HasSuffix(out, "30\nok\n") {
		t.Fatalf("multiprocessing: err=%v out=%q", err, out)
	}
}

// carveFixture is policyFixture with the CLI's settings folder and MCP file
// in the run folder, both read-only to the sandboxed code.
func carveFixture(t *testing.T) (Policy, string) {
	t.Helper()
	p, _, run := policyFixture(t)
	mkdir(t, filepath.Join(run, ".claude", "skills"))
	for _, f := range []string{filepath.Join(run, ".claude", "settings.json"), filepath.Join(run, ".mcp.json")} {
		if err := os.WriteFile(f, []byte("{}"), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	p.ReadOnly = []string{filepath.Join(run, ".claude"), filepath.Join(run, ".mcp.json")}
	return p, run
}

func TestReadOnlyRules(t *testing.T) {
	p, run := carveFixture(t)
	prof, err := p.seatbeltProfile()
	if err != nil {
		t.Fatal(err)
	}
	// Later rules win, so the carve-outs' denials follow every allow.
	last := 0
	for _, allow := range []string{
		"(allow file-read* file-write* (subpath " + sbplString(run) + "))",
		"(allow network-inbound network-bind (local unix-socket (subpath " + sbplString(run) + ")))",
	} {
		i := strings.Index(prof, allow)
		if i < 0 {
			t.Fatalf("no %s in\n%s", allow, prof)
		}
		last = max(last, i)
	}
	for _, c := range p.ReadOnly {
		for _, deny := range []string{
			"(deny file-write* (subpath " + sbplString(c) + "))",
			"(deny network-outbound (remote unix-socket (subpath " + sbplString(c) + ")))",
			"(deny network-inbound network-bind (local unix-socket (subpath " + sbplString(c) + ")))",
		} {
			if i := strings.Index(prof, deny); i < last {
				t.Errorf("%s missing or before the allows:\n%s", deny, prof)
			}
		}
	}
	args, err := p.bwrapArgs("true")
	if err != nil {
		t.Fatal(err)
	}
	got := strings.Join(args, " ")
	want := "--bind " + run + " " + run + " --ro-bind " + p.ReadOnly[0] + " " + p.ReadOnly[0] + " --ro-bind " + p.ReadOnly[1] + " " + p.ReadOnly[1] + " --remount-ro"
	if !strings.Contains(got, want) {
		t.Fatalf("bwrap args\n got %s\nwant …%s…", got, want)
	}
}

func TestRefusesReadOnlyPathsThatCannotBeExpressed(t *testing.T) {
	p, run := carveFixture(t)
	other := mkdir(t, filepath.Join(run, "other"))
	if err := os.Symlink(other, filepath.Join(run, ".codex")); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(filepath.Join(run, ".mcp.json"), filepath.Join(run, "AGENTS.md")); err != nil {
		t.Fatal(err)
	}
	envs := mkdir(t, filepath.Join(run, ".lyk-envs"))
	mkdir(t, filepath.Join(run, "a", "b"))
	for name, ro := range map[string]string{
		"missing":             filepath.Join(run, ".missing-config"),
		"folder link":         filepath.Join(run, ".codex"),
		"file link":           filepath.Join(run, "AGENTS.md"),
		"outside the folders": filepath.Join(p.Home, ".ssh"),
		"not a direct child":  filepath.Join(run, "a", "b"),
		"the folder itself":   run,
		"holds a write":       envs,
		"dotdot":              run + "/a/../.mcp.json",
		"relative":            ".mcp.json",
	} {
		q := p
		q.Write = append(slices.Clone(p.Write), envs)
		q.ReadOnly = []string{ro}
		if _, err := q.seatbeltProfile(); err == nil {
			t.Errorf("%s: seatbelt profile accepted %q", name, ro)
		}
		if _, err := q.bwrapArgs("true"); err == nil {
			t.Errorf("%s: bwrap args accepted %q", name, ro)
		}
	}
}

// The CLI's own files in the run folder can be read but not written,
// removed, renamed or replaced by a link; the rest of the folder stays
// writable.
func TestReadOnlyPathsHoldForReal(t *testing.T) {
	requireSandbox(t)
	p, run := carveFixture(t)
	if out, err := sandboxed(t, p, "cat .mcp.json .claude/settings.json && echo x > data.csv && mkdir -p data/sub && echo y > data/sub/f"); err != nil || out != "{}{}" {
		t.Fatalf("reads and ordinary writes: err=%v out=%q", err, out)
	}
	for _, script := range []string{
		"echo x > .claude/settings.json",
		"echo x > .claude/new.json",
		"mkdir .claude/hooks",
		"echo x > .claude/skills/SKILL.md",
		"echo x > .mcp.json",
		"echo x >> .mcp.json",
		"rm .mcp.json",
		"rm .claude/settings.json",
		"mv .mcp.json moved.json",
		"mv data.csv .mcp.json",
		"mv .claude moved",
		"rm -rf .claude",
		"ln -sf /etc/hosts .mcp.json",
		"rm -f .mcp.json && ln -s /etc/hosts .mcp.json",
		"chmod 777 .mcp.json",
		"touch -t 200001010000 .mcp.json",
	} {
		refused(t, p, script)
	}
	if runtime.GOOS == "darwin" {
		if _, err := os.Stat(filepath.Join(run, ".MCP.JSON")); err == nil {
			refused(t, p, "echo x > .MCP.JSON")
		}
	}
	if out, err := sandboxed(t, p, `/usr/bin/python3 -c "import socket; socket.socket(socket.AF_UNIX).bind('.claude/s.sock')"`); err == nil {
		t.Errorf("a socket was bound in a read-only folder: %q", out)
	}
	for f, want := range map[string]string{filepath.Join(run, ".mcp.json"): "{}", filepath.Join(run, ".claude", "settings.json"): "{}"} {
		if b, err := os.ReadFile(f); err != nil || string(b) != want {
			t.Errorf("%s changed: %q %v", f, b, err)
		}
	}
	for _, f := range []string{".claude/new.json", ".claude/hooks", ".claude/s.sock", "moved.json", "moved"} {
		if _, err := os.Lstat(filepath.Join(run, f)); err == nil {
			t.Errorf("%s exists", f)
		}
	}
	if fi, err := os.Lstat(filepath.Join(run, ".mcp.json")); err != nil || !fi.Mode().IsRegular() {
		t.Errorf(".mcp.json was replaced: %v", err)
	}
}

// Code may write a write folder, so a link in it may be its own: a policy
// path reached through one is refused, even when the link leads outside the
// home folder.
func TestRefusesAPathThroughALinkInAWriteFolder(t *testing.T) {
	home := tempDir(t)
	run := mkdir(t, filepath.Join(home, "run"))
	away := mkdir(t, filepath.Join(tempDir(t), "away"))
	if err := os.Symlink(away, filepath.Join(run, ".lyk-envs")); err != nil {
		t.Fatal(err)
	}
	outRun := mkdir(t, filepath.Join(tempDir(t), "run"))
	if err := os.Symlink(away, filepath.Join(outRun, "data")); err != nil {
		t.Fatal(err)
	}
	for name, p := range map[string]Policy{
		"write in home":     {Home: home, Write: []string{run, filepath.Join(run, ".lyk-envs")}},
		"write outside":     {Home: home, Write: []string{outRun, filepath.Join(outRun, "data")}},
		"read outside":      {Home: home, Write: []string{outRun}, Read: []string{filepath.Join(outRun, "data")}},
		"write order swaps": {Home: home, Write: []string{filepath.Join(outRun, "data"), outRun}},
	} {
		if _, err := p.seatbeltProfile(); err == nil {
			t.Errorf("%s: seatbelt profile accepted", name)
		}
		if _, err := p.bwrapArgs("true"); err == nil {
			t.Errorf("%s: bwrap args accepted", name)
		}
	}
}

// hiddenFixture is carveFixture with the CLI's MCP config, which holds the
// door's token, hidden from the code entirely.
func hiddenFixture(t *testing.T) (Policy, string) {
	t.Helper()
	p, run := carveFixture(t)
	cfg := filepath.Join(run, ".lyk-mcp.json")
	if err := os.WriteFile(cfg, []byte(`{"token":"DOOR TOKEN"}`), 0o600); err != nil {
		t.Fatal(err)
	}
	home := mkdir(t, filepath.Join(run, ".cli-home"))
	if err := os.WriteFile(filepath.Join(home, ".credentials.json"), []byte("DOOR TOKEN"), 0o600); err != nil {
		t.Fatal(err)
	}
	mcp := filepath.Join(run, ".claude", "mcp.json")
	if err := os.WriteFile(mcp, []byte("DOOR TOKEN"), 0o600); err != nil {
		t.Fatal(err)
	}
	p.Hidden = []string{cfg, home, mcp}
	return p, run
}

func TestHiddenRules(t *testing.T) {
	p, run := hiddenFixture(t)
	prof, err := p.seatbeltProfile()
	if err != nil {
		t.Fatal(err)
	}
	allow := strings.Index(prof, "(allow file-read* file-write* (subpath "+sbplString(run)+"))")
	for _, h := range p.Hidden {
		if i := strings.Index(prof, "(deny file-read* file-write* (subpath "+sbplString(h)+"))"); i < allow || allow < 0 {
			t.Errorf("%s not denied after the allows:\n%s", h, prof)
		}
	}
	args, err := p.bwrapArgs("true")
	if err != nil {
		t.Fatal(err)
	}
	got := strings.Join(args, " ")
	for _, want := range []string{
		"--ro-bind /dev/null " + p.Hidden[0] + " ",
		"--tmpfs " + p.Hidden[1] + " --remount-ro " + p.Hidden[1] + " ",
		"--ro-bind /dev/null " + p.Hidden[2] + " ",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("bwrap args lack %q:\n%s", want, got)
		}
	}
	// the hidden file in a read-only folder is covered after that folder's bind
	if strings.Index(got, "--ro-bind "+p.ReadOnly[0]+" ") > strings.Index(got, "/dev/null "+p.Hidden[2]) {
		t.Errorf("hidden file bound before its read-only folder:\n%s", got)
	}
	q := p
	q.Hidden = []string{filepath.Join(run, "nope.json")}
	if _, err := q.seatbeltProfile(); err == nil {
		t.Error("a missing hidden path was accepted")
	}
	q.Hidden = []string{filepath.Join(p.Home, ".ssh")}
	if _, err := q.seatbeltProfile(); err == nil {
		t.Error("a hidden path outside the write folders was accepted")
	}
}

// The code reads nothing of a hidden path, though the CLI outside does.
func TestHiddenPathsHoldForReal(t *testing.T) {
	requireSandbox(t)
	p, run := hiddenFixture(t)
	for _, script := range []string{"cat .lyk-mcp.json", "cat .cli-home/.credentials.json", "cat .claude/mcp.json", "ls .cli-home", "grep -r TOKEN ."} {
		out, _ := sandboxed(t, p, script)
		if strings.Contains(out, "DOOR TOKEN") {
			t.Errorf("%s: read the token: %q", script, out)
		}
	}
	for _, script := range []string{"echo x > .lyk-mcp.json", "rm .lyk-mcp.json", "echo x > .cli-home/new", "rm .claude/mcp.json"} {
		refused(t, p, script)
	}
	if runtime.GOOS == "darwin" {
		refused(t, p, "cat .lyk-mcp.json")
		refused(t, p, "cat .cli-home/.credentials.json")
	}
	if b, err := os.ReadFile(filepath.Join(run, ".lyk-mcp.json")); err != nil || !strings.Contains(string(b), "DOOR TOKEN") {
		t.Fatalf("outside the sandbox the config is %q %v", b, err)
	}
}

// A link inside a write folder is found whatever spelling of that folder a
// path uses, on a file system that takes several.
func TestALinkInAWriteFolderIsFoundUnderAnySpelling(t *testing.T) {
	run, other := spellingDir(t, "Run", "rUN")
	away := mkdir(t, filepath.Join(tempDir(t), "away"))
	if err := os.Symlink(away, filepath.Join(run, "data")); err != nil {
		t.Fatal(err)
	}
	p := Policy{Home: tempDir(t), Write: []string{run, filepath.Join(other, "data")}}
	if _, err := p.seatbeltProfile(); err == nil {
		t.Error("seatbelt profile accepted a path through a link in a write folder spelled otherwise")
	}
	if _, err := p.bwrapArgs("true"); err == nil {
		t.Error("bwrap args accepted a path through a link in a write folder spelled otherwise")
	}
}

func TestServicePolicyRefusesUnenforceableMacLoopback(t *testing.T) {
	profile, err := (Policy{Home: tempDir(t), LoopbackOnly: true}).seatbeltProfile()
	if !errors.Is(err, ErrLoopbackOnlyUnsupported) || profile != "" {
		t.Fatalf("profile %q error %v", profile, err)
	}
}

func TestDefaultPolicyKeepsItsNetworkRules(t *testing.T) {
	profile, err := (Policy{Home: tempDir(t)}).seatbeltProfile()
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(profile, `(allow network-inbound network-bind (local ip))`) || !strings.Contains(profile, `(allow network-outbound (remote ip))`) {
		t.Fatal(profile)
	}
}

func TestLinuxServicePolicyKeepsDocumentedSharedNetwork(t *testing.T) {
	p := Policy{Home: tempDir(t)}
	before, err := p.bwrapArgs("/bin/true")
	if err != nil {
		t.Fatal(err)
	}
	p.LoopbackOnly = true
	after, err := p.bwrapArgs("/bin/true")
	if err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(before, after) {
		t.Fatalf("LoopbackOnly silently changed Linux network: %v vs %v", before, after)
	}
}
