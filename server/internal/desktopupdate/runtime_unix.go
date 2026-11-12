//go:build darwin || linux

package desktopupdate

import (
	"context"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"sync"
	"time"

	"golang.org/x/sys/unix"

	"lykeion/server/internal/accountlookup"
)

// Each gate requires separate filesystem, sandbox and release-feed acceptance.
const (
	darwinArm64ProofReady = false
	darwinX64ProofReady   = false
	linuxArm64ProofReady  = false
	linuxX64ProofReady    = false
)

func platformProofReady(platform, arch string) bool {
	switch platform + "/" + arch {
	case "darwin/arm64":
		return darwinArm64ProofReady
	case "darwin/amd64":
		return darwinX64ProofReady
	case "linux/arm64":
		return linuxArm64ProofReady
	case "linux/amd64":
		return linuxX64ProofReady
	default:
		return false
	}
}

// StreamVerified serves one authenticated artifact to inherited pipes only.
// Every production platform remains disabled until its security proof is complete.
func StreamVerified(ctx context.Context, input, output *os.File) error {
	bad := checkError{Generic}
	if !platformProofReady(runtime.GOOS, runtime.GOARCH) || ctx.Err() != nil {
		return checkError{Unsupported}
	}
	if input == nil || output == nil {
		return bad
	}
	ctx, closeLiveness, err := startLiveness(ctx, 3, int(input.Fd()), int(output.Fd()))
	if err != nil {
		return bad
	}
	defer closeLiveness()
	pipes, err := openStreamPipes(ctx, input, output)
	if err != nil {
		return bad
	}
	defer pipes.close()
	keys, err := parseBuildTrust(embeddedKeys, embeddedVersion)
	if err != nil {
		return bad
	}
	arch := ""
	switch runtime.GOARCH {
	case "amd64":
		arch = "x64"
	case "arm64":
		arch = "arm64"
	default:
		return bad
	}
	uid := os.Geteuid()
	if uid <= 0 {
		return bad
	}
	account, err := accountlookup.LookupId(strconv.Itoa(uid))
	if err != nil || account == nil || account.Uid != strconv.Itoa(uid) || !filepath.IsAbs(account.HomeDir) || filepath.Clean(account.HomeDir) != account.HomeDir {
		return bad
	}
	cache, err := openLockedCache(ctx, account.HomeDir, uint32(uid))
	if err != nil {
		return bad
	}
	defer cache.close()
	if ctx.Err() != nil {
		return bad
	}
	transport, err := secureTransport()
	if err != nil {
		return bad
	}
	defer transport.CloseIdleConnections()
	if err := cache.checkAndStreamWithLimits(ctx, keys, runtime.GOOS, arch, embeddedVersion, transport, reserveBytes, maxCacheBytes, &deadlineInput{ctx: ctx, file: pipes.input}, pipes.output); err != nil {
		return bad
	}
	return nil
}

type streamPipes struct {
	input, output *os.File
	stop          func() bool
	done          chan struct{}
	original      [2]int
	flags         [2]int
	identity      [2]unix.Stat_t
	once          sync.Once
}

func (p *streamPipes) close() {
	p.once.Do(func() {
		if p.stop != nil && !p.stop() {
			<-p.done
		}
		if p.input != nil {
			_ = p.input.Close()
		}
		if p.output != nil {
			_ = p.output.Close()
		}
		for i, fd := range p.original {
			if fd >= 0 {
				var current unix.Stat_t
				if unix.Fstat(fd, &current) == nil && current.Dev == p.identity[i].Dev && current.Ino == p.identity[i].Ino && current.Mode == p.identity[i].Mode {
					_, _ = unix.FcntlInt(uintptr(fd), unix.F_SETFL, p.flags[i])
				}
			}
		}
	})
}

