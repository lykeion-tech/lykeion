package sandbox

import (
	"bytes"
	"encoding/binary"
	"fmt"
	"os"
	"os/exec"
	"runtime"
	"sync"
)

var commandFiles sync.Map

// CloseCommand releases sandbox-owned launch descriptors. Callers must defer it
// immediately after Command succeeds, including when Start subsequently fails.
// It is idempotent and never closes descriptors supplied by the caller.
func CloseCommand(cmd *exec.Cmd) {
	if value, ok := commandFiles.LoadAndDelete(cmd); ok {
		for _, f := range value.([]*os.File) {
			_ = f.Close()
		}
	}
}

func attachNoNetworkFilter(cmd *exec.Cmd) error {
	filter, err := noNetworkFilter(runtime.GOARCH)
	if err != nil {
		return err
	}
	f, err := os.CreateTemp("", "lyk-seccomp-")
	if err != nil {
		return err
	}
	if err = os.Remove(f.Name()); err != nil {
		f.Close()
		return err
	}
	if _, err = f.Write(filter); err != nil {
		f.Close()
		return err
	}
	if _, err = f.Seek(0, 0); err != nil {
		f.Close()
		return err
	}
	fd := 3 + len(cmd.ExtraFiles)
	cmd.ExtraFiles = append(cmd.ExtraFiles, f)
	cmd.Args = append([]string{cmd.Args[0], "--seccomp", fmt.Sprint(fd)}, cmd.Args[1:]...)
	commandFiles.Store(cmd, []*os.File{f})
	return nil
}

// The filter checks the syscall ABI before syscall numbers, rejecting compat
// and x32 execution. Denying socket creation and connection also closes pathname
// Unix sockets in readonly mounts, including sockets created after launch.
func noNetworkFilter(arch string) ([]byte, error) {
	var audit uint32
	var denied []uint32
	switch arch {
	case "amd64":
		audit = 0xc000003e
		denied = []uint32{41, 42, 43, 44, 46, 49, 50, 288, 307, 425, 426, 427}
	case "arm64":
		audit = 0xc00000b7
		denied = []uint32{198, 200, 201, 202, 203, 206, 211, 242, 269, 425, 426, 427}
	default:
		return nil, fmt.Errorf("NoNetwork seccomp is unsupported on %s", arch)
	}
	var b bytes.Buffer
	ins := func(code uint16, jt, jf byte, k uint32) {
		_ = binary.Write(&b, binary.LittleEndian, struct {
			Code   uint16
			JT, JF byte
			K      uint32
		}{code, jt, jf, k})
	}
	ins(0x20, 0, 0, 4) // seccomp_data.arch
	ins(0x15, 1, 0, audit)
	ins(0x06, 0, 0, 0x80000000) // kill unknown ABI
	ins(0x20, 0, 0, 0)          // seccomp_data.nr
	if arch == "amd64" {
		ins(0x35, 0, 1, 0x40000000)
		ins(0x06, 0, 0, 0x80000000)
	}
	for _, nr := range denied {
		ins(0x15, 0, 1, nr)
		ins(0x06, 0, 0, 0x00050001)
	} // EPERM
	ins(0x06, 0, 0, 0x7fff0000)
	return b.Bytes(), nil
}
