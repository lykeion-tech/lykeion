package envs

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

type fakeRun struct {
	mu                              sync.Mutex
	calls                           []string
	envs                            [][]string
	failVerify, failJulia, failVenv bool
}

type waitingContext struct {
	context.Context
	waiting chan struct{}
	once    sync.Once
}

func (c *waitingContext) Done() <-chan struct{} {
	c.once.Do(func() { close(c.waiting) })
	return c.Context.Done()
}

func (f *fakeRun) run(_ context.Context, env []string, dir, name string, args ...string) ([]byte, error) {
	line := strings.Join(append([]string{filepath.Base(name)}, args...), " ")
	f.mu.Lock()
	f.calls = append(f.calls, line)
	f.envs = append(f.envs, env)
	f.mu.Unlock()
	switch {
	case filepath.Base(name) == "micromamba" && (slicesIndex(args, "install") >= 0 || slicesIndex(args, "create") >= 0):
		prefix := args[slicesIndex(args, "-p")+1]
		if err := os.MkdirAll(filepath.Join(prefix, "bin"), 0o755); err != nil {
			return nil, err
		}
		return nil, os.WriteFile(filepath.Join(prefix, "bin", "python"), nil, 0o755)
	case filepath.Base(name) == "micromamba" && slicesIndex(args, "export") >= 0:
		return []byte("@EXPLICIT\nhttps://conda.anaconda.org/conda-forge/python-3.13.16.conda\n"), nil
	case len(args) >= 4 && args[1] == "venv":
		if f.failVenv {
			return nil, errors.New("venv: ensurepip failed")
		}
		venv := args[len(args)-1]
		if err := os.MkdirAll(filepath.Join(venv, "bin"), 0o755); err != nil {
			return nil, err
		}
		return nil, os.WriteFile(filepath.Join(venv, "bin", "python"), nil, 0o755)
	case strings.Contains(line, "freeze --local"):
		return []byte("requests==2.32.0\n"), nil
	case name == "/bin/sh" || name == "/bin/cat":
		cmd := exec.Command(name, args...)
		cmd.Dir = dir
		return cmd.Output()
	case strings.Contains(line, "import numpy") && f.failVerify:
		return nil, errors.New("ModuleNotFoundError: No module named 'numpy'")
	case strings.Contains(line, "Pkg.add"):
		project, _ := envValue(env, "JULIA_PROJECT")
		return nil, os.WriteFile(filepath.Join(project, "Manifest.toml"), []byte("julia_version = \"1.12.6\"\n"), 0o644)
	case strings.Contains(line, "using DataFrames, CSV, IJulia") && f.failJulia:
		return nil, errors.New("ArgumentError: Package CSV not found")
	}
	return nil, nil
}

func slicesIndex(s []string, v string) int {
	for i, x := range s {
		if x == v {
			return i
		}
	}
	return -1
}

func fakeBases(t *testing.T) (*Bases, *fakeRun) {
	t.Helper()
	f := &fakeRun{}
	b := &Bases{Dir: t.TempDir(), Run: f.run, Fetch: func(_ context.Context, _, _, dest string) error {
		if err := os.MkdirAll(filepath.Dir(dest), 0o755); err != nil {
			return err
		}
		return os.WriteFile(dest, nil, 0o755)
	}}
	b.vol = newFakeVolume()
	return b, f
}

// emptyOrAbsent fails unless dir holds nothing.
func emptyOrAbsent(t *testing.T, dir string) {
	t.Helper()
	entries, err := os.ReadDir(dir)
	if err != nil && !os.IsNotExist(err) {
		t.Fatal(err)
	}
	if len(entries) > 0 {
		t.Fatalf("%s holds %v", dir, entries)
	}
}

// newStudy is a study whose folders are prepared with the bases' runner,
// as the door's sandbox would.
func newStudy(b *Bases, dir string) *Study {
	s := ForStudy(b, dir)
	s.Run = func(ctx context.Context, env []string, dir, name string, args ...string) ([]byte, error) {
		return b.Run(ctx, isolated(env...), dir, name, args...)
	}
	return s
}

func index(calls []string, sub string) int {
	for i, c := range calls {
		if strings.Contains(c, sub) {
			return i
		}
	}
	return -1
}

func TestABaseIsBuiltVerifiedAndMarkedReady(t *testing.T) {
	b, f := fakeBases(t)
	if err := b.buildBaseForTest(context.Background(), Python); err != nil {
		t.Fatal(err)
	}
	create, export, verify := index(f.calls, "micromamba "), index(f.calls, "env export"), index(f.calls, "import numpy, pandas, scipy, matplotlib, statsmodels, ipykernel, jupyter_client")
	if create < 0 || !(create < export && export < verify) {
		t.Fatalf("order create=%d export=%d verify=%d: %q", create, export, verify, f.calls)
	}
	if !strings.Contains(f.calls[create], " install ") || !strings.Contains(f.calls[create], "-c conda-forge --override-channels") {
		t.Fatalf("create: %s", f.calls[create])
	}
	for _, pin := range pythonPins {
		if !strings.Contains(f.calls[create]+" ", " "+pin+" ") {
			t.Fatalf("create lacks %s: %s", pin, f.calls[create])
		}
	}
	if _, err := os.Stat(scratch(filepath.Join(b.Dir, "python"))); !os.IsNotExist(err) {
		t.Fatalf("the install's scratch folder was sealed into the base: %v", err)
	}
	if v := b.vol.(*fakeVolume); !v.readOnly[filepath.Join(b.Dir, "python")] {
		t.Fatal("the finished base is not mounted read-only")
	}
	if !b.Ready(Python) {
		t.Fatal("python not ready")
	}
	if _, err := os.Stat(filepath.Join(b.Dir, "python", "ready")); err != nil {
		t.Fatal(err)
	}
	lock, err := os.ReadFile(filepath.Join(b.Dir, "python", "lock.txt"))
	if err != nil || !strings.HasPrefix(string(lock), "@EXPLICIT") {
		t.Fatalf("lock %q %v", lock, err)
	}
}

