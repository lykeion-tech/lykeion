package kernels

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"lykeion/server/internal/daemon/envs"
	"lykeion/server/internal/daemon/sandbox"
)

// With LYK_BRIDGE_STUB=1 the test binary stands in for bridge.py: it answers
// each exec with "ran <code>", handles requests concurrently so that a second
// request arriving before the first is answered shows up as an overlap, and
// for "hang" sleeps past the request's timeout and answers as a restarted
// session would. "slow" answers late and outside the overlap count, "huge"
// answers with a line longer than the daemon reads, and "garbage" with a line
// that isn't JSON. A restart of "cobol" fails as an unknown language would,
// and one of "mute" fails without saying why.
func TestMain(m *testing.M) {
	if os.Getenv("LYK_BRIDGE_STUB") == "1" {
		stub()
		return
	}
	os.Exit(m.Run())
}

func stub() {
	var out sync.Mutex
	var inFlight atomic.Int32
	write := func(v any) {
		b, _ := json.Marshal(v)
		out.Lock()
		os.Stdout.Write(append(b, '\n'))
		out.Unlock()
	}
	sc := bufio.NewScanner(os.Stdin)
	for sc.Scan() {
		var req struct {
			ID       int     `json:"id"`
			Op       string  `json:"op"`
			Language string  `json:"language"`
			Code     string  `json:"code"`
			Timeout  float64 `json:"timeout"`
		}
		if json.Unmarshal(sc.Bytes(), &req) != nil || req.Op == "shutdown" {
			return
		}
		if req.Op == "restart" {
			if req.Language == "cobol" {
				write(map[string]any{"id": req.ID, "error": "RuntimeError: unknown language 'cobol'", "ok": false})
			} else if req.Language == "mute" {
				write(map[string]any{"id": req.ID, "ok": false})
			} else {
				write(map[string]any{"id": req.ID, "ok": true, "restarted": true})
			}
			continue
		}
		switch req.Code {
		case "huge":
			out.Lock()
			os.Stdout.Write([]byte(`{"id":1,"stdout":"` + strings.Repeat("x", maxLine+1) + "\"}\n"))
			out.Unlock()
			continue
		case "garbage":
			write("not json")
			continue
		case "noisy":
			fmt.Fprintln(os.Stderr, "first line")
			fmt.Fprintln(os.Stderr, strings.Repeat("y", 10<<10))
			for range 2 << 10 {
				fmt.Fprintln(os.Stderr, strings.Repeat("z", 1<<10))
			}
			write(map[string]any{"id": req.ID, "stdout": "ran noisy", "ok": true})
			continue
		case "slow":
			go func() {
				time.Sleep(300 * time.Millisecond)
				write(map[string]any{"id": req.ID, "stdout": "ran slow", "ok": true})
			}()
			continue
		}
		go func() {
			overlap := inFlight.Add(1) > 1
			defer inFlight.Add(-1)
			if req.Code == "hang" {
				time.Sleep(time.Duration(req.Timeout*float64(time.Second)) + 100*time.Millisecond)
				write(map[string]any{"id": req.ID, "stdout": "", "error": "the cell ran past its time limit; the session restarted and its variables are gone", "ok": false, "restarted": true})
				return
			}
			time.Sleep(50 * time.Millisecond)
			if overlap {
				write(map[string]any{"id": req.ID, "error": "overlap", "ok": false})
				return
			}
			write(map[string]any{"id": req.ID, "stdout": "ran " + req.Code, "ok": true})
		}()
	}
}

// plain starts the bridge as it is; the door passes its sandbox instead.
func plain(name string, args ...string) (*exec.Cmd, error) { return exec.Command(name, args...), nil }

func TestStartNeedsACommand(t *testing.T) {
	if _, err := Start(context.Background(), nil, os.Args[0], nil, t.TempDir(), ""); err == nil {
		t.Fatal("a bridge started with no command to start it")
	}
}

