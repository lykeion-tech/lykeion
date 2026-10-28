package kernels

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"time"

	"lykeion/server/internal/processidentity"
)

type pidRecord struct {
	pid, parent, inner int
	namespace, start   string
}
type pidState struct {
	kernel string
	bridge int
	pids   []int
}

// resolvePIDs translates namespace-local IDs only inside this live bridge's
// descendant tree. Numeric equality alone is never a host process identity.
func resolvePIDs(rows []pidRecord, root int, start string, bridgeInner int, inner []int) ([]int, int) {
	if start == "" || bridgeInner <= 0 {
		return nil, 0
	}
	live := false
	for _, p := range rows {
		if p.pid == root && p.start == start {
			live = true
		}
	}
	if !live {
		return nil, 0
	}
	descendants := func(root int) map[int]bool {
		seen := map[int]bool{root: true}
		for changed := true; changed; {
			changed = false
			for _, p := range rows {
				if !seen[p.pid] && seen[p.parent] {
					seen[p.pid] = true
					changed = true
				}
			}
		}
		return seen
	}
	tree := descendants(root)
	var bridge pidRecord
	for _, p := range rows {
		if tree[p.pid] && p.inner == bridgeInner {
			if bridge.pid != 0 {
				return nil, 0
			}
			bridge = p
		}
	}
	if bridge.pid == 0 {
		return nil, 0
	}
	tree = descendants(bridge.pid)
	wanted := map[int]bool{}
	for _, id := range inner {
		if id > 0 {
			wanted[id] = true
		}
	}
	var out []int
	for _, p := range rows {
		if p.pid != bridge.pid && tree[p.pid] && p.namespace == bridge.namespace && wanted[p.inner] {
			out = append(out, p.pid)
		}
	}
	return out, bridge.pid
}
func processSnapshot() ([]pidRecord, error) {
	if runtime.GOOS == "linux" {
		entries, e := os.ReadDir("/proc")
		if e != nil {
			return nil, e
		}
		var out []pidRecord
		for _, entry := range entries {
			pid, e := strconv.Atoi(entry.Name())
			if e != nil {
				continue
			}
			dir := filepath.Join("/proc", entry.Name())
			stat, e := os.ReadFile(filepath.Join(dir, "stat"))
			if e != nil {
				continue
			}
			i := strings.LastIndexByte(string(stat), ')')
			if i < 0 {
				continue
			}
			f := strings.Fields(string(stat)[i+1:])
			if len(f) < 20 {
				continue
			}
			parent, e := strconv.Atoi(f[1])
			if e != nil {
				continue
			}
			ns, e := os.Readlink(filepath.Join(dir, "ns/pid"))
			if e != nil {
				continue
			}
			status, e := os.ReadFile(filepath.Join(dir, "status"))
			if e != nil {
				continue
			}
			inner := 0
			own := false
			for _, line := range strings.Split(string(status), "\n") {
				v := strings.Fields(line)
				if len(v) > 1 && v[0] == "NSpid:" {
					inner, _ = strconv.Atoi(v[len(v)-1])
				}
				if len(v) > 1 && v[0] == "Uid:" {
					uid, _ := strconv.Atoi(v[1])
					own = uid == os.Getuid()
				}
			}
			if own && inner > 0 {
				out = append(out, pidRecord{pid: pid, parent: parent, inner: inner, namespace: ns, start: f[19]})
			}
		}
		return out, nil
	}
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	b, e := exec.CommandContext(ctx, "/bin/ps", "-axo", "pid=,ppid=,lstart=").Output()
	if e != nil {
		return nil, e
	}
	var out []pidRecord
	for _, line := range strings.Split(string(b), "\n") {
		f := strings.Fields(line)
		if len(f) != 7 {
			continue
		}
		pid, e := strconv.Atoi(f[0])
		if e != nil {
			continue
		}
		parent, e := strconv.Atoi(f[1])
		if e != nil {
			continue
		}
		out = append(out, pidRecord{pid: pid, parent: parent, inner: pid, namespace: "host", start: strings.Join(f[2:], " ")})
	}
	return out, nil
}
func processStart(pid int) string { start, _ := processidentity.Start(pid); return start }
func (b *bridge) hostPIDs() ([]int, int) {
	select {
	case <-b.done:
		return nil, 0
	default:
	}
	state := b.pidState.Load()
	if state == nil {
		return nil, 0
	}
	rows, e := processSnapshot()
	if e != nil {
		return nil, 0
	}
	for i := range rows {
		if rows[i].pid == b.cmd.Process.Pid {
			rows[i].start = processStart(rows[i].pid)
		}
	}
	return resolvePIDs(rows, b.cmd.Process.Pid, b.start, state.bridge, state.pids)
}

// PIDs returns verified host kernel process IDs, including while a cell holds
// the serial request lock. The bridge publishes its PID set after each change.
func (s *Sessions) PIDs() []int {
	b := s.current.Load()
	if b == nil || s.closing.Load() {
		return nil
	}
	pids, _ := b.hostPIDs()
	return pids
}
func (b *bridge) signal(sig os.Signal) error {
	_, pid := b.hostPIDs()
	if pid == 0 {
		// Legacy adapters without PID notifications share the launcher's namespace.
		if runtime.GOOS == "linux" {
			return fmt.Errorf("bridge process identity unavailable")
		}
		return b.cmd.Process.Signal(sig)
	}
	p, e := os.FindProcess(pid)
	if e != nil {
		return e
	}
	defer p.Release()
	return p.Signal(sig)
}

// KernelName is the bridge's actual resolved active kernel, never a queued
// request's label. A dead or closed bridge publishes no active name.
func (s *Sessions) KernelName() string {
	b := s.current.Load()
	if b == nil || s.closing.Load() {
		return ""
	}
	pids, _ := b.hostPIDs()
	if len(pids) == 0 {
		return ""
	}
	state := b.pidState.Load()
	if state == nil {
		return ""
	}
	return state.kernel
}
