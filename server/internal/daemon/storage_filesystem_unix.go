//go:build darwin || linux

package daemon

import (
	"fmt"
	"os"
	"syscall"
)

func storageFilesystem(root *os.Root, name string, info os.FileInfo) (string, int64, error) {
	f, err := root.Open(name)
	if err != nil {
		return "", 0, err
	}
	defer f.Close()
	var st syscall.Statfs_t
	if err = syscall.Fstatfs(int(f.Fd()), &st); err != nil {
		return "", 0, err
	}
	return fmt.Sprint(info.Sys().(*syscall.Stat_t).Dev), int64(st.Bavail) * int64(st.Bsize), nil
}
