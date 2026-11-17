package envs

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func condaStudy(t *testing.T) (*Study, *[]string) {
	t.Helper()
	b, f := fakeBases(t)
	if err := b.buildBaseForTest(context.Background(), Python); err != nil {
		t.Fatal(err)
	}
	calls := []string{}
	s := ForStudy(b, t.TempDir())
	venvVersion := "3.13"
	s.Run = func(ctx context.Context, env []string, dir, name string, args ...string) ([]byte, error) {
		line := name + " " + strings.Join(args, " ")
		calls = append(calls, line)
		if strings.Contains(line, "--dry-run") {
			v := "3.11.12"
			for _, a := range args {
				if strings.HasPrefix(a, "python=") {
					v = strings.TrimPrefix(a, "python=") + ".12"
				}
			}
			return json.Marshal(map[string]any{"success": true, "actions": map[string]any{"LINK": []map[string]string{{"name": "python", "version": v}}}})
		}
		if strings.Contains(line, "sys.version_info") {
			if name == filepath.Join(s.Dir, "python", "bin", "python") {
				return []byte(venvVersion + "\n"), nil
			}
			return []byte("3.11\n"), nil
		}
		if name == filepath.Join(s.Dir, "conda", "bin", "python") && strings.Contains(line, "-m venv") {
			venvVersion = "3.11"
		}
		if strings.Contains(line, "freeze --local") {
			return nil, nil
		}
		if filepath.Base(name) == "micromamba" {
			if got, _ := envValue(env, "MAMBA_ROOT_PREFIX"); got != filepath.Join(s.Dir, ".mamba") {
				t.Errorf("root %q", got)
			}
		}
		out, err := f.run(ctx, env, dir, name, args...)
		if err == nil && len(args) > 1 && args[0] == "-m" && args[1] == "venv" {
			err = os.WriteFile(filepath.Join(s.Dir, "python", "pyvenv.cfg"), []byte("version = "+venvVersion+".12\n"), 0600)
		}
		return out, err
	}
	return s, &calls
}

