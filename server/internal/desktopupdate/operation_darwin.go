//go:build darwin

package desktopupdate

import (
	"context"
	"crypto/ed25519"
	"errors"
	"io"
	"net/http"
)

// Caller supplies only trusted build identity and a fixed transport; no renderer input is accepted.
func (cache *lockedCache) checkAndCacheWithLimits(ctx context.Context, keys []ed25519.PublicKey, platform, arch, current string, transport http.RoundTripper, reserve, budget int64) (Result, error) {
	errUnsafe := errors.New("update check unavailable")
	if cache == nil {
		return Result{}, errUnsafe
	}
	cache.mu.Lock()
	defer cache.mu.Unlock()
	if ctx.Err() != nil || !cache.valid() || cache.now == nil || !stableVersion.MatchString(current) || transport == nil {
		return Result{}, errUnsafe
	}
	if _, err := trustedKeyMap(keys); err != nil {
		return Result{}, errUnsafe
	}
	if err := cache.recoverJournal(); err != nil {
		return Result{}, errUnsafe
	}
	used, err := cache.inventory(nil)
	if err != nil {
		return Result{}, errUnsafe
	}
	if used > budget {
		return Result{}, checkError{CacheFull}
	}
	filename := "desktop-stable-" + platform + "-" + arch + ".json"
	raw, err := fetchMetadata(ctx, updateClient(transport, filename), platform, arch)
	if err != nil {
		if ErrorCategory(err) == Network {
			return Result{}, checkError{Network}
		}
		return Result{}, errUnsafe
	}
	m, err := verifyMetadata(raw, keys, platform, arch, cache.now())
	if err != nil {
		return Result{}, checkError{InvalidMetadata}
	}
	if m.version != current && !newerVersion(m.version, current) {
		return Result{}, checkError{InvalidMetadata}
	}
	if err := cache.commitHighWaterLocked(platform, arch, m.sequence, m.payloadDigest); err != nil {
		return Result{}, errUnsafe
	}
	if m.version == current {
		return Result{State: "current", Version: m.version}, nil
	}
	open := func(ctx context.Context) (io.ReadCloser, error) {
		return openArtifact(ctx, updateClient(transport, m.filename), m)
	}
	if err := cache.storeArtifactLocked(ctx, m, open, reserve, budget); err != nil {
		if ErrorCategory(err) == CacheFull {
			return Result{}, checkError{CacheFull}
		}
		return Result{}, errUnsafe
	}
	return Result{State: "downloaded", Version: m.version}, nil
}
