package daemon

import (
	"bufio"
	"context"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"

	"lykeion/server/internal/daemon/sandbox"
	"lykeion/server/pkg/agent"
)

func TestCoresearchConfigurationStaysImmutableAcrossNativeReset(t *testing.T) {
	if runtime.GOOS == "linux" {
		if _, err := exec.LookPath("bwrap"); err != nil {
			t.Skip("bubblewrap unavailable")
		}
	}
	home, err := os.UserHomeDir()
	must(t, err)
	run, outside := t.TempDir(), t.TempDir()
	must(t, os.MkdirAll(filepath.Join(run, ".codex"), 0700))
	must(t, os.WriteFile(filepath.Join(run, ".codex/config.toml"), []byte("hostile"), 0600))
	sentinel := filepath.Join(outside, "keep")
	must(t, os.WriteFile(sentinel, []byte("unrelated"), 0600))
	policy := sandbox.Policy{Home: home, HomeRead: true, Write: []string{run}}
	var placeholders []placeholder
	must(t, protectRunFolder(&policy, run, true, &placeholders))
	ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
	defer cancel()
	script := "echo ready; read proceed; if echo hostile > .codex/config.toml 2>/dev/null; then exit 41; fi; if mv .codex .codex-moved 2>/dev/null; then exit 42; fi; if ln -s " + strconv.Quote(outside) + " .codex 2>/dev/null; then exit 43; fi; echo protected"
	cmd, err := policy.Command(ctx, "/bin/sh", "-c", script)
	must(t, err)
	cmd.Dir = run
	cmd.Env = os.Environ()
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	cmd.Cancel = func() error { return syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL) }
	cmd.WaitDelay = time.Second
	in, err := cmd.StdinPipe()
	must(t, err)
	out, err := cmd.StdoutPipe()
	must(t, err)
	must(t, cmd.Start())
	reader := bufio.NewReader(out)
	line, err := reader.ReadString('\n')
	must(t, err)
	if line != "ready\n" {
		t.Fatal(line)
	}
	session, err := (agent.Codex{Bin: "/usr/bin/true"}).Execute(t.Context(), "unused", agent.ExecOptions{Dir: run, Coresearch: true, ConfigHome: filepath.Join(cliAccountFixture(t), ".codex"), Sandbox: func(ctx context.Context, _ agent.CLIPaths, _ string, _ ...string) (*exec.Cmd, error) {
		return exec.CommandContext(ctx, "/usr/bin/true"), nil
	}})
	must(t, err)
	for range session.Messages {
	}
	<-session.Done
	if _, err := os.Stat(filepath.Join(run, ".codex/config.toml")); !os.IsNotExist(err) {
		t.Fatalf("managed config was not reset: %v", err)
	}
	_, err = io.WriteString(in, "proceed\n")
	must(t, err)
	must(t, in.Close())
	tail, err := io.ReadAll(reader)
	must(t, err)
	must(t, cmd.Wait())
	if !strings.Contains(string(tail), "protected") {
		t.Fatalf("existing writer changed reserved config: %s", tail)
	}
	body, err := os.ReadFile(sentinel)
	must(t, err)
	if string(body) != "unrelated" {
		t.Fatal("outside sentinel changed")
	}
}