func TestAFailedBaseBuildLeavesNothing(t *testing.T) {
	b, f := fakeBases(t)
	if err := os.MkdirAll(filepath.Join(b.Dir, "r"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(b.Dir, "r", "ready"), nil, 0o644); err != nil {
		t.Fatal(err)
	}
	f.failVerify = true
	if err := b.buildBaseForTest(context.Background(), Python); err == nil {
		t.Fatal("want error")
	}
	if b.Ready(Python) {
		t.Fatal("python ready after a failed build")
	}
	emptyOrAbsent(t, filepath.Join(b.Dir, "python"))
	emptyOrAbsent(t, b.images())
	if !b.Ready(R) {
		t.Fatal("the ready R base was disturbed")
	}
}

func TestJuliaInstallsThePythonBaseFirst(t *testing.T) {
	b, f := fakeBases(t)
	f.failVerify = true
	if err := b.buildBaseForTest(context.Background(), Julia); err == nil || !strings.Contains(err.Error(), "Python") {
		t.Fatalf("Julia without a Python base: %v", err)
	}
	if b.Ready(Julia) || index(f.calls, "Pkg.add") >= 0 {
		t.Fatalf("julia built without Python: %q", f.calls)
	}
	f.failVerify, f.calls = false, nil
	if err := b.buildBaseForTest(context.Background(), Julia); err != nil {
		t.Fatal(err)
	}
	if py, jl := index(f.calls, "-p "+filepath.Join(b.Dir, "python")), index(f.calls, "Pkg.add"); py < 0 || jl < py || !b.Ready(Python) || !b.Ready(Julia) {
		t.Fatalf("Python %d before Julia %d: %q", py, jl, f.calls)
	}
}

func TestAStudyInstallsOnlyTheLanguageItUses(t *testing.T) {
	b, f := fakeBases(t)
	s := newStudy(b, t.TempDir())
	if _, _, err := s.Interpreter(R); err != nil {
		t.Fatal(err)
	}
	if !b.Ready(Python) || !b.Ready(R) || b.Ready(Julia) || index(f.calls, "Pkg.add") >= 0 {
		t.Fatalf("python %v r %v julia %v: %q", b.Ready(Python), b.Ready(R), b.Ready(Julia), f.calls)
	}
}

func TestAFetchWithTheWrongHashIsRefused(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.Write([]byte("tampered")) }))
	defer srv.Close()
	sum := sha256.Sum256([]byte("original"))
	dir := t.TempDir()
	dest := filepath.Join(dir, "micromamba")
	if err := NewBases(t.TempDir()).Fetch(context.Background(), srv.URL, hex.EncodeToString(sum[:]), dest); err == nil {
		t.Fatal("want hash error")
	}
	if entries, _ := os.ReadDir(dir); len(entries) != 0 {
		t.Fatalf("left %v", entries)
	}
	good := sha256.Sum256([]byte("tampered"))
	if err := NewBases(t.TempDir()).Fetch(context.Background(), srv.URL, hex.EncodeToString(good[:]), dest); err != nil {
		t.Fatal(err)
	}
	if got, _ := os.ReadFile(dest); string(got) != "tampered" {
		t.Fatalf("got %q", got)
	}
}

func TestFetchRejectsOversizedStreamWithoutReplacingExistingFile(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.(http.Flusher).Flush() // No Content-Length: enforce the limit while reading.
		_, _ = w.Write([]byte("123456789"))
	}))
	defer srv.Close()
	dir := t.TempDir()
	dest := filepath.Join(dir, "artifact")
	if err := os.WriteFile(dest, []byte("previous"), 0o600); err != nil {
		t.Fatal(err)
	}
	sum := sha256.Sum256([]byte("123456789"))
	err := fetchBounded(context.Background(), srv.URL, hex.EncodeToString(sum[:]), dest, 8)
	if err == nil || !strings.Contains(err.Error(), "exceeds") {
		t.Fatalf("expected size error, got %v", err)
	}
	if got, err := os.ReadFile(dest); err != nil || string(got) != "previous" {
		t.Fatalf("previous artifact changed: %q, %v", got, err)
	}
	if entries, err := os.ReadDir(dir); err != nil || len(entries) != 1 {
		t.Fatalf("temporary download left behind: %v, %v", entries, err)
	}
}

func TestFetchAcceptsStreamAtSizeLimit(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte("12345678"))
	}))
	defer srv.Close()
	dest := filepath.Join(t.TempDir(), "artifact")
	sum := sha256.Sum256([]byte("12345678"))
	if err := fetchBounded(context.Background(), srv.URL, hex.EncodeToString(sum[:]), dest, 8); err != nil {
		t.Fatal(err)
	}
	if got, err := os.ReadFile(dest); err != nil || string(got) != "12345678" {
		t.Fatalf("download: %q, %v", got, err)
	}
}

