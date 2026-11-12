//go:build darwin

package desktopupdate

import (
	"bufio"
	"bytes"
	"context"
	"crypto/ed25519"
	"crypto/sha256"
	"crypto/sha512"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestSignedSessionStreamsExactHeldArtifact(t *testing.T) {
	home, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	cache, err := openLockedCache(context.Background(), home, uint32(os.Geteuid()))
	if err != nil {
		t.Fatal(err)
	}
	defer cache.close()
	content := []byte("signed fixture artifact")
	digest := sha256.Sum256(content)
	now := time.Now()
	payload := []any{1, "stable", 7, "1.2.3", now.Unix(), now.Add(time.Hour).Unix(), "darwin", "arm64", "Lykeion-1.2.3-arm64-mac.zip", len(content), hex.EncodeToString(digest[:])}
	envelope, key := signedEnvelope(t, payload)
	requests := 0
	transport := roundTripFunc(func(r *http.Request) (*http.Response, error) {
		requests++
		body := content
		if requests == 1 {
			body = envelope
		}
		return &http.Response{StatusCode: 200, Header: make(http.Header), Body: io.NopCloser(bytes.NewReader(body)), ContentLength: int64(len(body)), Request: r}, nil
	})
	var output bytes.Buffer
	err = cache.checkAndStreamWithLimits(context.Background(), []ed25519.PublicKey{key}, "darwin", "arm64", "1.0.0", transport, 4096, 1<<20, bytes.NewBufferString("STREAM\n"), &output)
	if err != nil {
		t.Fatal(err)
	}
	line, err := output.ReadBytes('\n')
	if err != nil {
		t.Fatal(err)
	}
	var descriptor streamDescriptor
	if err := json.Unmarshal(line, &descriptor); err != nil {
		t.Fatal(err)
	}
	hash512 := sha512.Sum512(content)
	if descriptor.State != "downloaded" || descriptor.Version != "1.2.3" || descriptor.Size != int64(len(content)) || descriptor.SHA256 != hex.EncodeToString(digest[:]) || descriptor.SHA512 != base64.StdEncoding.EncodeToString(hash512[:]) {
		t.Fatalf("descriptor %+v", descriptor)
	}
	if !bytes.Equal(output.Bytes(), append(content, byte(1))) {
		t.Fatal("invalid stream framing")
	}
}

type sessionHookWriter struct {
	bytes.Buffer
	hook func()
}

func (w *sessionHookWriter) Write(p []byte) (int, error) {
	n, err := w.Buffer.Write(p)
	if w.hook != nil {
		hook := w.hook
		w.hook = nil
		hook()
	}
	return n, err
}

func TestSignedSessionRejectsMutationAndInvalidRequest(t *testing.T) {
	for _, scenario := range []string{"mutation", "truncation", "substitution", "expired-after-descriptor", "bad-request", "expired", "bad-signature"} {
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
			content := []byte("verified stream content")
			digest := sha256.Sum256(content)
			now := time.Now()
			issued, expiry := now.Unix(), now.Add(time.Hour).Unix()
			if scenario == "expired" {
				issued, expiry = now.Add(-2*time.Hour).Unix(), now.Add(-time.Hour).Unix()
			}
			payload := []any{1, "stable", 7, "1.2.3", issued, expiry, "darwin", "arm64", "Lykeion-1.2.3-arm64-mac.zip", len(content), hex.EncodeToString(digest[:])}
			envelope, key := signedEnvelope(t, payload)
			if scenario == "bad-signature" {
				envelope[len(envelope)/2] ^= 1
			}
			requests := 0
			transport := roundTripFunc(func(r *http.Request) (*http.Response, error) {
				requests++
				body := content
				if requests == 1 {
					body = envelope
				}
				return &http.Response{StatusCode: 200, Header: make(http.Header), Body: io.NopCloser(bytes.NewReader(body)), ContentLength: int64(len(body)), Request: r}, nil
			})
			artifact := filepath.Join(home, ".config", "lyk", "profiles", "desktop-updates-v1", "artifact-"+hex.EncodeToString(digest[:]))
			output := &sessionHookWriter{}
			switch scenario {
			case "mutation":
				output.hook = func() {
					if err := os.WriteFile(artifact, bytes.Repeat([]byte{'X'}, len(content)), 0600); err != nil {
						t.Error(err)
					}
				}
			case "truncation":
				output.hook = func() {
					if err := os.Truncate(artifact, 1); err != nil {
						t.Error(err)
					}
				}
			case "expired-after-descriptor":
				output.hook = func() { cache.now = func() time.Time { return now.Add(2 * time.Hour) } }
			case "substitution":
				output.hook = func() {
					if err := os.Rename(artifact, artifact+".old"); err != nil {
						t.Error(err)
					}
					if err := os.WriteFile(artifact, content, 0600); err != nil {
						t.Error(err)
					}
				}
			}
			command := "STREAM\n"
			if scenario == "bad-request" {
				command = "STREAM\nX"
			}
			err = cache.checkAndStreamWithLimits(context.Background(), []ed25519.PublicKey{key}, "darwin", "arm64", "1.0.0", transport, 4096, 1<<20, bytes.NewBufferString(command), output)
			if err == nil {
				t.Fatal("unsafe session accepted")
			}
			data := output.Bytes()
			if len(data) > 0 && data[len(data)-1] == 1 {
				t.Fatal("unsafe session emitted success trailer")
			}
			if (scenario == "expired" || scenario == "bad-signature") && len(data) != 0 {
				t.Fatal("unverified session emitted descriptor")
			}
		})
	}
}

