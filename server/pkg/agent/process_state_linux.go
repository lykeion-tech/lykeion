//go:build linux

package agent

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
)

func processGroupMembers(group int) ([]groupMember, error) {
	return observeLinuxGroup(group, "/proc")
}

func observeLinuxGroup(group int, root string) ([]groupMember, error) {
	entries, err := os.ReadDir(root)
	if err != nil {
		return nil, err
	}
	var members []groupMember
	for _, entry := range entries {
		pid, err := strconv.Atoi(entry.Name())
		if err != nil {
			continue
		}
		b, err := os.ReadFile(filepath.Join(root, entry.Name(), "stat"))
		if errors.Is(err, os.ErrNotExist) || errors.Is(err, syscall.ESRCH) {
			return nil, errProcessObservationIncomplete
		}
		if err != nil {
			return nil, err
		}
		end := strings.LastIndexByte(string(b), ')')
		if end < 0 {
			return nil, fmt.Errorf("invalid process stat for %d", pid)
		}
		fields := strings.Fields(string(b[end+1:]))
		if len(fields) < 20 {
			return nil, fmt.Errorf("short process stat for %d", pid)
		}
		pgid, err := strconv.Atoi(fields[2])
		if err != nil {
			return nil, err
		}
		if pgid != group {
			continue
		}
		members = append(members, groupMember{
			owner:   processOwner{PID: pid, Started: fields[19]},
			stopped: fields[0] == "T" || fields[0] == "t",
			exited:  fields[0] == "Z" || fields[0] == "X",
		})
	}
	return members, nil
}
