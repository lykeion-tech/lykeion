package envs

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
)

// fuse2fs mounts an ext4 image as the person through FUSE, without
// administrator rights. fallocate reserves the image's whole size.
type fuse2fs struct{ fuse2fs, mkfs, fallocate, fusermount string }

func platformVolume() (volume, error) {
	var v fuse2fs
	var missing []string
	for _, t := range []struct {
		dst  *string
		name []string
	}{{&v.fuse2fs, []string{"fuse2fs"}}, {&v.mkfs, []string{"mkfs.ext4"}}, {&v.fallocate, []string{"fallocate"}}, {&v.fusermount, []string{"fusermount3", "fusermount"}}} {
		for _, n := range t.name {
			if p, err := exec.LookPath(n); err == nil {
				*t.dst = p
				break
			}
		}
		if *t.dst == "" {
			missing = append(missing, t.name[0])
		}
	}
	if len(missing) > 0 {
		return nil, fmt.Errorf("install %s (packages fuse2fs, e2fsprogs and util-linux) so environments can be installed with a size limit", strings.Join(missing, ", "))
	}
	if _, err := os.Stat("/dev/fuse"); err != nil {
		return nil, errors.New("FUSE (/dev/fuse) is unavailable, so environments cannot be installed with a size limit")
	}
	return v, nil
}

func (fuse2fs) ext() string { return ".img" }

func (fuse2fs) mounted(dir string) bool { return mountRoot(dir) }

func (v fuse2fs) create(ctx context.Context, image string, size int64) error {
	if err := reserve(filepath.Dir(image), size+hostReserve); err != nil {
		return err
	}
	if err := cmdRun(ctx, v.fallocate, "-l", strconv.FormatInt(size, 10), image); err != nil {
		return err
	}
	owner := fmt.Sprintf("root_owner=%d:%d", os.Getuid(), os.Getgid())
	return cmdRun(ctx, v.mkfs, "-q", "-F", "-m", "0", "-E", owner, image)
}

func (v fuse2fs) attach(ctx context.Context, image, dir string, readOnly bool) error {
	opts := "rw"
	if readOnly {
		opts = "ro"
	}
	return cmdRun(ctx, v.fuse2fs, image, dir, "-o", opts+",nosuid,nodev")
}

func (v fuse2fs) detach(ctx context.Context, dir string) error {
	return cmdRun(ctx, v.fusermount, "-u", dir)
}

// seal keeps the ext4 image; it is only ever mounted read-only afterwards.
func (fuse2fs) seal(_ context.Context, building, sealed string) error {
	return os.Rename(building, sealed)
}

func cmdRun(ctx context.Context, name string, args ...string) error {
	out, err := exec.CommandContext(ctx, name, args...).CombinedOutput()
	if err != nil {
		return fmt.Errorf("%s: %w: %s", name, err, tail(strings.TrimSpace(string(out)), 512))
	}
	return nil
}
