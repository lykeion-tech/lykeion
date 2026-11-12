//go:build darwin

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
	"testing"
	"time"
)

type livenessProbeWriter struct {
	output io.Writer
	marker string
	calls  int
}

func (w *livenessProbeWriter) Write(p []byte) (int, error) {
	w.calls++
	if w.calls == 2 {
		_ = os.WriteFile(w.marker, []byte("ready"), 0600)
	}
	return w.output.Write(p)
}

func TestLivenessProcessChild(t *testing.T) {
	mode := os.Getenv("LYK_TEST_LIVENESS_MODE")
	if mode == "" {
		return
	}
	home, marker, finished := os.Getenv("LYK_TEST_LIVENESS_HOME"), os.Getenv("LYK_TEST_LIVENESS_READY"), os.Getenv("LYK_TEST_LIVENESS_FINISHED")
	ctx, closeLive, err := startLiveness(context.Background(), 3, int(os.Stdin.Fd()), int(os.Stdout.Fd()))
	if err != nil {
		os.Exit(41)
	}
	pipes, err := openStreamPipes(ctx, os.Stdin, os.Stdout)
	if err != nil {
		closeLive()
		os.Exit(42)
	}
	cache, err := openLockedCache(ctx, home, uint32(os.Geteuid()))
	if err != nil {
		pipes.close()
		closeLive()
		os.Exit(43)
	}
	if mode == "lock" {
		_ = os.WriteFile(marker, []byte("ready"), 0600)
		other, err := openLockedCache(ctx, home, uint32(os.Geteuid()))
		if err == nil {
			other.close()
		}
	} else {
		content := bytes.Repeat([]byte("x"), 2<<20)
		sum := sha256.Sum256(content)
		now := time.Now()
		envelope, key := signedEnvelope(t, []any{1, "stable", 7, "1.2.3", now.Unix(), now.Add(time.Hour).Unix(), "darwin", "arm64", "Lykeion-1.2.3-arm64-mac.zip", len(content), hex.EncodeToString(sum[:])})
		requests := 0
		transport := roundTripFunc(func(r *http.Request) (*http.Response, error) {
			requests++
			if (mode == "metadata" && requests == 1) || (mode == "download" && requests == 2) {
				_ = os.WriteFile(marker, []byte("ready"), 0600)
				<-r.Context().Done()
				return nil, r.Context().Err()
			}
			body := content
			if requests == 1 {
				body = envelope
			}
			return &http.Response{StatusCode: 200, Header: make(http.Header), Body: io.NopCloser(bytes.NewReader(body)), ContentLength: int64(len(body)), Request: r}, nil
		})
		output := io.Writer(pipes.output)
		if mode == "stream" {
			output = &livenessProbeWriter{output: pipes.output, marker: marker}
		}
		_ = cache.checkAndStreamWithLimits(ctx, []ed25519.PublicKey{key}, "darwin", "arm64", "1.0.0", transport, 4096, 8<<20, &deadlineInput{ctx: ctx, file: pipes.input}, output)
	}
	if ctx.Err() == nil {
		select {
		case <-ctx.Done():
		case <-time.After(time.Second):
		}
	}
	canceled := ctx.Err() != nil
	cache.close()
	pipes.close()
	closeLive()
	if !canceled {
		os.Exit(44)
	}
	_ = os.WriteFile(finished, []byte("closed"), 0600)
	os.Exit(0)
}

func TestLivenessProbeRecovery(t *testing.T) {
	if os.Getenv("LYK_TEST_LIVENESS_RECOVER") != "1" {
		return
	}
	home, err := filepath.EvalSymlinks(os.Getenv("LYK_TEST_LIVENESS_HOME"))
	if err != nil {
		os.Exit(51)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	cache, err := openLockedCache(ctx, home, uint32(os.Geteuid()))
	if err != nil {
		os.Exit(52)
	}
	cache.mu.Lock()
	err = cache.recoverJournal()
	cache.mu.Unlock()
	cache.close()
	if err != nil {
		os.Exit(53)
	}
	os.Exit(0)
}
