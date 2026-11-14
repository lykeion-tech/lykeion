package envs

import (
	"context"
	"fmt"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
)

// hdiutil makes and mounts disk images as the person, without administrator
// rights. A UDIF image reserves its whole size when made; an ULFO image is
// compressed and read-only by format.
type hdiutil struct{}

func platformVolume() (volume, error) {
	if _, err := exec.LookPath("/usr/bin/hdiutil"); err != nil {
		return nil, err
	}
	return hdiutil{}, nil
}

func (hdiutil) ext() string { return ".dmg" }

func (hdiutil) mounted(dir string) bool { return mountRoot(dir) }

func (hdiutil) create(ctx context.Context, image string, size int64) error {
	if err := reserve(filepath.Dir(image), size+hostReserve); err != nil {
		return err
	}
	return hdiutilRun(ctx, "create", "-quiet", "-type", "UDIF", "-fs", "APFS", "-volname", "Lykeion", "-size", strconv.FormatInt(size>>20, 10)+"m", image)
}

func (hdiutil) attach(ctx context.Context, image, dir string, readOnly bool) error {
	args := []string{"attach", "-quiet", "-nobrowse", "-noautoopen", "-owners", "on", "-mountpoint", dir}
	if readOnly {
		args = append(args, "-readonly")
	}
	return hdiutilRun(ctx, append(args, image)...)
}

func (hdiutil) detach(ctx context.Context, dir string) error {
	return hdiutilRun(ctx, "detach", "-quiet", dir)
}

func (hdiutil) seal(ctx context.Context, building, sealed string) error {
	return hdiutilRun(ctx, "convert", "-quiet", building, "-format", "ULFO", "-o", sealed)
}

func hdiutilRun(ctx context.Context, args ...string) error {
	out, err := exec.CommandContext(ctx, "/usr/bin/hdiutil", args...).CombinedOutput()
	if err != nil {
		return fmt.Errorf("hdiutil %s: %w: %s", args[0], err, tail(strings.TrimSpace(string(out)), 512))
	}
	return nil
}
