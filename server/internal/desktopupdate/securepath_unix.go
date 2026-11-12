//go:build darwin || linux

package desktopupdate

import (
	"errors"
	"os"
	"path/filepath"
	"strings"

	"golang.org/x/sys/unix"
)

// openPrivateDirectory returns an owned descriptor rooted under the real user's
// hidden profiles. Every traversal and creation is relative to a retained fd.
func openPrivateDirectory(home string, uid uint32) (int, error) {
	return openPrivateDirectoryWithSync(home, uid, durableSync)
}

func openPrivateDirectoryWithSync(home string, uid uint32, syncDir func(int) error) (int, error) {
	errUnsafe := errors.New("private update directory unavailable")
	if uid == 0 || int(uid) != os.Geteuid() || home == "/" || !filepath.IsAbs(home) || filepath.Clean(home) != home {
		return -1, errUnsafe
	}
	components := strings.Split(strings.TrimPrefix(home, "/"), "/")
	if len(components) == 0 {
		return -1, errUnsafe
	}
	components = append(components, ".config", "lyk", "profiles", "desktop-updates-v1")
	fd, err := unix.Open("/", unix.O_RDONLY|unix.O_DIRECTORY|unix.O_NOFOLLOW|unix.O_CLOEXEC, 0)
	if err != nil {
		return -1, errUnsafe
	}
	valid := func(fd int, leaf bool) bool {
		var info unix.Stat_t
		if unix.Fstat(fd, &info) != nil {
			return false
		}
		mode := uint32(info.Mode)
		if mode&unix.S_IFMT != unix.S_IFDIR || (info.Uid != 0 && info.Uid != uid) || mode&06000 != 0 || !privateACL(fd) || !privateFilesystem(fd) {
			return false
		}
		if leaf {
			return info.Uid == uid && mode&0777 == 0700
		}
		if mode&0022 == 0 {
			return true
		}
		return info.Uid == 0 && mode&unix.S_ISVTX != 0
	}
	if !valid(fd, false) {
		unix.Close(fd)
		return -1, errUnsafe
	}
	for i, part := range components {
		if part == "" || part == "." || part == ".." {
			unix.Close(fd)
			return -1, errUnsafe
		}
		next, openErr := unix.Openat(fd, part, unix.O_RDONLY|unix.O_DIRECTORY|unix.O_NOFOLLOW|unix.O_CLOEXEC, 0)
		if errors.Is(openErr, unix.ENOENT) && i >= len(components)-4 {
			mkdirErr := unix.Mkdirat(fd, part, 0700)
			if mkdirErr != nil && !errors.Is(mkdirErr, unix.EEXIST) {
				unix.Close(fd)
				return -1, errUnsafe
			}
			next, openErr = unix.Openat(fd, part, unix.O_RDONLY|unix.O_DIRECTORY|unix.O_NOFOLLOW|unix.O_CLOEXEC, 0)
		}
		if openErr != nil {
			unix.Close(fd)
			return -1, errUnsafe
		}
		if syncDir == nil || syncDir(fd) != nil {
			unix.Close(next)
			unix.Close(fd)
			return -1, errUnsafe
		}
		unix.Close(fd)
		fd = next
		if !valid(fd, i == len(components)-1) {
			unix.Close(fd)
			return -1, errUnsafe
		}
		if syncDir(fd) != nil {
			unix.Close(fd)
			return -1, errUnsafe
		}
	}
	return fd, nil
}
