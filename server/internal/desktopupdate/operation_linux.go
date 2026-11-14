//go:build linux

package desktopupdate

import (
	"context"
	"crypto/ed25519"
	"errors"
	"io"
	"net/http"
)

func (*lockedCache) checkAndCacheWithLimits(context.Context, []ed25519.PublicKey, string, string, string, http.RoundTripper, int64, int64) (Result, error) {
	return Result{}, errors.New("update check unavailable")
}

func (*lockedCache) checkAndStreamWithLimits(context.Context, []ed25519.PublicKey, string, string, string, http.RoundTripper, int64, int64, io.Reader, io.Writer) error {
	return errors.New("update stream unavailable")
}
