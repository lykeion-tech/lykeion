// Package envs builds the daemon's per-machine language bases (Python and R
// from conda-forge through micromamba, Julia from its official release) and
// gives each study its own add-on folders layered over them.
package envs

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"time"
	"unicode/utf8"
)

const Python, R, Julia = "python", "r", "julia"

var ErrNotReady = errors.New("environment not ready")

var displayNames = map[string]string{Python: "Python", R: "R", Julia: "Julia"}

type notReady string

func (e notReady) Error() string {
	return fmt.Sprintf("the %s environment isn't ready on this machine", displayNames[string(e)])
}
func (e notReady) Unwrap() error { return ErrNotReady }

type Bases struct {
	Dir string
	// Run's env, when non-nil, is the command's whole environment; nil
	// inherits the daemon's.
	Run   func(ctx context.Context, env []string, dir, name string, args ...string) ([]byte, error)
	Fetch func(ctx context.Context, url, sha256, dest string) error

	mu    sync.Mutex
	locks map[string]*sync.Mutex
	// states holds the bases being built or that failed in this process.
	states map[string]string
}

func NewBases(dir string) *Bases {
	return &Bases{Dir: dir, Run: run, Fetch: fetch}
}

func run(ctx context.Context, env []string, dir, name string, args ...string) ([]byte, error) {
	cmd := exec.CommandContext(ctx, name, args...)
	cmd.Env = env
	cmd.Dir = dir
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	out, err := cmd.Output()
	if err != nil {
		return out, fmt.Errorf("%s %s: %w: %s", filepath.Base(name), strings.Join(args, " "), err, tail(stderr.String(), 4096))
	}
	return out, nil
}

// isolated is the daemon's environment without anything that would point a
// build or a check at the person's own Python, R, Julia, conda or Jupyter
// setup, with extra appended.
func isolated(extra ...string) []string {
	set := map[string]bool{}
	for _, e := range extra {
		k, _, _ := strings.Cut(e, "=")
		set[k] = true
	}
	var env []string
	for _, e := range os.Environ() {
		k, _, _ := strings.Cut(e, "=")
		if set[k] || k == "R_HOME" || k == "VIRTUAL_ENV" || hasAnyPrefix(k, "CONDA_", "MAMBA_", "PYTHON", "R_LIBS", "R_PROFILE", "R_ENVIRON", "JULIA_", "JUPYTER") {
			continue
		}
		env = append(env, e)
	}
	return append(env, extra...)
}

func hasAnyPrefix(s string, prefixes ...string) bool {
	for _, p := range prefixes {
		if strings.HasPrefix(s, p) {
			return true
		}
	}
	return false
}

func tail(s string, n int) string {
	s = strings.TrimSpace(s)
	if len(s) > n {
		return "…" + s[len(s)-n:]
	}
	return s
}

// fetch writes beside dest and renames only after the hash matches, so dest
// never holds unverified bytes.
func fetch(ctx context.Context, url, sum, dest string) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return err
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("fetch %s: %s", url, resp.Status)
	}
	if err := os.MkdirAll(filepath.Dir(dest), 0o755); err != nil {
		return err
	}
	tmp, err := os.CreateTemp(filepath.Dir(dest), ".fetch-*")
	if err != nil {
		return err
	}
	defer os.Remove(tmp.Name())
	h := sha256.New()
	_, err = io.Copy(io.MultiWriter(tmp, h), resp.Body)
	if cerr := tmp.Close(); err == nil {
		err = cerr
	}
	if err != nil {
		return err
	}
	if got := hex.EncodeToString(h.Sum(nil)); got != sum {
		return fmt.Errorf("fetch %s: sha256 %s, want %s", url, got, sum)
	}
	return os.Rename(tmp.Name(), dest)
}

