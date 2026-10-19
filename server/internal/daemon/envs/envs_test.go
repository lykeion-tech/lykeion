package envs

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

type fakeRun struct {
	calls      []string
	failVerify bool
}

func (f *fakeRun) run(_ context.Context, _ []string, _, name string, args ...string) ([]byte, error) {
	line := strings.Join(append([]string{filepath.Base(name)}, args...), " ")
	f.calls = append(f.calls, line)
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
		if err := os.MkdirAll(filepath.Join(args[3], "bin"), 0o755); err != nil {
			return nil, err
		}
		return nil, os.WriteFile(filepath.Join(args[3], "bin", "python"), nil, 0o755)
	case strings.Contains(line, "freeze --local"):
		return []byte("requests==2.32.0\n"), nil
	case strings.Contains(line, "import numpy") && f.failVerify:
		return nil, errors.New("ModuleNotFoundError: No module named 'numpy'")
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
	for _, p := range []string{"python", "python.building"} {
		if _, err := os.Stat(filepath.Join(b.Dir, p)); !os.IsNotExist(err) {
			t.Fatalf("%s left behind: %v", p, err)
		}
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
	env := s.Env()
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
