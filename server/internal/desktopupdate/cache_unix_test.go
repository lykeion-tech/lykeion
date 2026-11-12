//go:build darwin || linux

package desktopupdate

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func TestVerifiedCacheEnforcesBudgetAndContent(t *testing.T) {
	if runtime.GOOS != "darwin" {
		t.Skip("Linux storage remains disabled")
	}
	home, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	cache, err := openLockedCache(context.Background(), home, uint32(os.Geteuid()))
	if err != nil {
		t.Fatal(err)
	}
	defer cache.close()
	content := []byte("verified update")
	sum, payload := sha256.Sum256(content), sha256.Sum256([]byte("signed metadata"))
	m := metadata{sequence: 7, platform: "darwin", arch: "arm64", size: int64(len(content)), digest: hex.EncodeToString(sum[:]), payloadDigest: payload, issued: time.Now().Unix(), expires: time.Now().Add(time.Hour).Unix()}
	opened := false
	open := func(context.Context) (io.ReadCloser, error) {
		opened = true
		return io.NopCloser(bytes.NewReader(content)), nil
	}
	if err := cache.storeArtifactWithLimits(context.Background(), m, open, 4096, 4096); err == nil {
		t.Fatal("accepted over-budget cache")
	}
	if opened {
		t.Fatal("opened network source before securing storage budget")
	}
	if err := cache.storeArtifactWithLimits(context.Background(), m, open, 4096, 1<<20); err != nil {
		t.Fatalf("store valid artifact: %v", err)
	}
	if !opened {
		t.Fatal("valid artifact was not read")
	}
	got, err := os.ReadFile(filepath.Join(home, ".config", "lyk", "profiles", "desktop-updates-v1", "artifact-"+m.digest))
	if err != nil || !bytes.Equal(got, content) {
		t.Fatalf("cached content %q, error %v", got, err)
	}
	orphan := filepath.Join(home, ".config", "lyk", "profiles", "desktop-updates-v1", "state-linux-x64")
	if err := os.WriteFile(orphan, []byte("corrupt"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := cache.storeArtifactWithLimits(context.Background(), m, open, 4096, 1<<20); err == nil {
		t.Fatal("accepted corrupt other-target state")
	}
	if err := os.Remove(orphan); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(home, ".config", "lyk", "profiles", "desktop-updates-v1", "unknown"), []byte("preserve"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := cache.storeArtifactWithLimits(context.Background(), m, open, 4096, 1<<20); err == nil {
		t.Fatal("accepted unknown cache entry")
	}
}

type blockedReader struct {
	closed chan struct{}
	once   sync.Once
}

func (r *blockedReader) Read([]byte) (int, error) { <-r.closed; return 0, io.EOF }
func (r *blockedReader) Close() error             { r.once.Do(func() { close(r.closed) }); return nil }

func TestCanceledDownloadClosesSourceAndReleasesCache(t *testing.T) {
	if runtime.GOOS != "darwin" {
		t.Skip("Linux storage remains disabled")
	}
	home, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	cache, err := openLockedCache(context.Background(), home, uint32(os.Geteuid()))
	if err != nil {
		t.Fatal(err)
	}
	defer cache.close()
	sum, payload := sha256.Sum256([]byte("x")), sha256.Sum256([]byte("signed"))
	m := metadata{sequence: 7, platform: "darwin", arch: "arm64", size: 1, digest: hex.EncodeToString(sum[:]), payloadDigest: payload, issued: time.Now().Unix(), expires: time.Now().Add(time.Hour).Unix()}
	reader := &blockedReader{closed: make(chan struct{})}
	opened := make(chan struct{})
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() {
		done <- cache.storeArtifactWithLimits(ctx, m, func(context.Context) (io.ReadCloser, error) { close(opened); return reader, nil }, 4096, 1<<20)
	}()
	<-opened
	cancel()
	select {
	case err := <-done:
		if err == nil {
			t.Fatal("canceled download succeeded")
		}
	case <-time.After(3 * time.Second):
		t.Fatal("canceled download did not unblock")
	}
	check := make(chan bool, 1)
	go func() { cache.mu.Lock(); defer cache.mu.Unlock(); check <- cache.valid() }()
	select {
	case valid := <-check:
		if !valid {
			t.Fatal("cache lock was not retained safely")
		}
	case <-time.After(time.Second):
		t.Fatal("cache operation lock was not released")
	}
}

func TestDownloadRejectsClockRollbackBeforePublication(t *testing.T) {
	if runtime.GOOS != "darwin" {
		t.Skip("Linux storage remains disabled")
	}
	home, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	cache, err := openLockedCache(context.Background(), home, uint32(os.Geteuid()))
	if err != nil {
		t.Fatal(err)
	}
	defer cache.close()
	content := []byte("x")
	sum, payload := sha256.Sum256(content), sha256.Sum256([]byte("signed"))
	var clock atomic.Int64
	clock.Store(time.Now().Unix())
	cache.now = func() time.Time { return time.Unix(clock.Load(), 0) }
	m := metadata{sequence: 7, platform: "darwin", arch: "arm64", size: 1, digest: hex.EncodeToString(sum[:]), payloadDigest: payload, issued: clock.Load(), expires: clock.Load() + 3600}
	reader, writer := io.Pipe()
	opened := make(chan struct{})
	done := make(chan error, 1)
	go func() {
		done <- cache.storeArtifactWithLimits(context.Background(), m, func(context.Context) (io.ReadCloser, error) { close(opened); return reader, nil }, 4096, 1<<20)
	}()
	<-opened
	clock.Add(-7200)
	if _, err := writer.Write(content); err != nil {
		t.Fatal(err)
	}
	if err := writer.Close(); err != nil {
		t.Fatal(err)
	}
	if err := <-done; err == nil {
		t.Fatal("published after clock moved behind issuance")
	}
	if _, err := os.Stat(filepath.Join(home, ".config", "lyk", "profiles", "desktop-updates-v1", "artifact-"+m.digest)); !os.IsNotExist(err) {
		t.Fatalf("artifact exists after invalid clock: %v", err)
	}
}

func TestMissingReservationFailsBeforePublication(t *testing.T) {
	if runtime.GOOS != "darwin" {
		t.Skip("Linux storage remains disabled")
	}
	home, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	cache, err := openLockedCache(context.Background(), home, uint32(os.Geteuid()))
	if err != nil {
		t.Fatal(err)
	}
	defer cache.close()
	content := []byte("x")
	sum, payload := sha256.Sum256(content), sha256.Sum256([]byte("signed"))
	m := metadata{sequence: 7, platform: "darwin", arch: "arm64", size: 1, digest: hex.EncodeToString(sum[:]), payloadDigest: payload, issued: time.Now().Unix(), expires: time.Now().Add(time.Hour).Unix()}
	dir := filepath.Join(home, ".config", "lyk", "profiles", "desktop-updates-v1")
	open := func(context.Context) (io.ReadCloser, error) {
		paths, err := filepath.Glob(filepath.Join(dir, ".reserve-*"))
		if err != nil || len(paths) != 1 {
			t.Fatalf("reservation fixture: %v %v", paths, err)
		}
		if err := os.Remove(paths[0]); err != nil {
			t.Fatal(err)
		}
		return io.NopCloser(bytes.NewReader(content)), nil
	}
	if err := cache.storeArtifactWithLimits(context.Background(), m, open, 4096, 1<<20); err == nil {
		t.Fatal("published without reservation")
	}
	if _, err := os.Stat(filepath.Join(dir, "artifact-"+m.digest)); !os.IsNotExist(err) {
		t.Fatalf("artifact exists after reservation removal: %v", err)
	}
}

func TestCacheInventoryRejectsTooManyEntriesWithoutRemovingThem(t *testing.T) {
	if runtime.GOOS != "darwin" {
		t.Skip("Linux storage remains disabled")
	}
	home, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	cache, err := openLockedCache(context.Background(), home, uint32(os.Geteuid()))
	if err != nil {
		t.Fatal(err)
	}
	defer cache.close()
	dir := filepath.Join(home, ".config", "lyk", "profiles", "desktop-updates-v1")
	var last string
	for i := 0; i < maxCacheEntries; i++ {
		body := []byte(fmt.Sprintf("artifact-%d", i))
		hash := sha256.Sum256(body)
		last = filepath.Join(dir, "artifact-"+hex.EncodeToString(hash[:]))
		if err := os.WriteFile(last, body, 0600); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := cache.inventory(nil); err == nil {
		t.Fatal("accepted too many cache entries")
	}
	if _, err := os.Stat(last); err != nil {
		t.Fatalf("removed an existing entry: %v", err)
	}
}