func TestSetUpCondaInstallsIntoTheStudy(t *testing.T) {
	s, calls := condaStudy(t)
	if _, err := s.SetUpConda(context.Background(), "3.11", []string{"ffmpeg"}, ""); err != nil {
		t.Fatal(err)
	}
	want := "create -y -p " + filepath.Join(s.Dir, "conda") + " -c conda-forge -c bioconda --override-channels python=3.11 ipykernel ffmpeg"
	if !strings.Contains(strings.Join(*calls, "\n"), want) {
		t.Fatalf("missing %s: %v", want, *calls)
	}
	if _, err := os.Stat(filepath.Join(s.Dir, "conda", ".lyk-ready")); err != nil {
		t.Fatal(err)
	}
	if index(*calls, "--clear --system-site-packages "+filepath.Join(s.Dir, "python")) < 0 {
		t.Fatalf("no conda venv: %v", *calls)
	}
}
func TestAStudyKeepsOnePythonVersion(t *testing.T) {
	s, calls := condaStudy(t)
	s.PythonVersion = "3.11"
	_, err := s.SetUpConda(context.Background(), "3.12", nil, "")
	if err == nil || err.Error() != "This study uses Python 3.11; a study keeps one Python version." {
		t.Fatalf("%v", err)
	}
	if len(*calls) != 0 {
		t.Fatalf("ran %v", *calls)
	}
}
func TestAnUnusedDefaultVenvIsReplaced(t *testing.T) {
	s, calls := condaStudy(t)
	if err := s.prepare(context.Background()); err != nil {
		t.Fatal(err)
	}
	if _, err := s.SetUpConda(context.Background(), "3.11", nil, ""); err != nil {
		t.Fatal(err)
	}
	if index(*calls, filepath.Join(s.Dir, "conda", "bin", "python")+" -m venv --clear --system-site-packages") < 0 {
		t.Fatalf("%v", *calls)
	}
}
func TestALockFileCreatesTheEnvironment(t *testing.T) {
	s, calls := condaStudy(t)
	lock := filepath.Join(t.TempDir(), "explicit.txt")
	os.WriteFile(lock, []byte("@EXPLICIT\n"), 0600)
	if _, err := s.SetUpConda(context.Background(), "", nil, lock); err != nil {
		t.Fatal(err)
	}
	if index(*calls, "--file "+lock) < 0 {
		t.Fatalf("%v", *calls)
	}
}
func TestAHalfMadeEnvironmentIsRebuilt(t *testing.T) {
	s, calls := condaStudy(t)
	os.MkdirAll(filepath.Join(s.Dir, "conda"), 0755)
	os.WriteFile(filepath.Join(s.Dir, "conda", "broken"), nil, 0600)
	if _, err := s.SetUpConda(context.Background(), "3.11", nil, ""); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(s.Dir, "conda", "broken")); !os.IsNotExist(err) {
		t.Fatalf("half-made files survived %v", err)
	}
	if index(*calls, "create -y") < 0 {
		t.Fatalf("%v", *calls)
	}
}
func TestARecordedVersionIsRestoredWhenTheFolderIsGone(t *testing.T) {
	s, calls := condaStudy(t)
	s.PythonVersion = "3.11"
	if err := s.prepare(context.Background()); err != nil {
		t.Fatal(err)
	}
	create, venv := index(*calls, "create -y"), index(*calls, "-m venv")
	if create < 0 || venv < create || strings.Contains((*calls)[venv], filepath.Join(s.Bases.Dir, "python")) {
		t.Fatalf("%v", *calls)
	}
}
func TestStudyBinComesFirstOnPath(t *testing.T) {
	s, _ := condaStudy(t)
	s.Env()
	want := filepath.Join(s.Dir, "bin") + string(os.PathListSeparator) + filepath.Join(s.Dir, "conda", "bin") + string(os.PathListSeparator)
	if !strings.HasPrefix(s.PathPrefix(), want) {
		t.Fatal(s.PathPrefix())
	}
	if fi, err := os.Stat(filepath.Join(s.Dir, "bin")); err != nil || !fi.IsDir() {
		t.Fatalf("bin: %v", err)
	}
}
func TestSetupIsSerialisedPerStudy(t *testing.T) {
	s, _ := condaStudy(t)
	original := s.Run
	var active, peak atomic.Int32
	s.Run = func(ctx context.Context, e []string, d, n string, a ...string) ([]byte, error) {
		v := active.Add(1)
		if v > peak.Load() {
			peak.Store(v)
		}
		defer active.Add(-1)
		time.Sleep(time.Millisecond)
		return original(ctx, e, d, n, a...)
	}
	other := ForStudy(s.Bases, s.Dir)
	other.Run = s.Run
	var wg sync.WaitGroup
	for _, study := range []*Study{s, other} {
		wg.Add(1)
		go func(s *Study) {
			defer wg.Done()
			if _, e := s.SetUpConda(context.Background(), "3.11", nil, ""); e != nil {
				t.Error(e)
			}
		}(study)
	}
	wg.Wait()
	if peak.Load() != 1 {
		t.Fatalf("overlapping Run calls: %d", peak.Load())
	}
}
func TestSetupStopsAtThirtyMinutes(t *testing.T) {
	s, _ := condaStudy(t)
	old := setupLimit
	setupLimit = 50 * time.Millisecond
	defer func() { setupLimit = old }()
	s.Run = func(ctx context.Context, _ []string, _, _ string, _ ...string) ([]byte, error) {
		<-ctx.Done()
		return nil, ctx.Err()
	}
	_, err := s.SetUpConda(context.Background(), "3.11", nil, "")
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatal(err)
	}
	if _, e := os.Stat(filepath.Join(s.Dir, "conda", ".lyk-ready")); !os.IsNotExist(e) {
		t.Fatalf("ready after timeout: %v", e)
	}
}
func TestLockResolvedPythonCannotChangeTheVersion(t *testing.T) {
	s, calls := condaStudy(t)
	s.PythonVersion = "3.12"
	// The explicit lock can ignore solver pins, so its resolved version must be checked.
	original := s.Run
	s.Run = func(c context.Context, e []string, d, n string, a ...string) ([]byte, error) {
		if strings.Contains(strings.Join(a, " "), "--dry-run") {
			return []byte(`{"success":true,"actions":{"LINK":[{"name":"python","version":"3.11.12"}]}}`), nil
		}
		return original(c, e, d, n, a...)
	}
	_, err := s.SetUpConda(context.Background(), "", nil, "/lock.txt")
	if err == nil || err.Error() != "This study uses Python 3.12; a study keeps one Python version." {
		t.Fatal(err)
	}
	for _, call := range *calls {
		if strings.Contains(call, "micromamba") && !strings.Contains(call, "--dry-run") {
			t.Fatalf("applied incompatible lock: %s", call)
		}
	}
}

