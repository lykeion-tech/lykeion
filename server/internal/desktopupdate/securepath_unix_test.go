//go:build darwin || linux

package desktopupdate

import (
	"context"
	"crypto/sha256"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"testing"
	"time"

	"golang.org/x/sys/unix"
)

func TestPrivateDirectoryRejectsSymlinkAndUnsafeOwnerMode(t *testing.T) {
	home := t.TempDir()
	resolved, err := filepath.EvalSymlinks(home)
	if err != nil {
		t.Fatal(err)
	}
	if fd, err := openPrivateDirectory(resolved, uint32(os.Geteuid()+1)); err == nil {
		unix.Close(fd)
		t.Fatal("accepted a forged user ID")
	}
	if runtime.GOOS == "linux" {
		if fd, err := openPrivateDirectory(resolved, uint32(os.Geteuid())); err == nil {
			unix.Close(fd)
			t.Fatal("enabled an unverified Linux filesystem")
		}
		return
	}
	fd, err := openPrivateDirectory(resolved, uint32(os.Geteuid()))
	if err != nil {
		t.Fatalf("create private update directory: %v", err)
	}
	unix.Close(fd)
	leaf := filepath.Join(resolved, ".config", "lyk", "profiles", "desktop-updates-v1")
	if err := os.Chmod(leaf, 0777); err != nil {
		t.Fatal(err)
	}
	if fd, err := openPrivateDirectory(resolved, uint32(os.Geteuid())); err == nil {
		unix.Close(fd)
		t.Fatal("accepted writable private directory")
	}
	if err := os.Chmod(leaf, 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(leaf); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(t.TempDir(), leaf); err != nil {
		t.Fatal(err)
	}
	if fd, err := openPrivateDirectory(resolved, uint32(os.Geteuid())); err == nil {
		unix.Close(fd)
		t.Fatal("accepted symlinked private directory")
	}
}

func TestPrivateDirectoryRetryResyncsExistingAncestor(t *testing.T) {
	if runtime.GOOS != "darwin" {
		t.Skip("Linux storage remains disabled")
	}
	home, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	var expected unix.Stat_t
	if err := unix.Stat(home, &expected); err != nil {
		t.Fatal(err)
	}
	failHome := func(fd int) error {
		var current unix.Stat_t
		if unix.Fstat(fd, &current) != nil {
			return errors.New("stat failed")
		}
		if current.Dev == expected.Dev && current.Ino == expected.Ino {
			if _, err := os.Stat(filepath.Join(home, ".config")); err == nil {
				return errors.New("injected parent sync failure")
			}
		}
		return durableSync(fd)
	}
	if fd, err := openPrivateDirectoryWithSync(home, uint32(os.Geteuid()), failHome); err == nil {
		unix.Close(fd)
		t.Fatal("accepted failed parent sync")
	}
	if _, err := os.Stat(filepath.Join(home, ".config")); err != nil {
		t.Fatalf("fixture did not create directory: %v", err)
	}
	if fd, err := openPrivateDirectoryWithSync(home, uint32(os.Geteuid()), failHome); err == nil {
		unix.Close(fd)
		t.Fatal("retry did not re-sync existing parent")
	}
	fd, err := openPrivateDirectory(home, uint32(os.Geteuid()))
	if err != nil {
		t.Fatalf("recovery after sync restored: %v", err)
	}
	unix.Close(fd)
}

func TestHighWaterCrossProcessLockAndCrash(t *testing.T) {
	if runtime.GOOS != "darwin" {
		t.Skip("Linux storage remains disabled")
	}
	if scenario := os.Getenv("LYK_UPDATE_TEST_SCENARIO"); scenario != "" {
		cache, err := openLockedCache(context.Background(), os.Getenv("LYK_UPDATE_TEST_HOME"), uint32(os.Geteuid()))
		if err != nil {
			t.Fatal(err)
		}
		switch scenario {
		case "older":
			defer cache.close()
			digest := sha256.Sum256([]byte("8"))
			if err := cache.commitHighWater("darwin", "arm64", 8, digest); err == nil {
				t.Fatal("accepted older sequence")
			}
		case "crash":
			if err := writePrivateState(cache.dir, "state-darwin-arm64", cache.uid, []byte("7 "+strings.Repeat("a", 64)+"\n"), cache.syncDir); err != nil {
				t.Fatal(err)
			}
			os.Exit(0)
		default:
			t.Fatal("unknown test scenario")
		}
		return
	}
	newHome := func() string {
		home, err := filepath.EvalSymlinks(t.TempDir())
		if err != nil {
			t.Fatal(err)
		}
		return home
	}
	child := func(home, scenario string) *exec.Cmd {
		command := exec.Command(os.Args[0], "-test.run=^TestHighWaterCrossProcessLockAndCrash$", "-test.timeout=10s")
		command.Env = append(os.Environ(), "LYK_UPDATE_TEST_HOME="+home, "LYK_UPDATE_TEST_SCENARIO="+scenario)
		return command
	}
	home := newHome()
	cache, err := openLockedCache(context.Background(), home, uint32(os.Geteuid()))
	if err != nil {
		t.Fatal(err)
	}
	digest := sha256.Sum256([]byte("9"))
	if err := cache.commitHighWater("darwin", "arm64", 9, digest); err != nil {
		t.Fatal(err)
	}
	process := child(home, "older")
	if err := process.Start(); err != nil {
		t.Fatal(err)
	}
	done := make(chan error, 1)
	go func() { done <- process.Wait() }()
	select {
	case err := <-done:
		cache.close()
		t.Fatalf("child did not wait for OS lock: %v", err)
	case <-time.After(100 * time.Millisecond):
	}
	cache.close()
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("child failed after lock release: %v", err)
		}
	case <-time.After(5 * time.Second):
		_ = process.Process.Kill()
		t.Fatal("child stayed blocked after lock release")
	}
	crashHome := newHome()
	if out, err := child(crashHome, "crash").CombinedOutput(); err != nil {
		t.Fatalf("crash fixture: %v %s", err, out)
	}
	crashCache, err := openLockedCache(context.Background(), crashHome, uint32(os.Geteuid()))
	if err != nil {
		t.Fatalf("OS lock did not release on process exit: %v", err)
	}
	defer crashCache.close()
	if err := crashCache.commitHighWater("darwin", "arm64", 9, digest); err == nil {
		t.Fatal("accepted state created without initialized sentinel")
	}
}

