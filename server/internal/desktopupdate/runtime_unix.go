//go:build darwin || linux

package desktopupdate

import (
	"context"
	"os"
	"os/user"
	"path/filepath"
	"runtime"
	"strconv"
	"time"
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
	stop, err := armStreamPipes(ctx, input, output)
	if err != nil {
		return bad
	}
	defer stop()
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
	account, err := user.LookupId(strconv.Itoa(uid))
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
	if err := cache.checkAndStreamWithLimits(ctx, keys, runtime.GOOS, arch, embeddedVersion, transport, reserveBytes, maxCacheBytes, &deadlineInput{ctx: ctx, file: input}, output); err != nil {
		return bad
	}
	return nil
}

func armStreamPipes(ctx context.Context, input, output *os.File) (func() bool, error) {
	bad := checkError{Generic}
	if ctx.Err() != nil || input == nil || output == nil {
		return nil, bad
	}
	inInfo, inErr := input.Stat()
	outInfo, outErr := output.Stat()
	if inErr != nil || outErr != nil || inInfo.Mode()&(os.ModeNamedPipe|os.ModeSocket) == 0 || outInfo.Mode()&(os.ModeNamedPipe|os.ModeSocket) == 0 || output.SetWriteDeadline(time.Now().Add(31*time.Minute)) != nil {
		return nil, bad
	}
	return context.AfterFunc(ctx, func() {
		_ = input.SetReadDeadline(time.Now())
		_ = output.SetWriteDeadline(time.Now())
	}), nil
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
	account, err := user.LookupId(strconv.Itoa(uid))
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
