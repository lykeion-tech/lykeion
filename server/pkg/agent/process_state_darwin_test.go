//go:build darwin

package agent

import (
	"errors"
	"golang.org/x/sys/unix"
	"testing"
)

func TestDarwinGroupObservationRejectsVanishedOrChangedCapturedPID(t *testing.T) {
	for _, scenario := range []string{"vanished", "changed group", "changed PID"} {
		t.Run(scenario, func(t *testing.T) {
			members, err := observeDarwinGroup(101, []int32{102}, func(int) (*unix.KinfoProc, error) {
				if scenario == "vanished" {
					return nil, unix.ESRCH
				}
				p := &unix.KinfoProc{}
				p.Proc.P_pid, p.Eproc.Pgid = 102, 101
				if scenario == "changed group" {
					p.Eproc.Pgid = 999
				}
				if scenario == "changed PID" {
					p.Proc.P_pid = 999
				}
				return p, nil
			})
			if !errors.Is(err, errProcessObservationIncomplete) || len(members) != 0 {
				t.Fatalf("incomplete observation accepted: %+v %v", members, err)
			}
		})
	}
}
