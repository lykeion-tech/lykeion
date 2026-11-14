//go:build darwin

package desktopupdate

import (
	"bytes"

	"golang.org/x/sys/unix"
)

// Only local APFS has candidate enforcement evidence; all other mounts fail closed.
func privateFilesystem(fd int) bool {
	var info unix.Statfs_t
	if unix.Fstatfs(fd, &info) != nil {
		return false
	}
	name, _, _ := bytes.Cut(info.Fstypename[:], []byte{0})
	return string(name) == "apfs" && info.Flags&unix.MNT_LOCAL != 0
}
