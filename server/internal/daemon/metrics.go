package daemon

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"runtime"
	"slices"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/shirou/gopsutil/v4/cpu"
	"github.com/shirou/gopsutil/v4/disk"
	"github.com/shirou/gopsutil/v4/mem"
	"github.com/shirou/gopsutil/v4/process"
	"lykeion/server/internal/compute"
)

type ProcessTreeSample struct {
	RSSBytes  int64
	Processes int
}

// sampleProcessTree counts both descendants and surviving members of the root's
// process group, so a reparented worker still contributes to the build cap.
func sampleProcessTree(ctx context.Context, root int) (ProcessTreeSample, error) {
	data, err := exec.CommandContext(ctx, "/bin/ps", "-axo", "pid=,ppid=,pgid=,rss=").Output()
	if err != nil {
		return ProcessTreeSample{}, err
	}
	return parseProcessTree(string(data), root)
}
func parseProcessTree(data string, root int) (ProcessTreeSample, error) {
	type proc struct {
		pid, parent, group int
		rss                int64
	}
	var rows []proc
	for _, line := range strings.Split(data, "\n") {
		f := strings.Fields(line)
		if len(f) == 0 {
			continue
		}
		if len(f) != 4 {
			return ProcessTreeSample{}, fmt.Errorf("invalid process sample")
		}
		n := make([]int64, 4)
		for i := range f {
			v, e := strconv.ParseInt(f[i], 10, 64)
			if e != nil || v < 0 {
				return ProcessTreeSample{}, fmt.Errorf("invalid process sample")
			}
			n[i] = v
		}
		rows = append(rows, proc{int(n[0]), int(n[1]), int(n[2]), n[3] * 1024})
	}
	seen := map[int]bool{root: true}
	for changed := true; changed; {
		changed = false
		for _, p := range rows {
			if !seen[p.pid] && (seen[p.parent] || p.group == root) {
				seen[p.pid] = true
				changed = true
			}
		}
	}
	var s ProcessTreeSample
	for _, p := range rows {
		if seen[p.pid] {
			s.RSSBytes += p.rss
			s.Processes++
		}
	}
	return s, nil
}

// Sample sums each root and its descendants once. Missing processes are
// normal as kernels retire; failure to enumerate the process table is not.
func Sample(ctx context.Context, pids []int) (compute.Sample, error) {
	all, err := process.ProcessesWithContext(ctx)
	if err != nil {
		return compute.Sample{}, err
	}
	parents := map[int32]int32{}
	byID := map[int32]*process.Process{}
	for _, p := range all {
		parent, e := p.PpidWithContext(ctx)
		if e == nil {
			parents[p.Pid] = parent
			byID[p.Pid] = p
		}
	}
	seen := map[int32]bool{}
	for _, pid := range pids {
		if pid > 0 {
			if _, ok := byID[int32(pid)]; ok {
				seen[int32(pid)] = true
			}
		}
	}
	for changed := true; changed; {
		changed = false
		for pid, parent := range parents {
			if !seen[pid] && seen[parent] {
				seen[pid] = true
				changed = true
			}
		}
	}
	var out compute.Sample
	for pid := range seen {
		p := byID[pid]
		m, e := p.MemoryInfoWithContext(ctx)
		if e != nil {
			if processGone(e) {
				continue
			}
			return compute.Sample{}, e
		}
		cpu, e := p.CPUPercentWithContext(ctx)
		if e != nil {
			if processGone(e) {
				continue
			}
			return compute.Sample{}, e
		}
		out.RSS += m.RSS
		out.CPU += cpu
	}
	return out, nil
}

func processGone(err error) bool {
	return errors.Is(err, os.ErrNotExist) || errors.Is(err, syscall.ESRCH) || errors.Is(err, process.ErrorProcessNotRunning)
}

// Machine reports bytes and CPU percent in units of one core (100 percent).
func Machine(ctx context.Context, dataDir string) (compute.MachineSample, error) {
	m, e := mem.VirtualMemoryWithContext(ctx)
	if e != nil {
		return compute.MachineSample{}, e
	}
	disk, e := disk.UsageWithContext(ctx, dataDir)
	if e != nil {
		return compute.MachineSample{}, e
	}
	cpus, e := cpu.PercentWithContext(ctx, 100*time.Millisecond, false)
	if e != nil || len(cpus) != 1 {
		return compute.MachineSample{}, fmt.Errorf("CPU sample unavailable: %v", e)
	}
	cores := runtime.NumCPU()
	return compute.MachineSample{MemUsed: m.Used, MemTotal: m.Total, CPUUsed: cpus[0] * float64(cores), Cores: cores, DiskFree: disk.Free, DiskTotal: disk.Total}, nil
}

func (d *daemon) sampleKernels(ctx context.Context) {
	d.chatsMu.Lock()
	hosts := make([]*chatHost, 0, len(d.chats))
	for _, h := range d.chats {
		hosts = append(hosts, h)
	}
	d.chatsMu.Unlock()
	for _, h := range hosts {
		h.mu.Lock()
		s := h.sessions
		h.mu.Unlock()
		if s == nil {
			continue
		}
		pids := s.PIDs()
		if len(pids) == 0 {
			continue
		}
		sample, e := Sample(ctx, pids)
		// Revalidate ownership after reading the process table: a retired root
		// must not turn a reused numeric PID into another process's sample.
		after := s.PIDs()
		slices.Sort(pids)
		slices.Sort(after)
		if e != nil || !slices.Equal(pids, after) {
			continue
		}
		h.mu.Lock()
		same := h.sessions == s
		h.mu.Unlock()
		if !same {
			continue
		}
		_ = d.coreAPI(h.work.WorkspaceID).Do(ctx, "POST", d.corePath("chats/"+h.work.ChatID.String()+"/kernel"), map[string]any{"sample": sample, "kernel": s.KernelName()}, nil)
	}
}
