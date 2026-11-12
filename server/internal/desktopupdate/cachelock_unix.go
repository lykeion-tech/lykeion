//go:build darwin || linux

package desktopupdate

import (
	"context"
	"errors"
	"os"
	"sync"
	"time"

	"golang.org/x/sys/unix"
)

type lockedCache struct {
	mu        sync.Mutex
	dir, lock int
	uid       uint32
	syncDir   func(int) error
	now       func() time.Time
}

func openLockedCache(ctx context.Context, home string, uid uint32) (*lockedCache, error) {
	dir, err := openPrivateDirectory(home, uid)
	if err != nil {
		return nil, err
	}
	lock, err := acquireCacheLock(ctx, dir, uid)
	if err != nil {
		unix.Close(dir)
		return nil, err
	}
	return &lockedCache{dir: dir, lock: lock, uid: uid, syncDir: durableSync, now: time.Now}, nil
}

func (cache *lockedCache) close() {
	if cache == nil {
		return
	}
	cache.mu.Lock()
	defer cache.mu.Unlock()
	if cache.lock >= 0 {
		_ = unix.Close(cache.lock)
		cache.lock = -1
	}
	if cache.dir >= 0 {
		_ = unix.Close(cache.dir)
		cache.dir = -1
	}
}

func (cache *lockedCache) valid() bool {
	if cache == nil || cache.dir < 0 || cache.lock < 0 || cache.uid == 0 || int(cache.uid) != os.Geteuid() {
		return false
	}
	var dir, opened, entry unix.Stat_t
	if unix.Fstat(cache.dir, &dir) != nil || uint32(dir.Mode)&unix.S_IFMT != unix.S_IFDIR || dir.Uid != cache.uid || uint32(dir.Mode)&07777 != 0700 || !privateACL(cache.dir) || !privateFilesystem(cache.dir) {
		return false
	}
	if unix.Fstat(cache.lock, &opened) != nil || unix.Fstatat(cache.dir, ".lock", &entry, unix.AT_SYMLINK_NOFOLLOW) != nil || uint32(opened.Mode)&unix.S_IFMT != unix.S_IFREG || uint32(opened.Mode)&07777 != 0600 || opened.Uid != cache.uid || opened.Nlink != 1 || opened.Dev != entry.Dev || opened.Ino != entry.Ino || !privateACL(cache.lock) {
		return false
	}
	return unix.Flock(cache.lock, unix.LOCK_EX|unix.LOCK_NB) == nil
}

func acquireCacheLock(ctx context.Context, directory int, uid uint32) (int, error) {
	errUnsafe := errors.New("private update cache lock unavailable")
	if uid == 0 || int(uid) != os.Geteuid() {
		return -1, errUnsafe
	}
	if err := ctx.Err(); err != nil {
		return -1, err
	}
	fd, err := unix.Openat(directory, ".lock", unix.O_CREAT|unix.O_RDWR|unix.O_NOFOLLOW|unix.O_CLOEXEC, 0600)
	if err != nil {
		return -1, errUnsafe
	}
	valid := func() bool {
		var openInfo, entryInfo unix.Stat_t
		if unix.Fstat(fd, &openInfo) != nil || unix.Fstatat(directory, ".lock", &entryInfo, unix.AT_SYMLINK_NOFOLLOW) != nil {
			return false
		}
		mode := uint32(openInfo.Mode)
		return mode&unix.S_IFMT == unix.S_IFREG && mode&07777 == 0600 && openInfo.Uid == uid && openInfo.Nlink == 1 && openInfo.Dev == entryInfo.Dev && openInfo.Ino == entryInfo.Ino && privateACL(fd)
	}
	if !valid() {
		unix.Close(fd)
		return -1, errUnsafe
	}
	for {
		if err := unix.Flock(fd, unix.LOCK_EX|unix.LOCK_NB); err == nil {
			if ctx.Err() != nil {
				unix.Close(fd)
				return -1, ctx.Err()
			}
			if !valid() {
				unix.Close(fd)
				return -1, errUnsafe
			}
			return fd, nil
		} else if !errors.Is(err, unix.EWOULDBLOCK) && !errors.Is(err, unix.EAGAIN) {
			unix.Close(fd)
			return -1, errUnsafe
		}
		timer := time.NewTimer(25 * time.Millisecond)
		select {
		case <-ctx.Done():
			if !timer.Stop() {
				<-timer.C
			}
			unix.Close(fd)
			return -1, ctx.Err()
		case <-timer.C:
		}
	}
}
