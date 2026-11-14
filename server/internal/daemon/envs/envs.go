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
	"io/fs"
	"log/slog"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"time"
	"unicode/utf8"

	"lykeion/server/internal/daemon/sandbox"
)

const Python, R, Julia = "python", "r", "julia"

var ErrNotReady = errors.New("environment not ready")

// ErrBoundedProvisioningUnavailable prevents an unbounded installer from
// consuming host storage while bounded provisioning is not implemented.
var ErrBoundedProvisioningUnavailable = errors.New("bounded scientific environment setup is unavailable on this machine; use an existing ready environment until a supported update is available")

var ErrDamaged = errors.New("the study's Python environment is damaged")

var displayNames = map[string]string{Python: "Python", R: "R", Julia: "Julia"}

type notReady string

func (e notReady) Error() string {
	return fmt.Sprintf("the %s environment isn't ready on this machine", displayNames[string(e)])
}
func (e notReady) Unwrap() error    { return ErrNotReady }
func (e notReady) Language() string { return string(e) }

type Bases struct {
	Dir string
	// Run's env, when non-nil, is the command's whole environment; nil
	// inherits the daemon's.
	Run   func(ctx context.Context, env []string, dir, name string, args ...string) ([]byte, error)
	Fetch func(ctx context.Context, url, sha256, dest string) error

	// vol caps every install; nil means this machine installs nothing, and
	// volErr says why.
	vol    volume
	volErr error

	mu    sync.Mutex
	locks map[string]*sync.Mutex
	// states holds the bases being built or that failed in this process.
	states  map[string]string
	studies map[string]studyState
}

// NewBases gives the bases at dir without installing any: missing bases stay
// missing.
func NewBases(dir string) *Bases {
	return &Bases{Dir: dir, Run: run, Fetch: fetch}
}

// NewBoundedBases installs missing bases on demand, each in its own capped
// volume, where this machine can mount one without administrator rights.
func NewBoundedBases(dir string) *Bases {
	b := NewBases(dir)
	b.vol, b.volErr = platformVolume()
	return b
}

// run runs one install command in the sandbox, which lets it write only in
// the install's volume. Outside an install nothing runs.
func run(ctx context.Context, env []string, dir, name string, args ...string) ([]byte, error) {
	scope, ok := installOf(ctx)
	if !ok {
		return nil, errors.New("installer commands run only inside a size-limited install")
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return nil, err
	}
	p := sandbox.Policy{Home: home, Read: scope.read, Write: []string{scope.write}}
	cmd, err := p.Command(ctx, name, args...)
	if err != nil {
		return nil, err
	}
	defer sandbox.CloseCommand(cmd)
	killGroupOnCancel(cmd)
	cmd.WaitDelay = 5 * time.Second
	if env == nil {
		env = os.Environ()
	}
	cmd.Env = withScratch(env, scope.write)
	cmd.Dir = dir
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	out, err := cmd.Output()
	if err != nil {
		return out, fmt.Errorf("%s %s: %w: %s", filepath.Base(name), strings.Join(args, " "), err, tail(stderr.String(), 4096))
	}
	return out, nil
}

// withScratch points HOME and TMPDIR inside the install's volume unless env
// already does; the sandbox would refuse writes anywhere else.
func withScratch(env []string, write string) []string {
	out := env[:len(env):len(env)]
	for _, k := range []string{"HOME", "TMPDIR"} {
		v := ""
		for _, e := range env {
			if key, val, _ := strings.Cut(e, "="); key == k {
				v = val
			}
		}
		if v != "" && isUnder(v, write) {
			continue
		}
		d := filepath.Join(scratch(write), strings.ToLower(k))
		os.MkdirAll(d, 0o700)
		out = append(out, k+"="+d)
	}
	return out
}

func isUnder(path, dir string) bool {
	rel, err := filepath.Rel(dir, path)
	return err == nil && rel != ".." && !strings.HasPrefix(rel, "../")
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
	for _, e := range Isolate(os.Environ()) {
		if k, _, _ := strings.Cut(e, "="); !set[k] {
			env = append(env, e)
		}
	}
	return append(env, extra...)
}

// Isolate is env without anything that would point a build, a check or a
// study step at the person's own Python, R, Julia, conda or Jupyter setup.
func Isolate(env []string) []string {
	var out []string
	for _, e := range env {
		k, _, _ := strings.Cut(e, "=")
		if k == "R_HOME" || k == "VIRTUAL_ENV" || hasAnyPrefix(k, "ELAN_", "LEAN_", "LAKE_", "CONDA_", "MAMBA_", "PYTHON", "R_LIBS", "R_PROFILE", "R_ENVIRON", "JULIA_", "JUPYTER") {
			continue
		}
		out = append(out, e)
	}
	return out
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
	// This caps each direct artifact stream; package installation is separate.
	return fetchBounded(ctx, url, sum, dest, 512<<20)
}

