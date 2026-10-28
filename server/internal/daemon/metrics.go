package daemon

import (
	"context"
	"fmt"
	"os/exec"
	"strconv"
	"strings"
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
