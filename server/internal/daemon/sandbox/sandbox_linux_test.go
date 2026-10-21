package sandbox

import (
	"bufio"
	"context"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"testing"
	"time"
)

// /proc/<pid>/root of a process outside the sandbox is the whole host file
// system; the sandbox must not see such a process at all.
func TestProcRootOfAnOutsideProcessIsRefused(t *testing.T) {
	requireSandbox(t)
	p, secret, _ := policyFixture(t)
	if b, err := os.ReadFile("/proc/" + strconv.Itoa(os.Getpid()) + "/root" + secret); err != nil || string(b) != "PRIVATE KEY" {
		t.Fatalf("control: secret unreadable through our own /proc root: %v", err)
	}
	refused(t, p, "cat "+shq("/proc/"+strconv.Itoa(os.Getpid())+"/root"+secret))
}

// Sockets under /run (dbus, docker, a user's agents) are out of reach.
func TestRunSocketsAreUnreachable(t *testing.T) {
	requireSandbox(t)
	dir, err := os.MkdirTemp("/run", "lyk")
	if err != nil {
		t.Skip("cannot create a socket under /run: ", err)
	}
	t.Cleanup(func() { os.RemoveAll(dir) })
	sock := filepath.Join(dir, "d.sock")
	serveUnix(t, sock)
	p, _, _ := policyFixture(t)
	if out, err := sandboxed(t, p, "curl -q -sS --unix-socket "+shq(sock)+" http://x/"); err == nil || strings.Contains(out, "hello") {
		t.Fatalf("socket under /run reachable: err=%v out=%q", err, out)
	}
}

// Root in the sandbox keeps no capabilities, so it cannot unmount the empty
// home and uncover the real one.
func TestNoCapabilitiesInside(t *testing.T) {
	requireSandbox(t)
	p, secret, _ := policyFixture(t)
	if out, err := sandboxed(t, p, "grep CapEff /proc/self/status"); err != nil || !strings.Contains(out, "0000000000000000") {
		t.Fatalf("capabilities inside: err=%v out=%q", err, out)
	}
	out, err := sandboxed(t, p, "umount "+shq(p.Home)+" ; cat "+shq(secret))
	if err == nil || strings.Contains(out, "PRIVATE KEY") {
		t.Fatalf("home unmounted: err=%v out=%q", err, out)
	}
}

// showsThePersonsHome is the sandbox without the empty mount over the
// policy's home folder.
func showsThePersonsHome(p Policy, ctx context.Context, name string, args ...string) (*exec.Cmd, error) {
	argv, err := p.bwrapArgs(name, args...)
	if err != nil {
		return nil, err
	}
	r, err := p.resolve()
	if err != nil {
		return nil, err
	}
	var kept []string
	for i := 0; i < len(argv); i++ {
		if (argv[i] == "--tmpfs" || argv[i] == "--remount-ro") && i+1 < len(argv) && argv[i+1] == r.home {
			i++
			continue
		}
		kept = append(kept, argv[i])
	}
	bwrap, err := exec.LookPath("bwrap")
	if err != nil {
		return nil, err
	}
	return exec.CommandContext(ctx, bwrap, kept...), nil
}

func TestHomeSocketsAreUnreachable(t *testing.T) {
	requireSandbox(t)
	home := shortDir(t)
	write := mkdir(t, filepath.Join(home, "write"))
	p := Policy{Home: home, HomeRead: true, Write: []string{write}}
	sockets := []string{
		filepath.Join(mkdir(t, filepath.Join(home, ".docker", "run")), "docker.sock"),
		filepath.Join(mkdir(t, filepath.Join(home, "a", "b")), "agent.sock"),
		filepath.Join(write, "allowed.sock"),
	}
	for _, path := range sockets {
		serveUnix(t, path)
	}
	for i, path := range sockets {
		t.Run(filepath.Base(path), func(t *testing.T) {
			script := "python3 -c " + shq("import socket; s=socket.socket(socket.AF_UNIX); s.connect("+strconv.Quote(path)+")")
			out, err := sandboxed(t, p, script)
			if i == 2 {
				if err != nil {
					t.Fatalf("writable socket refused: %v %s", err, out)
				}
			} else if err == nil {
				t.Fatalf("home socket reachable: %s", path)
			}
		})
	}
}