// What the bridge and its kernels write to stderr reaches the daemon's log
// a line at a time, marked as theirs and capped, never raw.
func TestTheBridgesStderrIsLoggedAndCapped(t *testing.T) {
	var buf safeBuffer
	defer func(l *slog.Logger) { slog.SetDefault(l) }(slog.Default())
	slog.SetDefault(slog.New(slog.NewTextHandler(&buf, nil)))
	s := startStub(t)
	if r, err := s.Exec(context.Background(), "python", "noisy"); err != nil || r.Stdout != "ran noisy" {
		t.Fatalf("%+v %v", r, err)
	}
	s.Close()
	got := buf.String()
	if !strings.Contains(got, "session bridge") || !strings.Contains(got, "first line") {
		t.Fatalf("log %q", got[:min(len(got), 500)])
	}
	if strings.Contains(got, strings.Repeat("y", 5<<10)) {
		t.Error("a long line was logged whole")
	}
	if len(got) > 1<<20+256<<10 { // 2 MiB were written
		t.Errorf("%d bytes logged", len(got))
	}
}

type safeBuffer struct {
	mu sync.Mutex
	b  strings.Builder
}

func (s *safeBuffer) Write(p []byte) (int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.b.Write(p)
}

func (s *safeBuffer) String() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.b.String()
}

func startStub(t *testing.T) *Sessions {
	t.Helper()
	s, err := Start(context.Background(), plain, os.Args[0], append(os.Environ(), "LYK_BRIDGE_STUB=1"), t.TempDir(), "")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { s.Close() })
	return s
}

// The bridge file lives outside the run folder, so the agent never sees it,
// and Python starts it with -I, so a study file named like a module the
// bridge imports cannot stand in for it. The bridge still runs in the run
// folder, and its file goes with Close.
func TestTheBridgeLivesOutsideTheRunFolder(t *testing.T) {
	dir, seen := t.TempDir(), t.TempDir()
	python := filepath.Join(seen, "python")
	os.WriteFile(python, []byte("#!/bin/sh\nprintf '%s\\n' \"$@\" > "+seen+"/args\npwd -P > "+seen+"/cwd\ncp \"$2\" "+seen+"/bridge\nexec "+os.Args[0]+"\n"), 0o755)
	s, err := Start(context.Background(), plain, python, append(os.Environ(), "LYK_BRIDGE_STUB=1"), dir, "")
	if err != nil {
		t.Fatal(err)
	}
	if r, err := s.Exec(context.Background(), "python", "x"); err != nil || r.Stdout != "ran x" {
		t.Fatalf("exec: %+v %v", r, err)
	}
	args, _ := os.ReadFile(filepath.Join(seen, "args"))
	argv := strings.Split(strings.TrimSpace(string(args)), "\n")
	if len(argv) != 2 || argv[0] != "-I" || strings.HasPrefix(argv[1], dir) {
		t.Fatalf("bridge argv: %q", argv)
	}
	b, _ := os.ReadFile(filepath.Join(seen, "bridge"))
	if string(b) != bridgePy {
		t.Fatal("the bridge file does not hold the bridge")
	}
	cwd, _ := os.ReadFile(filepath.Join(seen, "cwd"))
	if real, _ := filepath.EvalSymlinks(dir); strings.TrimSpace(string(cwd)) != real {
		t.Fatalf("bridge cwd %q, want %q", cwd, real)
	}
	if entries, _ := os.ReadDir(dir); len(entries) != 0 {
		t.Fatalf("the run folder holds %v", entries)
	}
	s.Close()
	if _, err := os.Stat(argv[1]); !os.IsNotExist(err) {
		t.Fatalf("the bridge file outlived Close: %v", err)
	}
}

func TestExecSendsCodeAndReturnsOutput(t *testing.T) {
	s := startStub(t)
	got, err := s.Exec(context.Background(), "python", "1+1")
	if err != nil {
		t.Fatal(err)
	}
	if want := (Result{Stdout: "ran 1+1", OK: true}); got != want {
		t.Fatalf("got %+v, want %+v", got, want)
	}
}

