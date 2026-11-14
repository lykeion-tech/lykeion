//go:build linux

package desktopupdate

import "errors"

// Linux storage enforcement remains disabled until real-host verification.
func preallocate(int, int64) error { return errors.New("update storage reservation unavailable") }
