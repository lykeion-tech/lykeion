//go:build unix

package testutil

import (
	"fmt"
	"os"
	"runtime"
	"strconv"
	"strings"
	"syscall"
)

// OwnedFixtureHostPID resolves a fixture's namespace PID only among processes
// in its known supervisor group. A namespace PID is never a host signal target.
func OwnedFixtureHostPID(group, reported int) (int, error) {
	if group <= 1 || reported <= 0 {
		return 0, fmt.Errorf("invalid owned fixture identity")
	}
	if runtime.GOOS != "linux" {
		actual, err := syscall.Getpgid(reported)
		if err != nil {
			return 0, err
		}
		if actual != group {
			return 0, fmt.Errorf("fixture left supervisor group")
		}
		return reported, nil
	}
	entries, err := os.ReadDir("/proc")
	if err != nil {
		return 0, err
	}
	for _, entry := range entries {
		pid, err := strconv.Atoi(entry.Name())
		if err != nil || pid <= 1 {
			continue
		}
		actual, err := syscall.Getpgid(pid)
		if err != nil || actual != group {
			continue
		}
		data, err := os.ReadFile("/proc/" + entry.Name() + "/status")
		if err != nil {
			continue
		}
		for _, line := range strings.Split(string(data), "\n") {
			if !strings.HasPrefix(line, "NSpid:") {
				continue
			}
			fields := strings.Fields(line)
			if len(fields) < 2 {
				continue
			}
			host, _ := strconv.Atoi(fields[1])
			inner, _ := strconv.Atoi(fields[len(fields)-1])
			if host == pid && inner == reported {
				return pid, nil
			}
		}
	}
	return 0, fmt.Errorf("fixture is not observable in its supervisor group")
}