func platform() (string, error) {
	switch runtime.GOOS + "/" + runtime.GOARCH {
	case "darwin/arm64":
		return "osx-arm64", nil
	case "darwin/amd64":
		return "osx-64", nil
	case "linux/amd64":
		return "linux-64", nil
	case "linux/arm64":
		return "linux-aarch64", nil
	}
	return "", fmt.Errorf("no language bases for platform %s/%s", runtime.GOOS, runtime.GOARCH)
}

// baseDir is where a base's lock.txt and ready marker live.
func (b *Bases) baseDir(lang string) string {
	if lang == Julia {
		return filepath.Join(b.Dir, "julia", "base")
	}
	return filepath.Join(b.Dir, lang)
}

func (b *Bases) juliaBin() string {
	return filepath.Join(b.Dir, "julia", juliaVersion, "bin")
}

func (b *Bases) jupyterDirs() []string {
	var dirs []string
	for _, d := range [][2]string{
		{Python, filepath.Join(b.Dir, "python", "share", "jupyter")},
		{R, filepath.Join(b.Dir, "r", "share", "jupyter")},
		{Julia, filepath.Join(b.Dir, "julia", "jupyter")},
	} {
		if b.Ready(d[0]) {
			dirs = append(dirs, d[1])
		}
	}
	return dirs
}

func (b *Bases) Ready(lang string) bool {
	if _, ok := displayNames[lang]; !ok {
		return false
	}
	_, err := os.Stat(filepath.Join(b.baseDir(lang), "ready"))
	return err == nil
}

func (b *Bases) lock(lang string) *sync.Mutex {
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.locks == nil {
		b.locks = map[string]*sync.Mutex{}
	}
	if b.locks[lang] == nil {
		b.locks[lang] = &sync.Mutex{}
	}
	return b.locks[lang]
}

// Ensure builds a base once. A conda prefix embeds its own absolute path, so
// a base is built where it will live; until its ready marker is written it
// counts as absent, and a failed or interrupted build is removed whole.
func (b *Bases) Ensure(ctx context.Context, lang string) error {
	if _, ok := displayNames[lang]; !ok {
		return fmt.Errorf("unknown language %q", lang)
	}
	l := b.lock(lang)
	l.Lock()
	defer l.Unlock()
	if b.Ready(lang) {
		return nil
	}
	b.setState(lang, "building")
	err := b.build(ctx, lang)
	if err != nil {
		b.setState(lang, "failed: "+cut(err.Error(), 200))
	} else {
		b.setState(lang, "")
	}
	return err
}

func (b *Bases) build(ctx context.Context, lang string) error {
	plat, err := platform()
	if err != nil {
		return err
	}
	if lang != Python && !b.Ready(Python) {
		return fmt.Errorf("the %s base needs the Python base, which isn't ready", displayNames[lang])
	}
	root := filepath.Join(b.Dir, lang)
	if err := os.RemoveAll(root); err != nil {
		return err
	}
	switch lang {
	case Python, R:
		err = b.buildConda(ctx, plat, lang)
	case Julia:
		err = b.buildJulia(ctx, plat)
	}
	if err == nil {
		err = os.WriteFile(filepath.Join(b.baseDir(lang), "ready"), nil, 0o644)
	}
	if err != nil {
		os.RemoveAll(root)
		return fmt.Errorf("building the %s base: %w", displayNames[lang], err)
	}
	return nil
}

func (b *Bases) setState(lang, state string) {
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.states == nil {
		b.states = map[string]string{}
	}
	if state == "" {
		delete(b.states, lang)
	} else {
		b.states[lang] = state
	}
}

