package daemon

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"lykeion/server/internal/daemon/sandbox"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync/atomic"
	"syscall"
	"testing"
	"time"

	"github.com/google/uuid"
	"lykeion/server/internal/coresearch"
	"lykeion/server/internal/daemon/envs"
)

func TestRunSeedEnv(t *testing.T) {
	t.Setenv("LYK_TOKEN", "secret")
	t.Setenv("PATH", "/personal")
	t.Setenv("PERSONAL", "secret")
	s := envs.ForStudy(&envs.Bases{Dir: t.TempDir()}, t.TempDir())
	got := strings.Join(recordedEnv(s, []string{"VIRTUAL_ENV=/project"}, t.TempDir(), 42), "\n")
	for _, bad := range []string{"LYK_TOKEN", "/personal", "PERSONAL"} {
		if strings.Contains(got, bad) {
			t.Fatal(got)
		}
	}
	for _, want := range []string{"PYTHONHASHSEED=0", "LYK_SEED=42", "VIRTUAL_ENV=/project"} {
		if !strings.Contains(got, want) {
			t.Fatal(got)
		}
	}
}
func TestRunSeesOnlyDeclaredInputs(t *testing.T) {
	f := newCoreFixture(t)
	f.saved("data/a.csv", "saved", 1)
	ref := f.files[0]
	must(t, os.WriteFile(filepath.Join(f.dir(), "ambient"), []byte("secret"), 0600))
	dir := t.TempDir()
	c := coresearch.ClaimedRun{WorkContext: f.work, ID: uuid.New(), Inputs: []coresearch.FileRef{ref}}
	f.saved("script.py", "print('ok')", 1)
	c.Script = f.files[0]
	must(t, f.d.restoreRecorded(t.Context(), c, dir))
	b, e := os.ReadFile(filepath.Join(dir, "in/data/a.csv"))
	must(t, e)
	if string(b) != "saved" {
		t.Fatal(string(b))
	}
	if _, e = os.Stat(filepath.Join(dir, "in/ambient")); !errors.Is(e, os.ErrNotExist) {
		t.Fatal(e)
	}
	info, e := os.Stat(filepath.Join(dir, "in/data/a.csv"))
	must(t, e)
	if info.Mode().Perm()&0222 != 0 {
		t.Fatal(info.Mode())
	}
}
func TestRunOutputsNeverFollowLinks(t *testing.T) {
	dir := t.TempDir()
	outside := filepath.Join(t.TempDir(), "secret")
	must(t, os.WriteFile(outside, []byte("secret"), 0600))
	must(t, os.Symlink(outside, filepath.Join(dir, "link")))
	must(t, os.Mkdir(filepath.Join(dir, "nested"), 0700))
	must(t, os.WriteFile(filepath.Join(dir, "nested/ok"), []byte("ok"), 0600))
	must(t, os.Mkdir(filepath.Join(dir, ".git"), 0700))
	must(t, os.WriteFile(filepath.Join(dir, ".git/config"), []byte("private"), 0600))
	outputs, skipped, e := collectRecordedOutputs(dir)
	must(t, e)
	defer closeRecordedOutputs(outputs)
	if len(outputs) != 1 || outputs[0].path != "nested/ok" || len(skipped) != 2 {
		t.Fatalf("outputs=%v skipped=%v", outputs, skipped)
	}
}
func TestRunOutputCap(t *testing.T) {
	dir := t.TempDir()
	f, e := os.Create(filepath.Join(dir, "huge"))
	must(t, e)
	must(t, f.Truncate((50<<20)+1))
	must(t, f.Close())
	outputs, skipped, e := collectRecordedOutputs(dir)
	must(t, e)
	defer closeRecordedOutputs(outputs)
	if len(outputs) != 0 || skipped["huge"] != "over 50 MiB" {
		t.Fatal(outputs, skipped)
	}
}
func TestRunNeedsReadyEnvironment(t *testing.T) {
	f := newCoreFixture(t)
	c := coresearch.ClaimedRun{WorkContext: f.work, ID: uuid.New(), Script: coresearch.FileRef{Path: "script.py"}}
	e := f.d.dispatchCoresearchRun(context.Background(), c)
	if e == nil || !strings.Contains(e.Error(), "set up the project environment first (lyk env set-up)") {
		t.Fatal(e)
	}
}

