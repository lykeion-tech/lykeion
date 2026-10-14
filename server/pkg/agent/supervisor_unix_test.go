//go:build unix

package agent

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"

	"lykeion/server/internal/processidentity"
)

func TestSupervisorKillsLingeringDescendantAfterSuccess(t *testing.T) {
	escaped := filepath.Join(t.TempDir(), "escaped")
	bin := script(t, `(sleep 0.5; echo leaked > '`+escaped+`') &
echo '{"type":"result","is_error":false}'`)
	sess, err := (Claude{Bin: bin}).Execute(t.Context(), "p", ExecOptions{Dir: t.TempDir()})
	if err != nil {
		t.Fatal(err)
	}
	_, res := collect(t, sess)
	if res.Err != nil {
		t.Fatal(res.Err)
	}
	time.Sleep(600 * time.Millisecond)
	if _, err := os.Stat(escaped); !os.IsNotExist(err) {
		t.Fatal("descendant survived completed CLI")
	}
}
func TestSupervisorStatusCannotOverrideProtocolOrExitFailure(t *testing.T) {
	for _, body := range []string{`echo '{"type":"result","is_error":false}';exit 7`, `exit 0`, `echo '{"type":"result","is_error":true,"result":"provider refused"}'`} {
		sess, err := (Claude{Bin: script(t, body)}).Execute(t.Context(), "p", ExecOptions{Dir: t.TempDir()})
		if err != nil {
			t.Fatal(err)
		}
		_, res := collect(t, sess)
		if res.Err == nil {
			t.Fatalf("status accepted failure: %s", body)
		}
	}
}
func TestSupervisorMissingAndStaleExitMarkersFailClosed(t *testing.T) {
	for _, body := range []string{`kill -KILL "$PPID"; exit 0`, `printf 'old-nonce 0\n' > "$PWD/.agent-exit";kill -KILL "$PPID";exit 0`} {
		dir := t.TempDir()
		sess, err := (Claude{Bin: script(t, body)}).Execute(t.Context(), "p", ExecOptions{Dir: dir})
		if err != nil {
			t.Fatal(err)
		}
		_, res := collect(t, sess)
		if res.Err == nil {
			t.Fatal("missing/stale marker accepted")
		}
	}
}
func TestRecoveryRefusesReusedProcessIdentity(t *testing.T) {
	child := exec.Command("sleep", "30")
	child.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	if err := child.Start(); err != nil {
		t.Fatal(err)
	}
	defer func() { child.Process.Kill(); child.Wait() }()
	birth, err := processidentity.Start(child.Process.Pid)
	if err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	b, _ := json.Marshal(processOwner{PID: child.Process.Pid, Started: birth + "-stale", Nonce: "owned-attempt"})
	if err := os.WriteFile(filepath.Join(dir, ".agent-process"), b, 0600); err != nil {
		t.Fatal(err)
	}
	if err := RecoverScratch(dir); err != nil {
		t.Fatal(err)
	}
	if err := child.Process.Signal(syscall.Signal(0)); err != nil {
		t.Fatalf("unrelated reused PID signalled: %v", err)
	}
}
func TestLaunchGatePreventsUnjournaledCLI(t *testing.T) {
	dir := t.TempDir()
	launched := filepath.Join(dir, "launched")
	original := exec.Command("/bin/sh", "-c", "echo launched > '"+launched+"'")
	original.Dir = dir
	cmd, read, write, _, err := supervised(context.Background(), original)
	if err != nil {
		t.Fatal(err)
	}
	defer read.Close()
	if err := configureProcess(cmd); err != nil {
		t.Fatal(err)
	}
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	write.Close()
	err = cmd.Wait()
	var exit *exec.ExitError
	if !errors.As(err, &exit) || exit.ExitCode() != 125 {
		t.Fatalf("gate EOF status: %v", err)
	}
	if _, err := os.Stat(launched); !os.IsNotExist(err) {
		t.Fatal("CLI ran before ownership gate")
	}
}
func TestRetentionLimitPreservesCurrentRecord(t *testing.T) {
	dir := t.TempDir()
	r := &messageRetention{dir: dir}
	defer r.close()
	if err := r.put(Message{Type: "log", Content: []byte(`"first"`)}); err != nil {
		t.Fatal(err)
	}
	r.size = retainedMessageBytes
	err := r.put(Message{Type: "log", Content: []byte(`"overflow evidence"`)})
	if err == nil {
		t.Fatal("missing explicit cap failure")
	}
	b, err := os.ReadFile(r.paths[1])
	if err != nil || !strings.Contains(string(b), "overflow evidence") {
		t.Fatalf("overflow record lost: %s %v", b, err)
	}
}

func TestRecoveryRefusesLiveProcessWithoutOwnedGroup(t *testing.T) {
	child := exec.Command("sleep", "30")
	if err := child.Start(); err != nil {
		t.Fatal(err)
	}
	defer func() { child.Process.Kill(); child.Wait() }()
	group, err := syscall.Getpgid(child.Process.Pid)
	if err != nil {
		t.Fatal(err)
	}
	if group == child.Process.Pid {
		t.Fatal("fixture unexpectedly owns a group")
	}
	birth, err := processidentity.Start(child.Process.Pid)
	if err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	b, _ := json.Marshal(processOwner{PID: child.Process.Pid, Started: birth, Nonce: "owned-attempt"})
	if err := os.WriteFile(filepath.Join(dir, ".agent-process"), b, 0600); err != nil {
		t.Fatal(err)
	}
	if err := RecoverScratch(dir); err == nil {
		t.Fatal("unowned group accepted")
	}
	if err := child.Process.Signal(syscall.Signal(0)); err != nil {
		t.Fatal("unrelated child was signalled", err)
	}
}

func TestSupervisorPreservesExecutableResolution(t *testing.T) {
	t.Run("relative to command directory", func(t *testing.T) {
		dir := t.TempDir()
		if err := os.WriteFile(filepath.Join(dir, "harness"), []byte("#!/bin/sh\necho '{\"type\":\"result\",\"is_error\":false,\"session_id\":\"relative\"}'\n"), 0700); err != nil {
			t.Fatal(err)
		}
		sess, err := (Claude{Bin: "./harness"}).Execute(t.Context(), "p", ExecOptions{Dir: dir})
		if err != nil {
			t.Fatal(err)
		}
		_, result := collect(t, sess)
		if result.Err != nil || result.SessionID != "relative" {
			t.Fatalf("relative executable: %+v", result)
		}
	})
	t.Run("parent PATH remains authoritative", func(t *testing.T) {
		parent, child := t.TempDir(), t.TempDir()
		for dir, session := range map[string]string{parent: "parent", child: "child"} {
			body := "#!/bin/sh\necho '{\"type\":\"result\",\"is_error\":false,\"session_id\":\"" + session + "\"}'\n"
			if err := os.WriteFile(filepath.Join(dir, "harness"), []byte(body), 0700); err != nil {
				t.Fatal(err)
			}
		}
		t.Setenv("PATH", parent)
		sess, err := (Claude{Bin: "harness"}).Execute(t.Context(), "p", ExecOptions{Dir: t.TempDir(), Env: []string{"PATH=" + child}})
		if err != nil {
			t.Fatal(err)
		}
		_, result := collect(t, sess)
		if result.Err != nil || result.SessionID != "parent" {
			t.Fatalf("PATH resolution: %+v", result)
		}
	})
}
