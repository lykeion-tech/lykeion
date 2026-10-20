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

func (f *fakeRun) run(_ context.Context, env []string, _, name string, args ...string) ([]byte, error) {
	line := strings.Join(append([]string{filepath.Base(name)}, args...), " ")
	f.mu.Lock()
	f.calls = append(f.calls, line)
	f.envs = append(f.envs, env)
	f.mu.Unlock()
	switch {
	case filepath.Base(name) == "micromamba" && slicesIndex(args, "create") >= 0:
		prefix := args[slicesIndex(args, "-p")+1]
		if err := os.MkdirAll(filepath.Join(prefix, "bin"), 0o755); err != nil {
			return nil, err
		}
		return nil, os.WriteFile(filepath.Join(prefix, "bin", "python"), nil, 0o755)
	case filepath.Base(name) == "micromamba" && slicesIndex(args, "export") >= 0:
		return []byte("@EXPLICIT\nhttps://conda.anaconda.org/conda-forge/python-3.13.16.conda\n"), nil
	case len(args) == 4 && args[1] == "venv":
		if f.failVenv {
			return nil, errors.New("venv: ensurepip failed")
		}
		if err := os.MkdirAll(filepath.Join(args[3], "bin"), 0o755); err != nil {
			return nil, err
		}
		return nil, os.WriteFile(filepath.Join(args[3], "bin", "python"), nil, 0o755)
	case strings.Contains(line, "freeze --local"):
		return []byte("requests==2.32.0\n"), nil
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
	return b, f
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
	if err := b.Ensure(context.Background(), Python); err != nil {
		t.Fatal(err)
	}
	create, export, verify := index(f.calls, "micromamba "), index(f.calls, "env export"), index(f.calls, "import numpy, pandas, scipy, matplotlib, statsmodels, ipykernel, jupyter_client")
	if create < 0 || !(create < export && export < verify) {
		t.Fatalf("order create=%d export=%d verify=%d: %q", create, export, verify, f.calls)
	}
	if !strings.Contains(f.calls[create], " create ") || !strings.Contains(f.calls[create], "-c conda-forge --override-channels") {
		t.Fatalf("create: %s", f.calls[create])
	}
	for _, pin := range pythonPins {
		if !strings.Contains(f.calls[create]+" ", " "+pin+" ") {
			t.Fatalf("create lacks %s: %s", pin, f.calls[create])
		}
	}
	if clean := index(f.calls, "clean -a -y"); clean < verify {
		t.Fatalf("package cache not cleaned after verifying: %q", f.calls)
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
	if err := b.Ensure(context.Background(), Python); err == nil {
		t.Fatal("want error")
	}
	if b.Ready(Python) {
		t.Fatal("python ready after a failed build")
	}
	if _, err := os.Stat(filepath.Join(b.Dir, "python")); !os.IsNotExist(err) {
		t.Fatalf("python left behind: %v", err)
	}
	if !b.Ready(R) {
		t.Fatal("the ready R base was disturbed")
	}
}

func TestJuliaNeedsThePythonBase(t *testing.T) {
	b, f := fakeBases(t)
	err := b.Ensure(context.Background(), Julia)
	if err == nil || !strings.Contains(err.Error(), "Python") {
		t.Fatalf("err %v", err)
	}
	if len(f.calls) != 0 || b.Ready(Julia) {
		t.Fatalf("julia built anyway: %q", f.calls)
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
	if err := b.Ensure(context.Background(), Python); err != nil {
		t.Fatal(err)
	}
	s := ForStudy(b, t.TempDir())
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
	if p := s.PathPrefix(); p != filepath.Join(s.Dir, "python", "bin")+string(os.PathListSeparator)+filepath.Join(b.Dir, "python", "bin") {
		t.Fatalf("path %q", p)
	}
	_, _, err := s.Interpreter(R)
	if !errors.Is(err, ErrNotReady) || err.Error() != "the R environment isn't ready on this machine" {
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
		if err := b.Ensure(context.Background(), lang); err != nil {
			t.Fatal(err)
		}
	}
	one, two := ForStudy(b, t.TempDir()).Env(), ForStudy(b, t.TempDir()).Env()
	for _, k := range []string{"VIRTUAL_ENV", "R_LIBS_USER"} {
		a, okA := envValue(one, k)
		c, okC := envValue(two, k)
		if !okA || !okC || a == c {
			t.Fatalf("%s: %q %q", k, a, c)
		}
	}
	s := ForStudy(b, t.TempDir())
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
	if err := b.Ensure(context.Background(), Python); err != nil {
		t.Fatal(err)
	}
	lock, err := os.ReadFile(filepath.Join(b.Dir, "python", "lock.txt"))
	if err != nil {
		t.Fatal(err)
	}
	got, err := ForStudy(b, t.TempDir()).Packages(context.Background(), Python)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(got, "# python base\n"+string(lock)) || !strings.Contains(got, "# python study\nrequests==2.32.0\n") {
		t.Fatalf("packages %q", got)
	}
}

func TestAStaleBuildIsReplaced(t *testing.T) {
	b, _ := fakeBases(t)
	junk := filepath.Join(b.Dir, "python", "junk")
	if err := os.MkdirAll(junk, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := b.Ensure(context.Background(), Python); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(junk); !os.IsNotExist(err) || !b.Ready(Python) {
		t.Fatalf("junk %v ready %v", err, b.Ready(Python))
	}
}

func TestVerificationIgnoresThePersonsEnvironment(t *testing.T) {
	t.Setenv("PYTHONPATH", "/person/site")
	t.Setenv("R_LIBS_USER", "/person/rlib")
	t.Setenv("CONDA_PREFIX", "/person/conda")
	t.Setenv("JULIA_LOAD_PATH", "/person/julia")
	b, f := fakeBases(t)
	for _, lang := range []string{Python, R, Julia} {
		if err := b.Ensure(context.Background(), lang); err != nil {
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
	if v, _ := envValue(f.envs[mm], "MAMBA_ROOT_PREFIX"); v != filepath.Join(b.Dir, "micromamba", "root") {
		t.Fatalf("MAMBA_ROOT_PREFIX %q", v)
	}
	if v, _ := envValue(f.envs[mm], "HOME"); v != filepath.Join(b.Dir, "micromamba", "home") {
		t.Fatalf("micromamba HOME %q", v)
	}
}

func TestTheRBaseRegistersItsKernelThenVerifies(t *testing.T) {
	b, f := fakeBases(t)
	if err := b.Ensure(context.Background(), R); err == nil || !strings.Contains(err.Error(), "Python") {
		t.Fatalf("R without Python: %v", err)
	}
	if err := b.Ensure(context.Background(), Python); err != nil {
		t.Fatal(err)
	}
	f.calls, f.envs = nil, nil
	if err := b.Ensure(context.Background(), R); err != nil {
		t.Fatal(err)
	}
	create, export, spec, verify := index(f.calls, " create "), index(f.calls, "env export"), index(f.calls, "IRkernel::installspec"), index(f.calls, "library(IRkernel)")
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
	if err := b.Ensure(context.Background(), Python); err != nil {
		t.Fatal(err)
	}
	f.failJulia = true
	if err := b.Ensure(context.Background(), Julia); err == nil {
		t.Fatal("want error")
	}
	if _, err := os.Stat(filepath.Join(b.Dir, "julia")); !os.IsNotExist(err) {
		t.Fatalf("julia left behind: %v", err)
	}
	if b.Ready(Julia) || !b.Ready(Python) {
		t.Fatal("readiness wrong after a failed Julia build")
	}
}

func TestAStudyEnvWithEveryBaseReady(t *testing.T) {
	b, _ := fakeBases(t)
	if errs := b.EnsureAll(context.Background()); errs[Python] != nil || errs[R] != nil || errs[Julia] != nil {
		t.Fatal(errs)
	}
	lock, err := os.ReadFile(filepath.Join(b.Dir, "julia", "base", "lock.txt"))
	if err != nil || !strings.Contains(string(lock), "julia_version") {
		t.Fatalf("julia lock %q %v", lock, err)
	}
	s := ForStudy(b, t.TempDir())
	env, sep := s.Env(), string(os.PathListSeparator)
	for k, want := range map[string]string{
		"JULIA_PROJECT":    filepath.Join(s.Dir, "julia"),
		"JULIA_DEPOT_PATH": filepath.Join(s.Dir, "julia", "depot") + sep + filepath.Join(b.Dir, "julia", "depot"),
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
	b, f := fakeBases(t)
	var wg sync.WaitGroup
	for range 4 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if err := b.Ensure(context.Background(), Python); err != nil {
				t.Error(err)
			}
		}()
	}
	wg.Wait()
	creates := 0
	for _, c := range f.calls {
		if strings.Contains(c, " create ") {
			creates++
		}
	}
	if creates != 1 {
		t.Fatalf("%d builds", creates)
	}
}

func TestAFailedVenvLeavesOnlyBaseEntries(t *testing.T) {
	b, f := fakeBases(t)
	if err := b.Ensure(context.Background(), Python); err != nil {
		t.Fatal(err)
	}
	f.failVenv = true
	s := ForStudy(b, t.TempDir())
	env := s.Env()
	if v, ok := envValue(env, "VIRTUAL_ENV"); ok {
		t.Fatalf("VIRTUAL_ENV %q without a venv", v)
	}
	if _, ok := envValue(env, "JUPYTER_PATH"); !ok {
		t.Fatal("base entries dropped")
	}
	if p := s.PathPrefix(); p != filepath.Join(b.Dir, "python", "bin") {
		t.Fatalf("path %q", p)
	}
}

func TestBasesBuildForReal(t *testing.T) {
	if os.Getenv("LYK_TEST_ENVS") != "1" {
		t.Skip("set LYK_TEST_ENVS=1 to build the real bases")
	}
	dir := os.Getenv("LYK_TEST_ENVS_DIR")
	if dir == "" {
		dir = t.TempDir()
	}
	b := NewBases(dir)
	ctx := context.Background()
	for _, lang := range []string{Python, R, Julia} {
		start := time.Now()
		if err := b.Ensure(ctx, lang); err != nil {
			t.Fatalf("%s: %v", lang, err)
		}
		t.Logf("%s base ready in %s", lang, time.Since(start).Round(time.Second))
	}
	s := ForStudy(b, t.TempDir())
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
	if err := b.Ensure(context.Background(), Python); err != nil {
		t.Fatal(err)
	}
	b.Run = func(ctx context.Context, _ []string, _, _ string, _ ...string) ([]byte, error) {
		<-ctx.Done()
		return nil, ctx.Err()
	}
	ctx, cancel := context.WithCancel(context.Background())
	time.AfterFunc(50*time.Millisecond, cancel)
	start := time.Now()
	env := ForStudy(b, t.TempDir()).EnvContext(ctx)
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
		if slicesIndex(args, "create") >= 0 {
			close(started)
			<-release
			return nil, errors.New(reason)
		}
		return f.run(ctx, env, dir, name, args...)
	}
	done := make(chan error, 1)
	go func() { done <- b.Ensure(context.Background(), Python) }()
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
	want := "failed: " + err.Error()[:199]
	if got != want {
		t.Fatalf("failed state:\n got %q\nwant %q", got, want)
	}
	b.Run = f.run
	if err := b.Ensure(context.Background(), Python); err != nil {
		t.Fatal(err)
	}
	if s := b.States(); s[Python] != "ready" || len(s) != 1 {
		t.Fatalf("states after a rebuild: %v", s)
	}
}

func TestAFolderWithASpaceIsRefusedBeforeAnyBuild(t *testing.T) {
	b, f := fakeBases(t)
	b.Dir = filepath.Join(b.Dir, "Application Support", "envs")
	fetched := false
	b.Fetch = func(context.Context, string, string, string) error { fetched = true; return nil }
	err := b.Ensure(context.Background(), Python)
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
