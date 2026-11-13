package daemon

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"lykeion/server/internal/daemon/envs"
)

func TestSetupRunnerBoundsOutput(t *testing.T) {
	dir := t.TempDir()
	d := runDoor(t, newLykeion(t), dir, envs.Python)
	t.Run("exact limit", func(t *testing.T) {
		ctx, cancel := context.WithTimeout(t.Context(), 15*time.Second)
		defer cancel()
		out, err := d.runSandboxed(ctx, nil, dir, "/usr/bin/head", "-c", fmt.Sprint(32<<20), "/dev/zero")
		if err != nil || len(out) != 32<<20 {
			t.Fatalf("got %d bytes, %v", len(out), err)
		}
	})
	t.Run("ordinary failure", func(t *testing.T) {
		ctx, cancel := context.WithTimeout(t.Context(), 15*time.Second)
		defer cancel()
		out, err := d.runSandboxed(ctx, nil, dir, "/bin/sh", "-c", `printf 'answer'; printf 'diagnostic' >&2; exit 7`)
		if string(out) != "answer" || err == nil || !strings.Contains(err.Error(), "diagnostic") {
			t.Fatalf("got %q, %v", out, err)
		}
	})
	t.Run("network classification", func(t *testing.T) {
		ctx, cancel := context.WithTimeout(t.Context(), 15*time.Second)
		defer cancel()
		script := `printf 'network is unreachable\n' >&2; /usr/bin/head -c 8192 /dev/zero >&2; printf '\nno matching distribution\n' >&2; exit 1`
		out, err := d.runSandboxed(ctx, nil, dir, "/bin/sh", "-c", script)
		if err == nil {
			t.Fatal("setup succeeded")
		}
		var permanent unrebuildable
		if err := judged(err, string(out)); errors.As(err, &permanent) {
			t.Fatalf("network failure was marked unrebuildable: %v", err)
		}
	})
	for _, stream := range []string{"stdout", "stderr"} {
		t.Run(stream, func(t *testing.T) {
			ctx, cancel := context.WithTimeout(t.Context(), 15*time.Second)
			defer cancel()
			if err := os.Remove(filepath.Join(dir, "pgid")); err != nil && !os.IsNotExist(err) {
				t.Fatal(err)
			}
			script := `echo $$ > pgid; /usr/bin/head -c 33554433 /dev/zero`
			if stream == "stderr" {
				script += ` >&2`
			}
			script += `; sleep 2 & wait`
			start := time.Now()
			out, err := d.runSandboxed(ctx, nil, dir, "/bin/sh", "-c", script)
			if err == nil || !strings.Contains(err.Error(), "output exceeds") || len(out) != 0 {
				t.Fatalf("overflow: got %d bytes, %v", len(out), err)
			}
			if time.Since(start) > 12*time.Second || !groupGone(t, dir) {
				t.Fatal("installer process group did not stop after output overflow")
			}
		})
	}
}

func TestSetupRunnerBoundsOutputAfterParentExit(t *testing.T) {
	dir := t.TempDir()
	d := runDoor(t, newLykeion(t), dir, envs.Python)
	stop := filepath.Join(dir, "stop")
	exited := filepath.Join(dir, "exited")
	started := filepath.Join(dir, "started")
	// The child exits on its own even if the test is interrupted.
	t.Cleanup(func() {
		if _, err := os.Stat(started); err != nil {
			return
		}
		if err := os.WriteFile(stop, nil, 0600); err != nil {
			t.Errorf("stop test child: %v", err)
			return
		}
		deadline := time.Now().Add(3 * time.Second)
		for time.Now().Before(deadline) {
			if _, err := os.Stat(exited); err == nil {
				return
			}
			time.Sleep(20 * time.Millisecond)
		}
		t.Error("test child did not exit after stop")
	})
	ctx, cancel := context.WithTimeout(t.Context(), 15*time.Second)
	defer cancel()
	script := `p=$$; echo "$p" > pgid
(
  trap 'echo exited > "$1/exited"' EXIT
  trap '' PIPE
  echo started > "$1/started"
  n=0
  while kill -0 "$p" 2>/dev/null && [ "$n" -lt 300 ]; do n=$((n+1)); sleep .01; done
  sleep .2
  /usr/bin/head -c 33554433 /dev/zero
  echo wrote > "$1/wrote"
  n=0
  while [ ! -e "$1/stop" ] && [ "$n" -lt 200 ]; do n=$((n+1)); sleep .05; done
) &
exit 0`
	out, err := d.runSandboxed(ctx, nil, dir, "/bin/sh", "-c", script, "sh", dir)
	if err == nil || !strings.Contains(err.Error(), "output exceeds") || len(out) != 0 {
		t.Fatalf("overflow after parent exit: got %d bytes, %v", len(out), err)
	}
	if _, err := os.Stat(filepath.Join(dir, "wrote")); err != nil {
		t.Fatalf("child did not finish its bounded write: %v", err)
	}
}