func fetchBounded(ctx context.Context, url, sum, dest string, limit int64) error {
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
	if resp.ContentLength > limit {
		return fmt.Errorf("fetch %s: download exceeds %d bytes", url, limit)
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
	n, err := io.Copy(io.MultiWriter(tmp, h), io.LimitReader(resp.Body, limit+1))
	if cerr := tmp.Close(); err == nil {
		err = cerr
	}
	if err != nil {
		return err
	}
	if n > limit {
		return fmt.Errorf("fetch %s: download exceeds %d bytes", url, limit)
	}
	if got := hex.EncodeToString(h.Sum(nil)); got != sum {
		return fmt.Errorf("fetch %s: sha256 %s, want %s", url, got, sum)
	}
	return os.Rename(tmp.Name(), dest)
}

// CondaPlatform is this machine's conda platform, such as osx-arm64.
func CondaPlatform() (string, error) { return platform() }

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

// Folders are the bases' own folders, which a study's code and its agent
// may read: every language base and micromamba, built or not.
func (b *Bases) Folders() []string {
	return []string{filepath.Join(b.Dir, Python), filepath.Join(b.Dir, R), filepath.Join(b.Dir, Julia), filepath.Join(b.Dir, "micromamba"), filepath.Join(b.Dir, "lean")}
}

// mountDir is where a base's volume is mounted.
func (b *Bases) mountDir(lang string) string { return filepath.Join(b.Dir, lang) }

// baseDir is where a base's lock.txt and ready marker live.
func (b *Bases) baseDir(lang string) string {
	if lang == Julia {
		return filepath.Join(b.Dir, "julia", "base")
	}
	return filepath.Join(b.Dir, lang)
}

// juliaBundled is the release's own depot, which holds its precompiled
// standard library. Naming any depot drops it from Julia's defaults, and
// without it Julia recompiles REPL, which needs a terminal the sandbox does
// not give.
func (b *Bases) juliaBundled() string {
	return filepath.Join(b.Dir, "julia", juliaVersion, "share", "julia")
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
	if hasMarker(b.baseDir(lang)) {
		return true
	}
	// A sealed base is mounted again after a restart, on first use.
	return b.attachSealed(lang, b.mountDir(lang)) && hasMarker(b.baseDir(lang))
}

func hasMarker(dir string) bool {
	marker, err := os.Lstat(filepath.Join(dir, "ready"))
	return err == nil && marker.Mode().IsRegular()
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

// Ensure installs a missing base, and Python first for R and Julia, which
// use it. Only one install of a base runs at a time; a waiting caller can
// cancel. Without a volume to cap the install, nothing is installed.
func (b *Bases) Ensure(ctx context.Context, lang string) error {
	if _, ok := displayNames[lang]; !ok {
		return fmt.Errorf("unknown language %q", lang)
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	if b.Ready(lang) {
		return nil
	}
	if b.vol == nil {
		return fmt.Errorf("%s environment: %w", displayNames[lang], b.unavailable())
	}
	if lang != Python {
		if err := b.Ensure(ctx, Python); err != nil {
			return err
		}
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
	if b.Ready(lang) {
		return nil
	}
	b.setState(lang, "building")
	err := b.provision(ctx, lang, b.mountDir(lang), filepath.Join(b.baseDir(lang), "ready"), baseSizes[lang], func(ctx context.Context) error { return b.build(ctx, lang) })
	if err != nil {
		err = fmt.Errorf("installing the %s environment: %w", displayNames[lang], err)
		b.setState(lang, Failed(err))
	} else {
		b.setState(lang, "")
	}
	return err
}

// build installs a base into its folder, which is the root of the install's
// volume.
func (b *Bases) build(ctx context.Context, lang string) error {
	plat, err := platform()
	if err != nil {
		return err
	}
	if lang != Python && !b.Ready(Python) {
		return fmt.Errorf("the %s base needs the Python base, which isn't ready", displayNames[lang])
	}
	switch lang {
	case Python, R:
		err = b.buildConda(ctx, plat, lang)
	case Julia:
		err = b.buildJulia(ctx, plat)
	}
	if err != nil {
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
// base that is simply not installed yet, where it installs on first use, is
// left out.
func (b *Bases) States() map[string]string {
	out := map[string]string{}
	for lang := range displayNames {
		if b.Ready(lang) {
			out[lang] = "ready"
		}
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	for lang := range displayNames {
		if _, ready := out[lang]; ready {
			continue
		}
		if state := b.states[lang]; state != "" {
			out[lang] = state
		} else if b.vol == nil {
			out[lang] = Failed(b.unavailable())
		}
	}
	return out
}

// Failed is the state reported for something that failed with err, its
// reason cut to the 200 bytes the server accepts and stripped of the NUL bytes
// it refuses.
func Failed(err error) string {
	return "failed: " + cut(strings.ReplaceAll(err.Error(), "\x00", ""), 200)
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
	// environment it creates in $HOME/.conda, so both point inside the
	// install's volume, apart from the person's own conda.
	env := isolated("MAMBA_ROOT_PREFIX="+filepath.Join(scratch(prefix), "mamba"), "HOME="+filepath.Join(scratch(prefix), "home"))
	// The prefix is the volume's root and holds the install's scratch folder;
	// micromamba installs only into a folder already marked as an environment.
	if err := os.MkdirAll(filepath.Join(prefix, "conda-meta"), 0o755); err != nil {
		return err
	}
	pins := pythonPins
	if lang == R {
		pins = rPins
	}
	if _, err := b.Run(ctx, env, prefix, mm, append([]string{"--no-rc", "install", "-y", "-p", prefix, "-c", "conda-forge", "--override-channels"}, pins...)...); err != nil {
		return err
	}
	lock, err := b.Run(ctx, env, prefix, mm, "--no-rc", "env", "export", "-p", prefix, "--explicit")
	if err != nil {
		return err
	}
	if err := os.WriteFile(filepath.Join(prefix, "lock.txt"), lock, 0o644); err != nil {
		return err
	}
	// The package cache is in the scratch folder, removed before sealing.
	return b.verifyConda(ctx, lang, prefix)
}

func (b *Bases) verifyConda(ctx context.Context, lang, prefix string) error {
	if lang == Python {
		_, err := b.Run(ctx, isolated(), prefix, filepath.Join(prefix, "bin", "python"), "-I", "-c", "import numpy, pandas, scipy, matplotlib, statsmodels, ipykernel, jupyter_client")
		return err
	}
	// IRkernel registers itself through the Python base's jupyter.
	path := isolated("PATH=" + filepath.Join(b.Dir, "python", "bin") + string(os.PathListSeparator) + os.Getenv("PATH"))
	rscript := filepath.Join(prefix, "bin", "Rscript")
	if _, err := b.Run(ctx, path, prefix, rscript, "--vanilla", "-e", fmt.Sprintf("IRkernel::installspec(user = FALSE, prefix = %q)", prefix)); err != nil {
		return err
	}
	// --vanilla still honours R_LIBS_USER; clearing .libPaths leaves only the base's own library.
	_, err := b.Run(ctx, isolated(), prefix, rscript, "--vanilla", "-e", ".libPaths(character()); library(tidyverse); library(jsonlite); library(IRkernel)")
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
	if _, err := b.Run(ctx, isolated(), b.mountDir(Julia), "tar", "-xzf", tarball, "-C", release, "--strip-components=1"); err != nil {
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
		"JULIA_DEPOT_PATH="+filepath.Join(b.Dir, "julia", "depot")+string(os.PathListSeparator)+b.juliaBundled(),
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
	if _, err := b.Run(ctx, env, b.mountDir(Julia), jl, "-e", "using Pkg; Pkg.add(["+strings.Join(specs, ", ")+"])"); err != nil {
		return err
	}
	if _, err := b.Run(ctx, env, b.mountDir(Julia), jl, "-e", `using IJulia; IJulia.installkernel("Julia")`); err != nil {
		return err
	}
	manifest, err := os.ReadFile(filepath.Join(project, "Manifest.toml"))
	if err != nil {
		return err
	}
	if err := os.WriteFile(filepath.Join(project, "lock.txt"), manifest, 0o644); err != nil {
		return err
	}
	_, err = b.Run(ctx, env, b.mountDir(Julia), jl, "-e", "using DataFrames, CSV, IJulia")
	return err
}

type Study struct {
	// ReadOnly refuses implicit preparation or repair for recorded execution.
	ReadOnly      bool
	leanMu        sync.Mutex
	leanDir       string
	Bases         *Bases
	Dir           string
	PythonVersion string
	PersistPython func(context.Context, string) error
	// Run runs every step in the study's folders: making them, and the
	// study's own interpreters, which load whatever its code installed. The
	// code may have planted anything there, so Run must be the sandbox;
	// with none, nothing in the folders is touched. Its env holds only what
	// the study adds; the runner supplies the rest from its run.
	Run func(ctx context.Context, env []string, dir, name string, args ...string) ([]byte, error)
}

func ForStudy(b *Bases, dir string) *Study { return &Study{Bases: b, Dir: dir} }

// ForRun makes an independent view for run-specific callbacks. The selected
// Lean pair is snapshotted under its lock; shared study version/setup state
// remains with Bases. A run never copies the original view's mutex.
func (s *Study) ForRun() *Study {
	return &Study{Bases: s.Bases, Dir: s.Dir, PythonVersion: s.PythonVersion,
		PersistPython: s.PersistPython, Run: s.Run, leanDir: s.selectedLean(), ReadOnly: s.ReadOnly}
}

var errNoRunner = errors.New("the study's folders can only be used in a sandbox, and this study has none")

// prepare creates the add-on folders for the ready bases that lack them,
// through Run. A venv folder that exists without its interpreter is made
// again with --clear, in Run too: whatever the study's code left in it runs
// only there.
func (s *Study) prepare(ctx context.Context) error {
	if s.Run == nil {
		return errNoRunner
	}
	if s.ReadOnly {
		return s.Ready(Python)
	}
	b := s.Bases
	unlock, err := s.studyLock(ctx)
	if err != nil {
		return err
	}
	defer unlock()
	s.state()
	_, condaErr := os.Lstat(filepath.Join(s.Dir, "conda"))
	version := s.RecordedPython()
	needsChosen := version != "" && (version != DefaultPythonVersion || !s.hasVenv())
	// A recorded non-default choice must also replace a surviving default
	// venv after an interrupted setup. A marker without its Python is damaged.
	if needsChosen && s.condaReady() && !s.condaPython() {
		if _, err := s.Run(ctx, nil, s.Dir, "/bin/sh", "-c", `rm -f -- "$1/.lyk-ready"`, "sh", filepath.Join(s.Dir, "conda")); err != nil {
			return err
		}
	}
	if !s.condaReady() && (condaErr == nil || needsChosen) {
		setupCtx, cancel := context.WithTimeout(ctx, setupLimit)
		defer cancel()
		if _, err := s.setUpConda(setupCtx, s.RecordedPython(), nil, ""); err != nil {
			return err
		}
	}
	if s.condaReady() && s.condaPython() && !s.hasVenv() {
		if err := s.condaVenv(ctx, false); err != nil {
			return err
		}
	}
	var errs []error
	if b.Ready(Python) {
		venv := filepath.Join(s.Dir, "python")
		python := filepath.Join(b.Dir, "python", "bin", "python")
		if s.condaReady() && s.condaPython() {
			python = filepath.Join(s.Dir, "conda", "bin", "python")
		}
		ctx, cancel := context.WithTimeout(ctx, 2*time.Minute)
		defer cancel()
		if _, err := os.Lstat(venv); errors.Is(err, fs.ErrNotExist) {
			if _, err := s.Run(ctx, nil, s.Dir, python, "-m", "venv", "--system-site-packages", venv); err != nil {
				errs = append(errs, err)
			}
		} else if err != nil {
			errs = append(errs, err)
		} else if !s.hasVenv() {
			if _, err := s.Run(ctx, nil, s.Dir, python, "-m", "venv", "--clear", "--system-site-packages", venv); err != nil {
				errs = append(errs, fmt.Errorf("%w and could not be rebuilt: %v", ErrDamaged, err))
			} else if !s.hasVenv() {
				errs = append(errs, fmt.Errorf("%w and could not be rebuilt: it has no interpreter", ErrDamaged))
			}
		}
	}
	steps := []string{"/bin/mkdir -p bin"}
	if b.Ready(R) {
		steps = append(steps, `mkdir -p r/library && { [ -e r/Rprofile ] || printf '%s\n' "$1" > r/Rprofile; }`)
	}
	if b.Ready(Julia) {
		steps = append(steps, `mkdir -p julia && { [ -e julia/Project.toml ] || : > julia/Project.toml; }`)
	}
	if len(steps) > 0 {
		if _, err := s.Run(ctx, nil, s.Dir, "/bin/sh", "-c", strings.Join(steps, " && "), "sh", `options(repos = c(CRAN = "https://cloud.r-project.org"))`); err != nil {
			errs = append(errs, err)
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
	env, err := s.Environment(ctx)
	if err != nil {
		slog.Warn("study add-on folders", "dir", s.Dir, "err", err)
	}
	return env
}

// Environment prepares every study environment before it can be used by a process.
func (s *Study) Environment(ctx context.Context) ([]string, error) {
	if err := s.prepare(ctx); err != nil {
		return nil, err
	}
	b, sep := s.Bases, string(os.PathListSeparator)
	env := []string{"MAMBA_ROOT_PREFIX=" + filepath.Join(s.Dir, ".mamba")}
	if b.Ready(Python) && s.hasVenv() {
		env = append(env, "VIRTUAL_ENV="+filepath.Join(s.Dir, "python"))
	}
	if b.Ready(R) {
		env = append(env, "R_LIBS_USER="+filepath.Join(s.Dir, "r", "library"), "R_PROFILE_USER="+filepath.Join(s.Dir, "r", "Rprofile"))
	}
	if b.Ready(Julia) {
		env = append(env,
			"JULIA_PROJECT="+filepath.Join(s.Dir, "julia"),
			"JULIA_DEPOT_PATH="+filepath.Join(s.Dir, "julia", "depot")+sep+filepath.Join(b.Dir, "julia", "depot")+sep+b.juliaBundled(),
			"JULIA_LOAD_PATH=@"+sep+b.baseDir(Julia)+sep+"@stdlib")
	}
	if err := s.loadLean(ctx); err != nil {
		return nil, err
	}
	if dir := s.selectedLean(); dir != "" {
		env = append(env, "ELAN_HOME="+filepath.Join(dir, "elan"))
	}
	if dirs := b.jupyterDirs(); len(dirs) > 0 {
		env = append(env, "JUPYTER_PATH="+strings.Join(dirs, sep))
	}
	return env, nil
}

func (s *Study) PathPrefix() string {
	b := s.Bases
	dirs := []string{filepath.Join(s.Dir, "bin"), filepath.Join(s.Dir, "conda", "bin")}
	if dir := s.selectedLean(); dir != "" {
		dirs = append(dirs, filepath.Join(dir, "elan", "bin"))
	}
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
	return s.interpreter(context.Background(), lang)
}

func (s *Study) interpreter(ctx context.Context, lang string) (string, []string, error) {
	if s.ReadOnly {
		if err := s.Ready(lang); err != nil {
			return "", nil, err
		}
	}
	if _, ok := displayNames[lang]; !ok {
		return "", nil, fmt.Errorf("unknown language %q", lang)
	}
	if !s.Bases.Ready(lang) {
		if err := s.Bases.Ensure(ctx, lang); err != nil {
			return "", nil, fmt.Errorf("%w: %w", notReady(lang), err)
		}
	}
	if err := s.prepare(ctx); err != nil {
		return "", nil, err
	}
	switch lang {
	case Python:
		return filepath.Join(s.Dir, "python", "bin", "python"), nil, nil
	case R:
		return filepath.Join(s.Bases.Dir, "r", "bin", "Rscript"), nil, nil
	}
	return filepath.Join(s.Bases.juliaBin(), "julia"), nil, nil
}

func (s *Study) Packages(ctx context.Context, lang string) (string, error) {
	if _, _, err := s.interpreter(ctx, lang); err != nil {
		return "", err
	}
	run := s.Run
	if run == nil {
		return "", errNoRunner
	}
	var lock []byte
	var err error
	if lang == Python && s.condaReady() && s.condaPython() {
		// The study may replace its lock with a link. Read it only inside the
		// sandbox, just like its own interpreters and package manifests.
		lock, err = run(ctx, nil, s.Dir, "/bin/cat", filepath.Join(s.Dir, "conda", "lock.txt"))
	} else {
		lock, err = readBaseLock(filepath.Join(s.Bases.baseDir(lang), "lock.txt"))
	}
	if err != nil {
		return "", err
	}
	env, err := s.Environment(ctx)
	if err != nil {
		return "", err
	}
	var study []byte
	switch lang {
	case Python:
		study, err = run(ctx, env, s.Dir, filepath.Join(s.Dir, "python", "bin", "python"), "-m", "pip", "freeze", "--local")
	case R:
		study, err = run(ctx, env, s.Dir, filepath.Join(s.Bases.Dir, "r", "bin", "Rscript"), "-e",
			`ip <- installed.packages(lib.loc=Sys.getenv("R_LIBS_USER"))[,c("Package","Version")]; write.table(ip, row.names=FALSE, col.names=FALSE, quote=FALSE)`)
	case Julia:
		study, err = run(ctx, nil, s.Dir, "/bin/sh", "-c", `[ -e julia/Manifest.toml ] || exit 0; head -c 1048577 julia/Manifest.toml`)
		if err == nil && len(study) > 1<<20 {
			err = errors.New("the study's Julia manifest is over 1 MiB")
		}
	}
	if err != nil {
		return "", err
	}
	return "# " + lang + " base\n" + string(lock) + "\n# " + lang + " study\n" + string(study), nil
}
