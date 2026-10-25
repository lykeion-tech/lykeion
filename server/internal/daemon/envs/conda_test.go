package envs

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
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
	if err := b.Ensure(context.Background(), Python); err != nil {
		t.Fatal(err)
	}
	calls := []string{}
	s := ForStudy(b, t.TempDir())
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
			return []byte("3.11\n"), nil
		}
		if strings.Contains(line, "freeze --local") {
			return nil, nil
		}
		if filepath.Base(name) == "micromamba" {
			if got, _ := envValue(env, "MAMBA_ROOT_PREFIX"); got != filepath.Join(s.Dir, ".mamba") {
				t.Errorf("root %q", got)
			}
		}
		return f.run(ctx, env, dir, name, args...)
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