func TestSessionChild(t *testing.T) {
	if os.Getenv("LYK_TEST_SESSION_CHILD") != "1" {
		return
	}
	home := os.Getenv("LYK_TEST_SESSION_HOME")
	cache, err := openLockedCache(context.Background(), home, uint32(os.Geteuid()))
	if err != nil {
		t.Fatal(err)
	}
	defer cache.close()
	content := []byte("x")
	digest := sha256.Sum256(content)
	now := time.Now()
	envelope, key := signedEnvelope(t, []any{1, "stable", 7, "1.2.3", now.Unix(), now.Add(time.Hour).Unix(), "darwin", "arm64", "Lykeion-1.2.3-arm64-mac.zip", 1, hex.EncodeToString(digest[:])})
	requests := 0
	transport := roundTripFunc(func(r *http.Request) (*http.Response, error) {
		requests++
		body := content
		if requests == 1 {
			body = envelope
		}
		return &http.Response{StatusCode: 200, Header: make(http.Header), Body: io.NopCloser(bytes.NewReader(body)), ContentLength: int64(len(body)), Request: r}, nil
	})
	if err := cache.checkAndStreamWithLimits(context.Background(), []ed25519.PublicKey{key}, "darwin", "arm64", "1.0.0", transport, 4096, 1<<20, os.Stdin, os.Stdout); err != nil {
		t.Fatal(err)
	}
	os.Exit(0)
}

func TestSessionOneByteSubprocess(t *testing.T) {
	home, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	cmd := exec.Command(os.Args[0], "-test.run=^TestSessionChild$")
	cmd.Env = append(os.Environ(), "LYK_TEST_SESSION_CHILD=1", "LYK_TEST_SESSION_HOME="+home)
	cmd.Stdin = strings.NewReader("STREAM\n")
	data, err := cmd.Output()
	if err != nil {
		t.Fatal(err)
	}
	separator := bytes.IndexByte(data, '\n')
	if separator < 0 {
		t.Fatal("missing descriptor")
	}
	var descriptor streamDescriptor
	if json.Unmarshal(data[:separator], &descriptor) != nil || descriptor.Size != 1 || !bytes.Equal(data[separator+1:], []byte{'x', 1}) {
		t.Fatal("invalid one-byte session")
	}
}

func TestSessionProcessDeathReleasesCacheLock(t *testing.T) {
	home, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	cmd := exec.Command(os.Args[0], "-test.run=^TestSessionChild$")
	cmd.Env = append(os.Environ(), "LYK_TEST_SESSION_CHILD=1", "LYK_TEST_SESSION_HOME="+home)
	input, err := cmd.StdinPipe()
	if err != nil {
		t.Fatal(err)
	}
	defer input.Close()
	output, err := cmd.StdoutPipe()
	if err != nil {
		t.Fatal(err)
	}
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	done := false
	defer func() {
		if !done {
			_ = cmd.Process.Kill()
			_ = cmd.Wait()
		}
	}()
	line, err := bufio.NewReader(io.LimitReader(output, 1024)).ReadString('\n')
	if err != nil {
		t.Fatalf("descriptor missing: %v", err)
	}
	if !strings.Contains(line, `"size":1`) {
		t.Fatalf("unexpected descriptor %q", line)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 150*time.Millisecond)
	defer cancel()
	if other, err := openLockedCache(ctx, home, uint32(os.Geteuid())); err == nil {
		other.close()
		t.Fatal("session did not retain lock")
	}
	if err := cmd.Process.Kill(); err != nil {
		t.Fatal(err)
	}
	_ = cmd.Wait()
	done = true
	other, err := openLockedCache(context.Background(), home, uint32(os.Geteuid()))
	if err != nil {
		t.Fatalf("dead session retained lock: %v", err)
	}
	other.close()
	if strings.Contains(stderr.String(), home) {
		t.Fatal("session leaked private path")
	}
}
