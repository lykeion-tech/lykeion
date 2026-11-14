//go:build darwin

package desktopupdate

import (
	"context"
	"crypto/ed25519"
	"crypto/sha256"
	"crypto/sha512"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"net/http"

	"golang.org/x/sys/unix"
)

type streamDescriptor struct {
	State   string `json:"state"`
	Version string `json:"version"`
	Size    int64  `json:"size,omitempty"`
	SHA256  string `json:"sha256,omitempty"`
	SHA512  string `json:"sha512,omitempty"`
}

type contextReader struct {
	ctx    context.Context
	reader io.Reader
}

func (r contextReader) Read(p []byte) (int, error) {
	if err := r.ctx.Err(); err != nil {
		return 0, err
	}
	return r.reader.Read(p)
}

// The lock is retained through the descriptor, request, full stream and trailer.
func (cache *lockedCache) checkAndStreamWithLimits(ctx context.Context, keys []ed25519.PublicKey, platform, arch, current string, transport http.RoundTripper, reserve, budget int64, input io.Reader, output io.Writer) error {
	bad := errors.New("update stream unavailable")
	if cache == nil || input == nil || output == nil {
		return bad
	}
	cache.mu.Lock()
	defer cache.mu.Unlock()
	result, m, err := cache.checkAndCacheLocked(ctx, keys, platform, arch, current, transport, reserve, budget)
	if err != nil {
		return bad
	}
	if result.State == "current" {
		if ctx.Err() != nil || !cache.valid() || !validMetadataTime(m.issued, m.expires, cache.now()) {
			return bad
		}
		return json.NewEncoder(output).Encode(streamDescriptor{State: "current", Version: result.Version})
	}
	name := "artifact-" + m.digest
	f, before, err := cache.openOwned(name, unix.O_RDONLY)
	if err != nil {
		return bad
	}
	defer f.Close()
	if before.Size != m.size || m.size < 1 || m.size > maxArtifact {
		return bad
	}
	h256, h512 := sha256.New(), sha512.New()
	if n, err := io.CopyN(io.MultiWriter(h256, h512), contextReader{ctx, f}, m.size); err != nil || n != m.size || hex.EncodeToString(h256.Sum(nil)) != m.digest {
		return bad
	}
	if _, err = f.Seek(0, io.SeekStart); err != nil || !sessionArtifactValid(cache, m, name, int(f.Fd()), before) {
		return bad
	}
	d := streamDescriptor{State: "downloaded", Version: m.version, Size: m.size, SHA256: m.digest, SHA512: base64.StdEncoding.EncodeToString(h512.Sum(nil))}
	encoded, err := json.Marshal(d)
	if err != nil || len(encoded) > 1023 {
		return bad
	}
	encoded = append(encoded, '\n')
	if n, err := output.Write(encoded); err != nil || n != len(encoded) {
		return bad
	}
	request, err := io.ReadAll(io.LimitReader(contextReader{ctx, input}, 8))
	if err != nil || string(request) != "STREAM\n" || ctx.Err() != nil {
		return bad
	}
	h256, h512 = sha256.New(), sha512.New()
	if n, err := io.CopyN(io.MultiWriter(output, h256, h512), contextReader{ctx, f}, m.size); err != nil || n != m.size || ctx.Err() != nil || hex.EncodeToString(h256.Sum(nil)) != m.digest || base64.StdEncoding.EncodeToString(h512.Sum(nil)) != d.SHA512 || !sessionArtifactValid(cache, m, name, int(f.Fd()), before) {
		return bad
	}
	if n, err := output.Write([]byte{1}); err != nil || n != 1 {
		return bad
	}
	return nil
}

func sessionArtifactValid(cache *lockedCache, m metadata, name string, fd int, before unix.Stat_t) bool {
	var after unix.Stat_t
	if !cache.valid() || !validMetadataTime(m.issued, m.expires, cache.now()) || unix.Fstat(fd, &after) != nil || after.Dev != before.Dev || after.Ino != before.Ino || after.Mode != before.Mode || after.Uid != before.Uid || after.Nlink != 1 || after.Size != m.size || after.Mtim != before.Mtim || after.Ctim != before.Ctim || !privateACL(fd) || !sameNamedFile(cache.dir, name, &before) {
		return false
	}
	sequence, digest, present, err := cache.readHighWater(m.platform, m.arch)
	return err == nil && present && sequence == m.sequence && digest == hex.EncodeToString(m.payloadDigest[:])
}