func TestHighWaterRejectsRollbackAndMissingSentinel(t *testing.T) {
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
	a, b := sha256.Sum256([]byte("first")), sha256.Sum256([]byte("second"))
	if err := cache.commitHighWater("darwin", "arm64", 7, a); err != nil {
		t.Fatal(err)
	}
	if err := cache.commitHighWater("darwin", "arm64", 7, a); err != nil {
		t.Fatal(err)
	}
	if err := cache.commitHighWater("darwin", "arm64", 6, a); err == nil {
		t.Fatal("accepted rollback")
	}
	if err := cache.commitHighWater("darwin", "arm64", 7, b); err == nil {
		t.Fatal("accepted sequence equivocation")
	}
	if err := cache.commitHighWater("darwin", "arm64", 8, b); err != nil {
		t.Fatal(err)
	}
	if err := unix.Unlinkat(cache.dir, "initialized-darwin-arm64", 0); err != nil {
		t.Fatal(err)
	}
	if err := cache.commitHighWater("darwin", "arm64", 9, b); err == nil {
		t.Fatal("accepted missing initialized sentinel")
	}
}

func TestHighWaterRetrySyncsAfterUncertainRename(t *testing.T) {
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
	a, b := sha256.Sum256([]byte("first")), sha256.Sum256([]byte("second"))
	if err := cache.commitHighWater("darwin", "arm64", 7, a); err != nil {
		t.Fatal(err)
	}
	calls := 0
	cache.syncDir = func(fd int) error {
		calls++
		if calls == 2 {
			return errors.New("injected post-rename sync failure")
		}
		return durableSync(fd)
	}
	if err := cache.commitHighWater("darwin", "arm64", 8, b); err == nil {
		t.Fatal("accepted failed post-rename sync")
	}
	calls = 0
	cache.syncDir = func(fd int) error { calls++; return durableSync(fd) }
	if err := cache.commitHighWater("darwin", "arm64", 8, b); err != nil {
		t.Fatalf("durable retry: %v", err)
	}
	if calls == 0 {
		t.Fatal("same-sequence retry did not sync directory")
	}
}

