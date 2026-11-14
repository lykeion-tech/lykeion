//go:build darwin || linux

package desktopupdate

import (
	"context"
	"errors"
	"os"
	"sync"

	"golang.org/x/sys/unix"
)

// startLiveness consumes no user data. The peer must remain open and silent.
// Any byte, EOF, or I/O failure cancels the child session.
func startLiveness(parent context.Context, fd, stdinFD, stdoutFD int) (context.Context, func(), error) {
	bad := errors.New("update stream unavailable")
	if parent.Err() != nil || fd < 3 || fd == stdinFD || fd == stdoutFD {
		return nil, nil, bad
	}
	var original unix.Stat_t
	if unix.Fstat(fd, &original) != nil || (uint32(original.Mode)&unix.S_IFMT != unix.S_IFIFO && uint32(original.Mode)&unix.S_IFMT != unix.S_IFSOCK) {
		return nil, nil, bad
	}
	for _, other := range []int{stdinFD, stdoutFD} {
		if other < 0 {
			continue
		}
		var info unix.Stat_t
		if unix.Fstat(other, &info) != nil || (info.Dev == original.Dev && info.Ino == original.Ino) {
			return nil, nil, bad
		}
	}
	flags, err := unix.FcntlInt(uintptr(fd), unix.F_GETFL, 0)
	if err != nil || flags&unix.O_ACCMODE == unix.O_WRONLY {
		return nil, nil, bad
	}
	fdflags, err := unix.FcntlInt(uintptr(fd), unix.F_GETFD, 0)
	if err != nil {
		return nil, nil, bad
	}
	if _, err := unix.FcntlInt(uintptr(fd), unix.F_SETFD, fdflags|unix.FD_CLOEXEC); err != nil {
		return nil, nil, bad
	}
	dup, err := unix.FcntlInt(uintptr(fd), unix.F_DUPFD_CLOEXEC, 3)
	if err != nil {
		return nil, nil, bad
	}
	restore := func() {
		var now unix.Stat_t
		if unix.Fstat(fd, &now) == nil && now.Dev == original.Dev && now.Ino == original.Ino && now.Mode == original.Mode {
			_, _ = unix.FcntlInt(uintptr(fd), unix.F_SETFL, flags)
		}
	}
	if unix.SetNonblock(dup, true) != nil {
		_ = unix.Close(dup)
		restore()
		return nil, nil, bad
	}
	var probe [1]byte
	for {
		n, readErr := unix.Read(dup, probe[:])
		if n > 0 || readErr == nil {
			_ = unix.Close(dup)
			restore()
			return nil, nil, bad
		}
		if errors.Is(readErr, unix.EINTR) {
			continue
		}
		if !errors.Is(readErr, unix.EAGAIN) && !errors.Is(readErr, unix.EWOULDBLOCK) {
			_ = unix.Close(dup)
			restore()
			return nil, nil, bad
		}
		break
	}
	live := os.NewFile(uintptr(dup), "update liveness")
	if live == nil {
		_ = unix.Close(dup)
		restore()
		return nil, nil, bad
	}
	ctx, cancel := context.WithCancel(parent)
	done := make(chan struct{})
	go func() { defer close(done); var b [1]byte; _, _ = live.Read(b[:]); cancel() }()
	var once sync.Once
	closeMonitor := func() { once.Do(func() { cancel(); _ = live.Close(); <-done; restore() }) }
	if ctx.Err() != nil {
		closeMonitor()
		return nil, nil, bad
	}
	return ctx, closeMonitor, nil
}