// States reports each base as "ready", "building" or "failed: <reason>". A
// base not yet tried is absent.
func (b *Bases) States() map[string]string {
	out := map[string]string{}
	for lang := range displayNames {
		if b.Ready(lang) {
			out[lang] = "ready"
		}
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	for lang, state := range b.states {
		if _, ready := out[lang]; !ready {
			out[lang] = state
		}
	}
	return out
}

// cut keeps at most n bytes of s without splitting a character.
func cut(s string, n int) string {
	if len(s) <= n {
		return s
	}
	for n > 0 && !utf8.RuneStart(s[n]) {
		n--
	}
	return s[:n]
}

func (b *Bases) EnsureAll(ctx context.Context) map[string]error {
	errs := map[string]error{}
	for _, lang := range []string{Python, R, Julia} {
		errs[lang] = b.Ensure(ctx, lang)
	}
	return errs
}

func (b *Bases) micromamba(ctx context.Context, plat string) (string, error) {
	bin := filepath.Join(b.Dir, "micromamba", micromambaVersion, "micromamba")
	if _, err := os.Stat(bin); err == nil {
		return bin, nil
	}
	if err := b.Fetch(ctx, micromamba[plat].url, micromamba[plat].sha256, bin); err != nil {
		return "", err
	}
	return bin, os.Chmod(bin, 0o755)
}

func (b *Bases) buildConda(ctx context.Context, plat, lang string) error {
	mm, err := b.micromamba(ctx, plat)
	if err != nil {
		return err
	}
	prefix := filepath.Join(b.Dir, lang)
	// micromamba keeps its cache under the root prefix and records every
	// environment it creates in $HOME/.conda, so both point inside Dir, apart
	// from the person's own conda.
	env := isolated("MAMBA_ROOT_PREFIX="+filepath.Join(b.Dir, "micromamba", "root"), "HOME="+filepath.Join(b.Dir, "micromamba", "home"))
	pins := pythonPins
	if lang == R {
		pins = rPins
	}
	if _, err := b.Run(ctx, env, b.Dir, mm, append([]string{"--no-rc", "create", "-y", "-p", prefix, "-c", "conda-forge", "--override-channels"}, pins...)...); err != nil {
		return err
	}
	lock, err := b.Run(ctx, env, b.Dir, mm, "--no-rc", "env", "export", "-p", prefix, "--explicit")
	if err != nil {
		return err
	}
	if err := os.WriteFile(filepath.Join(prefix, "lock.txt"), lock, 0o644); err != nil {
		return err
	}
	if err := b.verifyConda(ctx, lang, prefix); err != nil {
		return err
	}
	// The packages are linked into the prefix, so the cache can go.
	if _, err := b.Run(ctx, env, b.Dir, mm, "--no-rc", "clean", "-a", "-y"); err != nil {
		slog.Warn("cleaning the conda package cache", "err", err)
	}
	return nil
}

func (b *Bases) verifyConda(ctx context.Context, lang, prefix string) error {
	if lang == Python {
		_, err := b.Run(ctx, isolated(), b.Dir, filepath.Join(prefix, "bin", "python"), "-I", "-c", "import numpy, pandas, scipy, matplotlib, statsmodels, ipykernel, jupyter_client")
		return err
	}
	// IRkernel registers itself through the Python base's jupyter.
	path := isolated("PATH=" + filepath.Join(b.Dir, "python", "bin") + string(os.PathListSeparator) + os.Getenv("PATH"))
	rscript := filepath.Join(prefix, "bin", "Rscript")
	if _, err := b.Run(ctx, path, b.Dir, rscript, "--vanilla", "-e", fmt.Sprintf("IRkernel::installspec(user = FALSE, prefix = %q)", prefix)); err != nil {
		return err
	}
	// --vanilla still honours R_LIBS_USER; clearing .libPaths leaves only the base's own library.
	_, err := b.Run(ctx, isolated(), b.Dir, rscript, "--vanilla", "-e", ".libPaths(character()); library(tidyverse); library(jsonlite); library(IRkernel)")
	return err
}

func (b *Bases) buildJulia(ctx context.Context, plat string) error {
	release := filepath.Join(b.Dir, "julia", juliaVersion)
	tarball := release + ".tar.gz"
	if err := b.Fetch(ctx, julia[plat].url, julia[plat].sha256, tarball); err != nil {
		return err
	}
	if err := os.MkdirAll(release, 0o755); err != nil {
		return err
	}
	if _, err := b.Run(ctx, isolated(), b.Dir, "tar", "-xzf", tarball, "-C", release, "--strip-components=1"); err != nil {
		return err
	}
	if err := os.Remove(tarball); err != nil {
		return err
	}
	project := b.baseDir(Julia)
	if err := os.MkdirAll(project, 0o755); err != nil {
		return err
	}
	jl := filepath.Join(b.juliaBin(), "julia")
	py := filepath.Join(b.Dir, "python", "bin")
	env := isolated(
		"JULIA_DEPOT_PATH="+filepath.Join(b.Dir, "julia", "depot"),
		"JULIA_PROJECT="+project,
		"JULIA_LOAD_PATH=@"+string(os.PathListSeparator)+"@stdlib",
		"JUPYTER_DATA_DIR="+filepath.Join(b.Dir, "julia", "jupyter"),
		"JUPYTER="+filepath.Join(py, "jupyter"),
		"PATH="+py+string(os.PathListSeparator)+os.Getenv("PATH"),
	)
	var specs []string
	for _, p := range juliaPins {
		specs = append(specs, fmt.Sprintf("PackageSpec(name=%q, version=%q)", p[0], p[1]))
	}
	if _, err := b.Run(ctx, env, b.Dir, jl, "-e", "using Pkg; Pkg.add(["+strings.Join(specs, ", ")+"])"); err != nil {
		return err
	}
	if _, err := b.Run(ctx, env, b.Dir, jl, "-e", `using IJulia; IJulia.installkernel("Julia")`); err != nil {
		return err
	}
	manifest, err := os.ReadFile(filepath.Join(project, "Manifest.toml"))
	if err != nil {
		return err
	}
	if err := os.WriteFile(filepath.Join(project, "lock.txt"), manifest, 0o644); err != nil {
		return err
	}
	_, err = b.Run(ctx, env, b.Dir, jl, "-e", "using DataFrames, CSV, IJulia")
	return err
}

type Study struct {
	Bases *Bases
	Dir   string
}

func ForStudy(b *Bases, dir string) *Study { return &Study{Bases: b, Dir: dir} }

// prepare creates the add-on folders for the ready bases that lack them.
func (s *Study) prepare(ctx context.Context) error {
	b := s.Bases
	l := b.lock("study:" + s.Dir)
	l.Lock()
	defer l.Unlock()
	var errs []error
	if b.Ready(Python) && !s.hasVenv() {
		ctx, cancel := context.WithTimeout(ctx, 2*time.Minute)
		defer cancel()
		if _, err := b.Run(ctx, isolated(), s.Dir, filepath.Join(b.Dir, "python", "bin", "python"), "-m", "venv", "--system-site-packages", filepath.Join(s.Dir, "python")); err != nil {
			errs = append(errs, err)
		}
	}
	if b.Ready(R) {
		errs = append(errs, os.MkdirAll(filepath.Join(s.Dir, "r", "library"), 0o755))
		profile := filepath.Join(s.Dir, "r", "Rprofile")
		if _, err := os.Stat(profile); err != nil {
			errs = append(errs, os.WriteFile(profile, []byte(`options(repos = c(CRAN = "https://cloud.r-project.org"))`+"\n"), 0o644))
		}
	}
	if b.Ready(Julia) {
		project := filepath.Join(s.Dir, "julia", "Project.toml")
		if _, err := os.Stat(project); err != nil {
			errs = append(errs, os.MkdirAll(filepath.Dir(project), 0o755), os.WriteFile(project, nil, 0o644))
		}
	}
	return errors.Join(errs...)
}

func (s *Study) hasVenv() bool {
	_, err := os.Stat(filepath.Join(s.Dir, "python", "bin", "python"))
	return err == nil
}

func (s *Study) Env() []string { return s.EnvContext(context.Background()) }

// EnvContext is Env, giving up on creating the add-on folders when ctx ends.
func (s *Study) EnvContext(ctx context.Context) []string {
	if err := s.prepare(ctx); err != nil {
		slog.Warn("study add-on folders", "dir", s.Dir, "err", err)
	}
	b, sep := s.Bases, string(os.PathListSeparator)
	var env []string
	if b.Ready(Python) && s.hasVenv() {
		env = append(env, "VIRTUAL_ENV="+filepath.Join(s.Dir, "python"))
	}
	if b.Ready(R) {
		env = append(env, "R_LIBS_USER="+filepath.Join(s.Dir, "r", "library"), "R_PROFILE_USER="+filepath.Join(s.Dir, "r", "Rprofile"))
	}
	if b.Ready(Julia) {
		env = append(env,
			"JULIA_PROJECT="+filepath.Join(s.Dir, "julia"),
			"JULIA_DEPOT_PATH="+filepath.Join(s.Dir, "julia", "depot")+sep+filepath.Join(b.Dir, "julia", "depot"),
			"JULIA_LOAD_PATH=@"+sep+b.baseDir(Julia)+sep+"@stdlib")
	}
	if dirs := b.jupyterDirs(); len(dirs) > 0 {
		env = append(env, "JUPYTER_PATH="+strings.Join(dirs, sep))
	}
	return env
}

func (s *Study) PathPrefix() string {
	b := s.Bases
	var dirs []string
	if b.Ready(Python) {
		if s.hasVenv() {
			dirs = append(dirs, filepath.Join(s.Dir, "python", "bin"))
		}
		dirs = append(dirs, filepath.Join(b.Dir, "python", "bin"))
	}
	if b.Ready(R) {
		dirs = append(dirs, filepath.Join(b.Dir, "r", "bin"))
	}
	if b.Ready(Julia) {
		dirs = append(dirs, b.juliaBin())
	}
	return strings.Join(dirs, string(os.PathListSeparator))
}

func (s *Study) Interpreter(lang string) (string, []string, error) {
	if _, ok := displayNames[lang]; !ok {
		return "", nil, fmt.Errorf("unknown language %q", lang)
	}
	if !s.Bases.Ready(lang) {
		return "", nil, notReady(lang)
	}
	switch lang {
	case Python:
		if err := s.prepare(context.Background()); err != nil {
			return "", nil, err
		}
		return filepath.Join(s.Dir, "python", "bin", "python"), nil, nil
	case R:
		return filepath.Join(s.Bases.Dir, "r", "bin", "Rscript"), nil, nil
	}
	return filepath.Join(s.Bases.juliaBin(), "julia"), nil, nil
}

func (s *Study) Packages(ctx context.Context, lang string) (string, error) {
	if _, _, err := s.Interpreter(lang); err != nil {
		return "", err
	}
	lock, err := os.ReadFile(filepath.Join(s.Bases.baseDir(lang), "lock.txt"))
	if err != nil {
		return "", err
	}
	var study []byte
	switch lang {
	case Python:
		study, err = s.Bases.Run(ctx, append(os.Environ(), s.Env()...), s.Dir, filepath.Join(s.Dir, "python", "bin", "python"), "-m", "pip", "freeze", "--local")
	case R:
		study, err = s.Bases.Run(ctx, append(os.Environ(), s.Env()...), s.Dir, filepath.Join(s.Bases.Dir, "r", "bin", "Rscript"), "-e",
			`ip <- installed.packages(lib.loc=Sys.getenv("R_LIBS_USER"))[,c("Package","Version")]; write.table(ip, row.names=FALSE, col.names=FALSE, quote=FALSE)`)
	case Julia:
		study, err = os.ReadFile(filepath.Join(s.Dir, "julia", "Manifest.toml"))
		if errors.Is(err, os.ErrNotExist) {
			study, err = nil, nil
		}
	}
	if err != nil {
		return "", err
	}
	return "# " + lang + " base\n" + string(lock) + "\n# " + lang + " study\n" + string(study), nil
}