func TestCondaAddingPythonReplacesTheDefaultVenv(t *testing.T) {
	s, calls := condaStudy(t)
	if err := s.prepare(context.Background()); err != nil {
		t.Fatal(err)
	}
	os.MkdirAll(filepath.Join(s.Dir, "conda"), 0755)
	os.WriteFile(filepath.Join(s.Dir, "conda", ".lyk-ready"), nil, 0600)
	original := s.Run
	s.Run = func(c context.Context, e []string, d, n string, a ...string) ([]byte, error) {
		if filepath.Base(n) == "micromamba" && len(a) > 1 && a[1] == "install" && !strings.Contains(strings.Join(a, " "), "--dry-run") {
			os.MkdirAll(filepath.Join(s.Dir, "conda", "bin"), 0755)
			os.WriteFile(filepath.Join(s.Dir, "conda", "bin", "python"), nil, 0755)
		}
		return original(c, e, d, n, a...)
	}
	if _, err := s.SetUpConda(context.Background(), "3.11", nil, ""); err != nil {
		t.Fatal(err)
	}
	if index(*calls, filepath.Join(s.Dir, "conda", "bin", "python")+" -m venv --clear") < 0 {
		t.Fatalf("kept default venv: %v", *calls)
	}
}
func TestFailedCondaInstallRemovesReadyMarker(t *testing.T) {
	s, _ := condaStudy(t)
	if _, err := s.SetUpConda(context.Background(), "3.11", nil, ""); err != nil {
		t.Fatal(err)
	}
	original := s.Run
	s.Run = func(c context.Context, e []string, d, n string, a ...string) ([]byte, error) {
		if filepath.Base(n) == "micromamba" && len(a) > 1 && a[1] == "install" && !strings.Contains(strings.Join(a, " "), "--dry-run") {
			return nil, errors.New("interrupted")
		}
		return original(c, e, d, n, a...)
	}
	if _, err := s.SetUpConda(context.Background(), "", []string{"ffmpeg"}, ""); err == nil {
		t.Fatal("install should fail")
	}
	if _, err := os.Stat(filepath.Join(s.Dir, "conda", ".lyk-ready")); !os.IsNotExist(err) {
		t.Fatalf("interrupted environment ready: %v", err)
	}
}

func TestSetUpReturnsInstallerErrorTail(t *testing.T) {
	s, _ := condaStudy(t)
	cause := errors.New(strings.Repeat("x", 6000) + "final installer reason")
	s.Run = func(context.Context, []string, string, string, ...string) ([]byte, error) { return nil, cause }
	_, err := s.SetUpConda(context.Background(), "3.11", nil, "")
	if err == nil || len(err.Error()) > 4100 || !strings.HasSuffix(err.Error(), "final installer reason") || !errors.Is(err, cause) {
		t.Fatalf("installer error tail missing: %v", err)
	}
}

func TestRecordedPythonNeverFallsBackToAnExistingDefault(t *testing.T) {
	for _, marker := range []bool{false, true} {
		t.Run(fmt.Sprint(marker), func(t *testing.T) {
			s, calls := condaStudy(t)
			if err := s.prepare(context.Background()); err != nil {
				t.Fatal(err)
			}
			if err := s.RememberPython("3.11"); err != nil {
				t.Fatal(err)
			}
			if marker {
				os.MkdirAll(filepath.Join(s.Dir, "conda"), 0755)
				os.WriteFile(filepath.Join(s.Dir, "conda", ".lyk-ready"), nil, 0600)
			}
			*calls = nil
			if err := s.prepare(context.Background()); err != nil {
				t.Fatal(err)
			}
			if index(*calls, "create -y") < 0 || index(*calls, filepath.Join(s.Dir, "conda", "bin", "python")+" -m venv --clear") < 0 {
				t.Fatalf("recorded version fell back to existing default: %v", *calls)
			}
		})
	}
}

