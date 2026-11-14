//go:build linux || darwin

package envs

import (
	"errors"
	"io"
	"os"

	"golang.org/x/sys/unix"
)

// readBaseLock bounds lock reads without following a final symlink or waiting
// for a FIFO in a damaged base.
func readBaseLock(path string) ([]byte, error) {
	fd, err := unix.Open(path, unix.O_RDONLY|unix.O_CLOEXEC|unix.O_NOFOLLOW|unix.O_NONBLOCK|unix.O_NOCTTY, 0)
	if err != nil {
		return nil, &os.PathError{Op: "open", Path: path, Err: err}
	}
	f := os.NewFile(uintptr(fd), path)
	defer f.Close()
	return readOpenedBaseLock(f)
}

func readOpenedBaseLock(f *os.File) ([]byte, error) {
	info, err := f.Stat()
	if err != nil {
		return nil, err
	}
	if !info.Mode().IsRegular() {
		return nil, errors.New("the shared base lock is not a regular file")
	}
	data, err := io.ReadAll(io.LimitReader(f, inventoryLimit+1))
	if err != nil {
		return nil, err
	}
	if len(data) > inventoryLimit {
		return nil, errors.New("the shared base lock is over 4 MiB")
	}
	return data, nil
}
