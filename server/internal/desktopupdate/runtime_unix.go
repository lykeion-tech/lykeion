//go:build darwin || linux

package desktopupdate

import (
	"context"
	"os"
	"os/user"
	"path/filepath"
	"runtime"
	"strconv"
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
