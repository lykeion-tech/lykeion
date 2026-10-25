//go:build darwin || linux

package cli

import (
	"bytes"
	"fmt"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"
)

func TestRuntimeListAfterDaemonRegisters(t *testing.T) {
	srv, m := serve(t)
	configDir := t.TempDir()
	t.Setenv("LYK_CONFIG", filepath.Join(configDir, "config.json"))
	loginPerson(t, srv, m, "ada@lab.org")
	lyk(t, "", "workspace", "create", "Lab", "lab")
	lyk(t, "", "workspace", "switch", "lab")

	binDir := t.TempDir()
	fake := filepath.Join(binDir, "claude")
	if err := os.WriteFile(fake, []byte("#!/bin/sh\nexit 0\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", binDir)
	t.Setenv("HOME", t.TempDir())
	t.Setenv("CODEX_HOME", "")
	t.Setenv("CLAUDE_CONFIG_DIR", t.TempDir())
	scratch := t.TempDir()
	if err := os.Chmod(scratch, 0o700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("LYK_DAEMON_HOME", scratch)
	// Bases already marked ready, so the daemon builds none from the network.
	envs, err := envDir()
	if err != nil {
		t.Fatal(err)
	}
	for _, base := range []string{"python", "r", filepath.Join("julia", "base")} {
		ready := filepath.Join(envs, base, "ready")
		if err := os.MkdirAll(filepath.Dir(ready), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(ready, nil, 0o644); err != nil {
			t.Fatal(err)
		}
	}
	done := make(chan error, 1)
	go func() {
		cmd := Root(strings.NewReader(""), &bytes.Buffer{})
		cmd.SetArgs([]string{"daemon", "run", "--claude-bin", fake, "--for", "2s"})
		done <- cmd.Execute()
	}()
	defer func() {
		select {
		case err := <-done:
			if err != nil {
				t.Errorf("daemon run: %v", err)
			}
		case <-time.After(3 * time.Second):
			t.Error("daemon did not join")
		}
	}()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		var out bytes.Buffer
		cmd := Root(strings.NewReader(""), &out)
		cmd.SetArgs([]string{"runtime", "list"})
		if err := cmd.Execute(); err == nil && strings.Contains(out.String(), "claude") && strings.Contains(out.String(), "online") {
			return
		}
		time.Sleep(100 * time.Millisecond)
	}
	t.Fatal("the daemon's runtime never appeared")
}

func TestTheEnvironmentsFolderAvoidsASpacedStateFolder(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("LYK_CONFIG", filepath.Join(home, "Library", "Application Support", "lyk", "config.json"))
	dir, err := envDir()
	if err != nil {
		t.Fatal(err)
	}
	if want := filepath.Join(home, ".lyk", "envs"); dir != want {
		t.Fatalf("envDir = %q, want %q", dir, want)
	}
}

func TestDaemonAndRuntimeCommandsAreRegistered(t *testing.T) {
	root := Root(strings.NewReader(""), &bytes.Buffer{})
	for _, path := range [][]string{{"daemon", "run"}, {"daemon", "start"}, {"daemon", "stop"}, {"daemon", "status"}, {"daemon", "logs"}, {"runtime", "list"}} {
		cmd, _, err := root.Find(path)
		if err != nil || cmd.Name() != path[len(path)-1] {
			t.Fatalf("missing command %v: %v", path, err)
		}
	}
}

func commandResult(args ...string) (string, error) {
	var out bytes.Buffer
	cmd := Root(strings.NewReader(""), &out)
	cmd.SetArgs(args)
	err := cmd.Execute()
	return out.String(), err
}

func TestDaemonRejectsMalformedAndReusedPID(t *testing.T) {
	t.Setenv("LYK_CONFIG", filepath.Join(t.TempDir(), "config.json"))
	path := filepath.Join(filepath.Dir(os.Getenv("LYK_CONFIG")), "daemon.pid")
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatal(err)
	}
	for _, raw := range []string{`{"pid":0,"started":"x","nonce":"x"}`, `{"pid":-1,"started":"x","nonce":"x"}`, "garbage"} {
		if err := os.WriteFile(path, []byte(raw), 0o600); err != nil {
			t.Fatal(err)
		}
		if _, err := commandResult("daemon", "stop"); err == nil {
			t.Fatalf("malformed PID accepted: %q", raw)
		}
	}
	stale := daemonOwner{PID: os.Getpid(), Started: "not-this-process", Nonce: "stale"}
	if err := writeOwner(path, stale); err != nil {
		t.Fatal(err)
	}
	if _, err := commandResult("daemon", "stop"); err == nil || !strings.Contains(err.Error(), "stale") {
		t.Fatalf("reused PID was signaled: %v", err)
	}
	if got, err := commandResult("daemon", "status"); err != nil || !strings.Contains(got, "not running") {
		t.Fatalf("stale status: %q %v", got, err)
	}
	if owner, err := readOwner(path); err != nil || owner != stale {
		t.Fatalf("stale ownership was removed: %+v %v", owner, err)
	}
}

func TestDaemonStopWaitsForOwnedChild(t *testing.T) {
	t.Setenv("LYK_CONFIG", filepath.Join(t.TempDir(), "config.json"))
	path := filepath.Join(filepath.Dir(os.Getenv("LYK_CONFIG")), "daemon.pid")
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatal(err)
	}
	child := exec.Command("/bin/sh", "-c", "exec sleep 30")
	if err := os.Mkdir(filepath.Join(filepath.Dir(path), "daemon"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := child.Start(); err != nil {
		t.Fatal(err)
	}
	wait := make(chan error, 1)
	go func() { wait <- child.Wait() }()
	t.Cleanup(func() { _ = child.Process.Kill(); <-wait })
	started, err := processStart(child.Process.Pid)
	if err != nil {
		t.Fatal(err)
	}
	owner := daemonOwner{PID: child.Process.Pid, Started: started, Nonce: "owned-test-child"}
	if err := writeOwner(path, owner); err != nil {
		t.Fatal(err)
	}
	if got, err := commandResult("daemon", "status"); err != nil || !strings.Contains(got, fmt.Sprint(child.Process.Pid)) {
		t.Fatalf("owned status: %q %v", got, err)
	}
	if _, err := commandResult("daemon", "stop"); err != nil {
		t.Fatalf("stop owned child: %v", err)
	}
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatalf("ownership remained after stop: %v", err)
	}
}

func TestDaemonOwnerCleanupDoesNotRemoveSuccessor(t *testing.T) {
	path := filepath.Join(t.TempDir(), "daemon.pid")
	old := daemonOwner{PID: 12, Started: "one", Nonce: "old"}
	newer := daemonOwner{PID: 13, Started: "two", Nonce: "new"}
	if err := writeOwner(path, newer); err != nil {
		t.Fatal(err)
	}
	if err := clearOwner(path, old); err != nil {
		t.Fatal(err)
	}
	if got, err := readOwner(path); err != nil || got != newer {
		t.Fatalf("new owner removed: %+v %v", got, err)
	}
}

func TestUnreadyChildShutdownRefusesReusedPID(t *testing.T) {
	child := exec.Command("/bin/sh", "-c", "exec sleep 30")
	if err := child.Start(); err != nil {
		t.Fatal(err)
	}
	wait := make(chan error, 1)
	go func() { wait <- child.Wait() }()
	t.Cleanup(func() { _ = child.Process.Kill() })
	started, err := processStart(child.Process.Pid)
	if err != nil {
		t.Fatal(err)
	}
	if err := stopUnreadyChild(child.Process.Pid, "different-process-birth", wait); err == nil || !strings.Contains(err.Error(), "reused") {
		t.Fatalf("unready reused PID was signaled: %v", err)
	}
	if current, err := processStart(child.Process.Pid); err != nil || current != started {
		t.Fatalf("owned child disappeared: %q %v", current, err)
	}
	if err := stopUnreadyChild(child.Process.Pid, started, wait); err != nil {
		t.Fatalf("stop verified child: %v", err)
	}
}

func TestDaemonLogsLast200LinesAndScannerError(t *testing.T) {
	t.Setenv("LYK_CONFIG", filepath.Join(t.TempDir(), "config.json"))
	path := filepath.Join(filepath.Dir(os.Getenv("LYK_CONFIG")), "daemon.log")
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatal(err)
	}
	var lines strings.Builder
	for i := range 250 {
		fmt.Fprintln(&lines, i)
	}
	if err := os.WriteFile(path, []byte(lines.String()), 0o600); err != nil {
		t.Fatal(err)
	}
	got, err := commandResult("daemon", "logs")
	if err != nil || strings.Contains(got, "\n49\n") || !strings.HasPrefix(got, "50\n") || !strings.HasSuffix(got, "249\n") {
		t.Fatalf("bounded logs: %q %v", got, err)
	}
	if err := os.WriteFile(path, []byte(strings.Repeat("x", 100000)), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := commandResult("daemon", "logs"); err == nil {
		t.Fatal("oversized log line was silently ignored")
	}
}

func TestPrivateScratchRootRejectsSymlinkAndBroadPermissions(t *testing.T) {
	root := t.TempDir()
	if err := os.Chmod(root, 0o700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("LYK_DAEMON_HOME", root)
	if got, err := scratchHome(); err != nil || got != root {
		t.Fatalf("private root: %q %v", got, err)
	}
	if err := os.Chmod(root, 0o755); err != nil {
		t.Fatal(err)
	}
	if _, err := scratchHome(); err == nil {
		t.Fatal("broadly accessible root accepted")
	}
	target := t.TempDir()
	link := filepath.Join(t.TempDir(), "scratch")
	if err := os.Symlink(target, link); err != nil {
		t.Fatal(err)
	}
	t.Setenv("LYK_DAEMON_HOME", link)
	if _, err := scratchHome(); err == nil {
		t.Fatal("symlink scratch root accepted")
	}
}

func TestDaemonRunRejectsMissingExplicitHarness(t *testing.T) {
	t.Setenv("LYK_CONFIG", filepath.Join(t.TempDir(), "config.json"))
	t.Setenv("PATH", t.TempDir())
	root := t.TempDir()
	if err := os.Chmod(root, 0o700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("LYK_DAEMON_HOME", root)
	if err := SaveConfig(os.Getenv("LYK_CONFIG"), Config{Server: "http://unused", Token: "lyk_test"}); err != nil {
		t.Fatal(err)
	}
	if _, err := commandResult("daemon", "run", "--claude-bin", filepath.Join(t.TempDir(), "missing")); err == nil || !strings.Contains(err.Error(), "--claude-bin") {
		t.Fatalf("missing explicit harness accepted: %v", err)
	}
}

func TestBackgroundDaemonHelper(t *testing.T) {
	if os.Getenv("LYK_TEST_DAEMON_HELPER") != "1" {
		return
	}
	config := os.Getenv("LYK_CONFIG")
	path := filepath.Join(filepath.Dir(config), "daemon.pid")
	started, err := processStart(os.Getpid())
	if err != nil {
		os.Exit(3)
	}
	nonce := ""
	for _, arg := range os.Args {
		if strings.HasPrefix(arg, "--nonce=") {
			nonce = strings.TrimPrefix(arg, "--nonce=")
		}
	}
	if nonce == "" {
		os.Exit(5)
	}
	lock, err := os.OpenFile(filepath.Join(filepath.Dir(path), "daemon", "daemon.lock"), os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil || syscall.Flock(int(lock.Fd()), syscall.LOCK_EX|syscall.LOCK_NB) != nil {
		os.Exit(6)
	}
	defer lock.Close()
	if err := writeOwner(path, daemonOwner{PID: os.Getpid(), Started: started, Nonce: nonce}); err != nil {
		os.Exit(4)
	}
	sig := make(chan os.Signal, 1)
	signal.Notify(sig, syscall.SIGTERM)
	<-sig
}

func TestDaemonStartDetectsImmediateExit(t *testing.T) {
	t.Setenv("LYK_CONFIG", filepath.Join(t.TempDir(), "config.json"))
	if err := SaveConfig(os.Getenv("LYK_CONFIG"), Config{Server: "http://unused", Token: "lyk_test"}); err != nil {
		t.Fatal(err)
	}
	old := daemonExecutable
	daemonExecutable = func() (string, error) { return "/usr/bin/false", nil }
	t.Cleanup(func() { daemonExecutable = old })
	if _, err := commandResult("daemon", "start"); err == nil || !strings.Contains(err.Error(), "before readiness") {
		t.Fatalf("immediate child failure was accepted: %v", err)
	}
}

func TestDaemonStartWaitsForOwnedReadinessAndRejectsConcurrentStart(t *testing.T) {
	t.Setenv("LYK_CONFIG", filepath.Join(t.TempDir(), "config.json"))
	t.Setenv("LYK_TEST_DAEMON_HELPER", "1")
	if err := SaveConfig(os.Getenv("LYK_CONFIG"), Config{Server: "http://unused", Token: "lyk_test"}); err != nil {
		t.Fatal(err)
	}
	oldExecutable, oldArgs := daemonExecutable, daemonChildArgs
	daemonExecutable = os.Executable
	daemonChildArgs = func(nonce string) []string {
		return []string{"-test.run=^TestBackgroundDaemonHelper$", "--", "--nonce=" + nonce}
	}
	t.Cleanup(func() { daemonExecutable, daemonChildArgs = oldExecutable, oldArgs })
	t.Cleanup(func() { _, _ = commandResult("daemon", "stop") })
	got, err := commandResult("daemon", "start")
	if err != nil || !strings.Contains(got, "Daemon started") {
		t.Fatalf("start before child readiness: %q %v", got, err)
	}
	if _, err := commandResult("daemon", "start"); err == nil || !strings.Contains(err.Error(), "already running") {
		t.Fatalf("second start: %v", err)
	}
	if _, err := commandResult("daemon", "stop"); err != nil {
		t.Fatalf("stop: %v", err)
	}
}

func TestConcurrentDaemonStartsElectOneOwner(t *testing.T) {
	t.Setenv("LYK_CONFIG", filepath.Join(t.TempDir(), "config.json"))
	t.Setenv("LYK_TEST_DAEMON_HELPER", "1")
	if err := SaveConfig(os.Getenv("LYK_CONFIG"), Config{Server: "http://unused", Token: "lyk_test"}); err != nil {
		t.Fatal(err)
	}
	oldExecutable, oldArgs := daemonExecutable, daemonChildArgs
	daemonExecutable = os.Executable
	daemonChildArgs = func(nonce string) []string {
		return []string{"-test.run=^TestBackgroundDaemonHelper$", "--", "--nonce=" + nonce}
	}
	t.Cleanup(func() { daemonExecutable, daemonChildArgs = oldExecutable, oldArgs })
	t.Cleanup(func() { _, _ = commandResult("daemon", "stop") })
	results := make(chan error, 2)
	for range 2 {
		go func() { _, err := commandResult("daemon", "start"); results <- err }()
	}
	var successes int
	for range 2 {
		if err := <-results; err == nil {
			successes++
		}
	}
	if successes != 1 {
		t.Fatalf("%d concurrent starts succeeded; want exactly one", successes)
	}
	if _, err := commandResult("daemon", "stop"); err != nil {
		t.Fatalf("stop elected owner: %v", err)
	}
}

func TestManagedDaemonLogPrivacyLocationContract(t *testing.T) {
	for _, config := range []string{filepath.Join(t.TempDir(), "config.json"), filepath.Join(t.TempDir(), "custom", "settings.json")} {
		e := &env{path: config}
		if got, want := logFile(e), filepath.Join(filepath.Dir(stateDir(e)), "daemon.log"); got != want {
			t.Fatalf("managed log %s differs from daemon privacy path %s", got, want)
		}
	}
}