func TestFetchRejectsOversizedDeclaredLengthBeforeReadingBody(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Length", "9")
		w.WriteHeader(http.StatusOK)
		w.(http.Flusher).Flush()
		<-r.Context().Done()
	}))
	defer srv.Close()
	dir := t.TempDir()
	dest := filepath.Join(dir, "artifact")
	if err := os.WriteFile(dest, []byte("previous"), 0o600); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	err := fetchBounded(ctx, srv.URL, "unused", dest, 8)
	if err == nil || !strings.Contains(err.Error(), "exceeds") {
		t.Fatalf("expected size error before reading body, got %v", err)
	}
	if got, err := os.ReadFile(dest); err != nil || string(got) != "previous" {
		t.Fatalf("previous artifact changed: %q, %v", got, err)
	}
	if entries, err := os.ReadDir(dir); err != nil || len(entries) != 1 {
		t.Fatalf("temporary download left behind: %v, %v", entries, err)
	}
}

func envValue(env []string, key string) (string, bool) {
	for _, e := range env {
		if v, ok := strings.CutPrefix(e, key+"="); ok {
			return v, true
		}
	}
	return "", false
}

func TestAStudyEnvNamesOnlyReadyBases(t *testing.T) {
	b, _ := fakeBases(t)
	if err := b.buildBaseForTest(context.Background(), Python); err != nil {
		t.Fatal(err)
	}
	s := newStudy(b, t.TempDir())
	env := s.Env()
	if v, ok := envValue(env, "VIRTUAL_ENV"); !ok || v != filepath.Join(s.Dir, "python") {
		t.Fatalf("VIRTUAL_ENV %q %v", v, ok)
	}
	for _, k := range []string{"R_LIBS_USER", "R_PROFILE_USER", "JULIA_PROJECT", "JULIA_DEPOT_PATH"} {
		if _, ok := envValue(env, k); ok {
			t.Fatalf("%s set without its base: %q", k, env)
		}
	}
	if v, _ := envValue(env, "JUPYTER_PATH"); v != filepath.Join(b.Dir, "python", "share", "jupyter") {
		t.Fatalf("JUPYTER_PATH %q", v)
	}
	if p := s.PathPrefix(); p != strings.Join([]string{filepath.Join(s.Dir, "bin"), filepath.Join(s.Dir, "conda", "bin"), filepath.Join(s.Dir, "python", "bin"), filepath.Join(b.Dir, "python", "bin")}, string(os.PathListSeparator)) {
		t.Fatalf("path %q", p)
	}
	b.vol = nil
	_, _, err := s.Interpreter(R)
	if !errors.Is(err, ErrNotReady) || !strings.Contains(err.Error(), "bounded scientific environment setup is unavailable") {
		t.Fatalf("err %v", err)
	}
	name, _, err := s.Interpreter(Python)
	if err != nil || name != filepath.Join(s.Dir, "python", "bin", "python") {
		t.Fatalf("python %q %v", name, err)
	}
}

func TestStudiesHaveSeparateAddOnFolders(t *testing.T) {
	b, _ := fakeBases(t)
	for _, lang := range []string{Python, R} {
		if err := b.buildBaseForTest(context.Background(), lang); err != nil {
			t.Fatal(err)
		}
	}
	one, two := newStudy(b, t.TempDir()).Env(), newStudy(b, t.TempDir()).Env()
	for _, k := range []string{"VIRTUAL_ENV", "R_LIBS_USER"} {
		a, okA := envValue(one, k)
		c, okC := envValue(two, k)
		if !okA || !okC || a == c {
			t.Fatalf("%s: %q %q", k, a, c)
		}
	}
	s := newStudy(b, t.TempDir())
	s.Env()
	profile, err := os.ReadFile(filepath.Join(s.Dir, "r", "Rprofile"))
	if err != nil || string(profile) != `options(repos = c(CRAN = "https://cloud.r-project.org"))`+"\n" {
		t.Fatalf("Rprofile %q %v", profile, err)
	}
	if fi, err := os.Stat(filepath.Join(s.Dir, "r", "library")); err != nil || !fi.IsDir() {
		t.Fatal(err)
	}
}

func TestPackagesJoinsBaseAndStudyParts(t *testing.T) {
	b, _ := fakeBases(t)
	if err := b.buildBaseForTest(context.Background(), Python); err != nil {
		t.Fatal(err)
	}
	lock, err := os.ReadFile(filepath.Join(b.Dir, "python", "lock.txt"))
	if err != nil {
		t.Fatal(err)
	}
	got, err := newStudy(b, t.TempDir()).Packages(context.Background(), Python)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(got, "# python base\n"+string(lock)) || !strings.Contains(got, "# python study\nrequests==2.32.0\n") {
		t.Fatalf("packages %q", got)
	}
}

func TestFilesWhereABaseGoesArePreserved(t *testing.T) {
	b, f := fakeBases(t)
	junk := filepath.Join(b.Dir, "python", "junk")
	if err := os.MkdirAll(junk, 0o755); err != nil {
		t.Fatal(err)
	}
	err := b.buildBaseForTest(context.Background(), Python)
	if err == nil || !strings.Contains(err.Error(), "move them away") {
		t.Fatalf("install over existing files: %v", err)
	}
	if _, err := os.Stat(junk); err != nil || b.Ready(Python) || len(f.calls) != 0 {
		t.Fatalf("junk %v ready %v calls %q", err, b.Ready(Python), f.calls)
	}
}

func TestAnInterruptedInstallIsReplaced(t *testing.T) {
	b, _ := fakeBases(t)
	v := b.vol.(*fakeVolume)
	v.busy = true
	if err := b.buildBaseForTest(context.Background(), Python); err == nil || !strings.Contains(err.Error(), "not published") {
		t.Fatalf("install whose volume stays busy: %v", err)
	}
	if b.Ready(Python) {
		t.Fatal("a volume still in use was published")
	}
	v.busy = false
	if err := b.buildBaseForTest(context.Background(), Python); err != nil {
		t.Fatal(err)
	}
	if !b.Ready(Python) || v.creates != 2 {
		t.Fatalf("ready %v creates %d", b.Ready(Python), v.creates)
	}
}

