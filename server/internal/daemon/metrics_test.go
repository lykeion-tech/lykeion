package daemon

import (
	"context"
	"os"
	"os/exec"
	"syscall"
	"testing"
	"time"

	"github.com/shirou/gopsutil/v4/process"
	"lykeion/server/internal/daemon/kernels"
)

func TestProcessTreeSample(t *testing.T) {
	s, e := sampleProcessTree(context.Background(), os.Getpid())
	if e != nil {
		t.Fatal(e)
	}
	if s.RSSBytes <= 0 || s.Processes < 1 {
		t.Fatalf("%+v", s)
	}
}
func TestProcessTreeAggregation(t *testing.T) {
	s, e := parseProcessTree("10 1 10 100\n11 10 10 200\n12 11 10 300\n13 1 13 900\n", 10)
	if e != nil || s.RSSBytes != 600*1024 || s.Processes != 3 {
		t.Fatalf("%+v %v", s, e)
	}
}

func TestSampleProcessTree(t *testing.T) {
	cmd := exec.Command("/bin/sh", "-c", "sleep 30 & wait")
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	defer func() { syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL); cmd.Wait() }()
	time.Sleep(50 * time.Millisecond)
	tree, e := Sample(t.Context(), []int{cmd.Process.Pid, 2147483647, cmd.Process.Pid})
	if e != nil || tree.RSS == 0 {
		t.Fatalf("%+v %v", tree, e)
	}
	own, e := process.NewProcess(int32(cmd.Process.Pid))
	if e != nil {
		t.Fatal(e)
	}
	mem, e := own.MemoryInfo()
	if e != nil {
		t.Fatal(e)
	}
	if tree.RSS <= mem.RSS {
		t.Fatalf("child missing: %+v <= %d", tree, mem.RSS)
	}
	machine, e := Machine(t.Context(), t.TempDir())
	if e != nil || machine.MemTotal == 0 || machine.Cores < 1 || machine.DiskFree == 0 {
		t.Fatalf("%+v %v", machine, e)
	}
}

func TestKernelMetricsSampleOnlyLiveOwnedSessions(t *testing.T) {
	f := newCoreFixture(t)
	h := f.d.chats[f.work.ChatID]
	s, e := kernels.Start(t.Context(), func(_ string, _ ...string) (*exec.Cmd, error) {
		cmd := exec.Command("/bin/sh", "-c", `sleep 30 & child=$!; trap 'kill "$child" 2>/dev/null; wait "$child" 2>/dev/null' EXIT; printf '{"ready":true,"bridge_pid":%d,"pids":[%d],"kernel":"python3"}\n' "$$" "$child"; read shutdown`)
		cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
		return cmd, nil
	}, "unused", os.Environ(), h.dir, t.TempDir())
	must(t, e)
	defer s.Close()
	h.sessions = s
	h.publishedSessions.Store(s)
	waitFor(t, func() bool { return len(s.PIDs()) == 1 })
	must(t, f.d.kernelState(t.Context(), h, "busy", 0))
	f.d.sampleKernels(t.Context())
	f.mu.Lock()
	n := len(f.samples)
	valid := n == 1 && f.samples[0].RSS > 0 && len(f.kernelNames) > 0 && f.kernelNames[len(f.kernelNames)-1] == "python3"
	f.mu.Unlock()
	if !valid {
		t.Fatal("missing owned live sample or actual kernel name")
	}
	h.mu.Lock()
	must(t, h.closeLocked())
	h.mu.Unlock()
	f.d.sampleKernels(t.Context())
	f.mu.Lock()
	defer f.mu.Unlock()
	if len(f.samples) != n {
		t.Fatal("fabricated stopped sample")
	}
}
