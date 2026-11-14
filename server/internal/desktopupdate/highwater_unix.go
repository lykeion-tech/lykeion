//go:build darwin || linux

package desktopupdate

import (
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"os"
	"strconv"
	"strings"

	"golang.org/x/sys/unix"
)

const initializedMarker = "lykeion-desktop-update-v1\n"

func readPrivateFile(dir int, name string, uid uint32) ([]byte, bool, error) {
	fd, err := unix.Openat(dir, name, unix.O_RDONLY|unix.O_NONBLOCK|unix.O_NOFOLLOW|unix.O_CLOEXEC, 0)
	if errors.Is(err, unix.ENOENT) {
		return nil, false, nil
	}
	if err != nil {
		return nil, false, errors.New("update state unavailable")
	}
	f := os.NewFile(uintptr(fd), name)
	defer f.Close()
	var opened, entry unix.Stat_t
	if unix.Fstat(fd, &opened) != nil || unix.Fstatat(dir, name, &entry, unix.AT_SYMLINK_NOFOLLOW) != nil ||
		uint32(opened.Mode)&unix.S_IFMT != unix.S_IFREG || uint32(opened.Mode)&07777 != 0600 ||
		opened.Uid != uid || opened.Nlink != 1 || opened.Dev != entry.Dev || opened.Ino != entry.Ino ||
		opened.Size < 1 || opened.Size > 1024 || !privateACL(fd) {
		return nil, false, errors.New("update state unavailable")
	}
	contents, err := io.ReadAll(io.LimitReader(f, 1025))
	if err != nil || int64(len(contents)) != opened.Size || durableSync(fd) != nil {
		return nil, false, errors.New("update state unavailable")
	}
	return contents, true, nil
}

func writePrivateState(dir int, name string, uid uint32, contents []byte, syncDir func(int) error) error {
	var nonce [16]byte
	if _, err := rand.Read(nonce[:]); err != nil {
		return err
	}
	temp := ".state-" + hex.EncodeToString(nonce[:])
	fd, err := unix.Openat(dir, temp, unix.O_WRONLY|unix.O_CREAT|unix.O_EXCL|unix.O_NOFOLLOW|unix.O_CLOEXEC, 0600)
	if err != nil {
		return errors.New("update state unavailable")
	}
	f := os.NewFile(uintptr(fd), temp)
	defer f.Close()
	var created unix.Stat_t
	if unix.Fstat(fd, &created) != nil || created.Uid != uid || created.Nlink != 1 || uint32(created.Mode)&07777 != 0600 || !privateACL(fd) {
		return errors.New("update state unavailable")
	}
	defer func() {
		var current unix.Stat_t
		if unix.Fstatat(dir, temp, &current, unix.AT_SYMLINK_NOFOLLOW) == nil && current.Dev == created.Dev && current.Ino == created.Ino {
			_ = unix.Unlinkat(dir, temp, 0)
		}
	}()
	if n, err := f.Write(contents); err != nil || n != len(contents) {
		return errors.New("update state unavailable")
	}
	if durableSync(fd) != nil {
		return errors.New("update state unavailable")
	}
	var entry unix.Stat_t
	if unix.Fstatat(dir, temp, &entry, unix.AT_SYMLINK_NOFOLLOW) != nil || entry.Dev != created.Dev || entry.Ino != created.Ino {
		return errors.New("update state unavailable")
	}
	if unix.Renameat(dir, temp, dir, name) != nil || syncDir(dir) != nil {
		return errors.New("update state unavailable")
	}
	return nil
}

func (cache *lockedCache) commitHighWater(platform, arch string, sequence int64, digest [32]byte) error {
	if cache == nil {
		return errors.New("update state unavailable")
	}
	cache.mu.Lock()
	defer cache.mu.Unlock()
	return cache.commitHighWaterLocked(platform, arch, sequence, digest)
}

func (cache *lockedCache) readHighWater(platform, arch string) (int64, string, bool, error) {
	errUnsafe := errors.New("update state unavailable")
	if (platform != "darwin" && platform != "linux") || (arch != "x64" && arch != "arm64") {
		return 0, "", false, errUnsafe
	}
	state, hasState, err := readPrivateFile(cache.dir, "state-"+platform+"-"+arch, cache.uid)
	if err != nil {
		return 0, "", false, errUnsafe
	}
	marker, hasMarker, err := readPrivateFile(cache.dir, "initialized-"+platform+"-"+arch, cache.uid)
	if err != nil || hasState != hasMarker || (hasMarker && string(marker) != initializedMarker) {
		return 0, "", false, errUnsafe
	}
	if !hasState {
		return 0, "", false, nil
	}
	parts := strings.Split(strings.TrimSuffix(string(state), "\n"), " ")
	if len(parts) != 2 || len(parts[1]) != 64 {
		return 0, "", false, errUnsafe
	}
	prior, err := strconv.ParseInt(parts[0], 10, 64)
	if err != nil || prior < 1 || prior > maxSafeInteger || fmt.Sprintf("%d %s\n", prior, parts[1]) != string(state) {
		return 0, "", false, errUnsafe
	}
	if _, err := hex.DecodeString(parts[1]); err != nil || strings.ToLower(parts[1]) != parts[1] {
		return 0, "", false, errUnsafe
	}
	return prior, parts[1], true, nil
}

// Caller holds cache.mu across this operation or an enclosing download transaction.
func (cache *lockedCache) commitHighWaterLocked(platform, arch string, sequence int64, digest [32]byte) error {
	errUnsafe := errors.New("update state unavailable")
	if !cache.valid() {
		return errUnsafe
	}
	dir, uid := cache.dir, cache.uid
	if (platform != "darwin" && platform != "linux") || (arch != "x64" && arch != "arm64") || sequence < 1 || sequence > maxSafeInteger {
		return errUnsafe
	}
	stateName := "state-" + platform + "-" + arch
	markerName := "initialized-" + platform + "-" + arch
	prior, priorDigest, hasState, err := cache.readHighWater(platform, arch)
	if err != nil {
		return errUnsafe
	}
	if cache.syncDir == nil || cache.syncDir(dir) != nil {
		return errUnsafe
	}
	digestText := hex.EncodeToString(digest[:])
	if hasState {
		if sequence < prior || (sequence == prior && digestText != priorDigest) {
			return errors.New("update metadata is older than previously accepted")
		}
		if sequence == prior {
			return nil
		}
	}
	if !cache.valid() {
		return errUnsafe
	}
	if err := writePrivateState(dir, stateName, uid, []byte(fmt.Sprintf("%d %s\n", sequence, digestText)), cache.syncDir); err != nil {
		return errUnsafe
	}
	if !hasState {
		fd, err := unix.Openat(dir, markerName, unix.O_WRONLY|unix.O_CREAT|unix.O_EXCL|unix.O_NOFOLLOW|unix.O_CLOEXEC, 0600)
		if err != nil {
			return errUnsafe
		}
		f := os.NewFile(uintptr(fd), markerName)
		n, writeErr := f.WriteString(initializedMarker)
		syncErr := durableSync(fd)
		closeErr := f.Close()
		if writeErr != nil || n != len(initializedMarker) || syncErr != nil || closeErr != nil || cache.syncDir(dir) != nil {
			return errUnsafe
		}
		if _, present, err := readPrivateFile(dir, markerName, uid); err != nil || !present {
			return errUnsafe
		}
	}
	return nil
}
