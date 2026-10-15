package agent

import (
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"testing"
	"time"
)

// logAgentTestFailure runs only after a deadline has already failed. It leaves
// the process and session intact so the original assertion still fails.
func logAgentTestFailure(t *testing.T, dir string, session *Session) {
	t.Helper()
	stacks := make([]byte, 1<<20)
	n := runtime.Stack(stacks, true)
	t.Logf("agent failure goroutines:\n%s", stacks[:n])
	select {
	case result := <-session.Done:
		t.Logf("agent had already finished: %+v", result)
	default:
		t.Log("agent Done is still pending")
	}
	var owner struct {
		PID int `json:"pid"`
	}
	for _, name := range []string{".agent-process", ".agent-exit"} {
		b, err := os.ReadFile(filepath.Join(dir, name))
		t.Logf("%s: %s (%v)", name, b, err)
		if name == ".agent-process" {
			_ = json.Unmarshal(b, &owner)
		}
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Logf("scratch listing: %v", err)
	}
	for _, entry := range entries {
		info, err := entry.Info()
		if err == nil {
			t.Logf("scratch %s: mode=%s bytes=%d", entry.Name(), info.Mode(), info.Size())
		}
	}
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	processes, err := exec.CommandContext(ctx, "ps", "-axo", "pid,ppid,pgid,stat,etime,command").Output()
	if err != nil {
		t.Logf("process diagnostics: %v", err)
	} else {
		group := strconv.Itoa(owner.PID)
		for _, line := range strings.Split(string(processes), "\n") {
			fields := strings.Fields(line)
			if len(fields) >= 3 && owner.PID > 1 && (fields[0] == group || fields[2] == group) {
				t.Logf("owned process: %s", line)
			}
		}
	}
	ctxDisk, cancelDisk := context.WithTimeout(context.Background(), time.Second)
	defer cancelDisk()
	disk, err := exec.CommandContext(ctxDisk, "df", "-k", dir).CombinedOutput()
	t.Logf("scratch filesystem: %s (%v)", disk, err)
}
