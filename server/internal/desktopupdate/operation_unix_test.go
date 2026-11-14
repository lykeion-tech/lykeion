//go:build darwin || linux

package desktopupdate

import (
	"bytes"
	"context"
	"crypto/ed25519"
	"crypto/sha256"
	"encoding/hex"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"testing"
	"time"
)

func TestCheckAndCacheAuthenticatesBeforeArtifactFetch(t *testing.T) {
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
	content := []byte("verified artifact")
	sum := sha256.Sum256(content)
	now := time.Now()
	payload := []any{1, "stable", 7, "1.2.3", now.Unix(), now.Add(time.Hour).Unix(), "darwin", "arm64", "Lykeion-1.2.3-arm64-mac.zip", len(content), hex.EncodeToString(sum[:])}
	envelope, key := signedEnvelope(t, payload)
	requests := 0
	transport := roundTripFunc(func(r *http.Request) (*http.Response, error) {
		requests++
		body := content
		if requests == 1 || requests == 3 {
			body = envelope
		}
		return &http.Response{StatusCode: 200, Header: make(http.Header), Body: io.NopCloser(bytes.NewReader(body)), ContentLength: int64(len(body)), Request: r}, nil
	})
	if _, err := cache.checkAndCacheWithLimits(context.Background(), nil, "darwin", "arm64", "1.0.0", transport, 4096, 1<<20); err == nil || requests != 0 {
		t.Fatal("missing trust anchor made a network request")
	}
	result, err := cache.checkAndCacheWithLimits(context.Background(), []ed25519.PublicKey{key}, "darwin", "arm64", "1.0.0", transport, 4096, 1<<20)
	if err != nil || result.State != "downloaded" || result.Version != "1.2.3" || requests != 2 {
		t.Fatalf("result=%+v requests=%d err=%v", result, requests, err)
	}
	artifact := filepath.Join(home, ".config", "lyk", "profiles", "desktop-updates-v1", "artifact-"+hex.EncodeToString(sum[:]))
	if got, err := os.ReadFile(artifact); err != nil || !bytes.Equal(got, content) {
		t.Fatalf("cached %q error %v", got, err)
	}
	result, err = cache.checkAndCacheWithLimits(context.Background(), []ed25519.PublicKey{key}, "darwin", "arm64", "1.0.0", transport, 4096, 1<<20)
	if err != nil || result.State != "downloaded" || result.Version != "1.2.3" || requests != 3 {
		t.Fatalf("repeat result=%+v requests=%d err=%v", result, requests, err)
	}
}

func TestCheckAndCacheRejectsUnsafeInputWithoutArtifactFetch(t *testing.T) {
	if runtime.GOOS != "darwin" {
		t.Skip("Linux storage remains disabled")
	}
	for _, scenario := range []string{"bad-signature", "expired", "rollback", "equivocation", "corrupt-cache", "over-budget", "insufficient-space", "current"} {
		t.Run(scenario, func(t *testing.T) {
			home, err := filepath.EvalSymlinks(t.TempDir())
			if err != nil {
				t.Fatal(err)
			}
			cache, err := openLockedCache(context.Background(), home, uint32(os.Geteuid()))
			if err != nil {
				t.Fatal(err)
			}
			defer cache.close()
			now := time.Now()
			issued, expires := now.Unix(), now.Add(time.Hour).Unix()
			if scenario == "expired" {
				issued, expires = now.Add(-2*time.Hour).Unix(), now.Add(-time.Hour).Unix()
			}
			artifact := []byte("update")
			digest := sha256.Sum256(artifact)
			payload := []any{1, "stable", 7, "1.2.3", issued, expires, "darwin", "arm64", "Lykeion-1.2.3-arm64-mac.zip", len(artifact), hex.EncodeToString(digest[:])}
			envelope, key := signedEnvelope(t, payload)
			if scenario == "bad-signature" {
				envelope[len(envelope)/2] ^= 1
			}
			if scenario == "rollback" || scenario == "equivocation" {
				seq := int64(8)
				if scenario == "equivocation" {
					seq = 7
				}
				if err := cache.commitHighWater("darwin", "arm64", seq, sha256.Sum256([]byte("different payload"))); err != nil {
					t.Fatal(err)
				}
			}
			if scenario == "corrupt-cache" {
				if err := os.WriteFile(filepath.Join(home, ".config", "lyk", "profiles", "desktop-updates-v1", "unknown"), []byte("preserve"), 0600); err != nil {
					t.Fatal(err)
				}
			}
			budget := int64(1 << 20)
			if scenario == "over-budget" {
				budget = 4096
			}
			if scenario == "insufficient-space" {
				budget = 200000
			}
			current := "1.0.0"
			if scenario == "current" {
				current = "1.2.3"
			}
			requests := 0
			transport := roundTripFunc(func(r *http.Request) (*http.Response, error) {
				requests++
				return &http.Response{StatusCode: 200, Header: make(http.Header), Body: io.NopCloser(bytes.NewReader(envelope)), ContentLength: int64(len(envelope)), Request: r}, nil
			})
			result, err := cache.checkAndCacheWithLimits(context.Background(), []ed25519.PublicKey{key}, "darwin", "arm64", current, transport, 4096, budget)
			if scenario == "current" {
				if err != nil || result.State != "current" || result.Version != "1.2.3" || requests != 1 {
					t.Fatalf("result=%+v requests=%d error=%v", result, requests, err)
				}
				return
			}
			if err == nil {
				t.Fatal("unsafe check succeeded")
			}
			if (scenario == "over-budget" || scenario == "insufficient-space") && ErrorCategory(err) != CacheFull {
				t.Fatalf("expected cache-full category, got %v", err)
			}
			if scenario == "corrupt-cache" || scenario == "over-budget" {
				if requests != 0 {
					t.Fatalf("network used before cache validation: %d", requests)
				}
			} else if requests != 1 {
				t.Fatalf("unexpected artifact request: %d", requests)
			}
		})
	}
}