func TestStudyBinDoesNotDependOnPersonPath(t *testing.T) {
	t.Setenv("PATH", "/no-person-tools")
	s, _ := condaStudy(t)
	if err := s.prepare(context.Background()); err != nil {
		t.Fatal(err)
	}
	if fi, err := os.Stat(filepath.Join(s.Dir, "bin")); err != nil || !fi.IsDir() {
		t.Fatalf("study bin missing: %v", err)
	}
}

func TestEveryLanguageRejectsFailedCondaRecovery(t *testing.T) {
	for _, lang := range []string{Python, R, Julia} {
		t.Run(lang, func(t *testing.T) {
			s, _ := condaStudy(t)
			for _, base := range []string{R, Julia} {
				if err := s.Bases.buildBaseForTest(context.Background(), base); err != nil {
					t.Fatal(err)
				}
			}
			if err := s.prepare(context.Background()); err != nil {
				t.Fatal(err)
			}
			if err := s.RememberPython("3.11"); err != nil {
				t.Fatal(err)
			}
			partial := filepath.Join(s.Dir, "conda", "bin", "partial-tool")
			os.MkdirAll(filepath.Dir(partial), 0700)
			os.WriteFile(partial, []byte("partial"), 0600)
			original := s.Run
			s.Run = func(c context.Context, e []string, d, n string, a ...string) ([]byte, error) {
				if filepath.Base(n) == "micromamba" {
					os.MkdirAll(filepath.Dir(partial), 0700)
					os.WriteFile(partial, []byte("partial"), 0600)
					return nil, errors.New("recovery unavailable")
				}
				return original(c, e, d, n, a...)
			}
			if _, err := s.Packages(context.Background(), lang); err == nil {
				t.Fatalf("%s inventory accepted an unready conda environment", lang)
			}
		})
	}
}