func TestRejectedRecordedLeaseCannotStartProcessOrPinPython(t *testing.T) {
	f, _, s, _ := newRecordedFixture(t, "")
	previous := f.d.api.HTTP.Transport
	var reports []map[string]string
	f.d.api.HTTP.Transport = coreRoundTrip(func(r *http.Request) (*http.Response, error) {
		if r.Method == http.MethodGet && (strings.Contains(r.URL.Path, "/runs/") || strings.Contains(r.URL.Path, "/setups/")) && strings.HasSuffix(r.URL.Path, "/status") {
			return &http.Response{StatusCode: http.StatusConflict, Body: io.NopCloser(strings.NewReader("expired claim")), Header: http.Header{}}, nil
		}
		if strings.Contains(r.URL.Path, "/setups/") && strings.HasSuffix(r.URL.Path, "/finish") {
			var body map[string]string
			must(t, json.NewDecoder(r.Body).Decode(&body))
			reports = append(reports, body)
			return &http.Response{StatusCode: http.StatusNoContent, Body: io.NopCloser(strings.NewReader("")), Header: http.Header{}}, nil
		}
		return previous.RoundTrip(r)
	})
	sentinel := filepath.Join(t.TempDir(), "started")
	run := f.d.recordedRunner(f.work, "runs", uuid.New(), s, sandbox.Policy{}, t.TempDir(), 0, nil)
	if _, err := run(t.Context(), nil, s.Dir, "/bin/sh", "-c", `touch "$1"`, "sh", sentinel); err == nil {
		t.Fatal("expired claim started a process")
	}
	if _, err := os.Stat(sentinel); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("process sentinel exists: %v", err)
	}
	v := "3.12"
	err := f.d.dispatchCoresearchSetup(t.Context(), coresearch.ClaimedSetup{WorkContext: f.work, ID: uuid.New(), Python: &v})
	if err == nil || s.RecordedPython() != "" {
		t.Fatalf("setup err=%v version=%q", err, s.RecordedPython())
	}
	for _, report := range reports {
		if report["status"] == "running" || report["python_version"] != "" {
			t.Fatalf("setup changed version: %v", report)
		}
	}
}
func TestRunFolderRemoved(t *testing.T) {
	f := newCoreFixture(t)
	c := coresearch.ClaimedRun{WorkContext: f.work, ID: uuid.New(), Script: coresearch.FileRef{Path: "script.py"}}
	_ = f.d.dispatchCoresearchRun(t.Context(), c)
	if _, e := os.Stat(filepath.Join(f.d.cfg.Home, "coresearch-runs", c.ID.String())); !errors.Is(e, os.ErrNotExist) {
		t.Fatal(e)
	}
}
func TestEnvShaStable(t *testing.T) {
	dir := t.TempDir()
	s := envs.ForStudy(&envs.Bases{Dir: t.TempDir()}, dir)
	for _, p := range []string{filepath.Join(s.Bases.Dir, "python"), filepath.Join(dir, "python/bin")} {
		must(t, os.MkdirAll(p, 0700))
	}
	for p, v := range map[string]string{filepath.Join(s.Bases.Dir, "python/ready"): "", filepath.Join(s.Bases.Dir, "python/lock.txt"): "base", filepath.Join(dir, "python/bin/python"): ""} {
		must(t, os.WriteFile(p, []byte(v), 0700))
	}
	s.ReadOnly = true
	s.Run = func(_ context.Context, _ []string, _ string, name string, args ...string) ([]byte, error) {
		if name == "/bin/sh" {
			return nil, nil
		}
		return []byte("numpy==1\n"), nil
	}
	a, e := recordedEnvSHA(t.Context(), s)
	must(t, e)
	b, e := recordedEnvSHA(t.Context(), s)
	must(t, e)
	if a != b || len(a) != 64 {
		t.Fatal(a, b)
	}
}

