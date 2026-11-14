package envs

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"
	"unicode"
)

// A base is installed into a fixed-size disk image mounted where the base
// lives, so the installer and everything it starts can fill at most that
// image: the image's space is reserved when it is made, and the sandbox lets
// the installer write nowhere else. A finished base is sealed read-only, so
// nothing an installer left running can change it afterwards.

// baseSizes caps each base's image, about twice what an install was
// measured to need at its peak (the Python base uses 654 MB once installed).
// The whole cap is reserved on disk while the base installs; sealing shrinks
// it to what the base uses.
var baseSizes = map[string]int64{Python: 3 << 30, R: 5 << 30, Julia: 5 << 30, "lean": 12 << 30}

// hostReserve is the disk an install leaves free beside its new image.
const hostReserve = 2 << 30

type volume interface {
	// create makes a writable image of size bytes, reserved on disk, and
	// refuses when that would leave the disk less than hostReserve free.
	create(ctx context.Context, image string, size int64) error
	attach(ctx context.Context, image, dir string, readOnly bool) error
	// detach never forces: it fails while any process still uses the volume.
	detach(ctx context.Context, dir string) error
	// seal makes a read-only image from a detached writable one.
	seal(ctx context.Context, building, sealed string) error
	// mounted reports whether a volume is mounted at dir.
	mounted(dir string) bool
	ext() string
}

type installKey struct{}

type installScope struct {
	write string
	read  []string
}

// installing marks ctx as an install whose commands may write only in write
// and also read the folders in read.
func installing(ctx context.Context, write string, read ...string) context.Context {
	return context.WithValue(ctx, installKey{}, installScope{write, read})
}

// installReads are the other bases' folders an install may read, such as
// micromamba and the Python base: never its own folder or one holding it.
// They are made first, since the sandbox grants only folders that exist and
// micromamba is fetched into its folder during the install.
func (b *Bases) installReads(write string) ([]string, error) {
	var out []string
	for _, f := range b.Folders() {
		if isUnder(write, f) {
			continue
		}
		if err := os.MkdirAll(f, 0o755); err != nil {
			return nil, err
		}
		out = append(out, f)
	}
	return out, nil
}

func installOf(ctx context.Context) (installScope, bool) {
	s, ok := ctx.Value(installKey{}).(installScope)
	return s, ok && s.write != ""
}

// scratch is the install's own home and temporary folder, inside its volume
// and removed before the volume is sealed.
func scratch(dir string) string { return filepath.Join(dir, ".lyk-install") }

func (b *Bases) images() string { return filepath.Join(b.Dir, "images") }

func (b *Bases) sealedImage(key string) string {
	return filepath.Join(b.images(), key+b.vol.ext())
}

// attachSealed mounts key's sealed image read-only at dir when it is not
// mounted yet. It reports whether a sealed image is mounted there.
func (b *Bases) attachSealed(key, dir string) bool {
	if b.vol == nil {
		return false
	}
	// Only a sealed image is mounted here, and an install in progress holds
	// the lock: its base is not ready yet.
	image := b.sealedImage(key)
	if _, err := os.Lstat(image); err != nil {
		return false
	}
	l := b.lock("mount:" + key)
	if !l.TryLock() {
		return false
	}
	defer l.Unlock()
	if b.vol.mounted(dir) {
		return true
	}
	if err := emptyDir(dir); err != nil {
		return false
	}
	ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
	defer cancel()
	return b.vol.attach(ctx, image, dir, true) == nil
}

// provision installs key at dir inside its own capped image and publishes it
// sealed. A volume that a process still uses is never published or removed:
// the install fails and the next attempt retries the detach.
// The ready marker is written only once the install succeeded, and removed
// again if its volume cannot be detached, so a writable volume never counts
// as ready.
func (b *Bases) provision(ctx context.Context, key, dir, marker string, size int64, build func(context.Context) error) error {
	if b.vol == nil {
		return b.unavailable()
	}
	// conda-forge's R scripts word-split R_HOME, and console-script shebangs
	// break on spaces too, so a base is never built under such a path.
	if strings.ContainsFunc(b.Dir, unicode.IsSpace) {
		return fmt.Errorf("the environments folder must not contain spaces: %s", b.Dir)
	}
	if err := os.MkdirAll(b.images(), 0o700); err != nil {
		return err
	}
	building := filepath.Join(b.images(), key+".building"+b.vol.ext())
	l := b.lock("mount:" + key)
	l.Lock()
	defer l.Unlock()
	if b.vol.mounted(dir) {
		if err := b.vol.detach(ctx, dir); err != nil {
			return fmt.Errorf("a previous install still uses %s: %w", dir, err)
		}
	}
	if err := os.RemoveAll(building); err != nil {
		return err
	}
	if err := emptyDir(dir); err != nil {
		return err
	}
	if err := b.vol.create(ctx, building, size); err != nil {
		os.RemoveAll(building)
		return fmt.Errorf("reserving %d GB for the install: %w", size>>30, err)
	}
	if err := b.vol.attach(ctx, building, dir, false); err != nil {
		os.RemoveAll(building)
		return err
	}
	reads, err := b.installReads(dir)
	if err == nil {
		err = os.MkdirAll(scratch(dir), 0o700)
	}
	if err == nil {
		err = build(installing(ctx, dir, reads...))
	}
	if err == nil {
		err = os.RemoveAll(scratch(dir))
	}
	if err == nil {
		err = os.WriteFile(marker, nil, 0o644)
	}
	if derr := b.vol.detach(context.WithoutCancel(ctx), dir); derr != nil {
		os.Remove(marker)
		return errors.Join(err, fmt.Errorf("a process the installer started still uses its volume, so the install was not published: %w", derr))
	}
	if err != nil {
		os.RemoveAll(building)
		return err
	}
	sealed := b.sealedImage(key)
	if err := b.vol.seal(ctx, building, sealed); err != nil {
		os.RemoveAll(sealed)
		os.RemoveAll(building)
		return fmt.Errorf("sealing the install: %w", err)
	}
	os.RemoveAll(building)
	return b.vol.attach(context.WithoutCancel(ctx), sealed, dir, true)
}

// emptyDir makes dir, or accepts it when it is an empty real folder. Anything
// already there belongs to someone and is never hidden or removed.
func emptyDir(dir string) error {
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	fi, err := os.Lstat(dir)
	if err != nil {
		return err
	}
	if !fi.IsDir() || fi.Mode()&os.ModeSymlink != 0 {
		return fmt.Errorf("%s is not a real folder", dir)
	}
	f, err := os.Open(dir)
	if err != nil {
		return err
	}
	defer f.Close()
	if _, err := f.Readdirnames(1); err != io.EOF {
		return fmt.Errorf("%s already holds files; move them away to install this environment", dir)
	}
	return nil
}

func (b *Bases) unavailable() error {
	if b.volErr != nil {
		return unavailableError{b.volErr}
	}
	return ErrBoundedProvisioningUnavailable
}

// unavailableError names what this machine lacks; the dashboard shows the
// text after the colon as the fix.
type unavailableError struct{ reason error }

func (e unavailableError) Error() string {
	return "bounded scientific environment setup is unavailable on this machine: " + e.reason.Error()
}

func (e unavailableError) Unwrap() error { return ErrBoundedProvisioningUnavailable }
