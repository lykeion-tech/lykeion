//go:build linux

package desktopupdate

import "errors"

// Linux durability remains disabled until the platform and filesystem are verified.
func durableSync(int) error { return errors.New("update durability unavailable") }
