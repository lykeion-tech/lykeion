package sandbox

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"testing"
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
