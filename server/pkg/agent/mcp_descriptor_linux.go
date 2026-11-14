//go:build linux

package agent

import (
	"golang.org/x/sys/unix"
	"os"
)

func mcpDescriptor(data []byte) (*os.File, error) {
	fd, err := unix.MemfdCreate("lykeion-mcp", unix.MFD_CLOEXEC|unix.MFD_ALLOW_SEALING)
	if err != nil {
		return nil, err
	}
	f := os.NewFile(uintptr(fd), "lykeion-mcp")
	if _, err = f.Write(data); err == nil {
		_, err = unix.FcntlInt(f.Fd(), unix.F_ADD_SEALS, unix.F_SEAL_WRITE|unix.F_SEAL_GROW|unix.F_SEAL_SHRINK|unix.F_SEAL_SEAL)
	}
	if err == nil {
		_, err = f.Seek(0, 0)
	}
	if err != nil {
		f.Close()
		return nil, err
	}
	return f, nil
}
