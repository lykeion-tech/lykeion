//go:build linux

package desktopupdate

import (
	"context"
	"io"
	"testing"
)

func TestLinuxStorageFailsClosedBeforeReading(t *testing.T) {
	cache := &lockedCache{}
	opened := false
	open := func(context.Context) (io.ReadCloser, error) {
		opened = true
		return nil, nil
	}
	if err := cache.storeArtifact(context.Background(), metadata{}, open); err == nil {
		t.Fatal("Linux storage unexpectedly available")
	}
	if err := cache.storeArtifactWithLimits(context.Background(), metadata{}, open, 1, 1); err == nil {
		t.Fatal("Linux bounded storage unexpectedly available")
	}
	if err := cache.storeArtifactLocked(context.Background(), metadata{}, open, 1, 1); err == nil {
		t.Fatal("Linux locked storage unexpectedly available")
	}
	if opened {
		t.Fatal("Linux storage opened the source")
	}
}