func TestCallsAreSerialised(t *testing.T) {
	s := startStub(t)
	var wg sync.WaitGroup
	results := make([]Result, 4)
	for i := range results {
		wg.Add(1)
		go func() {
			defer wg.Done()
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			r, err := s.Exec(ctx, "python", fmt.Sprint(i))
			if err != nil {
				t.Error(err)
			}
			results[i] = r
		}()
	}
	wg.Wait()
	for i, r := range results {
		if !r.OK || r.Stdout != fmt.Sprint("ran ", i) {
			t.Errorf("call %d: %+v", i, r)
		}
	}
}

func TestAHungCellTimesOutAndTheSessionRestarts(t *testing.T) {
	old := CellTimeout
	CellTimeout = 200 * time.Millisecond
	defer func() { CellTimeout = old }()
	s := startStub(t)
	r, err := s.Exec(context.Background(), "python", "hang")
	if err != nil {
		t.Fatal(err)
	}
	if r.OK || r.Error != "the cell ran past its time limit; the session restarted and its variables are gone" {
		t.Fatalf("got %+v", r)
	}
	if r, err := s.Exec(context.Background(), "python", "1"); err != nil || !r.OK {
		t.Fatalf("after the restart: %+v %v", r, err)
	}
}

func TestABridgeThatDiesIsStartedAgain(t *testing.T) {
	s := startStub(t)
	if _, err := s.Exec(context.Background(), "python", "1"); err != nil {
		t.Fatal(err)
	}
	s.b.cmd.Process.Kill()
	if _, err := s.Exec(context.Background(), "python", "2"); err == nil {
		t.Fatal("Exec on a dead bridge returned no error")
	}
	if r, err := s.Exec(context.Background(), "python", "3"); err != nil || r.Stdout != "ran 3" {
		t.Fatalf("the bridge was not started again: %+v %v", r, err)
	}
}

func TestABadReplyLineStopsTheBridgeAtOnce(t *testing.T) {
	for _, code := range []string{"huge", "garbage"} {
		t.Run(code, func(t *testing.T) {
			s := startStub(t)
			ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
			defer cancel()
			start := time.Now()
			if _, err := s.Exec(ctx, "python", code); err == nil || ctx.Err() != nil {
				t.Fatalf("Exec = %v, ctx %v", err, ctx.Err())
			}
			t.Logf("failed in %s", time.Since(start).Round(time.Millisecond))
			if r, err := s.Exec(context.Background(), "python", "1"); err != nil || r.Stdout != "ran 1" {
				t.Fatalf("the bridge was not started again: %+v %v", r, err)
			}
		})
	}
}

func TestACancelledCallsReplyIsNotTakenByTheNext(t *testing.T) {
	s := startStub(t)
	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()
	if _, err := s.Exec(ctx, "python", "slow"); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("got %v", err)
	}
	time.Sleep(400 * time.Millisecond)
	if r, err := s.Exec(context.Background(), "python", "2"); err != nil || r.Stdout != "ran 2" {
		t.Fatalf("got %+v %v", r, err)
	}
}

func TestNoBridgeStartsAfterClose(t *testing.T) {
	s := startStub(t)
	s.Close()
	if _, err := s.Exec(context.Background(), "python", "1"); err == nil {
		t.Fatal("a cell ran after Close")
	}
	s.mu.Lock()
	started := s.b != nil
	s.mu.Unlock()
	if started {
		t.Fatal("Exec started a bridge after Close")
	}
}

func TestARestartIsSentToTheBridge(t *testing.T) {
	s := startStub(t)
	if err := s.Restart(context.Background(), "python"); err != nil {
		t.Fatal(err)
	}
	if err := s.Restart(context.Background(), "cobol"); err == nil || !strings.Contains(err.Error(), "unknown language") {
		t.Fatalf("a failed restart: %v", err)
	}
	if err := s.Restart(context.Background(), "mute"); err == nil || err.Error() == "" {
		t.Fatalf("a restart that failed silently: %v", err)
	}
	if r, err := s.Exec(context.Background(), "python", "1"); err != nil || r.Stdout != "ran 1" {
		t.Fatalf("after the restart: %+v %v", r, err)
	}
}