func TestAdditiveToolsKeepInstalledVenvPackages(t *testing.T) {
	python, err := exec.LookPath("python3")
	if err != nil {
		t.Skip("python3 is required for the real temporary venv regression")
	}
	version, err := exec.Command(python, "-I", "-c", `import sys;print('%d.%d'%sys.version_info[:2])`).Output()
	if err != nil {
		t.Fatal(err)
	}
	v := strings.TrimSpace(string(version))
	s, _ := condaStudy(t)
	original := s.Run
	s.Run = func(c context.Context, e []string, d, n string, a ...string) ([]byte, error) {
		if strings.Contains(strings.Join(a, " "), "sys.version_info") {
			return []byte(v), nil
		}
		if len(a) > 1 && a[0] == "-m" && a[1] == "venv" {
			return exec.CommandContext(c, python, a...).CombinedOutput()
		}
		return original(c, e, d, n, a...)
	}
	if err := s.prepare(context.Background()); err != nil {
		t.Fatal(err)
	}
	dirs, err := filepath.Glob(filepath.Join(s.Dir, "python", "lib", "python*", "site-packages"))
	if err != nil || len(dirs) != 1 {
		t.Fatalf("venv sites %v %v", dirs, err)
	}
	installed := filepath.Join(dirs[0], "previous_install.py")
	if err := os.WriteFile(installed, []byte("value = 73\n"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := s.RememberPython(v); err != nil {
		t.Fatal(err)
	}
	if _, err := s.SetUpConda(context.Background(), "", []string{"ffmpeg"}, ""); err != nil {
		t.Fatal(err)
	}
	out, err := exec.Command(filepath.Join(s.Dir, "python", "bin", "python"), "-I", "-c", "import previous_install;print(previous_install.value)").CombinedOutput()
	if err != nil || strings.TrimSpace(string(out)) != "73" {
		t.Fatalf("additive tools removed installed package: %s %v", out, err)
	}
	t.Logf("retained local module on Python %s: %s", v, out)
}

func TestLockReplacesThePrefixWithoutAnotherSolve(t *testing.T) {
	s, calls := condaStudy(t)
	if _, err := s.SetUpConda(context.Background(), "3.11", nil, ""); err != nil {
		t.Fatal(err)
	}
	stale := filepath.Join(s.Dir, "conda", "old-package")
	os.WriteFile(stale, nil, 0600)
	lock := filepath.Join(t.TempDir(), "exact.txt")
	os.WriteFile(lock, []byte("@EXPLICIT\n"), 0600)
	*calls = nil
	if _, err := s.SetUpConda(context.Background(), "3.11", nil, lock); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(stale); !os.IsNotExist(err) {
		t.Error("lock retained an unrelated package")
	}
	mutations := 0
	for _, call := range *calls {
		if !strings.Contains(call, "micromamba") {
			continue
		}
		if strings.Contains(call, " install ") {
			t.Errorf("lock was re-solved: %s", call)
		}
		if strings.Contains(call, " create ") {
			if strings.Contains(call, "python=") || strings.Contains(call, "ipykernel") {
				t.Errorf("lock has additional package constraints: %s", call)
			}
			if !strings.Contains(call, "--dry-run") {
				mutations++
			}
		}
	}
	if mutations != 1 {
		t.Fatalf("expected one exact lock creation, got %d: %v", mutations, *calls)
	}
}

func TestLockWithoutKernelFailsWithoutChangingItsPackages(t *testing.T) {
	s, calls := condaStudy(t)
	original := s.Run
	s.Run = func(c context.Context, e []string, d, n string, a ...string) ([]byte, error) {
		if strings.Contains(strings.Join(a, " "), "import ipykernel") {
			return nil, errors.New("ModuleNotFoundError: ipykernel")
		}
		return original(c, e, d, n, a...)
	}
	_, err := s.SetUpConda(context.Background(), "", nil, "/exact-lock.txt")
	if err == nil || !strings.Contains(err.Error(), "lock") {
		t.Fatalf("missing kernel accepted: %v", err)
	}
	for _, call := range *calls {
		if strings.Contains(call, "micromamba") && strings.Contains(call, " install ") {
			t.Fatalf("lock changed to add a kernel: %s", call)
		}
	}
	if s.condaReady() {
		t.Fatal("incompatible lock marked ready")
	}
}

func TestLockAddsOnlyRequestedPackages(t *testing.T) {
	s, calls := condaStudy(t)
	if _, err := s.SetUpConda(context.Background(), "", []string{"ffmpeg=7"}, "/exact-lock.txt"); err != nil {
		t.Fatal(err)
	}
	created, installed := false, false
	for _, call := range *calls {
		if !strings.Contains(call, "micromamba") || strings.Contains(call, "--dry-run") {
			continue
		}
		if strings.Contains(call, " create ") {
			created = true
			if !strings.Contains(call, "--file /exact-lock.txt") || strings.Contains(call, "ffmpeg") || strings.Contains(call, "ipykernel") || strings.Contains(call, "python=") {
				t.Fatalf("lock creation included additions: %s", call)
			}
		}
		if strings.Contains(call, " install ") {
			if !created || installed || !strings.HasSuffix(call, "--override-channels ffmpeg=7") {
				t.Fatalf("unexpected additions: %s", call)
			}
			installed = true
		}
	}
	if !created || !installed {
		t.Fatalf("expected exact lock then requested addition: %v", *calls)
	}
}

func TestLockAdditionsCannotChangePython(t *testing.T) {
	for _, stage := range []string{"plan", "installed"} {
		t.Run(stage, func(t *testing.T) {
			s, calls := condaStudy(t)
			original := s.Run
			installed := false
			s.Run = func(c context.Context, e []string, d, n string, a ...string) ([]byte, error) {
				args := strings.Join(a, " ")
				if strings.Contains(args, "install") {
					if strings.Contains(args, "--dry-run") {
						if stage == "plan" {
							return []byte(`{"success":true,"actions":{"LINK":[{"name":"python","version":"3.12.1"}]}}`), nil
						}
					} else {
						installed = true
					}
				}
				if installed && strings.Contains(args, "sys.version_info") {
					return []byte("3.12"), nil
				}
				return original(c, e, d, n, a...)
			}
			_, err := s.SetUpConda(context.Background(), "", []string{"ffmpeg"}, "/exact-lock.txt")
			if err == nil || err.Error() != PythonConflict("3.11").Error() {
				t.Fatalf("lock's Python changed: %v", err)
			}
			if s.condaReady() {
				t.Fatal("different Python marked ready")
			}
			if stage == "plan" {
				for _, call := range *calls {
					if strings.Contains(call, " install ") && !strings.Contains(call, "--dry-run") {
						t.Fatalf("installed different Python: %s", call)
					}
				}
			}
		})
	}
}

func TestBrokenInterpreterDoesNotPreventCondaRecovery(t *testing.T) {
	for _, automatic := range []bool{false, true} {
		t.Run(fmt.Sprint(automatic), func(t *testing.T) {
			s, calls := condaStudy(t)
			if _, err := s.SetUpConda(context.Background(), "3.11", nil, ""); err != nil {
				t.Fatal(err)
			}
			if err := os.Remove(filepath.Join(s.Dir, "conda", ".lyk-ready")); err != nil {
				t.Fatal(err)
			}
			original := s.Run
			rebuilt := false
			s.Run = func(c context.Context, e []string, d, n string, a ...string) ([]byte, error) {
				args := strings.Join(a, " ")
				if n == filepath.Join(s.Dir, "python", "bin", "python") && strings.Contains(args, "sys.version_info") {
					return nil, errors.New("interpreter cannot load a library from the interrupted conda prefix")
				}
				if filepath.Base(n) == "micromamba" && strings.Contains(args, "create") && !strings.Contains(args, "--dry-run") {
					rebuilt = true
				}
				return original(c, e, d, n, a...)
			}
			*calls = nil
			var err error
			if automatic {
				err = s.prepare(context.Background())
			} else {
				_, err = s.SetUpConda(context.Background(), "", []string{"ffmpeg"}, "")
			}
			if err != nil || !rebuilt || !s.condaReady() {
				t.Fatalf("half-made environment did not recover: %v; commands=%v", err, *calls)
			}
			if index(*calls, "--upgrade --system-site-packages") < 0 {
				t.Fatalf("recovery did not preserve existing venv: %v", *calls)
			}
		})
	}
}

func TestLockCannotRemoveTheChosenPython(t *testing.T) {
	for _, state := range []string{"existing", "lost", "requested", "default-survives"} {
		t.Run(state, func(t *testing.T) {
			s, calls := condaStudy(t)
			python := ""
			if state == "existing" {
				if _, err := s.SetUpConda(context.Background(), "3.11", nil, ""); err != nil {
					t.Fatal(err)
				}
				venvPython := filepath.Join(s.Dir, "python", "bin", "python")
				if err := os.Remove(venvPython); err != nil {
					t.Fatal(err)
				}
				if err := os.Symlink(filepath.Join(s.Dir, "conda", "bin", "python"), venvPython); err != nil {
					t.Fatal(err)
				}
			} else if state == "requested" {
				python = "3.11"
			} else {
				if state == "default-survives" {
					if err := s.prepare(context.Background()); err != nil {
						t.Fatal(err)
					}
				}
				if err := s.RememberPython("3.11"); err != nil {
					t.Fatal(err)
				}
			}
			original := s.Run
			s.Run = func(c context.Context, e []string, d, n string, a ...string) ([]byte, error) {
				args := strings.Join(a, " ")
				if filepath.Base(n) == "micromamba" && strings.Contains(args, "--file") {
					if strings.Contains(args, "--dry-run") {
						return []byte(`{"success":true,"actions":{"LINK":[]}}`), nil
					}
					return nil, os.MkdirAll(filepath.Join(s.Dir, "conda", "bin"), 0755)
				}
				return original(c, e, d, n, a...)
			}
			*calls = nil
			_, err := s.SetUpConda(context.Background(), python, nil, "/tools-only-lock.txt")
			if err == nil || !strings.Contains(err.Error(), "lock") || s.condaReady() {
				t.Fatalf("lock without chosen Python accepted: err=%v ready=%v", err, s.condaReady())
			}
			for _, call := range *calls {
				if strings.Contains(call, "micromamba") && strings.Contains(call, " install ") {
					t.Fatalf("lock changed without requested additions: %s", call)
				}
			}
		})
	}
}

func TestToolsOnlyLockRequiresAUsableRecordedDefault(t *testing.T) {
	for _, state := range []string{"compatible", "broken", "wrong-minor"} {
		t.Run(state, func(t *testing.T) {
			s, calls := condaStudy(t)
			if err := s.prepare(context.Background()); err != nil {
				t.Fatal(err)
			}
			if err := s.RememberPython("3.13"); err != nil {
				t.Fatal(err)
			}
			original := s.Run
			checked := false
			s.Run = func(c context.Context, e []string, d, n string, a ...string) ([]byte, error) {
				args := strings.Join(a, " ")
				if filepath.Base(n) == "micromamba" && strings.Contains(args, "--file") {
					if strings.Contains(args, "--dry-run") {
						return []byte(`{"success":true,"actions":{"LINK":[]}}`), nil
					}
					return nil, os.MkdirAll(filepath.Join(s.Dir, "conda", "bin"), 0755)
				}
				if strings.Contains(args, "import sys, ipykernel") {
					checked = true
					if state == "broken" {
						return nil, errors.New("ModuleNotFoundError: ipykernel")
					}
					if state == "wrong-minor" {
						return []byte("3.11"), nil
					}
				}
				return original(c, e, d, n, a...)
			}
			*calls = nil
			_, err := s.SetUpConda(context.Background(), "", nil, "/tools-only-lock.txt")
			if state == "compatible" {
				if err != nil || !s.condaReady() {
					t.Fatalf("compatible default refused: %v", err)
				}
			} else if err == nil || s.condaReady() {
				t.Fatalf("incompatible default accepted: %v", err)
			}
			if !checked {
				t.Fatal("default interpreter and kernel were not checked")
			}
		})
	}
}

func TestRejectedLockPreservesReadyEnvironment(t *testing.T) {
	for _, failure := range []string{"python", "unresolvable"} {
		t.Run(failure, func(t *testing.T) {
			s, _ := condaStudy(t)
			if _, err := s.SetUpConda(t.Context(), "3.11", []string{"ffmpeg"}, ""); err != nil {
				t.Fatal(err)
			}
			prefix := filepath.Join(s.Dir, "conda")
			tool := filepath.Join(prefix, "previous-dependency")
			if err := os.WriteFile(tool, []byte("preserve installed tool"), 0600); err != nil {
				t.Fatal(err)
			}
			original := s.Run
			s.Run = func(ctx context.Context, env []string, dir, name string, args ...string) ([]byte, error) {
				if strings.Contains(strings.Join(args, " "), "--dry-run") {
					if failure == "unresolvable" {
						return nil, errors.New("unresolvable lock")
					}
					return []byte(`{"success":true,"actions":{"LINK":[{"name":"python","version":"3.12.1"}]}}`), nil
				}
				return original(ctx, env, dir, name, args...)
			}
			_, err := s.SetUpConda(t.Context(), "", nil, filepath.Join(s.Dir, "candidate.lock"))
			want := "This study uses Python 3.11; a study keeps one Python version."
			if failure == "unresolvable" {
				want = "unresolvable lock"
			}
			if err == nil || !strings.Contains(err.Error(), want) {
				t.Fatalf("wanted %q, got %v", want, err)
			}
			if data, err := os.ReadFile(tool); err != nil || string(data) != "preserve installed tool" {
				t.Fatalf("rejected lock deleted working tool: %q %v", data, err)
			}
			if !s.condaReady() || !s.condaPython() {
				t.Fatal("rejected lock removed ready marker or Python")
			}
			plans, err := filepath.Glob(filepath.Join(s.Dir, ".conda-plan-*"))
			if err != nil || len(plans) != 0 {
				t.Fatalf("candidate leftovers: %v %v", plans, err)
			}
		})
	}
}

func TestValidPythonVersion(t *testing.T) {
	for v, want := range map[string]bool{"3.9": true, "3.14": true, "3.15": true, "3.8": false, "3.16": false, "3.15.1": false} {
		if ValidPythonVersion(v) != want {
			t.Errorf("%s: want %v", v, want)
		}
	}
}
