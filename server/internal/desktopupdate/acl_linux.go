//go:build linux

package desktopupdate

import (
	"errors"

	"golang.org/x/sys/unix"
)

// An ACL, unreadable attribute, or unsupported ACL inspection fails closed.
func privateACL(fd int) bool {
	for _, name := range []string{"system.posix_acl_access", "system.posix_acl_default"} {
		_, err := unix.Fgetxattr(fd, name, nil)
		if !errors.Is(err, unix.ENODATA) {
			return false
		}
	}
	return true
}
