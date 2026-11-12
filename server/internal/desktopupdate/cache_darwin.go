//go:build darwin

package desktopupdate

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"os"
	"strings"
	"time"

	"golang.org/x/sys/unix"
)

type cacheJournal struct {
	Version    int    `json:"v"`
	Token      string `json:"t"`
	Digest     string `json:"h"`
	Size       int64  `json:"s"`
	TempDev    uint64 `json:"td"`
	TempIno    uint64 `json:"ti"`
	ReserveDev uint64 `json:"rd"`
	ReserveIno uint64 `json:"ri"`
}

func isHex(value string, length int) bool {
	if len(value) != length || strings.ToLower(value) != value {
		return false
	}
	_, err := hex.DecodeString(value)
	return err == nil
}

func sameNamedFile(dir int, name string, info *unix.Stat_t) bool {
	var entry unix.Stat_t
	return unix.Fstatat(dir, name, &entry, unix.AT_SYMLINK_NOFOLLOW) == nil && entry.Dev == info.Dev && entry.Ino == info.Ino
}

func (cache *lockedCache) openOwned(name string, flags int) (*os.File, unix.Stat_t, error) {
	var info unix.Stat_t
	fd, err := unix.Openat(cache.dir, name, flags|unix.O_NOFOLLOW|unix.O_CLOEXEC|unix.O_NONBLOCK, 0600)
	if err != nil {
		if errors.Is(err, unix.ENOENT) {
			return nil, info, err
		}
		return nil, info, errors.New("update cache unavailable")
	}
	f := os.NewFile(uintptr(fd), name)
	if unix.Fstat(fd, &info) != nil || uint32(info.Mode)&unix.S_IFMT != unix.S_IFREG || uint32(info.Mode)&07777 != 0600 || info.Uid != cache.uid || info.Nlink != 1 || info.Size < 0 || info.Blocks < 0 || !privateACL(fd) || !sameNamedFile(cache.dir, name, &info) {
		f.Close()
		return nil, unix.Stat_t{}, errors.New("update cache unavailable")
	}
	return f, info, nil
}

func (cache *lockedCache) removeKnown(name string, expected unix.Stat_t) error {
	f, info, err := cache.openOwned(name, unix.O_RDONLY)
	if err != nil {
		return err
	}
	defer f.Close()
	if info.Dev != expected.Dev || info.Ino != expected.Ino || !sameNamedFile(cache.dir, name, &info) || unix.Unlinkat(cache.dir, name, 0) != nil {
		return errors.New("update cache unavailable")
	}
	return cache.syncDir(cache.dir)
}

func (cache *lockedCache) saveJournal(j cacheJournal) error {
	body, err := json.Marshal(j)
	if err != nil || len(body) > 1024 {
		return errors.New("update cache unavailable")
	}
	return writePrivateState(cache.dir, ".journal", cache.uid, body, cache.syncDir)
}

func (cache *lockedCache) recoverJournal() error {
	body, present, err := readPrivateFile(cache.dir, ".journal", cache.uid)
	if err != nil || !present {
		return err
	}
	var j cacheJournal
	if json.Unmarshal(body, &j) != nil || j.Version != 1 || !isHex(j.Token, 32) || !isHex(j.Digest, 64) || j.Size < 1 || j.Size > maxArtifact {
		return errors.New("update cache recovery required")
	}
	canonical, err := json.Marshal(j)
	if err != nil || string(canonical) != string(body) {
		return errors.New("update cache recovery required")
	}
	for _, entry := range []struct {
		name     string
		dev, ino uint64
	}{
		{".download-" + j.Token, j.TempDev, j.TempIno},
		{".reserve-" + j.Token, j.ReserveDev, j.ReserveIno},
	} {
		var stat unix.Stat_t
		if err := unix.Fstatat(cache.dir, entry.name, &stat, unix.AT_SYMLINK_NOFOLLOW); errors.Is(err, unix.ENOENT) {
			continue
		} else if err != nil || entry.dev == 0 || entry.ino == 0 || uint64(stat.Dev) != entry.dev || uint64(stat.Ino) != entry.ino {
			return errors.New("update cache recovery required")
		}
		if err := cache.removeKnown(entry.name, stat); err != nil {
			return errors.New("update cache recovery required")
		}
	}
	journal, info, err := cache.openOwned(".journal", unix.O_RDONLY)
	if err != nil {
		return err
	}
	journal.Close()
	if err := cache.removeKnown(".journal", info); err != nil {
		return errors.New("update cache recovery required")
	}
	return nil
}

