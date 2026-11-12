//go:build darwin

package desktopupdate

import "golang.org/x/sys/unix"

func durableSync(fd int) error {
	_, err := unix.FcntlInt(uintptr(fd), unix.F_FULLFSYNC, 0)
	return err
}
