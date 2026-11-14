//go:build !linux && !darwin

package envs

import "errors"

func readBaseLock(string) ([]byte, error) {
	return nil, errors.New("bounded shared base lock reading is unavailable on this platform")
}