func TestASealedBaseIsMountedAgainAfterARestart(t *testing.T) {
	b, f := fakeBases(t)
	if err := b.buildBaseForTest(context.Background(), Python); err != nil {
		t.Fatal(err)
	}
	b.vol.(*fakeVolume).forget()
	f.calls = nil
	if !b.Ready(Python) || len(f.calls) != 0 || b.vol.(*fakeVolume).creates != 1 {
		t.Fatalf("ready %v calls %q", b.Ready(Python), f.calls)
	}
}

func TestNoVolumeInstallsNothing(t *testing.T) {
	b, f := fakeBases(t)
	b.vol, b.volErr = nil, errors.New("install fuse2fs")
	err := b.Ensure(context.Background(), Python)
	if !errors.Is(err, ErrBoundedProvisioningUnavailable) || !strings.Contains(err.Error(), "install fuse2fs") || len(f.calls) != 0 {
		t.Fatalf("err %v calls %q", err, f.calls)
	}
}

func TestVerificationIgnoresThePersonsEnvironment(t *testing.T) {
	t.Setenv("PYTHONPATH", "/person/site")
	t.Setenv("R_LIBS_USER", "/person/rlib")
	t.Setenv("CONDA_PREFIX", "/person/conda")
	t.Setenv("JULIA_LOAD_PATH", "/person/julia")
	b, f := fakeBases(t)
	for _, lang := range []string{Python, R, Julia} {
		if err := b.buildBaseForTest(context.Background(), lang); err != nil {
			t.Fatal(err)
		}
	}
	py := index(f.calls, "import numpy")
	if !strings.HasPrefix(f.calls[py], "python -I -c ") {
		t.Fatalf("python verify: %s", f.calls[py])
	}
	rv := index(f.calls, "library(tidyverse)")
	if !strings.HasPrefix(f.calls[rv], "Rscript --vanilla -e .libPaths(character()); library(tidyverse)") {
		t.Fatalf("R verify: %s", f.calls[rv])
	}
	for i, env := range f.envs {
		if env == nil {
			t.Fatalf("%s inherits the whole environment", f.calls[i])
		}
		for _, k := range []string{"PYTHONPATH", "R_LIBS_USER", "CONDA_PREFIX"} {
			if _, ok := envValue(env, k); ok {
				t.Fatalf("%s carries %s", f.calls[i], k)
			}
		}
		if v, ok := envValue(env, "JULIA_LOAD_PATH"); ok && v != "@"+string(os.PathListSeparator)+"@stdlib" {
			t.Fatalf("%s: JULIA_LOAD_PATH %q", f.calls[i], v)
		}
	}
	jv := index(f.calls, "using DataFrames, CSV, IJulia")
	if v, _ := envValue(f.envs[jv], "JULIA_LOAD_PATH"); v != "@"+string(os.PathListSeparator)+"@stdlib" {
		t.Fatalf("julia verify JULIA_LOAD_PATH %q", v)
	}
	mm := index(f.calls, "micromamba ")
	if v, _ := envValue(f.envs[mm], "MAMBA_ROOT_PREFIX"); v != filepath.Join(scratch(filepath.Join(b.Dir, "python")), "mamba") {
		t.Fatalf("MAMBA_ROOT_PREFIX %q", v)
	}
	if v, _ := envValue(f.envs[mm], "HOME"); v != filepath.Join(scratch(filepath.Join(b.Dir, "python")), "home") {
		t.Fatalf("micromamba HOME %q", v)
	}
}

func TestTheRBaseRegistersItsKernelThenVerifies(t *testing.T) {
	b, f := fakeBases(t)
	// R needs the Python base, which installs first.
	if err := b.buildBaseForTest(context.Background(), R); err != nil {
		t.Fatal(err)
	}
	if py, r := index(f.calls, "-p "+filepath.Join(b.Dir, "python")), index(f.calls, "-p "+filepath.Join(b.Dir, "r")); py < 0 || r < py {
		t.Fatalf("Python %d before R %d: %q", py, r, f.calls)
	}
	f.calls = f.calls[index(f.calls, "-p "+filepath.Join(b.Dir, "r")):]
	f.envs = f.envs[len(f.envs)-len(f.calls):]
	create, export, spec, verify := index(f.calls, " install "), index(f.calls, "env export"), index(f.calls, "IRkernel::installspec"), index(f.calls, "library(IRkernel)")
	if create < 0 || !(create < export && export < spec && spec < verify) {
		t.Fatalf("order %d %d %d %d: %q", create, export, spec, verify, f.calls)
	}
	for _, pin := range rPins {
		if !strings.Contains(f.calls[create]+" ", " "+pin+" ") {
			t.Fatalf("create lacks %s", pin)
		}
	}
	if !strings.Contains(f.calls[spec], fmt.Sprintf("prefix = %q", filepath.Join(b.Dir, "r"))) {
		t.Fatalf("installspec: %s", f.calls[spec])
	}
	if path, _ := envValue(f.envs[spec], "PATH"); !strings.HasPrefix(path, filepath.Join(b.Dir, "python", "bin")+string(os.PathListSeparator)) {
		t.Fatalf("installspec PATH %q", path)
	}
	if !b.Ready(R) {
		t.Fatal("R not ready")
	}
}

