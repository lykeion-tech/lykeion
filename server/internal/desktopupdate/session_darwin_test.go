//go:build darwin

package desktopupdate

import (
	"bufio"
	"bytes"
	"context"
	"crypto/ed25519"
	"crypto/rand"
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
	"runtime"
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

// Test-only executable path: real liveness, pipes, signed metadata and cache
// session with ephemeral trust and an in-memory transport. Production gates are untouched.
func TestPackagedStreamChild(t *testing.T) {
	mode := os.Getenv("LYK_TEST_PACKAGED_STREAM")
	if mode == "" {
		return
	}
	home, err := filepath.EvalSymlinks(os.Getenv("LYK_TEST_PACKAGED_HOME"))
	if err != nil {
		os.Exit(61)
	}
	ctx, closeLive, err := startLiveness(context.Background(), 3, int(os.Stdin.Fd()), int(os.Stdout.Fd()))
	if err != nil {
		os.Exit(62)
	}
	pipes, err := openStreamPipes(ctx, os.Stdin, os.Stdout)
	if err != nil {
		closeLive()
		os.Exit(63)
	}
	cache, err := openLockedCache(ctx, home, uint32(os.Geteuid()))
	if err != nil {
		pipes.close()
		closeLive()
		os.Exit(64)
	}
	content := []byte("small verified fixture artifact")
	sum := sha256.Sum256(content)
	now := time.Now()
	arch, filename := "arm64", "Lykeion-1.2.3-arm64-mac.zip"
	if runtime.GOARCH == "amd64" {
		arch, filename = "x64", "Lykeion-1.2.3-mac.zip"
	}
	envelope, key := signedEnvelope(t, []any{1, "stable", 7, "1.2.3", now.Unix(), now.Add(time.Hour).Unix(), "darwin", arch, filename, len(content), hex.EncodeToString(sum[:])})
	if mode == "invalid" {
		wrongKey, _, keyErr := ed25519.GenerateKey(rand.Reader)
		if keyErr != nil {
			os.Exit(66)
		}
		if _, err := verifyMetadata(envelope, []ed25519.PublicKey{key}, "darwin", arch, now); err != nil {
			os.Exit(67)
		}
		if _, err := verifyMetadata(envelope, []ed25519.PublicKey{wrongKey}, "darwin", arch, now); err == nil {
			os.Exit(68)
		}
		key = wrongKey
	}
	requests := 0
	transport := roundTripFunc(func(r *http.Request) (*http.Response, error) {
		requests++
		if mode == "invalid" && requests == 1 {
			_ = os.WriteFile(os.Getenv("LYK_TEST_PACKAGED_READY"), []byte("signature-rejected"), 0600)
		}
		if mode == "liveness" && requests == 1 {
			_ = os.WriteFile(os.Getenv("LYK_TEST_PACKAGED_READY"), []byte("ready"), 0600)
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
	if mode == "mutation" {
		artifact := filepath.Join(home, ".config", "lyk", "profiles", "desktop-updates-v1", "artifact-"+hex.EncodeToString(sum[:]))
		output = &sessionMutatingPipe{output: pipes.output, artifact: artifact, size: len(content)}
	}
	if mode == "blocked-stream" {
		output = &sessionBlockingPipe{ctx: ctx, output: pipes.output, marker: os.Getenv("LYK_TEST_PACKAGED_READY")}
	}
	err = cache.checkAndStreamWithLimits(ctx, []ed25519.PublicKey{key}, "darwin", arch, "1.0.0", transport, 4096, 1<<20, &deadlineInput{ctx: ctx, file: pipes.input}, output)
	cache.close()
	pipes.close()
	closeLive()
	if err != nil {
		os.Exit(65)
	}
	os.Exit(0)
}

type sessionBlockingPipe struct {
	ctx    context.Context
	output io.Writer
	marker string
	calls  int
}

func (w *sessionBlockingPipe) Write(p []byte) (int, error) {
	w.calls++
	if w.calls == 1 {
		return w.output.Write(p)
	}
	_ = os.WriteFile(w.marker, []byte("ready"), 0600)
	<-w.ctx.Done()
	return 0, w.ctx.Err()
}

type sessionMutatingPipe struct {
	output   io.Writer
	artifact string
	size     int
	mutated  bool
}

func (w *sessionMutatingPipe) Write(p []byte) (int, error) {
	n, err := w.output.Write(p)
	if !w.mutated {
		w.mutated = true
		_ = os.WriteFile(w.artifact, bytes.Repeat([]byte{'X'}, w.size), 0600)
	}
	return n, err
}
