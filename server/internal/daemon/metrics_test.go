package daemon

import (
	"context"
	"encoding/json"
	"net/http"
	"os"
	"os/exec"
	"sync"
	"syscall"
	"testing"
	"time"

	"github.com/shirou/gopsutil/v4/process"
	"lykeion/server/internal/compute"
	"lykeion/server/internal/coresearch"
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
	tree, e := new(cpuMeter).Sample(t.Context(), []int{cmd.Process.Pid, 2147483647, cmd.Process.Pid})
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

type metricTransport func(*http.Request) (*http.Response, error)

func (f metricTransport) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

func metricSession(t *testing.T, h *chatHost, name string) *kernels.Sessions {
	t.Helper()
	s, e := kernels.Start(t.Context(), func(_ string, _ ...string) (*exec.Cmd, error) {
		cmd := exec.Command("/bin/sh", "-c", `sleep 30 & child=$!; trap 'kill "$child" 2>/dev/null; wait "$child" 2>/dev/null' EXIT; printf '{"ready":true,"bridge_pid":%d,"pids":[%d],"kernel":"`+name+`"}\n' "$$" "$child"; read shutdown`)
		cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
		return cmd, nil
	}, "unused", os.Environ(), h.dir, t.TempDir())
	must(t, e)
	t.Cleanup(func() { s.Close() })
	waitFor(t, func() bool { return len(s.PIDs()) == 1 })
	return s
}

func TestKernelSamplePublicationOrdersStopAndReplacement(t *testing.T) {
	f := newCoreFixture(t)
	h := f.d.chats[f.work.ChatID]
	old, replacement := metricSession(t, h, "old"), metricSession(t, h, "replacement")
	h.sessions = old
	h.publishedSessions.Store(old)
	paused, release := make(chan struct{}), make(chan struct{})
	var releaseOnce sync.Once
	defer releaseOnce.Do(func() { close(release) })
	f.d.api.HTTP = &http.Client{Transport: metricTransport(func(r *http.Request) (*http.Response, error) {
		if r.GetBody == nil {
			return http.DefaultTransport.RoundTrip(r)
		}
		b, e := r.GetBody()
		if e != nil {
			return nil, e
		}
		var body struct {
			Sample *compute.Sample
			Kernel string
		}
		e = json.NewDecoder(b).Decode(&body)
		b.Close()
		if e != nil {
			return nil, e
		}
		if body.Sample != nil && body.Kernel == "old" {
			close(paused)
			select {
			case <-release:
			case <-r.Context().Done():
				return nil, r.Context().Err()
			}
		}
		return http.DefaultTransport.RoundTrip(r)
	})}
	sampled := make(chan struct{})
	go func() { f.d.sampleKernels(t.Context()); close(sampled) }()
	select {
	case <-paused:
	case <-time.After(5 * time.Second):
		t.Fatal("sample never reached transport")
	}
	replaced := make(chan error, 1)
	go func() {
		if e := f.d.controlChat(t.Context(), coresearch.ClaimedControl{WorkContext: f.work, Op: "stop", Seq: 1}); e != nil {
			replaced <- e
			return
		}
		h.mu.Lock()
		h.sessions = replacement
		h.publishedSessions.Store(replacement)
		e := f.d.kernelState(t.Context(), h, "idle", 0)
		h.mu.Unlock()
		replaced <- e
	}()
	overtook := false
	select {
	case e := <-replaced:
		must(t, e)
		overtook = true
	case <-time.After(300 * time.Millisecond):
	}
	releaseOnce.Do(func() { close(release) })
	select {
	case <-sampled:
	case <-time.After(5 * time.Second):
		t.Fatal("sample did not finish")
	}
	if !overtook {
		select {
		case e := <-replaced:
			must(t, e)
		case <-time.After(5 * time.Second):
			t.Fatal("stop/replacement did not finish")
		}
	}
	f.mu.Lock()
	names := append([]string(nil), f.kernelNames...)
	f.mu.Unlock()
	if overtook || names[len(names)-1] != "replacement" {
		t.Fatalf("old telemetry overtook replacement: overtook=%v names=%v", overtook, names)
	}
	// Process snapshots can be temporarily unavailable. Wait for an actual
	// replacement reading rather than require every sampling tick to succeed.
	waitFor(t, func() bool {
		f.d.sampleKernels(t.Context())
		f.mu.Lock()
		defer f.mu.Unlock()
		return len(f.samples) == 2 && f.kernelNames[len(f.kernelNames)-1] == "replacement"
	})
}

func TestKernelSampleStallDoesNotBlockStop(t *testing.T) {
	f := newCoreFixture(t)
	h := f.d.chats[f.work.ChatID]
	s := metricSession(t, h, "old")
	h.sessions = s
	h.publishedSessions.Store(s)
	paused := make(chan struct{})
	f.d.api.HTTP = &http.Client{Transport: metricTransport(func(r *http.Request) (*http.Response, error) {
		if r.GetBody != nil {
			b, e := r.GetBody()
			if e != nil {
				return nil, e
			}
			var body struct{ Sample *compute.Sample }
			e = json.NewDecoder(b).Decode(&body)
			b.Close()
			if e != nil {
				return nil, e
			}
			if body.Sample != nil {
				close(paused)
				<-r.Context().Done()
				return nil, r.Context().Err()
			}
		}
		return http.DefaultTransport.RoundTrip(r)
	})}
	ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
	defer cancel()
	sampled := make(chan struct{})
	go func() { f.d.sampleKernels(ctx); close(sampled) }()
	select {
	case <-paused:
	case <-time.After(5 * time.Second):
		t.Fatal("sample never reached transport")
	}
	stopped := make(chan error, 1)
	go func() {
		stopped <- f.d.controlChat(ctx, coresearch.ClaimedControl{WorkContext: f.work, Op: "stop", Seq: 1})
	}()
	select {
	case e := <-stopped:
		must(t, e)
	case <-time.After(3 * time.Second):
		t.Fatal("stalled telemetry blocked Stop")
	}
	select {
	case <-sampled:
	case <-time.After(time.Second):
		t.Fatal("publication lacked bounded timeout")
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	if len(f.samples) != 0 || len(f.states) == 0 || f.states[len(f.states)-1] != "stopped" {
		t.Fatalf("stale telemetry after Stop: %v %v", f.samples, f.states)
	}
}

// Kernels report their current CPU, not the average over their lifetime: one
// that worked and went idle reads low, one that idled and got busy reads high.
func TestSampleReportsCurrentCPU(t *testing.T) {
	start := func(code string) int {
		cmd := exec.Command("python3", "-c", "import time\ne = time.time() + 1.5\n"+code)
		if err := cmd.Start(); err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { cmd.Process.Kill(); cmd.Wait() })
		return cmd.Process.Pid
	}
	idle := start("while time.time() < e: pass\ntime.sleep(30)")
	busy := start("time.sleep(1.5)\nwhile True: pass")
	time.Sleep(1700 * time.Millisecond)
	var idleM, busyM cpuMeter
	for _, m := range []struct {
		m   *cpuMeter
		pid int
	}{{&idleM, idle}, {&busyM, busy}} {
		if _, e := m.m.Sample(t.Context(), []int{m.pid}); e != nil {
			t.Fatal(e)
		}
	}
	time.Sleep(500 * time.Millisecond)
	s, e := idleM.Sample(t.Context(), []int{idle})
	if e != nil || s.CPU > 20 {
		t.Fatalf("idle kernel reported %.1f%% CPU (%v)", s.CPU, e)
	}
	s, e = busyM.Sample(t.Context(), []int{busy})
	if e != nil || s.CPU < 60 {
		t.Fatalf("busy kernel reported %.1f%% CPU (%v)", s.CPU, e)
	}
}