func TestAFailedJuliaBuildLeavesNothing(t *testing.T) {
	b, f := fakeBases(t)
	if err := b.buildBaseForTest(context.Background(), Python); err != nil {
		t.Fatal(err)
	}
	f.failJulia = true
	if err := b.buildBaseForTest(context.Background(), Julia); err == nil {
		t.Fatal("want error")
	}
	emptyOrAbsent(t, filepath.Join(b.Dir, "julia"))
	if b.Ready(Julia) || !b.Ready(Python) {
		t.Fatal("readiness wrong after a failed Julia build")
	}
}

func TestAStudyEnvWithEveryBaseReady(t *testing.T) {
	b, _ := fakeBases(t)
	if errs := b.buildAllForTest(context.Background()); errs[Python] != nil || errs[R] != nil || errs[Julia] != nil {
		t.Fatal(errs)
	}
	lock, err := os.ReadFile(filepath.Join(b.Dir, "julia", "base", "lock.txt"))
	if err != nil || !strings.Contains(string(lock), "julia_version") {
		t.Fatalf("julia lock %q %v", lock, err)
	}
	s := newStudy(b, t.TempDir())
	env, sep := s.Env(), string(os.PathListSeparator)
	for k, want := range map[string]string{
		"JULIA_PROJECT":    filepath.Join(s.Dir, "julia"),
		"JULIA_DEPOT_PATH": filepath.Join(s.Dir, "julia", "depot") + sep + filepath.Join(b.Dir, "julia", "depot") + sep + b.juliaBundled(),
		"JULIA_LOAD_PATH":  "@" + sep + filepath.Join(b.Dir, "julia", "base") + sep + "@stdlib",
		"JUPYTER_PATH":     strings.Join([]string{filepath.Join(b.Dir, "python", "share", "jupyter"), filepath.Join(b.Dir, "r", "share", "jupyter"), filepath.Join(b.Dir, "julia", "jupyter")}, sep),
	} {
		if v, _ := envValue(env, k); v != want {
			t.Fatalf("%s = %q, want %q", k, v, want)
		}
	}
	if fi, err := os.Stat(filepath.Join(s.Dir, "julia", "Project.toml")); err != nil || fi.Size() != 0 {
		t.Fatalf("Project.toml %v", err)
	}
}

func TestConcurrentEnsureBuildsOnce(t *testing.T) {
	b, _ := fakeBases(t)
	var wg sync.WaitGroup
	for range 4 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if err := b.buildBaseForTest(context.Background(), Python); err != nil {
				t.Error(err)
			}
		}()
	}
	wg.Wait()
	if creates := b.vol.(*fakeVolume).creates; creates != 1 {
		t.Fatalf("%d builds", creates)
	}
}

func TestAFailedVenvDoesNotExportAnEnvironment(t *testing.T) {
	b, f := fakeBases(t)
	if err := b.buildBaseForTest(context.Background(), Python); err != nil {
		t.Fatal(err)
	}
	f.failVenv = true
	s := newStudy(b, t.TempDir())
	env, err := s.Environment(context.Background())
	if err == nil || !strings.Contains(err.Error(), "ensurepip failed") || len(env) != 0 {
		t.Fatalf("failed setup exported environment: %v %v", env, err)
	}
	if p := s.PathPrefix(); p != strings.Join([]string{filepath.Join(s.Dir, "bin"), filepath.Join(s.Dir, "conda", "bin"), filepath.Join(b.Dir, "python", "bin")}, string(os.PathListSeparator)) {
		t.Fatalf("path %q", p)
	}
}

func TestBasesBuildForReal(t *testing.T) {
	if os.Getenv("LYK_TEST_REAL_ENVS") == "" {
		t.Skip("set LYK_TEST_REAL_ENVS=1 to download and install every base")
	}
	dir := os.Getenv("LYK_TEST_ENVS_DIR")
	if dir == "" {
		dir = t.TempDir()
	}
	b := NewBoundedBases(dir)
	ctx := context.Background()
	for _, lang := range []string{Python, R, Julia} {
		start := time.Now()
		if err := b.buildBaseForTest(ctx, lang); err != nil {
			t.Fatalf("%s: %v", lang, err)
		}
		t.Logf("%s base ready in %s", lang, time.Since(start).Round(time.Second))
	}
	s := newStudy(b, t.TempDir())
	env := append(os.Environ(), s.Env()...)
	env = append(env, "PATH="+s.PathPrefix()+string(os.PathListSeparator)+os.Getenv("PATH"))
	t.Logf("study env: %q", env)
	for lang, code := range map[string][]string{
		Python: {"-c", "import numpy, pandas, scipy, matplotlib, statsmodels, ipykernel, jupyter_client; print(numpy.__version__)"},
		R:      {"-e", "library(tidyverse); library(jsonlite); library(IRkernel); cat(.libPaths(), sep='\\n')"},
		Julia:  {"-e", "using DataFrames, CSV, IJulia; println(Base.load_path())"},
	} {
		name, args, err := s.Interpreter(lang)
		if err != nil {
			t.Fatal(err)
		}
		out, err := b.Run(ctx, env, s.Dir, name, append(args, code...)...)
		if err != nil {
			t.Fatalf("%s: %v %s", lang, err, out)
		}
		t.Logf("%s: %s", lang, out)
		pkgs, err := s.Packages(ctx, lang)
		if err != nil {
			t.Fatalf("%s packages: %v", lang, err)
		}
		t.Logf("%s packages: %d bytes, study part %q", lang, len(pkgs), pkgs[strings.Index(pkgs, "# "+lang+" study\n"):])
	}
	out, err := b.Run(ctx, env, s.Dir, filepath.Join(dir, "python", "bin", "jupyter"), "kernelspec", "list")
	if err != nil {
		t.Fatalf("kernelspec list: %v", err)
	}
	t.Logf("kernelspecs:\n%s", out)
}