func TestHomeSocketsWalkSkipsFoldersAndLinks(t *testing.T) {
	home := shortDir(t)
	hidden := mkdir(t, filepath.Join(home, "hidden"))
	write := mkdir(t, filepath.Join(home, "write"))
	unreadable := mkdir(t, filepath.Join(home, "unreadable"))
	outside := shortDir(t)
	visible := filepath.Join(home, "visible.sock")
	for _, path := range []string{visible, filepath.Join(hidden, "hidden.sock"), filepath.Join(write, "write.sock"), filepath.Join(unreadable, "unreadable.sock"), filepath.Join(outside, "outside.sock")} {
		serveUnix(t, path)
	}
	if err := os.Symlink(outside, filepath.Join(home, "link")); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(unreadable, 0); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.Chmod(unreadable, 0700) })
	got, err := homeSockets(home, []string{hidden, write})
	if err != nil {
		t.Fatal(err)
	}
	want := []string{visible}
	if os.Geteuid() == 0 {
		want = append(want, filepath.Join(unreadable, "unreadable.sock"))
	}
	slices.Sort(got)
	slices.Sort(want)
	if !slices.Equal(got, want) {
		t.Fatalf("sockets = %v, want %v", got, want)
	}
}

func TestRestrictedRootsStillMaskExposedReadOnlySockets(t *testing.T) {
	requireSandbox(t)
	home := shortDir(t)
	root := mkdir(t, filepath.Join(home, "shared"))
	read := mkdir(t, filepath.Join(root, "read"))
	write := mkdir(t, filepath.Join(root, "write"))
	socket := filepath.Join(read, "service.sock")
	serveUnix(t, socket)
	p := Policy{Home: home, HomeRead: true, RestrictedRoots: []string{root}, Read: []string{read}, Write: []string{write}}
	script := "python3 -c " + shq("import socket; s=socket.socket(socket.AF_UNIX); s.connect("+strconv.Quote(socket)+")")
	if out, err := sandboxed(t, p, script); err == nil {
		t.Fatalf("socket in exposed readonly descendant reachable: %s", out)
	}
}

func TestLateReadOnlyRequiresPublicationAtLaunch(t *testing.T) {
	requireSandbox(t)
	home := shortDir(t)
	write := mkdir(t, filepath.Join(home, "write"))
	marker := filepath.Join(write, "marker")
	p := Policy{Home: home, Write: []string{write}, LateReadOnly: []string{marker}}
	cmd, err := p.Command(t.Context(), "/bin/sh", "-c", "true")
	if err != nil {
		t.Fatal(err)
	}
	if err := cmd.Run(); err == nil {
		t.Fatal("missing late marker did not fail launch")
	}
	ordinary := Policy{Home: home, Write: []string{write}, Blocked: []string{marker}}
	if _, err := ordinary.Command(t.Context(), "true"); err == nil {
		t.Fatal("ordinary missing Blocked contract weakened")
	}
	cmd, err = p.Command(t.Context(), "/bin/sh", "-c", "echo forged > "+shq(marker))
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(marker, []byte("published"), 0600); err != nil {
		t.Fatal(err)
	}
	if out, err := cmd.CombinedOutput(); err == nil {
		t.Fatalf("late marker was writable: %s", out)
	}
	b, err := os.ReadFile(marker)
	if err != nil || string(b) != "published" {
		t.Fatalf("late marker changed: %q %v", b, err)
	}
}

