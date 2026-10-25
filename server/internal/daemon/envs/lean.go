package envs

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"
)

var leanToolchainPattern = regexp.MustCompile(`^leanprover/lean4:v[0-9A-Za-z.\-]+$`)
var mathlibRevisionPattern = regexp.MustCompile(`^([0-9a-f]{7,40}|v[0-9A-Za-z.\-]+)$`)

func leanPair(toolchain, mathlib string) (string, error) {
	if !leanToolchainPattern.MatchString(toolchain) {
		return "", errors.New("lean must be a leanprover/lean4 version tag")
	}
	if mathlib == "" {
		_, mathlib, _ = strings.Cut(toolchain, ":")
	}
	if !mathlibRevisionPattern.MatchString(mathlib) {
		return "", errors.New("mathlib must be a version tag or a 7 to 40 character hexadecimal revision")
	}
	return strings.NewReplacer("/", "-", ":", "-").Replace(toolchain) + "@" + mathlib, nil
}

// EnsureLeanRoot reserves a trusted read-only root before a run's sandbox
// starts, so a newly built pair is visible to that already-running agent.
func (b *Bases) EnsureLeanRoot() error {
	root := filepath.Join(b.Dir, "lean")
	if err := os.MkdirAll(root, 0755); err != nil {
		return err
	}
	fi, err := os.Lstat(root)
	if err != nil {
		return err
	}
	if !fi.IsDir() || fi.Mode()&os.ModeSymlink != 0 {
		return errors.New("Lean base folder is not a real directory")
	}
	return nil
}

func (b *Bases) EnsureLean(ctx context.Context, toolchain, mathlib string) (dir string, err error) {
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
		return "", errors.New("Lean pair folder is not a real directory")
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

func (b *Bases) checkedLean(dir string) (string, error) {
	root := filepath.Join(b.Dir, "lean")
	if filepath.Dir(dir) != root {
		return "", errors.New("Lean pair is outside the trusted base folder")
	}
	tag, rev, ok := strings.Cut(strings.TrimPrefix(filepath.Base(dir), "leanprover-lean4-"), "@")
	toolchain := "leanprover/lean4:" + tag
	slug, err := leanPair(toolchain, rev)
	if err != nil || !ok || slug != filepath.Base(dir) {
		return "", errors.New("invalid Lean pair")
	}
	for _, p := range []string{root, dir} {
		fi, e := os.Lstat(p)
		if e != nil {
			return "", e
		}
		if !fi.IsDir() || fi.Mode()&os.ModeSymlink != 0 {
			return "", errors.New("Lean base folder is not a real directory")
		}
	}
	fi, err := os.Lstat(filepath.Join(dir, "ready"))
	if err != nil {
		return "", err
	}
	if !fi.Mode().IsRegular() {
		return "", errors.New("Lean pair is not ready")
	}
	return toolchain, nil
}

func (s *Study) selectedLean() string    { s.leanMu.Lock(); defer s.leanMu.Unlock(); return s.leanDir }
func (s *Study) rememberLean(dir string) { s.leanMu.Lock(); defer s.leanMu.Unlock(); s.leanDir = dir }

// loadLean reads the study's selection only in its sandbox; a link planted
// by an agent cannot make the daemon read a private file.
func (s *Study) loadLean(ctx context.Context) error {
	out, err := s.Run(ctx, nil, s.Dir, "/bin/sh", "-c", `[ -e lean/.lyk-pair ] || exit 0; head -c 1025 lean/.lyk-pair`)
	if err != nil {
		return err
	}
	if len(out) == 0 {
		s.rememberLean("")
		return nil
	}
	slug := string(out)
	if len(out) > 1024 || strings.ContainsAny(slug, "/\\\n\r\x00") {
		return errors.New("invalid Lean pair")
	}
	dir := filepath.Join(s.Bases.Dir, "lean", slug)
	if _, err := s.Bases.checkedLean(dir); err != nil {
		return err
	}
	s.rememberLean(dir)
	return nil
}

// UseLean only writes study-owned files through the sandbox. The manifest
// points every package at the trusted shared tree, leaving proofs and the
// root project's build products in the study.
func (s *Study) UseLean(ctx context.Context, dir string) error {
	if s.Run == nil {
		return errNoRunner
	}
	toolchain, err := s.Bases.checkedLean(dir)
	if err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(ctx, setupLimit)
	defer cancel()
	unlock, err := s.studyLock(ctx)
	if err != nil {
		return err
	}
	defer unlock()
	manifest, err := leanStudyManifest(dir)
	if err != nil {
		return err
	}
	// JSON string quoting is also valid for these TOML basic string values.
	path, _ := json.Marshal(filepath.Join(dir, "mathlib"))
	config := "name = \"study\"\ndefaultTargets = [\"Study\"]\n\n[[lean_lib]]\nname = \"Study\"\n\n[[require]]\nname = \"mathlib\"\npath = " + string(path) + "\n"
	_, err = s.Run(ctx, nil, s.Dir, "/bin/sh", "-c", `set -eu
mkdir -p lean
printf '%s' "$1" > lean/lakefile.toml
printf '%s\n' "$2" > lean/lean-toolchain
printf '%s' "$3" > lean/lake-manifest.json
printf '%s' "$4" > lean/.lyk-pair`, "sh", config, toolchain, string(manifest), filepath.Base(dir))
	if err == nil {
		s.rememberLean(dir)
	}
	return err
}

func leanStudyManifest(dir string) ([]byte, error) {
	data, err := os.ReadFile(filepath.Join(dir, "mathlib", "lake-manifest.json"))
	if err != nil {
		return nil, err
	}
	var manifest map[string]json.RawMessage
	if err := json.Unmarshal(data, &manifest); err != nil {
		return nil, err
	}
	var packages []map[string]any
	if err := json.Unmarshal(manifest["packages"], &packages); err != nil {
		return nil, err
	}
	for _, p := range packages {
		name, ok := p["name"].(string)
		if !ok || name == "" || name == "." || name == ".." || strings.ContainsAny(name, "/\\\x00") {
			return nil, errors.New("invalid Mathlib package name")
		}
		p["type"], p["dir"], p["inherited"] = "path", filepath.Join(dir, "mathlib", ".lake", "packages", name), true
		for _, key := range []string{"url", "rev", "inputRev", "subDir"} {
			delete(p, key)
		}
	}
	packages = append(packages, map[string]any{"name": "mathlib", "scope": "", "type": "path", "dir": filepath.Join(dir, "mathlib"), "inherited": false, "configFile": "lakefile.lean", "manifestFile": "lake-manifest.json"})
	return json.Marshal(map[string]any{"version": "1.2.0", "name": "study", "lakeDir": ".lake", "packagesDir": ".lake/packages", "packages": packages, "fixedToolchain": true})
}