func TestAStudyEnvStopsWithItsContext(t *testing.T) {
	b, _ := fakeBases(t)
	if err := b.buildBaseForTest(context.Background(), Python); err != nil {
		t.Fatal(err)
	}
	b.Run = func(ctx context.Context, _ []string, _, _ string, _ ...string) ([]byte, error) {
		<-ctx.Done()
		return nil, ctx.Err()
	}
	ctx, cancel := context.WithCancel(context.Background())
	time.AfterFunc(50*time.Millisecond, cancel)
	start := time.Now()
	env := newStudy(b, t.TempDir()).EnvContext(ctx)
	if time.Since(start) > 10*time.Second {
		t.Fatalf("EnvContext outlived its context by %v", time.Since(start))
	}
	if _, ok := envValue(env, "VIRTUAL_ENV"); ok {
		t.Fatalf("VIRTUAL_ENV set without a venv: %q", env)
	}
}

func TestStatesReportBuildingAndFailure(t *testing.T) {
	b, f := fakeBases(t)
	if s := b.States(); len(s) != 0 {
		t.Fatalf("states before any build: %v", s)
	}
	started, release := make(chan struct{}), make(chan struct{})
	reason := strings.Repeat("x", 173) + "é" + strings.Repeat("y", 300)
	b.Run = func(ctx context.Context, env []string, dir, name string, args ...string) ([]byte, error) {
		if slicesIndex(args, "install") >= 0 {
			close(started)
			<-release
			return nil, errors.New(reason)
		}
		return f.run(ctx, env, dir, name, args...)
	}
	done := make(chan error, 1)
	go func() { done <- b.buildBaseForTest(context.Background(), Python) }()
	<-started
	if s := b.States(); s[Python] != "building" || len(s) != 1 {
		t.Fatalf("states while building: %v", s)
	}
	close(release)
	err := <-done
	if err == nil {
		t.Fatal("want error")
	}
	got := b.States()[Python]
	want := Failed(err)
	if !strings.HasPrefix(want, "failed: installing the Python environment: ") || len(want) > len("failed: ")+200 {
		t.Fatalf("failed state %q", want)
	}
	if got != want {
		t.Fatalf("failed state:\n got %q\nwant %q", got, want)
	}
	b.Run = f.run
	if err := b.buildBaseForTest(context.Background(), Python); err != nil {
		t.Fatal(err)
	}
	if s := b.States(); s[Python] != "ready" || len(s) != 1 {
		t.Fatalf("states after a rebuild: %v", s)
	}
}