func TestHiddenProjectionPreservesApprovedHomeView(t *testing.T) {
	requireSandbox(t)
	home := tempDir(t)
	ordinary := filepath.Join(home, "ordinary")
	replaced := filepath.Join(home, "replaced")
	nested := mkdir(t, filepath.Join(home, "existing"))
	config := mkdir(t, filepath.Join(home, ".config"))
	for path, value := range map[string]string{ordinary: "before\n", replaced: "old-entry\n", filepath.Join(config, "ordinary"): "config-visible\n"} {
		if err := os.WriteFile(path, []byte(value), 0600); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.Symlink("existing", filepath.Join(home, "ordinary-link")); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(".netrc", filepath.Join(home, "secret-link")); err != nil {
		t.Fatal(err)
	}
	p := Policy{Home: home, HomeRead: true, Hidden: []string{filepath.Join(home, ".netrc"), filepath.Join(config, "gh")}, Read: []string{config}}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	script := `echo READY; read go; cat "$1/ordinary" "$1/replaced" "$1/ordinary-link/new-child" "$1/.config/ordinary"; for p in .netrc secret-link .config/gh/key new-root .config/new-sibling; do if cat "$1/$p" 2>/dev/null | grep inert; then exit 1; fi; done`
	cmd, err := p.Command(ctx, "sh", "-c", script, "sh", home)
	if err != nil {
		t.Fatal(err)
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		t.Fatal(err)
	}
	stdin, err := cmd.StdinPipe()
	if err != nil {
		t.Fatal(err)
	}
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { cancel(); _ = cmd.Wait() })
	reader := bufio.NewReader(stdout)
	if line, err := reader.ReadString('\n'); err != nil || line != "READY\n" {
		t.Fatalf("start: %q %v", line, err)
	}
	if err := os.MkdirAll(filepath.Join(config, "gh"), 0700); err != nil {
		t.Fatal(err)
	}
	for path, value := range map[string]string{ordinary: "in-place-visible\n", filepath.Join(home, "replacement"): "replacement-new\n", filepath.Join(nested, "new-child"): "child-visible\n", filepath.Join(home, ".netrc"): "inert-secret\n", filepath.Join(config, "gh/key"): "inert-secret\n", filepath.Join(home, "new-root"): "inert-new-root\n", filepath.Join(config, "new-sibling"): "inert-new-sibling\n"} {
		if err := os.WriteFile(path, []byte(value), 0600); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.Rename(filepath.Join(home, "replacement"), replaced); err != nil {
		t.Fatal(err)
	}
	if _, err := io.WriteString(stdin, "go\n"); err != nil {
		t.Fatal(err)
	}
	_ = stdin.Close()
	out, err := io.ReadAll(reader)
	if err != nil {
		t.Fatal(err)
	}
	if err := cmd.Wait(); err != nil {
		t.Fatalf("child: %v %q", err, out)
	}
	if string(out) != "in-place-visible\nold-entry\nchild-visible\nconfig-visible\n" {
		t.Fatalf("projected view: %q", out)
	}
	if b, err := os.ReadFile(replaced); err != nil || string(b) != "replacement-new\n" {
		t.Fatalf("host replacement changed: %q %v", b, err)
	}
}

func TestAbsentHiddenInMaskedTempDoesNotExposeSiblings(t *testing.T) {
	requireSandbox(t)
	home, outside := tempDir(t), shortDir(t)
	ordinary := filepath.Join(outside, "ordinary")
	if err := os.WriteFile(ordinary, []byte("inert-outside\n"), 0600); err != nil {
		t.Fatal(err)
	}
	p := Policy{Home: home, HomeRead: true, Hidden: []string{filepath.Join(outside, "absent-secret")}}
	if out, err := sandboxed(t, p, "cat "+shq(ordinary)); err == nil || strings.Contains(out, "inert-outside") {
		t.Fatalf("hidden path reopened a masked temp sibling: %v %q", err, out)
	}
	p.Read = []string{outside}
	if out, err := sandboxed(t, p, "cat "+shq(ordinary)); err != nil || out != "inert-outside\n" {
		t.Fatalf("explicit read grant lost: %v %q", err, out)
	}
}
