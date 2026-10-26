//go:build darwin || linux

// Package localfiles handles Coresearch chat files without following symlinks.
package localfiles

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"strings"
	"time"

	"golang.org/x/sys/unix"
)

type Folder struct{ dir *os.File }

func Open(folder string) (*Folder, error) {
	root, err := os.OpenRoot(folder)
	if err != nil {
		return nil, err
	}
	defer root.Close()
	dir, err := root.Open(".")
	if err != nil {
		return nil, err
	}
	return &Folder{dir}, nil
}
func (f *Folder) Close() error { return f.dir.Close() }

func child(dir *os.File, name string, flags int, mode uint32) (*os.File, error) {
	fd, err := unix.Openat(int(dir.Fd()), name, flags|unix.O_NOFOLLOW|unix.O_CLOEXEC|unix.O_NONBLOCK, mode)
	if err != nil {
		return nil, fmt.Errorf("%s: %w", name, err)
	}
	return os.NewFile(uintptr(fd), name), nil
}
func (f *Folder) parent(name string, create bool) (*os.File, string, error) {
	if name == "" || strings.HasPrefix(name, "/") || strings.ContainsAny(name, "\\\x00") {
		return nil, "", errors.New("a relative file path is required")
	}
	parts := strings.Split(name, "/")
	for _, p := range parts {
		if p == "" || p == "." || p == ".." {
			return nil, "", errors.New("invalid file path segment")
		}
	}
	current, err := child(f.dir, ".", unix.O_RDONLY|unix.O_DIRECTORY, 0)
	if err != nil {
		return nil, "", err
	}
	for _, part := range parts[:len(parts)-1] {
		next, e := child(current, part, unix.O_RDONLY|unix.O_DIRECTORY, 0)
		if create && errors.Is(e, unix.ENOENT) {
			if e = unix.Mkdirat(int(current.Fd()), part, 0700); e == nil || errors.Is(e, unix.EEXIST) {
				next, e = child(current, part, unix.O_RDONLY|unix.O_DIRECTORY, 0)
			}
		}
		current.Close()
		if e != nil {
			return nil, "", e
		}
		current = next
	}
	return current, parts[len(parts)-1], nil
}
func (f *Folder) Read(name string) (*os.File, error) {
	dir, leaf, err := f.parent(name, false)
	if err != nil {
		return nil, err
	}
	defer dir.Close()
	file, err := child(dir, leaf, unix.O_RDONLY, 0)
	if err != nil {
		return nil, err
	}
	info, err := file.Stat()
	if err != nil || !info.Mode().IsRegular() {
		file.Close()
		return nil, fmt.Errorf("%s: a regular file is required", name)
	}
	return file, nil
}

type Target struct {
	dir  *os.File
	name string
}

func (f *Folder) Prepare(name string) (*Target, error) {
	dir, leaf, err := f.parent(name, true)
	if err != nil {
		return nil, err
	}
	file, err := child(dir, leaf, unix.O_RDONLY, 0)
	if err == nil {
		info, e := file.Stat()
		file.Close()
		if e != nil || !info.Mode().IsRegular() {
			dir.Close()
			return nil, fmt.Errorf("%s: a regular file is required", name)
		}
	} else if !errors.Is(err, unix.ENOENT) {
		dir.Close()
		return nil, err
	}
	return &Target{dir, leaf}, nil
}
func (t *Target) Close() error { return t.dir.Close() }
func (t *Target) Replace(write func(io.Writer) error) error {
	var id [16]byte
	if _, err := rand.Read(id[:]); err != nil {
		return err
	}
	temp := ".lyk-write-" + hex.EncodeToString(id[:])
	file, err := child(t.dir, temp, unix.O_WRONLY|unix.O_CREAT|unix.O_EXCL, 0600)
	if err != nil {
		return err
	}
	defer unix.Unlinkat(int(t.dir.Fd()), temp, 0)
	err = write(file)
	if err == nil {
		err = file.Sync()
	}
	closeErr := file.Close()
	if err == nil {
		err = closeErr
	}
	if err != nil {
		return err
	}
	return unix.Renameat(int(t.dir.Fd()), temp, int(t.dir.Fd()), t.name)
}

type Pin struct {
	N      int    `json:"n"`
	SHA256 string `json:"sha256"`
}
type Manifest struct {
	Entries map[string]Pin
	lock    *os.File
	target  *Target
}

// LockManifest uses a stable .lyk/fill.lock inode. Every writer holds this lock
// from reading fill.json through its atomic replacement, preserving other pins.
// The lock file must never be removed or replaced.
func (f *Folder) LockManifest(ctx context.Context) (*Manifest, error) {
	dir, _, err := f.parent(".lyk/fill.json", true)
	if err != nil {
		return nil, err
	}
	defer dir.Close()
	lock, err := child(dir, "fill.lock", unix.O_RDWR|unix.O_CREAT, 0600)
	if err != nil {
		return nil, err
	}
	info, err := lock.Stat()
	if err != nil || !info.Mode().IsRegular() {
		lock.Close()
		return nil, errors.New("fill.lock must be a regular file")
	}
	for {
		if err := ctx.Err(); err != nil {
			lock.Close()
			return nil, err
		}
		err = unix.Flock(int(lock.Fd()), unix.LOCK_EX|unix.LOCK_NB)
		if err == nil {
			break
		}
		if !errors.Is(err, unix.EWOULDBLOCK) && !errors.Is(err, unix.EAGAIN) {
			lock.Close()
			return nil, err
		}
		select {
		case <-ctx.Done():
			lock.Close()
			return nil, ctx.Err()
		case <-time.After(25 * time.Millisecond):
		}
	}
	manifest := &Manifest{Entries: map[string]Pin{}, lock: lock}
	state := &Folder{dir: dir}
	file, err := state.Read("fill.json")
	if err == nil {
		decoder := json.NewDecoder(file)
		decoder.DisallowUnknownFields()
		err = decoder.Decode(&manifest.Entries)
		if err == nil {
			var extra any
			if trailing := decoder.Decode(&extra); !errors.Is(trailing, io.EOF) {
				err = errors.New("fill manifest must contain one JSON object")
			}
		}
		file.Close()
		if err == nil && manifest.Entries == nil {
			err = errors.New("fill manifest must be a JSON object")
		}
		if err == nil {
			for name, pin := range manifest.Entries {
				if pin.N <= 0 || pin.SHA256 == "" {
					err = fmt.Errorf("fill manifest has an invalid pin for %s", name)
					break
				}
			}
		}
	} else if errors.Is(err, os.ErrNotExist) {
		err = nil
	}
	if err == nil {
		manifest.target, err = state.Prepare("fill.json")
	}
	if err != nil {
		manifest.Close()
		return nil, fmt.Errorf("fill manifest: %w", err)
	}
	return manifest, nil
}
func (m *Manifest) Save() error {
	return m.target.Replace(func(w io.Writer) error { return json.NewEncoder(w).Encode(m.Entries) })
}
func (m *Manifest) Close() error {
	if m.target != nil {
		m.target.Close()
	}
	if m.lock != nil {
		unix.Flock(int(m.lock.Fd()), unix.LOCK_UN)
		return m.lock.Close()
	}
	return nil
}
