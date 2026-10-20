package sandbox

import (
	"bytes"
	"errors"
	"os"
	"syscall"
	"unsafe"
)

// fGetPath is F_GETPATH from <sys/fcntl.h>; syscall does not export it.
const fGetPath = 50

// canonical returns the kernel's own path for p: the spelling the file system
// keeps (case, Unicode normalisation) under its real folders, the form
// Seatbelt rules are matched against.
func canonical(p string) (string, error) {
	f, err := os.OpenFile(p, os.O_RDONLY|syscall.O_NONBLOCK, 0)
	if err != nil {
		return "", err
	}
	defer f.Close()
	buf := make([]byte, 1024) // MAXPATHLEN
	if _, _, e := syscall.Syscall(syscall.SYS_FCNTL, f.Fd(), fGetPath, uintptr(unsafe.Pointer(&buf[0]))); e != 0 {
		return "", e
	}
	n := bytes.IndexByte(buf, 0)
	if n <= 0 {
		return "", errors.New("F_GETPATH returned no path")
	}
	return string(buf[:n]), nil
}