func newRecordedFixture(t *testing.T, script string) (*coreFixture, coresearch.ClaimedRun, *envs.Study, *map[string]any) {
	t.Helper()
	if err := sandbox.Check(t.Context()); err != nil {
		t.Skipf("sandbox unavailable: %v", err)
	}
	f := newCoreFixture(t)
	s := fakeStudy(t, envs.Python)
	f.d.bases = s.Bases
	must(t, os.WriteFile(filepath.Join(s.Bases.Dir, "python/bin/python"), []byte("#!/bin/sh\necho base-inventory\n"), 0700))
	s, e := f.d.recordedStudy(t.Context(), f.work)
	must(t, e)
	must(t, os.MkdirAll(filepath.Join(s.Dir, "python/bin"), 0700))
	must(t, os.WriteFile(filepath.Join(s.Dir, "python/bin/python"), []byte("#!/bin/sh\nif [ \"$1\" = -m ] || [ \"$1\" = -c ]; then echo 'numpy==1'; exit; fi\nexec /bin/sh \"$@\"\n"), 0700))
	f.saved("script.py", script, 1)
	c := coresearch.ClaimedRun{WorkContext: f.work, ID: uuid.New(), Script: f.files[0], Seed: 42}
	facts := map[string]any{}
	original := http.DefaultTransport
	f.d.api.HTTP = &http.Client{Transport: coreRoundTrip(func(r *http.Request) (*http.Response, error) {
		if strings.Contains(r.URL.Path, "/runs/") && strings.HasSuffix(r.URL.Path, "/finish") {
			if e := r.ParseMultipartForm(1 << 20); e != nil {
				return nil, e
			}
			defer r.MultipartForm.RemoveAll()
			if e := json.Unmarshal([]byte(r.FormValue("facts")), &facts); e != nil {
				return nil, e
			}
			return &http.Response{StatusCode: 204, Body: io.NopCloser(strings.NewReader("")), Header: http.Header{}}, nil
		}
		return original.RoundTrip(r)
	})}
	return f, c, s, &facts
}
func TestRerunRefusesChangedEnvironment(t *testing.T) {
	f, c, _, facts := newRecordedFixture(t, "echo executed; echo result > out/result")
	expected := strings.Repeat("0", 64)
	c.ExpectedEnvSHA = &expected
	c.ExpectedEnvVersion = envs.FingerprintVersion
	e := f.d.dispatchCoresearchRun(t.Context(), c)
	if e == nil || !strings.Contains(e.Error(), recordedChanged) || (*facts)["stdout_tail"] != "" || (*facts)["status"] != "failed" {
		t.Fatal(e, *facts)
	}
}
func TestRunRefusesEnvironmentChangedDuringExecution(t *testing.T) {
	f, c, s, facts := newRecordedFixture(t, "echo started > out/started; while [ ! -e out/release ]; do sleep .05; done; echo result > out/result")
	done := make(chan error, 1)
	go func() { done <- f.d.dispatchCoresearchRun(t.Context(), c) }()
	dir := filepath.Join(f.d.cfg.Home, "coresearch-runs", c.ID.String(), "out")
	waitRecorded(t, func() bool { _, e := os.Stat(filepath.Join(dir, "started")); return e == nil })
	must(t, os.WriteFile(filepath.Join(s.Bases.Dir, "python/lock.txt"), []byte("changed"), 0600))
	must(t, os.WriteFile(filepath.Join(dir, "release"), nil, 0600))
	e := <-done
	if e == nil || !strings.Contains(e.Error(), "environment changed during execution") || (*facts)["status"] != "failed" {
		t.Fatal(e, *facts)
	}
	if len((*facts)["outputs"].(map[string]any)) != 0 {
		t.Fatal(*facts)
	}
}
func TestRunSandboxExecutesAndCleans(t *testing.T) {
	f, c, s, facts := newRecordedFixture(t, "printf '%s/%s' \"$LYK_SEED\" \"$PYTHONHASHSEED\" > out/result; test ! -e in/ambient; test -z \"$PERSONAL\"; echo completed")
	t.Setenv("PERSONAL", "secret")
	must(t, f.d.dispatchCoresearchRun(t.Context(), c))
	if (*facts)["status"] != "ok" || (*facts)["stdout_tail"] != "completed\n" {
		t.Fatal(*facts)
	}
	if _, e := os.Stat(filepath.Join(f.d.cfg.Home, "coresearch-runs", c.ID.String())); !errors.Is(e, os.ErrNotExist) {
		t.Fatal(e)
	}
	if _, e := os.Stat(filepath.Join(s.Dir, "python/bin/python.ran")); !errors.Is(e, os.ErrNotExist) {
		t.Fatal("inventory mutated", e)
	}
}
func TestRunRevocationStopsProcess(t *testing.T) {
	for _, status := range []string{"cancelled", "revoked"} {
		t.Run(status, func(t *testing.T) {
			f, c, _, facts := newRecordedFixture(t, "echo $$ > out/pid; while :; do sleep 1; done")
			previous := f.d.api.HTTP.Transport
			var lost atomic.Bool
			f.d.api.HTTP.Transport = coreRoundTrip(func(r *http.Request) (*http.Response, error) {
				if lost.Load() && strings.Contains(r.URL.Path, "/runs/") && strings.HasSuffix(r.URL.Path, "/finish") {
					return &http.Response{StatusCode: 404, Header: http.Header{}, Body: io.NopCloser(strings.NewReader("revoked"))}, nil
				}
				if lost.Load() && strings.Contains(r.URL.Path, "/runs/") && (strings.HasSuffix(r.URL.Path, "/status") || strings.HasSuffix(r.URL.Path, "/heartbeat")) {
					code, body := 200, `{"status":"cancelled"}`
					if status == "revoked" {
						code, body = 404, ""
					}
					return &http.Response{StatusCode: code, Body: io.NopCloser(strings.NewReader(body)), Header: http.Header{}}, nil
				}
				return previous.RoundTrip(r)
			})
			done := make(chan error, 1)
			go func() { done <- f.d.dispatchCoresearchRun(t.Context(), c) }()
			path := filepath.Join(f.d.cfg.Home, "coresearch-runs", c.ID.String(), "out/pid")
			waitRecorded(t, func() bool { b, e := os.ReadFile(path); return e == nil && len(b) > 0 })
			data, e := os.ReadFile(path)
			must(t, e)
			pid, e := strconv.Atoi(strings.TrimSpace(string(data)))
			must(t, e)
			lost.Store(true)
			select {
			case e := <-done:
				if e == nil {
					t.Fatal("run survived")
				}
			case <-time.After(8 * time.Second):
				t.Fatal("run did not stop")
			}
			if syscall.Kill(pid, 0) == nil {
				t.Fatal("process alive", pid)
			}
			if len(*facts) != 0 {
				t.Fatal("uploaded after authority loss", *facts)
			}
		})
	}
}
func TestRunScriptLayoutPreservesValidPaths(t *testing.T) {
	for _, path := range []string{"in/a.py", "out/a.py", "tmp/a.py"} {
		t.Run(path, func(t *testing.T) {
			f, c, _, facts := newRecordedFixture(t, "test \"$(cat in/a.py)\" = input; echo completed")
			c.Script.Path = path
			f.saved("a.py", "input", 1)
			c.Inputs = append(c.Inputs, f.files[0])
			must(t, f.d.dispatchCoresearchRun(t.Context(), c))
			if (*facts)["status"] != "ok" {
				t.Fatal(*facts)
			}
		})
	}
}
func TestSetupReportsPython(t *testing.T) {
	for _, pin := range []string{"", "3.12"} {
		t.Run(pin, func(t *testing.T) {
			f, _, s, _ := newRecordedFixture(t, "")
			if pin != "" {
				must(t, s.RememberPython(pin))
			}
			var reports []map[string]string
			previous := f.d.api.HTTP.Transport
			f.d.api.HTTP.Transport = coreRoundTrip(func(r *http.Request) (*http.Response, error) {
				if strings.Contains(r.URL.Path, "/setups/") && strings.HasSuffix(r.URL.Path, "/finish") {
					var body map[string]string
					must(t, json.NewDecoder(r.Body).Decode(&body))
					reports = append(reports, body)
					return &http.Response{StatusCode: 204, Body: io.NopCloser(strings.NewReader("")), Header: http.Header{}}, nil
				}
				return previous.RoundTrip(r)
			})
			_ = f.d.dispatchCoresearchSetup(t.Context(), coresearch.ClaimedSetup{WorkContext: f.work, ID: uuid.New(), Conda: []string{"numpy"}})
			want := pin
			if want == "" {
				want = "3.13"
			}
			if len(reports) < 2 || reports[0]["status"] != "running" || reports[0]["python_version"] != want || reports[len(reports)-1]["python_version"] != want {
				t.Fatal(reports)
			}
		})
	}
}
func TestSetupReservationFailureHasNoMutation(t *testing.T) {
	f, _, s, _ := newRecordedFixture(t, "")
	must(t, os.RemoveAll(filepath.Join(s.Dir, "python")))
	previous := f.d.api.HTTP.Transport
	f.d.api.HTTP.Transport = coreRoundTrip(func(r *http.Request) (*http.Response, error) {
		if strings.Contains(r.URL.Path, "/setups/") && strings.HasSuffix(r.URL.Path, "/finish") {
			var body map[string]string
			json.NewDecoder(r.Body).Decode(&body)
			if body["status"] == "running" {
				return &http.Response{StatusCode: 409, Body: io.NopCloser(strings.NewReader("conflict")), Header: http.Header{}}, nil
			}
			return &http.Response{StatusCode: 204, Body: io.NopCloser(strings.NewReader("")), Header: http.Header{}}, nil
		}
		return previous.RoundTrip(r)
	})
	v := "3.12"
	e := f.d.dispatchCoresearchSetup(t.Context(), coresearch.ClaimedSetup{WorkContext: f.work, ID: uuid.New(), Python: &v})
	if e == nil || s.RecordedPython() != "" {
		t.Fatal(e, s.RecordedPython())
	}
	entries, e := os.ReadDir(s.Dir)
	must(t, e)
	if len(entries) != 0 {
		t.Fatal("mutation before reservation", entries)
	}
}

