//go:build linux

package desktopupdate

import (
	"context"
	"errors"
	"io"
)

func (*lockedCache) storeArtifact(context.Context, metadata, func(context.Context) (io.ReadCloser, error)) error {
	return errors.New("update storage unavailable")
}

func (*lockedCache) storeArtifactWithLimits(context.Context, metadata, func(context.Context) (io.ReadCloser, error), int64, int64) error {
	return errors.New("update storage unavailable")
}

func (*lockedCache) storeArtifactLocked(context.Context, metadata, func(context.Context) (io.ReadCloser, error), int64, int64) error {
	return errors.New("update storage unavailable")
}
