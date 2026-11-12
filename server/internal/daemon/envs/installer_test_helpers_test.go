package envs

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// buildBaseForTest exercises the dormant builder with test-controlled runners.
// Production provisioning remains disabled until it is bounded.
func (b *Bases) buildBaseForTest(ctx context.Context, lang string) error {
	if _, ok := displayNames[lang]; !ok {
		return fmt.Errorf("unknown language %q", lang)
	}
	l := b.lock(lang)
	for !l.TryLock() {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(10 * time.Millisecond):
		}
	}
	defer l.Unlock()
	if err := ctx.Err(); err != nil {
		return err
	}
	if b.Ready(lang) {
		return nil
	}
	b.setState(lang, "building")
	err := b.build(ctx, lang)
	if err != nil {
		b.setState(lang, Failed(err))
	} else {
		b.setState(lang, "")
	}
	return err
}

func (b *Bases) buildAllForTest(ctx context.Context) map[string]error {
	errs := map[string]error{}
	for _, lang := range []string{Python, R, Julia} {
		errs[lang] = b.buildBaseForTest(ctx, lang)
	}
	return errs
}

func (b *Bases) buildLeanForTest(ctx context.Context, toolchain, mathlib string) (dir string, err error) {
	slug, err := leanPair(toolchain, mathlib)
	if err != nil {
		return "", err
	}
	ctx, cancel := context.WithTimeout(ctx, setupLimit)
	defer cancel()
	l := b.lock("lean:" + slug)
	for !l.TryLock() {
		select {
		case <-ctx.Done():
			return "", ctx.Err()
		case <-time.After(10 * time.Millisecond):
		}
	}
	defer l.Unlock()
	if err = ctx.Err(); err != nil {
		return "", err
	}
	if err = b.EnsureLeanRoot(); err != nil {
		return "", err
	}
	dir = filepath.Join(b.Dir, "lean", slug)
	if fi, e := os.Lstat(dir); e == nil && (!fi.IsDir() || fi.Mode()&os.ModeSymlink != 0) {
		return "", fmt.Errorf("Lean pair folder is not a real directory")
	}
	if fi, e := os.Lstat(filepath.Join(dir, "ready")); e == nil && fi.Mode().IsRegular() {
		return dir, nil
	}
	if err = os.RemoveAll(dir); err != nil {
		return "", err
	}
	if err = os.MkdirAll(dir, 0755); err != nil {
		return "", err
	}
	defer func() {
		if err != nil {
			os.RemoveAll(dir)
			dir = ""
		}
	}()
	_, mathlib, _ = strings.Cut(slug, "@")
	if err = b.buildLean(ctx, dir, toolchain, mathlib); err != nil {
		return dir, fmt.Errorf("building the Lean base: %w", err)
	}
	err = os.WriteFile(filepath.Join(dir, "ready"), nil, 0644)
	return dir, err
}