func TestSetupWithMissingBaseDoesNotPersistPython(t *testing.T) {
	f, _, s, _ := newRecordedFixture(t, "")
	must(t, os.Remove(filepath.Join(s.Bases.Dir, "python", "ready")))
	var reports []map[string]string
	previous := f.d.api.HTTP.Transport
	f.d.api.HTTP.Transport = coreRoundTrip(func(r *http.Request) (*http.Response, error) {
		if strings.Contains(r.URL.Path, "/setups/") && strings.HasSuffix(r.URL.Path, "/finish") {
			var body map[string]string
			must(t, json.NewDecoder(r.Body).Decode(&body))
			reports = append(reports, body)
			return &http.Response{StatusCode: 204, Body: io.NopCloser(strings.NewReader("")), Header: http.Header{}}, nil
		}
		return previous.RoundTrip(r)
	})
	v := "3.12"
	err := f.d.dispatchCoresearchSetup(t.Context(), coresearch.ClaimedSetup{WorkContext: f.work, ID: uuid.New(), Python: &v})
	if !errors.Is(err, envs.ErrBoundedProvisioningUnavailable) || s.RecordedPython() != "" {
		t.Fatalf("error=%v, version=%q", err, s.RecordedPython())
	}
	for _, report := range reports {
		if report["status"] == "running" || report["python_version"] != "" {
			t.Fatalf("persisted Python without a base: %v", report)
		}
	}
}

func waitRecorded(t *testing.T, condition func() bool) {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		if condition() {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatal("timed out waiting for recorded execution")
}

func TestRunHasNoNetwork(t *testing.T) {
	f, c, s, _ := newRecordedFixture(t, "")
	dir := t.TempDir()
	tmp := filepath.Join(dir, "tmp")
	for _, p := range []string{"in", "script", "out", "tmp"} {
		must(t, os.Mkdir(filepath.Join(dir, p), 0700))
	}
	p, e := f.d.recordedPolicy(s, dir, tmp, false)
	must(t, e)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { io.WriteString(w, "connected") }))
	defer server.Close()
	for _, offline := range []bool{false, true} {
		p.NoNetwork = offline
		cmd, e := p.Command(t.Context(), "/usr/bin/curl", "-sS", "--max-time", "2", server.URL)
		must(t, e)
		defer sandbox.CloseCommand(cmd)
		cmd.Dir = dir
		out, e := cmd.CombinedOutput()
		if offline {
			if e == nil || strings.Contains(string(out), "connected") {
				t.Fatal("network allowed", e, string(out))
			}
		} else if e != nil || string(out) != "connected" {
			t.Fatal("network control", e, string(out))
		}
	}
	_ = c
}
func TestRunCannotReadAmbientFiles(t *testing.T) {
	f, c, s, _ := newRecordedFixture(t, "")
	secret := filepath.Join(f.d.cfg.Home, "ambient")
	must(t, os.WriteFile(secret, []byte("secret"), 0600))
	script := `if cat ` + strconv.Quote(secret) + ` > out/leak 2>/dev/null; then exit 12; fi
if cat ` + strconv.Quote(filepath.Join(f.dir(), "chat-secret")) + ` > out/leak 2>/dev/null; then exit 13; fi
if echo bad > ` + strconv.Quote(filepath.Join(s.Dir, "mutation")) + `; then exit 14; fi
echo confined`
	must(t, os.WriteFile(filepath.Join(f.dir(), "chat-secret"), []byte("chat"), 0600))
	f.saved("script.py", script, 1)
	c.Script = f.files[0]
	must(t, f.d.dispatchCoresearchRun(t.Context(), c))
	if _, e := os.Stat(filepath.Join(s.Dir, "mutation")); !errors.Is(e, os.ErrNotExist) {
		t.Fatal(e)
	}
}
func TestRunAndSetupShareProjectGate(t *testing.T) {
	f, c, s, _ := newRecordedFixture(t, "echo started > out/started; while [ ! -e out/release ]; do sleep .05; done")
	runDone := make(chan error, 1)
	go func() { runDone <- f.d.dispatchCoresearchRun(t.Context(), c) }()
	dir := filepath.Join(f.d.cfg.Home, "coresearch-runs", c.ID.String(), "out")
	waitRecorded(t, func() bool { _, e := os.Stat(filepath.Join(dir, "started")); return e == nil })
	ctx, cancel := context.WithTimeout(t.Context(), 100*time.Millisecond)
	defer cancel()
	sibling := envs.ForStudy(s.Bases, s.Dir)
	_, e := sibling.ExecutionLease(ctx)
	if !errors.Is(e, context.DeadlineExceeded) {
		t.Fatal("run did not hold project lease", e)
	}
	must(t, os.WriteFile(filepath.Join(dir, "release"), nil, 0600))
	must(t, <-runDone)
	release, e := sibling.ExecutionLease(t.Context())
	must(t, e)
	release()
}

