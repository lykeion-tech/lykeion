//go:build linux

package processidentity

import (
	"errors"
	"os"
	"path/filepath"
	"strconv"
	"strings"
)

func Start(pid int) (string, error) {
	if pid <= 0 {
		return "", errors.New("invalid process ID")
	}
	b, err := os.ReadFile(filepath.Join("/proc", strconv.Itoa(pid), "stat"))
	if err != nil {
		return "", err
	}
	i := strings.LastIndexByte(string(b), ')')
	if i < 0 {
		return "", errors.New("invalid process stat")
	}
	fields := strings.Fields(string(b[i+1:]))
	if len(fields) < 20 {
		return "", errors.New("short process stat")
	}
	return fields[19], nil
}