func (cache *lockedCache) inventory(active *cacheJournal) (int64, error) {
	for _, platform := range []string{"darwin", "linux"} {
		for _, arch := range []string{"x64", "arm64"} {
			if _, _, _, err := cache.readHighWater(platform, arch); err != nil {
				return 0, errors.New("update cache recovery required")
			}
		}
	}
	fd, err := unix.Openat(cache.dir, ".", unix.O_RDONLY|unix.O_DIRECTORY|unix.O_NOFOLLOW|unix.O_CLOEXEC, 0)
	if err != nil {
		return 0, errors.New("update cache unavailable")
	}
	dir := os.NewFile(uintptr(fd), "update cache")
	defer dir.Close()
	names, err := dir.Readdirnames(maxCacheEntries + 1)
	if (err != nil && !errors.Is(err, io.EOF)) || len(names) > maxCacheEntries {
		return 0, errors.New("update cache unavailable")
	}
	var total int64
	var sawJournal, sawTemp, sawReserve bool
	for _, name := range names {
		known := name == ".lock" || name == "state-darwin-arm64" || name == "state-darwin-x64" || name == "state-linux-arm64" || name == "state-linux-x64" || name == "initialized-darwin-arm64" || name == "initialized-darwin-x64" || name == "initialized-linux-arm64" || name == "initialized-linux-x64" || (strings.HasPrefix(name, "artifact-") && isHex(strings.TrimPrefix(name, "artifact-"), 64))
		if active != nil && (name == ".journal" || name == ".download-"+active.Token || name == ".reserve-"+active.Token) {
			known = true
		}
		if !known {
			return 0, errors.New("update cache recovery required")
		}
		file, info, err := cache.openOwned(name, unix.O_RDONLY)
		if err != nil {
			return 0, err
		}
		if active != nil && name == ".journal" {
			sawJournal = true
			expected, err := json.Marshal(active)
			if err != nil || info.Size > 1024 {
				file.Close()
				return 0, errors.New("update cache recovery required")
			}
			actual, err := io.ReadAll(io.LimitReader(file, 1025))
			if err != nil || string(actual) != string(expected) {
				file.Close()
				return 0, errors.New("update cache recovery required")
			}
		}
		if active != nil && name == ".download-"+active.Token && (uint64(info.Dev) != active.TempDev || uint64(info.Ino) != active.TempIno) {
			file.Close()
			return 0, errors.New("update cache recovery required")
		}
		if active != nil && name == ".download-"+active.Token {
			sawTemp = true
		}
		if active != nil && name == ".reserve-"+active.Token && (uint64(info.Dev) != active.ReserveDev || uint64(info.Ino) != active.ReserveIno) {
			file.Close()
			return 0, errors.New("update cache recovery required")
		}
		if active != nil && name == ".reserve-"+active.Token {
			sawReserve = true
		}
		if strings.HasPrefix(name, "artifact-") {
			if info.Size > maxArtifact {
				file.Close()
				return 0, errors.New("update cache recovery required")
			}
			hash := sha256.New()
			_, err = io.Copy(hash, io.LimitReader(file, maxArtifact+1))
			if err != nil || hex.EncodeToString(hash.Sum(nil)) != strings.TrimPrefix(name, "artifact-") || info.Size > maxArtifact {
				file.Close()
				return 0, errors.New("update cache recovery required")
			}
		}
		file.Close()
		if info.Blocks > (1<<53)/512 {
			return 0, errors.New("update cache unavailable")
		}
		usage := info.Size
		if info.Blocks*512 > usage {
			usage = info.Blocks * 512
		}
		if usage > 1<<53-total-cacheEntryOverhead {
			return 0, errors.New("update cache unavailable")
		}
		total += usage + cacheEntryOverhead
	}
	if active != nil && (!sawJournal || !sawTemp || !sawReserve) {
		return 0, errors.New("update cache recovery required")
	}
	return total, nil
}

func (cache *lockedCache) storeArtifact(ctx context.Context, m metadata, openSource func(context.Context) (io.ReadCloser, error)) error {
	return cache.storeArtifactWithLimits(ctx, m, openSource, reserveBytes, maxCacheBytes)
}

// storeArtifactWithLimits is the common implementation; production uses only fixed limits above.
func (cache *lockedCache) storeArtifactWithLimits(ctx context.Context, m metadata, openSource func(context.Context) (io.ReadCloser, error), reserve, budget int64) error {
	if cache == nil {
		return errors.New("update cache unavailable")
	}
	cache.mu.Lock()
	defer cache.mu.Unlock()
	return cache.storeArtifactLocked(ctx, m, openSource, reserve, budget)
}