func installRecordedSetupStub(t *testing.T, s *envs.Study, body string) {
	t.Helper()
	path := filepath.Join(s.Bases.Dir, "micromamba/2.9.0-0/micromamba")
	must(t, os.MkdirAll(filepath.Dir(path), 0700))
	must(t, os.WriteFile(path, []byte("#!/bin/sh\n"+body), 0700))
}

const recordedSetupStub = `
case " $* " in
 *" --dry-run "*) echo '{"success":true,"actions":{"LINK":[{"name":"python","version":"3.13.16"}]}}'; exit;;
 *" export "*) echo '@EXPLICIT'; exit;;
esac
while [ "$1" != -p ]; do shift; done; shift; prefix=$1
mkdir -p "$prefix/bin"
cat > "$prefix/bin/python" <<'PYTHON'
#!/bin/sh
if [ "$1" = -I ]; then echo 3.13; exit; fi
if [ "$2" = venv ]; then
 for dest; do :; done
 mkdir -p "$dest/bin"
 cp "$0" "$dest/bin/python"
 chmod +x "$dest/bin/python"
 printf 'version = 3.13.16\n' > "$dest/pyvenv.cfg"
fi
PYTHON
chmod +x "$prefix/bin/python"
printf 'installed\n'
`

func TestSetupCompletesInsideSandbox(t *testing.T) {
	f, _, s, _ := newRecordedFixture(t, "")
	must(t, os.RemoveAll(filepath.Join(s.Dir, "python")))
	installRecordedSetupStub(t, s, recordedSetupStub)
	var final map[string]string
	reservations := 0
	previous := f.d.api.HTTP.Transport
	f.d.api.HTTP.Transport = coreRoundTrip(func(r *http.Request) (*http.Response, error) {
		if strings.Contains(r.URL.Path, "/setups/") && strings.HasSuffix(r.URL.Path, "/finish") {
			var body map[string]string
			json.NewDecoder(r.Body).Decode(&body)
			if body["status"] == "running" {
				if reservations == 0 {
					entries, _ := os.ReadDir(s.Dir)
					if len(entries) != 0 {
						t.Error("installation preceded reservation", entries)
					}
					if s.RecordedPython() != "" {
						t.Error("pin preceded reservation")
					}
				}
				reservations++
			} else {
				final = body
			}
			return &http.Response{StatusCode: 204, Body: io.NopCloser(strings.NewReader("")), Header: http.Header{}}, nil
		}
		return previous.RoundTrip(r)
	})
	must(t, f.d.dispatchCoresearchSetup(t.Context(), coresearch.ClaimedSetup{WorkContext: f.work, ID: uuid.New(), Conda: []string{"numpy"}}))
	if reservations == 0 || final["status"] != "ok" || final["python_version"] != "3.13" || !strings.Contains(final["log"], "installed") {
		t.Fatal(reservations, final)
	}
	if e := s.Ready(envs.Python); e != nil {
		t.Fatal(e)
	}
}
func TestSetupRevocationStopsProcess(t *testing.T) {
	for _, revoke := range []bool{false, true} {
		t.Run(strconv.FormatBool(revoke), func(t *testing.T) {
			f, _, s, _ := newRecordedFixture(t, "")
			installRecordedSetupStub(t, s, `echo $$ > setup-pid; while :; do sleep 1; done`)
			var lost atomic.Bool
			var terminal atomic.Int32
			previous := f.d.api.HTTP.Transport
			f.d.api.HTTP.Transport = coreRoundTrip(func(r *http.Request) (*http.Response, error) {
				if strings.Contains(r.URL.Path, "/setups/") {
					if lost.Load() && strings.HasSuffix(r.URL.Path, "/finish") {
						return &http.Response{StatusCode: 404, Header: http.Header{}, Body: io.NopCloser(strings.NewReader("revoked"))}, nil
					}
					if strings.HasSuffix(r.URL.Path, "/finish") {
						var body map[string]string
						json.NewDecoder(r.Body).Decode(&body)
						if body["status"] != "running" {
							terminal.Add(1)
						}
						return &http.Response{StatusCode: 204, Body: io.NopCloser(strings.NewReader("")), Header: http.Header{}}, nil
					}
					if (strings.HasSuffix(r.URL.Path, "/status") || strings.HasSuffix(r.URL.Path, "/heartbeat")) && lost.Load() {
						code, body := 200, `{"status":"cancelled"}`
						if revoke {
							code, body = 404, ""
						}
						return &http.Response{StatusCode: code, Body: io.NopCloser(strings.NewReader(body)), Header: http.Header{}}, nil
					}
				}
				return previous.RoundTrip(r)
			})
			done := make(chan error, 1)
			go func() {
				done <- f.d.dispatchCoresearchSetup(t.Context(), coresearch.ClaimedSetup{WorkContext: f.work, ID: uuid.New(), Conda: []string{"numpy"}})
			}()
			path := filepath.Join(s.Dir, "setup-pid")
			waitRecorded(t, func() bool { b, e := os.ReadFile(path); return e == nil && len(b) > 0 })
			data, e := os.ReadFile(path)
			must(t, e)
			pid, e := strconv.Atoi(strings.TrimSpace(string(data)))
			must(t, e)
			lost.Store(true)
			select {
			case e := <-done:
				if e == nil {
					t.Fatal("setup survived")
				}
			case <-time.After(8 * time.Second):
				t.Fatal("setup did not stop")
			}
			if syscall.Kill(pid, 0) == nil {
				t.Fatal("setup process alive", pid)
			}
			if terminal.Load() != 0 {
				t.Fatal("reported after loss", terminal.Load())
			}
		})
	}
}
func TestSetupConflictingVersionDoesNotMutate(t *testing.T) {
	f, _, s, _ := newRecordedFixture(t, "")
	must(t, s.RememberPython("3.12"))
	v := "3.13"
	e := f.d.dispatchCoresearchSetup(t.Context(), coresearch.ClaimedSetup{WorkContext: f.work, ID: uuid.New(), Python: &v})
	if e == nil || !strings.Contains(e.Error(), "keeps one Python") {
		t.Fatal(e)
	}
	if s.RecordedPython() != "3.12" {
		t.Fatal(s.RecordedPython())
	}
	if _, e = os.Stat(filepath.Join(s.Dir, ".mamba")); !errors.Is(e, os.ErrNotExist) {
		t.Fatal(e)
	}
}
func TestRunRealPython(t *testing.T) {
	python := os.Getenv("LYK_TEST_CELL_PYTHON")
	if python == "" {
		t.Skip("set LYK_TEST_CELL_PYTHON to existing readonly Python")
	}
	f, c, s, _ := newRecordedFixture(t, "")
	dir := t.TempDir()
	tmp := filepath.Join(dir, "tmp")
	for _, name := range []string{"in", "script", "out", "tmp", "tmp/home"} {
		must(t, os.Mkdir(filepath.Join(dir, name), 0700))
	}
	script := filepath.Join(dir, "script/check.py")
	must(t, os.WriteFile(script, []byte(`import os, pathlib, socket
assert os.environ['LYK_SEED'] == '42'
assert os.environ['PYTHONHASHSEED'] == '0'
assert [k for k in os.environ if k.startswith('LYK_')] == ['LYK_SEED']
assert not os.environ.get('PERSONAL_SECRET')
assert pathlib.Path('in/value').read_text() == 'input'
try: pathlib.Path('in/value').write_text('bad')
except PermissionError: pass
else: raise AssertionError('input writable')
try: socket.create_connection(('127.0.0.1', 80), timeout=1)
except OSError: pass
else: raise AssertionError('network available')
pathlib.Path('out/value').write_text('42')
print('real Python confined')
`), 0400))
	must(t, os.WriteFile(filepath.Join(dir, "in/value"), []byte("input"), 0400))
	p, e := f.d.recordedPolicy(s, dir, tmp, false)
	must(t, e)
	// This existing installation is an explicit readonly test runtime, never prepared or modified.
	p.Read = append(p.Read, filepath.Dir(filepath.Dir(python)))
	t.Setenv("LYK_TOKEN", "secret")
	t.Setenv("PERSONAL_SECRET", "secret")
	cmd, e := p.Command(t.Context(), python, script)
	must(t, e)
	defer sandbox.CloseCommand(cmd)
	cmd.Dir = dir
	cmd.Env = recordedEnv(s, nil, tmp, c.Seed)
	out, e := cmd.CombinedOutput()
	if e != nil || string(out) != "real Python confined\n" {
		t.Fatal(e, string(out))
	}
	b, e := os.ReadFile(filepath.Join(dir, "out/value"))
	must(t, e)
	if string(b) != "42" {
		t.Fatal(string(b))
	}
}