func openStreamPipes(ctx context.Context, input, output *os.File) (*streamPipes, error) {
	bad := checkError{Generic}
	if ctx.Err() != nil || input == nil || output == nil {
		return nil, bad
	}
	inInfo, inErr := input.Stat()
	outInfo, outErr := output.Stat()
	if inErr != nil || outErr != nil || inInfo.Mode()&(os.ModeNamedPipe|os.ModeSocket) == 0 || outInfo.Mode()&(os.ModeNamedPipe|os.ModeSocket) == 0 {
		return nil, bad
	}
	p := &streamPipes{original: [2]int{-1, -1}, done: make(chan struct{})}
	fds := [2]int{int(input.Fd()), int(output.Fd())}
	var identity [2]unix.Stat_t
	if fds[0] == fds[1] || unix.Fstat(fds[0], &identity[0]) != nil || unix.Fstat(fds[1], &identity[1]) != nil || (identity[0].Dev == identity[1].Dev && identity[0].Ino == identity[1].Ino) {
		return nil, bad
	}
	for i, fd := range fds {
		flags, err := unix.FcntlInt(uintptr(fd), unix.F_GETFL, 0)
		if err != nil {
			p.close()
			return nil, bad
		}
		if (i == 0 && flags&unix.O_ACCMODE == unix.O_WRONLY) || (i == 1 && flags&unix.O_ACCMODE == unix.O_RDONLY) {
			p.close()
			return nil, bad
		}
		dup, err := unix.FcntlInt(uintptr(fd), unix.F_DUPFD_CLOEXEC, 3)
		if err != nil {
			p.close()
			return nil, bad
		}
		p.original[i], p.flags[i], p.identity[i] = fd, flags, identity[i]
		if unix.SetNonblock(dup, true) != nil {
			_ = unix.Close(dup)
			p.close()
			return nil, bad
		}
		wrapped := os.NewFile(uintptr(dup), "update pipe")
		if wrapped == nil {
			_ = unix.Close(dup)
			p.close()
			return nil, bad
		}
		if i == 0 {
			p.input = wrapped
		} else {
			p.output = wrapped
		}
	}
	if p.input.SetReadDeadline(time.Now().Add(31*time.Minute)) != nil || p.output.SetWriteDeadline(time.Now().Add(31*time.Minute)) != nil {
		p.close()
		return nil, bad
	}
	p.stop = context.AfterFunc(ctx, func() {
		defer close(p.done)
		_ = p.input.SetReadDeadline(time.Now())
		_ = p.output.SetWriteDeadline(time.Now())
	})
	if ctx.Err() != nil {
		p.close()
		return nil, bad
	}
	return p, nil
}

type deadlineInput struct {
	ctx     context.Context
	file    *os.File
	started bool
}

func (d *deadlineInput) Read(p []byte) (int, error) {
	if err := d.ctx.Err(); err != nil {
		return 0, err
	}
	if !d.started {
		d.started = true
		if err := d.file.SetReadDeadline(time.Now().Add(30 * time.Second)); err != nil {
			return 0, err
		}
	}
	if err := d.ctx.Err(); err != nil {
		return 0, err
	}
	return d.file.Read(p)
}

// CheckAndCache never installs or launches an update.
func CheckAndCache(ctx context.Context) (Result, error) {
	errUnavailable := checkError{Generic}
	if !platformProofReady(runtime.GOOS, runtime.GOARCH) || ctx.Err() != nil {
		return Result{}, checkError{Unsupported}
	}
	keys, err := parseBuildTrust(embeddedKeys, embeddedVersion)
	if err != nil {
		return Result{}, checkError{Unconfigured}
	}
	arch := ""
	switch runtime.GOARCH {
	case "amd64":
		arch = "x64"
	case "arm64":
		arch = "arm64"
	default:
		return Result{}, errUnavailable
	}
	if runtime.GOOS != "darwin" && runtime.GOOS != "linux" {
		return Result{}, errUnavailable
	}
	uid := os.Geteuid()
	if uid <= 0 {
		return Result{}, errUnavailable
	}
	account, err := accountlookup.LookupId(strconv.Itoa(uid))
	if err != nil || account == nil || account.Uid != strconv.Itoa(uid) || !filepath.IsAbs(account.HomeDir) || filepath.Clean(account.HomeDir) != account.HomeDir {
		return Result{}, errUnavailable
	}
	cache, err := openLockedCache(ctx, account.HomeDir, uint32(uid))
	if err != nil {
		return Result{}, errUnavailable
	}
	defer cache.close()
	if ctx.Err() != nil {
		return Result{}, errUnavailable
	}
	transport, err := secureTransport()
	if err != nil {
		return Result{}, errUnavailable
	}
	defer transport.CloseIdleConnections()
	return cache.checkAndCacheWithLimits(ctx, keys, runtime.GOOS, arch, embeddedVersion, transport, reserveBytes, maxCacheBytes)
}
