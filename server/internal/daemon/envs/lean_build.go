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
)

var mathlibRepositories = map[string]bool{
	"https://github.com/leanprover-community/plausible":        true,
	"https://github.com/leanprover-community/LeanSearchClient": true,
	"https://github.com/leanprover-community/import-graph":     true,
	"https://github.com/leanprover-community/ProofWidgets4":    true,
	"https://github.com/leanprover-community/aesop":            true,
	"https://github.com/leanprover-community/quote4":           true,
	"https://github.com/leanprover-community/batteries":        true,
	"https://github.com/leanprover/lean4-cli":                  true,
}
var gitCommitPattern = regexp.MustCompile(`^[0-9a-f]{40}$`)

type mathlibPackage struct {
	Name         string `json:"name"`
	URL          string `json:"url"`
	Rev          string `json:"rev"`
	Type         string `json:"type"`
	SubDir       string `json:"subDir"`
	ConfigFile   string `json:"configFile"`
	ManifestFile string `json:"manifestFile"`
}

func readMathlibDependencies(dir, rev string) ([]mathlibPackage, error) {
	data, err := os.ReadFile(filepath.Join(dir, "lake-manifest.json"))
	if err != nil {
		return nil, err
	}
	var manifest struct {
		PackagesDir string           `json:"packagesDir"`
		LakeDir     string           `json:"lakeDir"`
		Packages    []mathlibPackage `json:"packages"`
	}
	if err := json.Unmarshal(data, &manifest); err != nil {
		return nil, err
	}
	if manifest.PackagesDir != ".lake/packages" || (manifest.LakeDir != "" && manifest.LakeDir != ".lake") {
		return nil, errors.New("Mathlib manifest uses unsupported package folders")
	}
	names := map[string]bool{}
	for _, p := range manifest.Packages {
		safeName := p.Name != "" && p.Name != "." && p.Name != ".." && !strings.ContainsAny(p.Name, "/\\\x00\r\n")
		safeConfig := p.ConfigFile == "" || p.ConfigFile == "lakefile.lean" || p.ConfigFile == "lakefile.toml"
		safeManifest := p.ManifestFile == "" || p.ManifestFile == "lake-manifest.json"
		if !mathlibRepositories[strings.TrimSuffix(p.URL, ".git")] || p.Type != "git" || !gitCommitPattern.MatchString(p.Rev) || !safeName || !safeConfig || !safeManifest || p.SubDir != "" || names[p.Name] {
			return nil, fmt.Errorf("Mathlib %s needs %s, which Lean builds don't fetch", rev, p.URL)
		}
		names[p.Name] = true
	}
	return manifest.Packages, nil
}

func (b *Bases) buildLean(ctx context.Context, dir, toolchain, revision string) error {
	plat, err := platform()
	if err != nil {
		return err
	}
	home := filepath.Join(dir, "home")
	installer := filepath.Join(dir, "installer")
	for _, path := range []string{home, installer, filepath.Join(dir, "tmp")} {
		if err := os.MkdirAll(path, 0755); err != nil {
			return err
		}
	}
	elanHome := filepath.Join(dir, "elan")
	// Use a complete environment: inherited Git configuration can override
	// even an isolated HOME, while elan/Lake controls can redirect downloads.
	env := []string{
		"HOME=" + home, "PATH=" + filepath.Join(elanHome, "bin") + ":/usr/bin:/bin:/usr/sbin:/sbin",
		"TMPDIR=" + filepath.Join(dir, "tmp"), "XDG_CACHE_HOME=" + filepath.Join(dir, "cache"),
		"ELAN_HOME=" + elanHome, "ELAN_TOOLCHAIN=" + toolchain,
		"GIT_CONFIG_GLOBAL=/dev/null", "GIT_CONFIG_NOSYSTEM=1", "GIT_TERMINAL_PROMPT=0",
	}
	archive := filepath.Join(dir, "elan-"+elanVersion+".tar.gz")
	if err := b.Fetch(ctx, elan[plat].url, elan[plat].sha256, archive); err != nil {
		return err
	}
	if _, err := b.Run(ctx, env, dir, "/usr/bin/tar", "-xzf", archive, "-C", installer); err != nil {
		return err
	}
	if _, err := b.Run(ctx, env, dir, filepath.Join(installer, "elan-init"), "-y", "--no-modify-path", "--default-toolchain", "none"); err != nil {
		return err
	}
	if err := os.Remove(archive); err != nil {
		return err
	}
	if err := os.RemoveAll(installer); err != nil {
		return err
	}
	elanBin := filepath.Join(elanHome, "bin", "elan")
	if _, err := b.Run(ctx, env, dir, elanBin, "toolchain", "install", toolchain); err != nil {
		return err
	}
	if _, err := b.Run(ctx, env, dir, elanBin, "default", toolchain); err != nil {
		return err
	}
	mathlib := filepath.Join(dir, "mathlib")
	git := func(work string, args ...string) ([]byte, error) {
		return b.Run(ctx, env, work, "/usr/bin/git", args...)
	}
	// Retain commit history so abbreviated revisions can be resolved locally;
	// blobs are fetched only for the selected checkout.
	if _, err := git(dir, "clone", "--no-checkout", "--filter=blob:none", "--no-recurse-submodules", "https://github.com/leanprover-community/mathlib4.git", mathlib); err != nil {
		return err
	}
	if _, err := git(mathlib, "checkout", "--detach", revision); err != nil {
		return err
	}
	mainCommit, err := git(mathlib, "rev-parse", "HEAD")
	if err != nil {
		return err
	}
	if !gitCommitPattern.MatchString(strings.TrimSpace(string(mainCommit))) {
		return errors.New("Mathlib checkout has no full commit")
	}
	packages, err := readMathlibDependencies(mathlib, revision)
	if err != nil {
		return err
	}
	for _, p := range packages {
		checkout := filepath.Join(mathlib, ".lake", "packages", p.Name)
		if err := os.MkdirAll(checkout, 0755); err != nil {
			return err
		}
		for _, args := range [][]string{{"init"}, {"remote", "add", "origin", p.URL}, {"fetch", "--depth=1", "origin", p.Rev}, {"checkout", "--detach", "FETCH_HEAD"}} {
			if _, err := git(checkout, args...); err != nil {
				return err
			}
		}
	}
	if _, err := b.Run(ctx, env, mathlib, filepath.Join(elanHome, "bin", "lake"), "exe", "cache", "get"); err != nil {
		return err
	}
	for _, p := range append(packages, mathlibPackage{Name: "", Rev: strings.TrimSpace(string(mainCommit))}) {
		checkout := mathlib
		if p.Name != "" {
			checkout = filepath.Join(mathlib, ".lake", "packages", p.Name)
		}
		actual, err := git(checkout, "rev-parse", "HEAD")
		if err != nil {
			return err
		}
		if strings.TrimSpace(string(actual)) != p.Rev {
			return fmt.Errorf("Mathlib checkout %s changed from its pinned commit %s", checkout, p.Rev)
		}
	}
	return ctx.Err()
}