func TestSetupTimeoutReportsFailure(t *testing.T) {
	old := recordedSetupLimit
	recordedSetupLimit = 200 * time.Millisecond
	defer func() { recordedSetupLimit = old }()
	f, _, s, _ := newRecordedFixture(t, "")
	installRecordedSetupStub(t, s, `while :; do sleep 1; done`)
	var final map[string]string
	previous := f.d.api.HTTP.Transport
	f.d.api.HTTP.Transport = coreRoundTrip(func(r *http.Request) (*http.Response, error) {
		if strings.Contains(r.URL.Path, "/setups/") && strings.HasSuffix(r.URL.Path, "/finish") {
			var body map[string]string
			json.NewDecoder(r.Body).Decode(&body)
			if body["status"] != "running" {
				final = body
			}
			return &http.Response{StatusCode: 204, Body: io.NopCloser(strings.NewReader("")), Header: http.Header{}}, nil
		}
		return previous.RoundTrip(r)
	})
	e := f.d.dispatchCoresearchSetup(t.Context(), coresearch.ClaimedSetup{WorkContext: f.work, ID: uuid.New(), Conda: []string{"numpy"}})
	if e == nil || final["status"] != "failed" || final["python_version"] != "3.13" {
		t.Fatal(e, final)
	}
}
func TestSetupPreparesReadyLanguageOverlays(t *testing.T) {
	f, _, s, _ := newRecordedFixture(t, "")
	installRecordedSetupStub(t, s, recordedSetupStub)
	for _, lang := range []string{"r", "julia/base"} {
		path := filepath.Join(s.Bases.Dir, lang)
		must(t, os.MkdirAll(path, 0700))
		must(t, os.WriteFile(filepath.Join(path, "ready"), nil, 0600))
	}
	must(t, f.d.dispatchCoresearchSetup(t.Context(), coresearch.ClaimedSetup{WorkContext: f.work, ID: uuid.New(), Conda: []string{"numpy"}}))
	for _, lang := range []string{envs.R, envs.Julia} {
		must(t, s.Ready(lang))
	}
}

