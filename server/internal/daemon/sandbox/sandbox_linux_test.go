package sandbox

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
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