func TestHighWaterSerializesConcurrentCommitsAndClose(t *testing.T) {
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
	a, b, c := sha256.Sum256([]byte("7")), sha256.Sum256([]byte("8")), sha256.Sum256([]byte("9"))
	if err := cache.commitHighWater("darwin", "arm64", 7, a); err != nil {
		t.Fatal(err)
	}
	started, release := make(chan struct{}), make(chan struct{})
	var once sync.Once
	cache.syncDir = func(fd int) error { once.Do(func() { close(started); <-release }); return durableSync(fd) }
	highDone := make(chan error, 1)
	go func() { highDone <- cache.commitHighWater("darwin", "arm64", 9, c) }()
	<-started
	lowDone := make(chan error, 1)
	go func() { lowDone <- cache.commitHighWater("darwin", "arm64", 8, b) }()
	closeDone := make(chan struct{})
	go func() { cache.close(); close(closeDone) }()
	select {
	case <-lowDone:
		close(release)
		t.Fatal("concurrent lower sequence was admitted")
	case <-closeDone:
		close(release)
		t.Fatal("cache closed during active commit")
	case <-time.After(100 * time.Millisecond):
	}
	close(release)
	if err := <-highDone; err != nil {
		t.Fatalf("high sequence failed: %v", err)
	}
	if err := <-lowDone; err == nil {
		t.Fatal("lower sequence accepted after high commit")
	}
	<-closeDone
}

func TestCacheLockRejectsLinkedLockAndHonorsCancellation(t *testing.T) {
	if runtime.GOOS != "darwin" {
		t.Skip("Linux private storage remains disabled until real-host validation")
	}
	home, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	fd, err := openPrivateDirectory(home, uint32(os.Geteuid()))
	if err != nil {
		t.Fatal(err)
	}
	defer unix.Close(fd)
	alreadyCanceled, stop := context.WithCancel(context.Background())
	stop()
	if lock, err := acquireCacheLock(alreadyCanceled, fd, uint32(os.Geteuid())); err == nil {
		unix.Close(lock)
		t.Fatal("already-canceled lock was admitted")
	}
	lock, err := acquireCacheLock(context.Background(), fd, uint32(os.Geteuid()))
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()
	if other, err := acquireCacheLock(ctx, fd, uint32(os.Geteuid())); err == nil {
		unix.Close(other)
		t.Fatal("second lock was admitted")
	}
	unix.Close(lock)
	leaf := filepath.Join(home, ".config", "lyk", "profiles", "desktop-updates-v1")
	if err := os.Link(filepath.Join(leaf, ".lock"), filepath.Join(leaf, "linked")); err != nil {
		t.Fatal(err)
	}
	if lock, err := acquireCacheLock(context.Background(), fd, uint32(os.Geteuid())); err == nil {
		unix.Close(lock)
		t.Fatal("hardlinked lock was accepted")
	}
}

func TestPreallocationRequiresNewEmptyFile(t *testing.T) {
	if runtime.GOOS != "darwin" {
		t.Skip("Linux reservation remains disabled until real-host validation")
	}
	file, err := os.CreateTemp(t.TempDir(), "reserve-")
	if err != nil {
		t.Fatal(err)
	}
	defer file.Close()
	if err := preallocate(int(file.Fd()), 1<<20); err != nil {
		t.Fatalf("reserve storage: %v", err)
	}
	var stat unix.Stat_t
	if err := unix.Fstat(int(file.Fd()), &stat); err != nil {
		t.Fatal(err)
	}
	if stat.Size != 1<<20 || stat.Blocks*512 < 1<<20 {
		t.Fatalf("storage was not allocated: size %d blocks %d", stat.Size, stat.Blocks)
	}
	if err := preallocate(int(file.Fd()), 1<<20); err == nil {
		t.Fatal("accepted a nonempty file")
	}
}
