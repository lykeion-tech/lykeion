//go:build darwin

package agent

import (
	"errors"
	"fmt"
	"os"
	"runtime"
	"unsafe"

	"golang.org/x/sys/unix"
)

func processGroupMembers(group int) ([]groupMember, error) {
	pids, err := processGroupPIDs(group)
	if err != nil {
		return nil, err
	}
	return observeDarwinGroup(group, pids, func(pid int) (*unix.KinfoProc, error) {
		return unix.SysctlKinfoProc("kern.proc.pid", pid)
	})
}

// Capture membership under the kernel process-list lock. The combined sysctl
// process listing can silently omit a PID that exits before its state callback.
func processGroupPIDs(group int) ([]int32, error) {
	const listPIDs, processGroupOnly = 1, 2
	bytes, _, errno := unix.Syscall6(unix.SYS_PROC_INFO, listPIDs, processGroupOnly, uintptr(group), 0, 0, 0)
	if errno != 0 {
		return nil, errno
	}
	if bytes == 0 || bytes > 1<<31-1 || bytes%4 != 0 {
		return nil, errors.New("invalid process-list buffer size")
	}
	pids := make([]int32, bytes/4)
	used, _, errno := unix.Syscall6(unix.SYS_PROC_INFO, listPIDs, processGroupOnly, uintptr(group), 0, uintptr(unsafe.Pointer(&pids[0])), bytes)
	runtime.KeepAlive(pids)
	if errno != 0 {
		return nil, errno
	}
	if used > bytes || used%4 != 0 {
		return nil, errors.New("invalid process-list return length")
	}
	if used == bytes {
		return nil, errProcessObservationIncomplete
	}
	return pids[:used/4], nil
}

func observeDarwinGroup(group int, pids []int32, read func(int) (*unix.KinfoProc, error)) ([]groupMember, error) {
	members := make([]groupMember, 0, len(pids))
	for _, pid := range pids {
		process, err := read(int(pid))
		if errors.Is(err, unix.ESRCH) || errors.Is(err, os.ErrNotExist) || errors.Is(err, unix.EIO) {
			return nil, errProcessObservationIncomplete
		}
		if err != nil {
			return nil, err
		}
		if process.Proc.P_pid != pid || int(process.Eproc.Pgid) != group {
			return nil, errProcessObservationIncomplete
		}
		birth := process.Proc.P_starttime
		members = append(members, groupMember{
			owner: processOwner{PID: int(pid), Started: fmt.Sprintf("%d:%d", birth.Sec, birth.Usec)},
			// Darwin's proc states are SSTOP=4 and SZOMB=5.
			stopped: process.Proc.P_stat == 4,
			exited:  process.Proc.P_stat == 5,
		})
	}
	return members, nil
}
