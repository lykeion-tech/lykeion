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
	if cache == nil {
		return Result{}, errors.New("update check unavailable")
	}
	cache.mu.Lock()
	defer cache.mu.Unlock()
	result, _, err := cache.checkAndCacheLocked(ctx, keys, platform, arch, current, transport, reserve, budget)
	return result, err
}

func (cache *lockedCache) checkAndCacheLocked(ctx context.Context, keys []ed25519.PublicKey, platform, arch, current string, transport http.RoundTripper, reserve, budget int64) (Result, metadata, error) {
	errUnsafe := errors.New("update check unavailable")
	if ctx.Err() != nil || !cache.valid() || cache.now == nil || !stableVersion.MatchString(current) || transport == nil {
		return Result{}, metadata{}, errUnsafe
	}
	if _, err := trustedKeyMap(keys); err != nil {
		return Result{}, metadata{}, errUnsafe
	}
	if err := cache.recoverJournal(); err != nil {
		return Result{}, metadata{}, errUnsafe
	}
	used, err := cache.inventory(nil)
	if err != nil {
		return Result{}, metadata{}, errUnsafe
	}
	if used > budget {
		return Result{}, metadata{}, checkError{CacheFull}
	}
	filename := "desktop-stable-" + platform + "-" + arch + ".json"
	raw, err := fetchMetadata(ctx, updateClient(transport, filename), platform, arch)
	if err != nil {
		if ErrorCategory(err) == Network {
			return Result{}, metadata{}, checkError{Network}
		}
		return Result{}, metadata{}, errUnsafe
	}
	m, err := verifyMetadata(raw, keys, platform, arch, cache.now())
	if err != nil {
		return Result{}, metadata{}, checkError{InvalidMetadata}
	}
	if m.version != current && !newerVersion(m.version, current) {
		return Result{}, metadata{}, checkError{InvalidMetadata}
	}
	if err := cache.commitHighWaterLocked(platform, arch, m.sequence, m.payloadDigest); err != nil {
		return Result{}, metadata{}, errUnsafe
	}
	if m.version == current {
		return Result{State: "current", Version: m.version}, m, nil
	}
	open := func(ctx context.Context) (io.ReadCloser, error) {
		return openArtifact(ctx, updateClient(transport, m.filename), m)
	}
	if err := cache.storeArtifactLocked(ctx, m, open, reserve, budget); err != nil {
		if ErrorCategory(err) == CacheFull {
			return Result{}, metadata{}, checkError{CacheFull}
		}
		return Result{}, metadata{}, errUnsafe
	}
	return Result{State: "downloaded", Version: m.version}, m, nil
}
