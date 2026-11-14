//go:build unix && !darwin && !linux

package agent

import "errors"

func processGroupMembers(int) ([]groupMember, error) {
	return nil, errors.New("process group observation requires macOS or Linux")
}