func TestEnsureCanceledWaiterDoesNotInterruptOwner(t *testing.T) {
	b, f := fakeBases(t)
	started, release := make(chan struct{}), make(chan struct{})
	b.Run = func(ctx context.Context, env []string, dir, name string, args ...string) ([]byte, error) {
		if slicesIndex(args, "install") >= 0 {
			close(started)
			<-release
		}
		return f.run(ctx, env, dir, name, args...)
	}
	ownerDone := make(chan error, 1)
	go func() { ownerDone <- b.buildBaseForTest(context.Background(), Python) }()
	var ownerOnce sync.Once
	var ownerErr error
	finishOwner := func() error {
		ownerOnce.Do(func() {
			close(release)
			select {
			case ownerErr = <-ownerDone:
			case <-time.After(5 * time.Second):
				ownerErr = errors.New("owner did not finish after release")
			}
		})
		return ownerErr
	}
	defer finishOwner()
	select {
	case <-started:
	case <-time.After(5 * time.Second):
		t.Fatal("owner did not start the installation")
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	waiting := &waitingContext{Context: ctx, waiting: make(chan struct{})}
	waiterDone := make(chan error, 1)
	go func() { waiterDone <- b.buildBaseForTest(waiting, Python) }()
	select {
	case <-waiting.waiting:
	case <-time.After(5 * time.Second):
		t.Fatal("waiter did not reach the contended lock")
	}
	cancel()
	waiterReturned := false
	select {
	case err := <-waiterDone:
		waiterReturned = true
		if !errors.Is(err, context.Canceled) {
			t.Errorf("waiter error = %v, want context canceled", err)
		}
	case <-time.After(5 * time.Second):
		t.Error("canceled waiter remained blocked behind the active installation")
	}
	if state := b.States()[Python]; state != "building" {
		t.Errorf("owner state changed while waiter canceled: %q", state)
	}
	if err := finishOwner(); err != nil {
		t.Fatalf("owner failed: %v", err)
	}
	if !waiterReturned {
		select {
		case <-waiterDone:
		case <-time.After(5 * time.Second):
			t.Error("waiter did not finish after owner release")
		}
	}
	if !b.Ready(Python) {
		t.Fatal("owner did not publish ready base")
	}
	if err := b.buildBaseForTest(context.Background(), Python); err != nil {
		t.Fatalf("ready base retry failed: %v", err)
	}
}

func TestEnsureOtherLanguageDoesNotWaitForBusyBase(t *testing.T) {
	b, f := fakeBases(t)
	if err := b.buildBaseForTest(context.Background(), Python); err != nil {
		t.Fatal(err)
	}
	started, release := make(chan struct{}), make(chan struct{})
	b.Run = func(ctx context.Context, env []string, dir, name string, args ...string) ([]byte, error) {
		if i := slicesIndex(args, "-p"); i >= 0 && i+1 < len(args) && args[i+1] == filepath.Join(b.Dir, R) && slicesIndex(args, "install") >= 0 {
			close(started)
			<-release
		}
		return f.run(ctx, env, dir, name, args...)
	}
	rDone := make(chan error, 1)
	go func() { rDone <- b.buildBaseForTest(context.Background(), R) }()
	var rOnce sync.Once
	var rErr error
	finishR := func() error {
		rOnce.Do(func() {
			close(release)
			select {
			case rErr = <-rDone:
			case <-time.After(5 * time.Second):
				rErr = errors.New("R installation did not finish after release")
			}
		})
		return rErr
	}
	defer finishR()
	select {
	case <-started:
	case <-time.After(5 * time.Second):
		t.Fatal("R installation did not start")
	}
	juliaDone := make(chan error, 1)
	go func() { juliaDone <- b.buildBaseForTest(context.Background(), Julia) }()
	juliaReturned := false
	select {
	case err := <-juliaDone:
		juliaReturned = true
		if err != nil {
			t.Errorf("Julia build failed: %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Error("Julia waited for unrelated R installation")
	}
	rErr = finishR()
	if !juliaReturned {
		select {
		case <-juliaDone:
		case <-time.After(5 * time.Second):
			t.Error("Julia did not finish after R release")
		}
	}
	if rErr != nil {
		t.Fatalf("R build failed: %v", rErr)
	}
}

func TestEnsureCanceledContextDoesNotReturnReadyBase(t *testing.T) {
	b, _ := fakeBases(t)
	if err := b.buildBaseForTest(context.Background(), Python); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := b.buildBaseForTest(ctx, Python); !errors.Is(err, context.Canceled) {
		t.Fatalf("canceled Ensure = %v, want context canceled", err)
	}
	if !b.Ready(Python) {
		t.Fatal("canceled read damaged a ready base")
	}
}

func TestAFolderWithASpaceIsRefusedBeforeAnyBuild(t *testing.T) {
	b, f := fakeBases(t)
	b.Dir = filepath.Join(b.Dir, "Application Support", "envs")
	fetched := false
	b.Fetch = func(context.Context, string, string, string) error { fetched = true; return nil }
	err := b.buildBaseForTest(context.Background(), Python)
	if err == nil || !strings.Contains(err.Error(), "must not contain spaces") || !strings.Contains(err.Error(), b.Dir) {
		t.Fatalf("err = %v", err)
	}
	if len(f.calls) != 0 || fetched {
		t.Fatalf("ran %v, fetched %v", f.calls, fetched)
	}
	if _, err := os.Stat(filepath.Dir(b.Dir)); !os.IsNotExist(err) {
		t.Fatalf("created %s: %v", filepath.Dir(b.Dir), err)
	}
}

// With no runner, the study's folders are left alone: code the study's
// earlier runs left there must only ever run in a sandbox.
func TestAStudyWithoutARunnerPreparesNothing(t *testing.T) {
	b, f := fakeBases(t)
	for _, lang := range []string{Python, R, Julia} {
		if err := b.buildBaseForTest(context.Background(), lang); err != nil {
			t.Fatal(err)
		}
	}
	before := len(f.calls)
	s := ForStudy(b, t.TempDir())
	s.Env()
	if _, _, err := s.Interpreter(Python); err == nil {
		t.Fatal("an interpreter without a runner")
	}
	if _, err := s.Packages(context.Background(), Julia); err == nil {
		t.Fatal("packages without a runner")
	}
	if es, _ := os.ReadDir(s.Dir); len(es) != 0 || len(f.calls) != before {
		t.Fatalf("prepared without a runner: %v %q", es, f.calls[before:])
	}
}

// A venv that exists but lacks its interpreter is made again with --clear
// through the runner; if that fails, the answer says it couldn't be.
func TestADamagedVenvIsRebuiltByTheRunner(t *testing.T) {
	b, f := fakeBases(t)
	if err := b.buildBaseForTest(context.Background(), Python); err != nil {
		t.Fatal(err)
	}
	s := newStudy(b, t.TempDir())
	if err := os.MkdirAll(filepath.Join(s.Dir, "python", "lib"), 0o755); err != nil {
		t.Fatal(err)
	}
	before := len(f.calls)
	if name, _, err := s.Interpreter(Python); err != nil || name != filepath.Join(s.Dir, "python", "bin", "python") {
		t.Fatalf("%q %v", name, err)
	}
	if i := index(f.calls[before:], "venv --clear --system-site-packages"); i < 0 {
		t.Fatalf("not rebuilt with --clear: %q", f.calls[before:])
	}
	broken := newStudy(b, t.TempDir())
	if err := os.MkdirAll(filepath.Join(broken.Dir, "python"), 0o755); err != nil {
		t.Fatal(err)
	}
	broken.Run = func(context.Context, []string, string, string, ...string) ([]byte, error) {
		return nil, errors.New("no space left")
	}
	_, _, err := broken.Interpreter(Python)
	if !errors.Is(err, ErrDamaged) || !strings.Contains(err.Error(), "the study's Python environment is damaged and could not be rebuilt: ") || !strings.Contains(err.Error(), "no space left") {
		t.Fatalf("%v", err)
	}
}

// R's and Julia's add-on files are made by the runner, so a link the code
// planted is followed only inside the sandbox.
func TestStudyFilesAreMadeByTheRunner(t *testing.T) {
	b, f := fakeBases(t)
	for _, lang := range []string{Python, R, Julia} {
		if err := b.buildBaseForTest(context.Background(), lang); err != nil {
			t.Fatal(err)
		}
	}
	s := newStudy(b, t.TempDir())
	var shells []string
	s.Run = func(ctx context.Context, env []string, dir, name string, args ...string) ([]byte, error) {
		if name == "/bin/sh" {
			shells = append(shells, strings.Join(args, " "))
		}
		return f.run(ctx, env, dir, name, args...)
	}
	s.Env()
	if len(shells) != 2 || !strings.Contains(shells[0], "r/library") || !strings.Contains(shells[0], "julia/Project.toml") || !strings.Contains(shells[1], "lean/.lyk-pair") {
		t.Fatalf("shell steps %q", shells)
	}
}

// Julia's manifest is read by the runner too: the daemon itself never opens
// a file the study's code could have swapped for a link.
func TestTheJuliaManifestIsReadByTheRunner(t *testing.T) {
	b, _ := fakeBases(t)
	for _, lang := range []string{Python, Julia} {
		if err := b.buildBaseForTest(context.Background(), lang); err != nil {
			t.Fatal(err)
		}
	}
	s := newStudy(b, t.TempDir())
	secret := filepath.Join(t.TempDir(), "secret")
	if err := os.WriteFile(secret, []byte("PRIVATE KEY"), 0o600); err != nil {
		t.Fatal(err)
	}
	s.Env()
	if err := os.Symlink(secret, filepath.Join(s.Dir, "julia", "Manifest.toml")); err != nil {
		t.Fatal(err)
	}
	s.Run = func(_ context.Context, _ []string, _, name string, args ...string) ([]byte, error) {
		if name == "/bin/sh" && strings.Contains(strings.Join(args, " "), "Manifest.toml") {
			return []byte("read by the runner\n"), nil
		}
		return nil, nil
	}
	got, err := s.Packages(context.Background(), Julia)
	if err != nil || strings.Contains(got, "PRIVATE KEY") || !strings.Contains(got, "read by the runner") {
		t.Fatalf("%q %v", got, err)
	}
}

// The study passes its runner only what the study adds; the runner, which
// knows the run, supplies the rest. Nothing of the daemon's own environment
// is handed on.
func TestTheRunnerGetsOnlyTheStudysEnvironment(t *testing.T) {
	t.Setenv("LYK_DAEMON_ONLY", "daemon-secret")
	b, f := fakeBases(t)
	for _, lang := range []string{Python, R} {
		if err := b.buildBaseForTest(context.Background(), lang); err != nil {
			t.Fatal(err)
		}
	}
	s := newStudy(b, t.TempDir())
	var envs [][]string
	s.Run = func(ctx context.Context, env []string, dir, name string, args ...string) ([]byte, error) {
		envs = append(envs, env)
		return f.run(ctx, append(os.Environ(), env...), dir, name, args...)
	}
	if _, err := s.Packages(context.Background(), Python); err != nil {
		t.Fatal(err)
	}
	if len(envs) == 0 {
		t.Fatal("the runner never ran")
	}
	for _, env := range envs {
		if strings.Contains(strings.Join(env, "\n"), "daemon-secret") {
			t.Fatalf("the daemon's environment reached the runner: %q", env)
		}
	}
}

func TestStudyRunViewPreservesStateAndOwnsCallbacks(t *testing.T) {
	original := ForStudy(&Bases{}, t.TempDir())
	original.PythonVersion = "3.11"
	original.rememberLean("trusted-pair")
	original.Run = func(context.Context, []string, string, string, ...string) ([]byte, error) {
		return []byte("original"), nil
	}
	persisted := ""
	original.PersistPython = func(_ context.Context, version string) error { persisted = version; return nil }
	view := original.ForRun()
	if view == original || view.Bases != original.Bases || view.Dir != original.Dir || view.PythonVersion != original.PythonVersion || view.selectedLean() != "trusted-pair" {
		t.Fatal("run view lost study identity or selected toolchain")
	}
	out, err := view.Run(t.Context(), nil, "", "")
	if err != nil || string(out) != "original" {
		t.Fatalf("runner not retained: %s %v", out, err)
	}
	if err := view.PersistPython(t.Context(), "3.11"); err != nil || persisted != "3.11" {
		t.Fatalf("persistence callback not retained: %s %v", persisted, err)
	}
	view.Run = func(context.Context, []string, string, string, ...string) ([]byte, error) { return []byte("run"), nil }
	view.PersistPython = func(context.Context, string) error { return errors.New("run callback") }
	view.rememberLean("run-selection")
	out, err = original.Run(t.Context(), nil, "", "")
	if err != nil || string(out) != "original" || original.selectedLean() != "trusted-pair" {
		t.Fatal("run-specific changes mutated original study")
	}
	if err := original.PersistPython(t.Context(), "unchanged"); err != nil || persisted != "unchanged" {
		t.Fatal("run replaced original persistence callback")
	}
	if err := view.RememberPython("3.11"); err != nil {
		t.Fatal(err)
	}
	if original.RecordedPython() != "3.11" {
		t.Fatal("run lost shared study version state")
	}
}

func TestStudyRunViewSnapshotsConcurrentLeanSelection(t *testing.T) {
	original := ForStudy(&Bases{}, t.TempDir())
	original.rememberLean("first")
	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		for range 1000 {
			original.rememberLean("second")
			original.rememberLean("first")
		}
	}()
	for range 1000 {
		view := original.ForRun()
		if selected := view.selectedLean(); selected != "first" && selected != "second" {
			t.Errorf("invalid snapshot %q", selected)
		}
		view.rememberLean("independent")
	}
	wg.Wait()
	if original.selectedLean() != "first" {
		t.Fatal("view changed the original selection")
	}
}