// Caller holds cache.mu, preserving the high-water decision through publication.
func (cache *lockedCache) storeArtifactLocked(ctx context.Context, m metadata, openSource func(context.Context) (io.ReadCloser, error), reserve, budget int64) error {
	errUnsafe := errors.New("update download unavailable")
	if ctx.Err() != nil || !cache.valid() || cache.now == nil || reserve < 1 || reserve > maxArtifact || budget < 1 || budget > maxCacheBytes || m.size < 1 || m.size > maxArtifact || !isHex(m.digest, 64) || !validMetadataTime(m.issued, m.expires, cache.now()) || openSource == nil {
		return errUnsafe
	}
	if err := cache.recoverJournal(); err != nil {
		return errUnsafe
	}
	used, err := cache.inventory(nil)
	if err != nil || used > budget {
		return errUnsafe
	}
	if err := cache.commitHighWaterLocked(m.platform, m.arch, m.sequence, m.payloadDigest); err != nil {
		return errUnsafe
	}
	// Inventory has validated the cache, but re-open and hash the specific file
	// before reusing it so its signed length and content are both checked.
	existing, info, err := cache.openOwned("artifact-"+m.digest, unix.O_RDONLY)
	if err == nil {
		hash := sha256.New()
		_, readErr := io.Copy(hash, io.LimitReader(existing, m.size+1))
		existing.Close()
		if readErr != nil || info.Size != m.size || hex.EncodeToString(hash.Sum(nil)) != m.digest || ctx.Err() != nil || !cache.valid() || !validMetadataTime(m.issued, m.expires, cache.now()) {
			return errUnsafe
		}
		return nil
	}
	if !errors.Is(err, unix.ENOENT) || m.size > budget-used || reserve > budget-used-m.size || 5*cacheEntryOverhead > budget-used-m.size-reserve {
		return errUnsafe
	}
	var nonce [16]byte
	if _, err := rand.Read(nonce[:]); err != nil {
		return errUnsafe
	}
	j := cacheJournal{Version: 1, Token: hex.EncodeToString(nonce[:]), Digest: m.digest, Size: m.size}
	if err := cache.saveJournal(j); err != nil {
		return errUnsafe
	}
	defer func() { _ = cache.recoverJournal() }()
	makeFile := func(name string) (*os.File, unix.Stat_t, error) {
		return cache.openOwned(name, unix.O_CREAT|unix.O_EXCL|unix.O_RDWR)
	}
	tmp, tmpInfo, err := makeFile(".download-" + j.Token)
	if err != nil {
		return errUnsafe
	}
	defer tmp.Close()
	j.TempDev, j.TempIno = uint64(tmpInfo.Dev), uint64(tmpInfo.Ino)
	if err := cache.saveJournal(j); err != nil {
		return errUnsafe
	}
	reserveFile, reserveInfo, err := makeFile(".reserve-" + j.Token)
	if err != nil {
		return errUnsafe
	}
	defer reserveFile.Close()
	j.ReserveDev, j.ReserveIno = uint64(reserveInfo.Dev), uint64(reserveInfo.Ino)
	if err := cache.saveJournal(j); err != nil {
		return errUnsafe
	}
	if preallocate(int(tmp.Fd()), m.size) != nil || preallocate(int(reserveFile.Fd()), reserve) != nil {
		return errUnsafe
	}
	if actual, err := cache.inventory(&j); err != nil || actual > budget {
		return errUnsafe
	}
	if ctx.Err() != nil || !cache.valid() {
		return errUnsafe
	}
	requestContext, cancel := context.WithTimeout(ctx, 30*time.Minute)
	defer cancel()
	source, err := openSource(requestContext)
	if err != nil {
		return errUnsafe
	}
	defer source.Close()
	stopWatcher := make(chan struct{})
	defer close(stopWatcher)
	go func() {
		select {
		case <-requestContext.Done():
			_ = source.Close()
		case <-stopWatcher:
		}
	}()
	hash := sha256.New()
	if n, err := io.CopyN(io.MultiWriter(tmp, hash), source, m.size); err != nil || n != m.size {
		return errUnsafe
	}
	var extra [1]byte
	if n, err := io.ReadFull(source, extra[:]); n != 0 || !errors.Is(err, io.EOF) {
		return errUnsafe
	}
	if requestContext.Err() != nil || hex.EncodeToString(hash.Sum(nil)) != m.digest || durableSync(int(tmp.Fd())) != nil || !cache.valid() || !validMetadataTime(m.issued, m.expires, cache.now()) {
		return errUnsafe
	}
	if actual, err := cache.inventory(&j); err != nil || actual > budget {
		return errUnsafe
	}
	if err := cache.commitHighWaterLocked(m.platform, m.arch, m.sequence, m.payloadDigest); err != nil {
		return errUnsafe
	}
	artifact := "artifact-" + m.digest
	if err := unix.RenameatxNp(cache.dir, ".download-"+j.Token, cache.dir, artifact, unix.RENAME_EXCL); err != nil {
		return errUnsafe
	}
	if cache.syncDir(cache.dir) != nil {
		return errUnsafe
	}
	if err := cache.removeKnown(".reserve-"+j.Token, reserveInfo); err != nil {
		return errUnsafe
	}
	journal, info, err := cache.openOwned(".journal", unix.O_RDONLY)
	if err != nil {
		return errUnsafe
	}
	journal.Close()
	if err := cache.removeKnown(".journal", info); err != nil {
		return errUnsafe
	}
	return nil
}
