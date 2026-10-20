//go:build unix

package sandbox

import (
	"bytes"
	"context"
	"fmt"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"

	"golang.org/x/sys/unix"
)

// A kernel starts in its own session; it must stay confined all the same.
func TestNewSessionChildIsConfined(t *testing.T) {
	requireSandbox(t)
	p, secret, _ := policyFixture(t)
	setsid := `perl -e 'use POSIX; POSIX::setsid() or die "setsid: $!"; exec @ARGV' `
	refused(t, p, setsid+"cat "+shq(secret))
	refused(t, p, setsid+"sh -c 'echo x > \"$0\"' "+shq(filepath.Join(p.Home, "probe")))
}

// The sandboxed code leads a session of its own, with no controlling
// terminal to push keystrokes into, and its process group is its pid.
func TestCommandStartsANewSession(t *testing.T) {
	requireSandbox(t)
	p, _, _ := policyFixture(t)
	cmd, err := p.Command(context.Background(), "/bin/sh", "-c", "sleep 30")
	if err != nil {
		t.Fatal(err)
	}
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	defer func() { _ = syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL); _ = cmd.Wait() }()
	sid, err := unix.Getsid(cmd.Process.Pid)
	if err != nil {
		t.Fatal(err)
	}
	own, _ := unix.Getsid(0)
	if sid != cmd.Process.Pid || sid == own {
		t.Fatalf("sandboxed sid %d, its pid %d, our sid %d", sid, cmd.Process.Pid, own)
	}
	if pgid, _ := unix.Getpgid(cmd.Process.Pid); pgid != cmd.Process.Pid {
		t.Fatalf("process group %d, want %d", pgid, cmd.Process.Pid)
	}
}

// The door kills a run by signalling its process group; that must reach a
// grandchild started inside the sandbox.
func TestGroupKillReachesGrandchildren(t *testing.T) {
	requireSandbox(t)
	p, _, run := policyFixture(t)
	nap := strconv.Itoa(3000 + os.Getpid()%1000)
	cmd, err := p.Command(context.Background(), "/bin/sh", "-c", "sh -c 'sleep "+nap+" & echo $! > pid; wait' & wait")
	if err != nil {
		t.Fatal(err)
	}
	cmd.Dir = run
	// A surviving grandchild holds the output pipe open; do not wait on it.
	cmd.WaitDelay = time.Second
	var out bytes.Buffer
	cmd.Stdout, cmd.Stderr = &out, &out
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	var pid int
	for deadline := time.Now().Add(10 * time.Second); pid == 0; time.Sleep(20 * time.Millisecond) {
		if time.Now().After(deadline) {
			_ = syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL)
			t.Fatalf("grandchild never started: %q", out.String())
		}
		if b, _ := os.ReadFile(filepath.Join(run, "pid")); len(b) > 0 {
			pid = sleeperPid(t, nap, b)
		}
	}
	if !alive(pid) {
		t.Fatalf("grandchild %d not running before the kill", pid)
	}
	if err := syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL); err != nil {
		t.Fatal(err)
	}
	_ = cmd.Wait()
	for deadline := time.Now().Add(5 * time.Second); alive(pid); time.Sleep(20 * time.Millisecond) {
		if time.Now().After(deadline) {
			_ = syscall.Kill(pid, syscall.SIGKILL)
			t.Fatalf("grandchild %d survived the group kill", pid)
		}
	}
}

// sleeperPid is the grandchild's pid as this process sees it. Inside bwrap's
// pid namespace $! is a different number, so on Linux the sleeper is found by
// its unique argument.
func sleeperPid(t *testing.T, nap string, reported []byte) int {
	t.Helper()
	if runtime.GOOS != "linux" {
		pid, _ := strconv.Atoi(strings.TrimSpace(string(reported)))
		return pid
	}
	ents, _ := os.ReadDir("/proc")
	for _, e := range ents {
		pid, err := strconv.Atoi(e.Name())
		if err != nil {
			continue
		}
		if b, _ := os.ReadFile("/proc/" + e.Name() + "/cmdline"); string(b) == "sleep\x00"+nap+"\x00" {
			return pid
		}
	}
	return 0
}

// alive reports whether pid is a running process; a zombie waiting for a
// reaper counts as dead.
func alive(pid int) bool {
	if runtime.GOOS == "linux" {
		b, err := os.ReadFile("/proc/" + strconv.Itoa(pid) + "/stat")
		if err != nil {
			return false
		}
		s := string(b)
		fields := strings.Fields(s[strings.LastIndexByte(s, ')')+1:])
		return len(fields) > 0 && fields[0] != "Z"
	}
	return syscall.Kill(pid, 0) == nil
}

// serveUnix answers "hello from <path>" on a unix socket at path.
func serveUnix(t *testing.T, path string) {
	t.Helper()
	l, err := net.Listen("unix", path)
	if err != nil {
		t.Fatal(err)
	}
	srv := &http.Server{Handler: http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		fmt.Fprint(w, "hello from "+path)
	})}
	go srv.Serve(l)
	t.Cleanup(func() { srv.Close() })
	// The socket must answer outside the sandbox, or a refusal inside it
	// proves nothing.
	if out, err := exec.Command("curl", "-q", "-sS", "--unix-socket", path, "http://x/").CombinedOutput(); err != nil || !strings.Contains(string(out), "hello") {
		t.Fatalf("control: socket %s unreachable outside the sandbox: %v %s", path, err, out)
	}
}

// Sockets are doors out of any file sandbox (an ssh agent, a docker
// daemon): only those inside a write folder are reachable.
func TestUnixSocketsOutsideWriteFoldersAreUnreachable(t *testing.T) {
	requireSandbox(t)
	p, _, _ := fixtureIn(t, shortDir(t))
	run := mkdir(t, filepath.Join(p.Home, "r"))
	p.Write = []string{run}
	inHome := filepath.Join(p.Home, "agent.sock")
	inTmp := filepath.Join(shortDir(t), "d.sock")
	inRun := filepath.Join(run, "k.sock")
	for _, s := range []string{inHome, inTmp, inRun} {
		serveUnix(t, s)
	}
	for _, s := range []string{inHome, inTmp} {
		out, err := sandboxed(t, p, "curl -q -sS --unix-socket "+shq(s)+" http://x/")
		if err == nil || strings.Contains(out, "hello") {
			t.Errorf("socket %s reachable from the sandbox: err=%v out=%q", s, err, out)
		}
	}
	if out, err := sandboxed(t, p, "curl -q -sS --unix-socket "+shq(inRun)+" http://x/"); err != nil || !strings.Contains(out, "hello") {
		t.Fatalf("socket in the write folder: err=%v out=%q", err, out)
	}
}
