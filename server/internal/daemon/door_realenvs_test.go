package daemon

import (
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"lykeion/server/internal/daemon/envs"
)

// TestRealEnvsRunInTheSandbox runs the real bases' interpreters through the
// door, in the sandbox the door builds for them. The bases' parent folder
// stands in for the home folder they live in, as ~/.lyk/envs does, so
// everything else in it is hidden from the code.
func TestRealEnvsRunInTheSandbox(t *testing.T) {
	if os.Getenv("LYK_TEST_ENVS") != "1" {
		t.Skip("set LYK_TEST_ENVS=1 and LYK_TEST_ENVS_DIR to built bases to run real environments in the sandbox")
	}
	bases := envs.NewBases(os.Getenv("LYK_TEST_ENVS_DIR"))
	for _, lang := range []string{envs.Python, envs.R, envs.Julia} {
		if !bases.Ready(lang) {
			t.Fatalf("the %s base isn't ready in LYK_TEST_ENVS_DIR", lang)
		}
	}
	home := filepath.Dir(bases.Dir)
	secret := filepath.Join(tempIn(t, home, "secret-"), "id_rsa")
	must(t, os.WriteFile(secret, []byte("PRIVATE KEY"), 0o600))
	studies := filepath.Join(bases.Dir, "studies")
	t.Cleanup(func() { os.RemoveAll(studies) })
	study := filepath.Join(studies, "w", "s")
	must(t, os.MkdirAll(study, 0o755))
	other := filepath.Join(studies, "w", "other", "notes.txt")
	must(t, os.MkdirAll(filepath.Dir(other), 0o755))
	must(t, os.WriteFile(other, []byte("OTHER STUDY"), 0o600))
	run := tempIn(t, home, "run-")
	old := toolTimeout
	toolTimeout = 15 * time.Minute
	t.Cleanup(func() { toolTimeout = old })

	l := newLykeion(t)
	d := newRunDoor(t, l, run, func(d *Door) {
		d.home = home
		d.interp = nil
		d.Study = envs.ForStudy(bases, study)
	})
	tool := func(name string, args map[string]any) string {
		t.Helper()
		start := time.Now()
		got, err := useTool(t, d, name, args)
		if err != nil {
			t.Fatalf("%s %v: %v", name, args, err)
		}
		t.Logf("%s %v in %s:\n%s", name, args, time.Since(start).Round(time.Millisecond), got)
		return got
	}
	cell := func(lang, code string) string {
		t.Helper()
		return tool("run_code", map[string]any{"language": lang, "code": code})
	}
	script := func(name, code string) string {
		t.Helper()
		must(t, os.WriteFile(filepath.Join(run, name), []byte(code), 0o644))
		return tool("run_script", map[string]any{"path": name})
	}

	if got := cell("python", `import numpy, pandas, matplotlib
matplotlib.use("Agg")
import matplotlib.pyplot as plt
plt.plot([1, 2])
plt.savefig("plot.png")
print(int(numpy.arange(3).sum()))`); !strings.HasPrefix(got, "run:x1\n3\n") {
		t.Errorf("python cell: %q", got)
	}
	if _, err := os.Stat(filepath.Join(run, "plot.png")); err != nil {
		t.Errorf("the plot was not saved in the run folder: %v", err)
	}
	if got := cell("python", `import subprocess, sys
r = subprocess.run([sys.executable, "-m", "pip", "install", "--quiet", "tabulate"], capture_output=True, text=True)
print("pip exit", r.returncode, r.stderr[-2000:])`); !strings.Contains(got, "pip exit 0") {
		t.Errorf("pip install: %q", got)
	}
	if got := script("tab.py", "import tabulate\nprint(tabulate.__file__)\n"); !strings.Contains(got, filepath.Join(study, "python")) {
		t.Errorf("tabulate is not in the study folder: %q", got)
	}
	if runs, _ := l.recorded(); !strings.Contains(fmt.Sprint(runs[len(runs)-1]["packages"]), "tabulate==") {
		t.Errorf("the run's packages miss tabulate: %v", runs[len(runs)-1]["packages"])
	}
	if got := script("pool.py", `from multiprocessing import Pool
if __name__ == "__main__":
    with Pool(2) as p:
        print(sum(p.map(abs, [-1, -2])))
`); !strings.HasSuffix(got, "\n3\n") {
		t.Errorf("multiprocessing: %q", got)
	}

	probe := filepath.Join(home, "lyk-sandbox-probe")
	got := script("peek.py", fmt.Sprintf(`import os
for f in (%q, %q):
    try:
        print(open(f).read())
    except OSError as e:
        print("refused", e.errno)
try:
    os.listdir(%q)
    print("listed")
except OSError as e:
    print("refused", e.errno)
try:
    open(%q, "w").write("x")
    print("wrote")
except OSError as e:
    print("refused", e.errno)
`, secret, other, home, probe))
	// bwrap shows the code an empty home holding only what it binds in
	refusals := 4
	if runtime.GOOS == "linux" {
		refusals = 3
	}
	if strings.Contains(got, "PRIVATE KEY") || strings.Contains(got, "OTHER STUDY") || strings.Count(got, "refused") != refusals {
		t.Errorf("the script reached the home folder: %q", got)
	}
	if _, err := os.Stat(probe); err == nil {
		os.Remove(probe)
		t.Error("the script wrote the home folder")
	}
	if got := cell("python", fmt.Sprintf("print(open(%q).read())", secret)); strings.Contains(got, "PRIVATE KEY") || !strings.Contains(got, "Error") {
		t.Errorf("a cell read the key: %q", got)
	}

	if got := cell("r", `f <- tempfile(); writeLines("r ok", f); cat(readLines(f), sum(1:3))`); !strings.HasSuffix(got, "r ok 6") {
		t.Errorf("r cell: %q", got)
	}
	if got := cell("r", `install.packages("praise", quiet = TRUE); library(praise); cat(find.package("praise"))`); !strings.Contains(got, filepath.Join(study, "r", "library", "praise")) {
		t.Errorf("praise is not in the study's R library: %q", got)
	}
	if got := script("a.R", "cat(sum(1:4))\n"); !strings.HasSuffix(got, "\n10") {
		t.Errorf("r script: %q", got)
	}

	if got := cell("julia", "using DataFrames; println(nrow(DataFrame(a = [1, 2])))"); !strings.HasSuffix(got, "\n2\n") {
		t.Errorf("julia cell: %q", got)
	}
	// A package the study adds is precompiled into the study's own depot.
	if got := cell("julia", `using Pkg; Pkg.add("Example"); using Example; println(hello("depot"))`); !strings.Contains(got, "Hello, depot") {
		t.Errorf("julia add: %q", got)
	}
	if cached, _ := filepath.Glob(filepath.Join(study, "julia", "depot", "compiled", "*", "Example", "*.ji")); len(cached) == 0 {
		t.Error("Example was not precompiled into the study's depot")
	}
	if got := script("a.jl", "println(1 + 1)\n"); !strings.HasSuffix(got, "\n2\n") {
		t.Errorf("julia script: %q", got)
	}
}

// tempIn is a fresh folder in dir, removed when the test ends.
func tempIn(t *testing.T, dir, pattern string) string {
	t.Helper()
	p, err := os.MkdirTemp(dir, pattern)
	must(t, err)
	t.Cleanup(func() { os.RemoveAll(p) })
	return p
}