func TestRealKernels(t *testing.T) {
	if os.Getenv("LYK_TEST_ENVS") != "1" {
		t.Skip("set LYK_TEST_ENVS=1 and LYK_TEST_ENVS_DIR to built bases to run real kernels")
	}
	b := envs.NewBases(os.Getenv("LYK_TEST_ENVS_DIR"))
	for _, lang := range []string{envs.Python, envs.R, envs.Julia} {
		if !b.Ready(lang) {
			t.Fatalf("the %s base isn't ready in LYK_TEST_ENVS_DIR", lang)
		}
	}
	st := envs.ForStudy(b, t.TempDir())
	st.Run = func(ctx context.Context, env []string, dir, name string, args ...string) ([]byte, error) {
		return b.Run(ctx, append(os.Environ(), env...), dir, name, args...)
	}
	python, _, err := st.Interpreter(envs.Python)
	if err != nil {
		t.Fatal(err)
	}
	env := append(os.Environ(), st.Env()...)
	env = append(env, "PATH="+st.PathPrefix()+string(os.PathListSeparator)+os.Getenv("PATH"))
	// A study file named like a module the bridge imports must not replace it.
	if err := os.WriteFile(filepath.Join(st.Dir, "queue.py"), []byte("raise SystemExit('the study queue.py was imported')\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	// The bridge runs in a sandbox like the door's: the bases' parent stands
	// in for the home folder they live in, and holds a key the kernels must
	// not read. Caches and the home folder go in the study's temp folder.
	// This is a smoke copy of the door's policy, since this package can't
	// import the daemon; TestRealEnvsRunInTheSandbox in package daemon runs
	// the real bases under the door's own policy.
	home := filepath.Dir(b.Dir)
	keyDir, err := os.MkdirTemp(home, "key-")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(keyDir)
	key := filepath.Join(keyDir, "id_rsa")
	if err := os.WriteFile(key, []byte("PRIVATE KEY"), 0o600); err != nil {
		t.Fatal(err)
	}
	tmp := filepath.Join(st.Dir, "tmp")
	policy := sandbox.Policy{Home: home, Write: []string{st.Dir}}
	for _, f := range b.Folders() {
		if fi, err := os.Stat(f); err == nil && fi.IsDir() {
			policy.Read = append(policy.Read, f)
		}
	}
	for _, kv := range [][2]string{{"TMPDIR", ""}, {"HOME", "home"}, {"JUPYTER_RUNTIME_DIR", "jupyter-runtime"}, {"JUPYTER_DATA_DIR", "jupyter-data"}, {"IPYTHONDIR", "ipython"}, {"MPLCONFIGDIR", "matplotlib"}, {"XDG_CACHE_HOME", "cache"}} {
		env = append(env, kv[0]+"="+filepath.Join(tmp, kv[1]))
	}
	if err := os.MkdirAll(filepath.Join(tmp, "home"), 0o700); err != nil {
		t.Fatal(err)
	}
	sandboxed := func(name string, args ...string) (*exec.Cmd, error) {
		return policy.Command(context.Background(), name, args...)
	}
	before := kernelProcesses(t)
	s, err := Start(context.Background(), sandboxed, python, env, st.Dir, tmp)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	ctx := context.Background()
	run := func(lang, code string) Result {
		t.Helper()
		start := time.Now()
		r, err := s.Exec(ctx, lang, code)
		if err != nil {
			t.Fatalf("%s %q: %v", lang, code, err)
		}
		logged := r
		if len(logged.Stdout) > 200 {
			logged.Stdout = fmt.Sprintf("%s…(%d bytes)…%s", logged.Stdout[:40], len(logged.Stdout), logged.Stdout[len(logged.Stdout)-40:])
		}
		t.Logf("%s %q in %s: %+v", lang, code, time.Since(start).Round(time.Millisecond), logged)
		return r
	}
	run("python", "x = 21")
	if r := run("python", "print(x*2)"); !r.OK || r.Stdout != "42\n" {
		t.Errorf("python: %+v", r)
	}
	if r := run("python", "import os; os.getcwd()"); !strings.Contains(r.Stdout, st.Dir[strings.LastIndex(st.Dir, "/")+1:]) {
		t.Errorf("python cwd is not the study folder: %+v", r)
	}
	if r := run("python", "import sys; print(sys.prefix)"); !strings.Contains(r.Stdout, filepath.Join(filepath.Base(st.Dir), "python")) {
		t.Errorf("the python kernel is not the study venv: %+v", r)
	}
	if r := run("r", "y <- 2; cat(y*3)"); !r.OK || r.Stdout != "6" {
		t.Errorf("r: %+v", r)
	}
	run("julia", "z = 4")
	if r := run("julia", "println(z+1)"); !r.OK || r.Stdout != "5\n" {
		t.Errorf("julia: %+v", r)
	}
	for lang, code := range map[string]string{"python": "print(open(%q).read())", "r": "cat(readLines(%q))", "julia": "print(read(%q, String))"} {
		if r := run(lang, fmt.Sprintf(code, key)); r.OK || strings.Contains(r.Stdout+r.Error, "PRIVATE KEY") {
			t.Errorf("the %s kernel read the key: %+v", lang, r)
		}
	}
	if r := run("python", "1/0"); r.OK || !strings.Contains(r.Error, "ZeroDivisionError") || strings.Contains(r.Error, "\x1b[") {
		t.Errorf("python error: %+v", r)
	}

	r := run("python", "print('x' * (33 << 20))")
	if !r.OK || len(r.Stdout) > 32<<20+100 || !strings.HasSuffix(r.Stdout, "[output cut at 32 MiB]\n") {
		t.Errorf("long output: OK %v, %d bytes, ends %q", r.OK, len(r.Stdout), r.Stdout[max(0, len(r.Stdout)-40):])
	}
	if r := run("python", "print(x*2)"); !r.OK || r.Stdout != "42\n" {
		t.Errorf("the session did not survive the long output: %+v", r)
	}
	old := CellTimeout
	CellTimeout = 3 * time.Second
	r = run("python", "import time; time.sleep(30)")
	CellTimeout = old
	if r.OK || r.Error != "the cell ran past its time limit; the session restarted and its variables are gone" {
		t.Errorf("timeout: %+v", r)
	}
	if r := run("python", "print(x)"); r.OK || !strings.Contains(r.Error, "NameError") {
		t.Errorf("the session kept its variables after the restart: %+v", r)
	}
	run("python", "x = 1")
	if err := s.Restart(ctx, "python"); err != nil {
		t.Errorf("restart: %v", err)
	}
	if r := run("python", "print(x)"); r.OK || !strings.Contains(r.Error, "NameError") {
		t.Errorf("the session kept its variables after a restart it was asked for: %+v", r)
	}
	if r := run("python", "import os; os._exit(1)"); r.OK || r.Error != "the session's kernel stopped; it restarted and its variables are gone" {
		t.Errorf("dead kernel: %+v", r)
	}
	if r := run("python", "print(7)"); !r.OK || r.Stdout != "7\n" {
		t.Errorf("after the kernel died: %+v", r)
	}
	if err := s.Close(); err != nil {
		t.Errorf("close: %v", err)
	}
	time.Sleep(time.Second)
	for pid, line := range kernelProcesses(t) {
		if _, ok := before[pid]; !ok {
			t.Errorf("a kernel outlived Close: %s", line)
		}
	}
}

// kernelProcesses lists the running Python, R and Julia kernels by pid.
func kernelProcesses(t *testing.T) map[string]string {
	out, err := exec.Command("ps", "-axo", "pid=,command=").Output()
	if err != nil {
		t.Fatal(err)
	}
	found := map[string]string{}
	for _, line := range strings.Split(string(out), "\n") {
		if strings.Contains(line, "ipykernel_launcher") || strings.Contains(line, "IRkernel::main") || strings.Contains(line, "IJulia.run_kernel") || strings.Contains(line, "lyk-bridge-") {
			found[strings.Fields(line)[0]] = line
		}
	}
	return found
}
