//go:build darwin

package desktopupdate

import (
	"errors"
	"os"

	"golang.org/x/sys/unix"
)

func preallocate(fd int, size int64) error {
	errUnsafe := errors.New("update storage reservation unavailable")
	var info unix.Stat_t
	if size < 1 || size > maxArtifact || unix.Fstat(fd, &info) != nil || uint32(info.Mode)&unix.S_IFMT != unix.S_IFREG || info.Uid != uint32(os.Geteuid()) || info.Nlink != 1 || info.Size != 0 || uint32(info.Mode)&07777 != 0600 || !privateACL(fd) {
		return errUnsafe
	}
	request := unix.Fstore_t{Flags: unix.F_ALLOCATEALL, Posmode: unix.F_PEOFPOSMODE, Length: size}
	if unix.FcntlFstore(uintptr(fd), unix.F_PREALLOCATE, &request) != nil || request.Bytesalloc < size || unix.Ftruncate(fd, size) != nil || durableSync(fd) != nil {
		return errUnsafe
	}
	if unix.Fstat(fd, &info) != nil || info.Size != size || info.Blocks < (size+511)/512 {
		return errUnsafe
	}
	return nil
}
