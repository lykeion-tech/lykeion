//go:build darwin

package agent

import (
	"fmt"

	"golang.org/x/sys/unix"
)

func processGroupMembers(group int) ([]groupMember, error) {
	processes, err := unix.SysctlKinfoProcSlice("kern.proc.pgrp", group)
	if err != nil {
		return nil, err
	}
	members := make([]groupMember, 0, len(processes))
	for _, process := range processes {
		birth := process.Proc.P_starttime
		members = append(members, groupMember{
			owner: processOwner{PID: int(process.Proc.P_pid), Started: fmt.Sprintf("%d:%d", birth.Sec, birth.Usec)},
			// Darwin's proc states are SSTOP=4 and SZOMB=5.
			stopped: process.Proc.P_stat == 4,
			exited:  process.Proc.P_stat == 5,
		})
	}
	return members, nil
}
