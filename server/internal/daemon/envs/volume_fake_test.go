package envs

import (
	"context"
	"errors"
	"os"
	"sync"
)

// fakeVolume keeps each image as a folder and mounts it by moving it into
// place, so tests see what a real volume holds at each step.
type fakeVolume struct {
	mu       sync.Mutex
	mounts   map[string]string // mountpoint → image
	readOnly map[string]bool
	busy     bool // detach fails, as while an installer's process lives
	creates  int
}

func newFakeVolume() *fakeVolume {
	return &fakeVolume{mounts: map[string]string{}, readOnly: map[string]bool{}}
}

func (*fakeVolume) ext() string { return ".img" }

func (v *fakeVolume) create(_ context.Context, image string, _ int64) error {
	v.mu.Lock()
	v.creates++
	v.mu.Unlock()
	return os.Mkdir(image, 0o755)
}

func (v *fakeVolume) attach(_ context.Context, image, dir string, readOnly bool) error {
	v.mu.Lock()
	defer v.mu.Unlock()
	if err := os.Remove(dir); err != nil {
		return err
	}
	if err := os.Rename(image, dir); err != nil {
		return err
	}
	v.mounts[dir], v.readOnly[dir] = image, readOnly
	return nil
}

func (v *fakeVolume) detach(_ context.Context, dir string) error {
	v.mu.Lock()
	defer v.mu.Unlock()
	if v.busy {
		return errors.New("resource busy")
	}
	image, ok := v.mounts[dir]
	if !ok {
		return errors.New("not mounted")
	}
	if err := os.Rename(dir, image); err != nil {
		return err
	}
	delete(v.mounts, dir)
	delete(v.readOnly, dir)
	return os.Mkdir(dir, 0o755)
}

func (*fakeVolume) seal(_ context.Context, building, sealed string) error {
	return os.Rename(building, sealed)
}

func (v *fakeVolume) mounted(dir string) bool {
	v.mu.Lock()
	defer v.mu.Unlock()
	_, ok := v.mounts[dir]
	return ok
}

// forget drops every mount without moving anything, as a restart does.
func (v *fakeVolume) forget() {
	v.mu.Lock()
	defer v.mu.Unlock()
	for dir, image := range v.mounts {
		os.Rename(dir, image)
		os.Mkdir(dir, 0o755)
	}
	v.mounts, v.readOnly = map[string]string{}, map[string]bool{}
}
