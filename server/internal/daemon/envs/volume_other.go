//go:build !linux && !darwin

package envs

import (
	"errors"
	"runtime"
)

func platformVolume() (volume, error) {
	return nil, errors.New("environments cannot be installed with a size limit on " + runtime.GOOS)
}