func TestRunOutputsAreImmutableSnapshots(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "value")
	must(t, os.WriteFile(path, []byte("original"), 0600))
	outputs, _, e := collectRecordedOutputs(dir)
	must(t, e)
	defer closeRecordedOutputs(outputs)
	must(t, os.WriteFile(path, []byte("changed after collection"), 0600))
	snapshot, e := coresearch.OpenSnapshot(outputs[0].file, outputs[0].size, outputs[0].sha)
	must(t, e)
	defer snapshot.Close()
	b, e := io.ReadAll(snapshot)
	must(t, e)
	sum := sha256.Sum256(b)
	if string(b) != "original" || outputs[0].sha != hex.EncodeToString(sum[:]) {
		t.Fatal(string(b), outputs[0].sha)
	}
}
func TestRunUsesSavedScriptNotChatEdits(t *testing.T) {
	f, c, _, facts := newRecordedFixture(t, "echo saved")
	must(t, os.WriteFile(filepath.Join(f.dir(), c.Script.Path), []byte("echo local-edit"), 0600))
	must(t, f.d.dispatchCoresearchRun(t.Context(), c))
	if (*facts)["stdout_tail"] != "saved\n" {
		t.Fatal(*facts)
	}
}
func TestRunRejectsBadBlobBeforeExecute(t *testing.T) {
	f, c, _, facts := newRecordedFixture(t, "echo execute")
	f.mu.Lock()
	f.blobs[c.Script.SHA256] = "tampered"
	f.mu.Unlock()
	e := f.d.dispatchCoresearchRun(t.Context(), c)
	if e == nil || (*facts)["status"] != "failed" || (*facts)["stdout_tail"] != "" {
		t.Fatal(e, *facts)
	}
}

func TestRecordedOutputSnapshotsDoNotRetainDescriptors(t *testing.T) {
	dir := t.TempDir()
	for i := 0; i < 240; i++ {
		must(t, os.WriteFile(filepath.Join(dir, strconv.Itoa(i)), nil, 0600))
	}
	count := func() int {
		f, e := os.Open("/dev/fd")
		must(t, e)
		defer f.Close()
		names, e := f.Readdirnames(-1)
		must(t, e)
		return len(names)
	}
	before := count()
	outputs, _, err := collectRecordedOutputs(dir)
	must(t, err)
	defer closeRecordedOutputs(outputs)
	after := count()
	if len(outputs) != 240 || after > before+8 {
		t.Fatalf("outputs=%d retained descriptors=%d", len(outputs), after-before)
	}
}

func TestRecordedInventoryIncludesReachableRAndJulia(t *testing.T) {
	for _, lang := range []string{envs.R, envs.Julia} {
		t.Run(lang, func(t *testing.T) {
			s := envs.ForStudy(envs.NewBases(t.TempDir()), t.TempDir())
			s.ReadOnly = true
			for _, dir := range []string{filepath.Join(s.Bases.Dir, "python"), filepath.Join(s.Bases.Dir, "r"), filepath.Join(s.Bases.Dir, "julia/base"), filepath.Join(s.Dir, "python/bin"), filepath.Join(s.Dir, "r/library"), filepath.Join(s.Dir, "julia")} {
				must(t, os.MkdirAll(dir, 0700))
			}
			for _, path := range []string{filepath.Join(s.Bases.Dir, "python/ready"), filepath.Join(s.Bases.Dir, "python/lock.txt"), filepath.Join(s.Bases.Dir, "r/ready"), filepath.Join(s.Bases.Dir, "julia/base/ready"), filepath.Join(s.Dir, "python/bin/python"), filepath.Join(s.Dir, "r/Rprofile"), filepath.Join(s.Dir, "julia/Project.toml")} {
				must(t, os.WriteFile(path, nil, 0700))
			}
			version := "v1"
			s.Run = func(_ context.Context, _ []string, _ string, name string, args ...string) ([]byte, error) {
				if name == "/bin/sh" {
					return nil, nil
				}
				if lang == envs.R && strings.Contains(name, "Rscript") || lang == envs.Julia && filepath.Base(name) == "julia" {
					return []byte(version), nil
				}
				return []byte("stable"), nil
			}
			a, e := recordedEnvSHA(t.Context(), s)
			must(t, e)
			version = "v2"
			b, e := recordedEnvSHA(t.Context(), s)
			must(t, e)
			if a == b {
				t.Fatal("changed reachable language inventory did not change environment SHA")
			}
		})
	}
}

