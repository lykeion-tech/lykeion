//go:build unix

package sandbox

import (
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"
)

// Killing a group as its shell forks must not leave the new child running.
func TestKillGroupReachesAChildBeingForked(t *testing.T) {
	groups := map[string]bool{}
	for i := range 60 {
		dir := t.TempDir()
		cmd := exec.Command("/bin/sh", "-c", "echo > started\nfor i in 1 2 3 4 5 6 7 8 9 10 11 12 13 14 15 16 17 18 19 20; do sleep 10 & done\nwait\n")
		cmd.Dir = dir
		cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
		if err := cmd.Start(); err != nil {
			t.Fatal(err)
		}
		waited := make(chan struct{})
		go func() { cmd.Wait(); close(waited) }()
		pgid := cmd.Process.Pid
		t.Cleanup(func() { syscall.Kill(-pgid, syscall.SIGKILL) })
		for deadline := time.Now().Add(10 * time.Second); ; time.Sleep(time.Millisecond) {
			if _, err := os.Stat(filepath.Join(dir, "started")); err == nil {
				break
			}
			if time.Now().After(deadline) {
				t.Fatal("the shell did not start")
			}
		}
		time.Sleep(time.Duration(i%10) * 200 * time.Microsecond)
		if err := KillGroup(pgid); err != nil {
			t.Fatal(err)
		}
		<-waited
		groups[strconv.Itoa(pgid)] = true
	}
	time.Sleep(300 * time.Millisecond)
	out, err := exec.Command("ps", "-A", "-o", "pgid=,stat=,command=").Output()
	if err != nil {
		t.Fatal(err)
	}
	for _, line := range strings.Split(string(out), "\n") {
		// exiting (E) and zombie (Z) processes were reached
		if f := strings.Fields(line); len(f) > 2 && groups[f[0]] && !strings.ContainsAny(f[1], "EZ") {
			t.Errorf("still running: %s", line)
		}
	}
}
