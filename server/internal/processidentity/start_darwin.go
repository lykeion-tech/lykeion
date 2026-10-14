//go:build darwin

package processidentity

import (
	"errors"
	"fmt"

	"golang.org/x/sys/unix"
)

func Start(pid int) (string, error) {
	if pid <= 0 {
		return "", errors.New("invalid process ID")
	}
	p, err := unix.SysctlKinfoProc("kern.proc.pid", pid)
	if err != nil {
		return "", err
	}
	if int(p.Proc.P_pid) != pid {
		return "", errors.New("process ID changed")
	}
	t := p.Proc.P_starttime
	return fmt.Sprintf("%d:%d", t.Sec, t.Usec), nil
}