func TestRecordedInventoryRealOwnedRuntime(t *testing.T) {
	base, project := os.Getenv("LYK_CORESEARCH_INVENTORY_BASES"), os.Getenv("LYK_CORESEARCH_INVENTORY_PROJECT")
	if base == "" || project == "" {
		t.Skip("explicit task-owned runtime paths required")
	}
	if !strings.HasPrefix(base, "/tmp/lyk-test/coresearch-live-") || !strings.HasPrefix(project, base+"/coresearch/") {
		t.Fatal("requires task-owned live runtime")
	}
	f := newCoreFixture(t)
	s := envs.ForStudy(envs.NewBases(base), project)
	if os.Getenv("LYK_CORESEARCH_INVENTORY_INSTALL_JULIA") == "1" {
		must(t, s.Bases.Ensure(t.Context(), envs.Julia))
		tmp := t.TempDir()
		must(t, os.Mkdir(filepath.Join(tmp, "home"), 0700))
		policy, err := f.d.recordedPolicy(s, s.Dir, tmp, true)
		must(t, err)
		s.Run = f.d.recordedRunner(f.work, "setups", uuid.New(), s, policy, tmp, 0, nil)
		_, err = s.Environment(t.Context())
		must(t, err)
	}
	s.ReadOnly = true
	dir := t.TempDir()
	tmp := filepath.Join(dir, "tmp")
	for _, name := range []string{"tmp/home", "out", "in", "script"} {
		must(t, os.MkdirAll(filepath.Join(dir, name), 0700))
	}
	policy, err := f.d.recordedPolicy(s, dir, tmp, false)
	must(t, err)
	runner := f.d.recordedRunner(f.work, "runs", uuid.New(), s, policy, tmp, 0, nil)
	s.Run = func(ctx context.Context, env []string, dir, name string, args ...string) ([]byte, error) {
		out, err := runner(ctx, env, dir, name, args...)
		if err != nil {
			t.Logf("inventory process %s: %s", name, out)
		}
		return out, err
	}
	first, err := s.Fingerprint(t.Context())
	must(t, err)
	second, err := s.Fingerprint(t.Context())
	must(t, err)
	if first != second {
		t.Fatalf("nondeterministic live inventory %s != %s", first, second)
	}
	t.Logf("actual sandbox inventories: Python=%v R=%v Julia=%v SHA=%s", s.Bases.Ready(envs.Python), s.Bases.Ready(envs.R), s.Bases.Ready(envs.Julia), first)
}

func TestRecordedReceiptRecoveryUsesDurableOutputs(t *testing.T) {
	f, c, _, _ := newRecordedFixture(t, "echo original > out/result; echo finished")
	previous := f.d.api.HTTP.Transport
	f.d.api.HTTP.Transport = coreRoundTrip(func(r *http.Request) (*http.Response, error) {
		if strings.HasSuffix(r.URL.Path, "/finish") {
			return &http.Response{StatusCode: 503, Header: http.Header{}, Body: io.NopCloser(strings.NewReader("temporary"))}, nil
		}
		return previous.RoundTrip(r)
	})
	if e := f.d.dispatchCoresearchRun(t.Context(), c); e == nil {
		t.Fatal("expected lost finish")
	}
	if _, e := os.Stat(filepath.Join(f.d.cfg.Home, "coresearch-runs", c.ID.String())); !errors.Is(e, os.ErrNotExist) {
		t.Fatal("execution scratch survived", e)
	}
	must(t, f.d.recoverCoresearch())
	var received string
	f.d.api.HTTP.Transport = coreRoundTrip(func(r *http.Request) (*http.Response, error) {
		if strings.HasSuffix(r.URL.Path, "/finish") {
			reader, e := r.MultipartReader()
			if e != nil {
				return nil, e
			}
			for {
				part, e := reader.NextPart()
				if e == io.EOF {
					break
				}
				if e != nil {
					return nil, e
				}
				b, e := io.ReadAll(part)
				if e != nil {
					return nil, e
				}
				if part.FileName() != "" {
					received = string(b)
				}
			}
			return &http.Response{StatusCode: 204, Header: http.Header{}, Body: io.NopCloser(strings.NewReader(""))}, nil
		}
		return previous.RoundTrip(r)
	})
	must(t, f.d.flushCoresearch(t.Context()))
	if received != "original\n" {
		t.Fatalf("recovered bytes %q", received)
	}
}

func TestRecordedInventoryFailsClosedAndSortsPackages(t *testing.T) {
	f, _, s, _ := newRecordedFixture(t, ":")
	defer f.d.stopChats()
	mode := "forward"
	s.ReadOnly = true
	s.Run = func(_ context.Context, _ []string, _ string, name string, _ ...string) ([]byte, error) {
		if name == "/bin/sh" {
			return nil, nil
		}
		switch mode {
		case "failure":
			return nil, errors.New("broken inventory")
		case "oversized":
			return make([]byte, (4<<20)+1), nil
		case "reverse":
			return []byte("b==2\na==1\n"), nil
		}
		return []byte("a==1\nb==2\n"), nil
	}
	a, e := s.Fingerprint(t.Context())
	must(t, e)
	mode = "reverse"
	b, e := s.Fingerprint(t.Context())
	must(t, e)
	if a != b {
		t.Fatal("package order changes inventory")
	}
	for _, v := range []string{"failure", "oversized"} {
		mode = v
		if sha, e := s.Fingerprint(t.Context()); e == nil || sha != "" {
			t.Fatal("inventory did not fail closed", v, e)
		}
	}
}
