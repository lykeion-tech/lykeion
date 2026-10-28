package kernels

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"sync/atomic"
	"syscall"
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
	interrupts := make(chan os.Signal, 1)
	signal.Notify(interrupts, syscall.SIGUSR1)
	var out sync.Mutex
	var inFlight atomic.Int32
	write := func(v any) {
		b, _ := json.Marshal(v)
		out.Lock()
		os.Stdout.Write(append(b, '\n'))
		out.Unlock()
	}
	if os.Getenv("LYK_BRIDGE_NO_READY") != "1" {
		write(map[string]any{"ready": true,"bridge_pid":os.Getpid(),"pids":[]int{}})
	}
	sc := bufio.NewScanner(os.Stdin)
	for sc.Scan() {
		var req struct {
			ID          int     `json:"id"`
			CellID      string  `json:"cell_id"`
			Kernel      string  `json:"kernel"`
			Op          string  `json:"op"`
			Language    string  `json:"language"`
			Code        string  `json:"code"`
			Timeout     float64 `json:"timeout"`
			Interrupted bool    `json:"interrupted"`
		}
		if json.Unmarshal(sc.Bytes(), &req) != nil || req.Op == "shutdown" {
			return
		}
		if req.Op == "restart_kernel" {
			write(map[string]any{"id": req.ID, "ok": true})
			continue
		}
		if req.Op == "cell" {
			write(map[string]any{"id": req.ID, "cell_ready": true})
			go func() {
				r := map[string]any{"id": req.ID, "cell_id": req.CellID, "ok": true, "execution_count": 7, "outputs": []map[string]any{}}
				outputs := []map[string]any{}
				switch {
				case req.Interrupted:
					r["ok"], r["interrupted"] = false, true
				case req.Kernel == "missing":
					r["ok"] = false
					outputs = append(outputs, map[string]any{"type": "error", "ename": "RuntimeError", "evalue": `no Jupyter kernel named "missing" is installed`})
				case strings.HasPrefix(req.Code, "ignore-term:"):
					signal.Ignore(syscall.SIGTERM)
					os.WriteFile(strings.TrimPrefix(req.Code, "ignore-term:"), []byte("ready"), 0600)
					select {}
				case strings.HasPrefix(req.Code, "wait:"):
					os.WriteFile(strings.TrimPrefix(req.Code, "wait:"), []byte("ready"), 0600)
					<-interrupts
					r["ok"], r["interrupted"] = false, true
				case req.Code == "images":
					outputs = append(outputs, map[string]any{"type": "image", "mime": "image/png", "data": []byte{137, 80, 78, 71}}, map[string]any{"type": "image", "mime": "image/svg+xml", "data": []byte("<svg/>")})
				case req.Code == "other":
					outputs = append(outputs, map[string]any{"type": "text", "text": "fallback (text/html shown as text)"})
				case req.Code == "large-text":
					outputs = append(outputs, map[string]any{"type": "stream", "name": "stdout", "text": strings.Repeat("<>&", 2<<20)})
				case req.Code == "large-image":
					outputs = append(outputs, map[string]any{"type": "image", "mime": "image/png", "data": make([]byte, 16<<20)})
				case req.Code == "wrong-id":
					r["cell_id"] = "another-cell"
				default:
					outputs = append(outputs, map[string]any{"type": "text", "text": req.Kernel + ":" + req.Code})
				}
				r["outputs"] = outputs
				write(r)
			}()
			continue
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

func TestCellKeepsImages(t *testing.T) {
	s := startStub(t)
	r, err := s.Cell(context.Background(), "python3", "images")
	if err != nil || !r.OK || r.ExecutionCount != 7 || len(r.Outputs) != 2 {
		t.Fatalf("%+v %v", r, err)
	}
	if r.Outputs[0].MIME != "image/png" || string(r.Outputs[0].Data) != string([]byte{137, 80, 78, 71}) || string(r.Outputs[1].Data) != "<svg/>" {
		t.Fatalf("%+v", r)
	}
	wire, _ := json.Marshal(r.Outputs)
	if !strings.Contains(string(wire), `"type":"image"`) || !strings.Contains(string(wire), `"data":"PHN2Zy8+"`) || strings.Contains(string(wire), `"Type"`) {
		t.Fatalf("wire: %s", wire)
	}
}

func TestCellOtherMimeAsText(t *testing.T) {
	r, err := startStub(t).Cell(context.Background(), "python3", "other")
	if err != nil || len(r.Outputs) != 1 || r.Outputs[0].Type != "text" || r.Outputs[0].Text != "fallback (text/html shown as text)" {
		t.Fatalf("%+v %v", r, err)
	}
}

func TestCellUnknownKernelIsAnErrorOutput(t *testing.T) {
	r, err := startStub(t).Cell(context.Background(), "missing", "")
	if err != nil || r.OK || len(r.Outputs) != 1 || r.Outputs[0].Type != "error" || r.Outputs[0].Evalue != `no Jupyter kernel named "missing" is installed` {
		t.Fatalf("%+v %v", r, err)
	}
}

func TestCellCutAt20MiB(t *testing.T) {
	for _, code := range []string{"large-text", "large-image"} {
		t.Run(code, func(t *testing.T) {
			r, err := startStub(t).Cell(context.Background(), "python3", code)
			if err != nil {
				t.Fatal(err)
			}
			b, err := json.Marshal(r.Outputs)
			if err != nil || len(b) > 20<<20 {
				t.Fatalf("serialized outputs = %d: %v", len(b), err)
			}
			if len(r.Outputs) == 0 || r.Outputs[len(r.Outputs)-1].Text != "(output cut at 20 MiB)" {
				t.Fatal("missing cut note")
			}
			if code == "large-image" && len(r.Outputs) != 1 {
				t.Fatal("oversized image was retained or corrupted")
			}
		})
	}
}

func TestCellServerIDRoundTrip(t *testing.T) {
	s := startStub(t)
	if _, err := s.CellWithID(context.Background(), "65be5728-a41c-42db-8e86-9c8c2c71e77b", "custom-kernel", "ok"); err != nil {
		t.Fatal(err)
	}
	if _, err := s.CellWithID(context.Background(), "65be5728-a41c-42db-8e86-9c8c2c71e77b", "custom-kernel", "wrong-id"); err == nil {
		t.Fatal("accepted mismatched server cell id")
	}
}

func runningStubCell(t *testing.T, s *Sessions) <-chan CellResult {
	t.Helper()
	ready := filepath.Join(t.TempDir(), "ready")
	done := make(chan CellResult, 1)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	t.Cleanup(cancel)
	go func() { r, _ := s.Cell(ctx, "python3", "wait:"+ready); done <- r }()
	deadline := time.Now().Add(2 * time.Second)
	for {
		if _, err := os.Stat(ready); err == nil {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("cell did not start")
		}
		time.Sleep(5 * time.Millisecond)
	}
	return done
}

func TestInterruptDoesNotWaitForTheLock(t *testing.T) {
	s := startStub(t)
	done := runningStubCell(t, s)
	interrupted := make(chan error, 1)
	go func() { interrupted <- s.Interrupt() }()
	select {
	case err := <-interrupted:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("Interrupt waited for cell lock")
	}
	select {
	case r := <-done:
		if !r.Interrupted || r.OK {
			t.Fatalf("%+v", r)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("cell did not interrupt")
	}
}

func TestRestartKernelDuringCell(t *testing.T) {
	s := startStub(t)
	done := runningStubCell(t, s)
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	if err := s.RestartKernel(ctx, "python3"); err != nil {
		t.Fatal(err)
	}
	if r := <-done; !r.Interrupted {
		t.Fatalf("%+v", r)
	}
}

func TestCloseDuringCell(t *testing.T) {
	s := startStub(t)
	runningStubCell(t, s)
	done := make(chan error, 1)
	go func() { done <- s.Close() }()
	select {
	case err := <-done:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("Close waited for cell timeout")
	}
}

func TestExecUnchanged(t *testing.T) {
	r, err := startStub(t).Exec(context.Background(), "python", "images")
	if err != nil || r != (Result{Stdout: "ran images", OK: true}) {
		t.Fatalf("%+v %v", r, err)
	}
}

// This runs the real bridge and ipykernel in the OS sandbox. The supplied
// interpreter is read-only; all specs, caches and runtime files are task-owned.
func TestRealCellPlotPNG(t *testing.T) {
	if os.Getenv("LYK_TEST_ENVS") != "1" {
		t.Skip("set LYK_TEST_ENVS=1 and LYK_TEST_CELL_PYTHON to an interpreter with jupyter_client, ipykernel and matplotlib")
	}
	python := os.Getenv("LYK_TEST_CELL_PYTHON")
	if python == "" {
		t.Skip("LYK_TEST_CELL_PYTHON is not configured")
	}
	root := t.TempDir()
	for _, name := range []string{"python3", "custom-science"} {
		dir := filepath.Join(root, "jupyter", "kernels", name)
		if err := os.MkdirAll(dir, 0700); err != nil {
			t.Fatal(err)
		}
		spec, _ := json.Marshal(map[string]any{"argv": []string{python, "-m", "ipykernel_launcher", "-f", "{connection_file}"}, "display_name": name, "language": "python", "env": map[string]string{"LYK_SPEC_SECRET": "must-not-leak", "SPEC_ALLOWED": "kept"}})
		if err := os.WriteFile(filepath.Join(dir, "kernel.json"), spec, 0600); err != nil {
			t.Fatal(err)
		}
	}
	home, _ := os.UserHomeDir()
	policy := sandbox.Policy{Home: home, Write: []string{root}, Read: []string{filepath.Dir(filepath.Dir(python))}}
	command := func(name string, args ...string) (*exec.Cmd, error) {
		return policy.Command(context.Background(), name, args...)
	}
	env := []string{"PATH=" + filepath.Dir(python) + ":/usr/bin:/bin", "HOME=" + root, "TMPDIR=" + root, "JUPYTER_PATH=" + filepath.Join(root, "jupyter"), "JUPYTER_RUNTIME_DIR=" + root, "JUPYTER_DATA_DIR=" + root, "IPYTHONDIR=" + filepath.Join(root, "ipython"), "MPLCONFIGDIR=" + filepath.Join(root, "matplotlib"), "XDG_CACHE_HOME=" + filepath.Join(root, "cache"), "LYK_PARENT_SECRET=must-not-leak"}
	s, err := Start(context.Background(), command, python, env, root, root)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	cell := func(kernel, code string) CellResult {
		t.Helper()
		r, err := s.CellWithID(ctx, "65be5728-a41c-42db-8e86-9c8c2c71e77b", kernel, code)
		if err != nil {
			t.Fatal(err)
		}
		return r
	}
	r := cell("python3", "%matplotlib inline\nimport matplotlib.pyplot as plt\nplt.plot([1,2]); plt.show()")
	images := 0
	for _, o := range r.Outputs {
		if o.Type == "image" && o.MIME == "image/png" && len(o.Data) > 8 && string(o.Data[:8]) == "\x89PNG\r\n\x1a\n" {
			images++
		}
	}
	if !r.OK || r.ExecutionCount != 1 || images != 1 {
		t.Fatalf("plot: ok=%v images=%d outputs=%+v", r.OK, images, r.Outputs)
	}
	t.Log("real sandboxed matplotlib plot produced one PNG")
	r = cell("python3", "from IPython.display import display, HTML, SVG\ndisplay(HTML('<b>hello</b>'))\ndisplay(SVG('<svg xmlns=\"http://www.w3.org/2000/svg\"/>'))")
	if !r.OK || len(r.Outputs) != 2 || r.Outputs[0].Type != "text" || !strings.Contains(r.Outputs[0].Text, "(text/html shown as text)") || r.Outputs[1].MIME != "image/svg+xml" || !strings.Contains(string(r.Outputs[1].Data), "<svg") {
		t.Fatalf("rich: %+v", r)
	}
	r = cell("python3", "import os\nassert not any(k.startswith('LYK_') for k in os.environ)\nassert os.environ['SPEC_ALLOWED'] == 'kept'\nx=42\nprint(os.getpid())")
	if !r.OK {
		t.Fatalf("environment: %+v", r)
	}
	var firstPID int
	for _, o := range r.Outputs {
		if o.Type == "stream" {
			fmt.Sscanf(o.Text, "%d", &firstPID)
		}
	}
	if firstPID == 0 {
		t.Fatal("no kernel pid")
	}
	r = cell("custom-science", "print('custom kernel ready')")
	if !r.OK {
		t.Fatalf("custom: %+v", r)
	}
	if syscall.Kill(firstPID, 0) == nil {
		t.Fatal("former named kernel survived switch")
	}
	r = cell("python3", "print(x)")
	if r.OK || len(r.Outputs) == 0 || r.Outputs[0].Ename != "NameError" {
		t.Fatalf("kernel switch kept variables: %+v", r)
	}
	r = cell("missing", "")
	if r.OK || len(r.Outputs) != 1 || r.Outputs[0].Evalue != `no Jupyter kernel named "missing" is installed` {
		t.Fatalf("unknown kernel: %+v", r)
	}
	for _, code := range []string{"print('<>&' * (4 << 20))", "from IPython.display import display\nimport base64\ndisplay({'image/png': base64.b64encode(b'x' * (16 << 20)).decode('ascii')}, raw=True)"} {
		r = cell("python3", code)
		b, _ := json.Marshal(r.Outputs)
		if len(b) > 20<<20 || len(r.Outputs) == 0 || r.Outputs[len(r.Outputs)-1].Text != "(output cut at 20 MiB)" {
			t.Fatalf("real cap: %d bytes", len(b))
		}
	}
	ready := filepath.Join(root, "ready")
	done := make(chan CellResult, 1)
	go func() {
		r, _ := s.Cell(ctx, "python3", fmt.Sprintf("import time\nopen(%q,'w').close()\ntime.sleep(60)", ready))
		done <- r
	}()
	deadline := time.Now().Add(10 * time.Second)
	for {
		if _, err := os.Stat(ready); err == nil {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("real cell did not start")
		}
		time.Sleep(10 * time.Millisecond)
	}
	if err := s.Interrupt(); err != nil {
		t.Fatal(err)
	}
	select {
	case r := <-done:
		if !r.Interrupted || r.OK {
			t.Fatalf("interrupt: %+v", r)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("real interrupt did not finish within 2 seconds")
	}
	if err := s.RestartKernel(ctx, "python3"); err != nil {
		t.Fatal(err)
	}
	r = cell("python3", "import os\nprint(os.getpid())")
	var lastPID int
	for _, o := range r.Outputs {
		if o.Type == "stream" {
			fmt.Sscanf(o.Text, "%d", &lastPID)
		}
	}
	if !r.OK || lastPID == 0 {
		t.Fatalf("restart: %+v", r)
	}
	// Submitted context cancellation retires the physical bridge before
	// another cell can run; explicit Interrupt above preserves the kernel.
	cancelReady := filepath.Join(root, "cancel-ready")
	cancelCtx, cancelCell := context.WithCancel(ctx)
	canceled := make(chan error, 1)
	go func() {
		_, err := s.Cell(cancelCtx, "python3", fmt.Sprintf("import time\nopen(%q,'w').close()\ntime.sleep(60)", cancelReady))
		canceled <- err
	}()
	deadline = time.Now().Add(10 * time.Second)
	for {
		if _, err := os.Stat(cancelReady); err == nil {
			break
		}
		if time.Now().After(deadline) {
			cancelCell()
			t.Fatal("real cancellation cell did not start")
		}
		time.Sleep(10 * time.Millisecond)
	}
	cancelCell()
	select {
	case err := <-canceled:
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("real cancellation: %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("real submitted cancellation did not finish")
	}
	if syscall.Kill(lastPID, 0) == nil {
		t.Fatal("canceled real kernel outlived its physical bridge")
	}
	r = cell("python3", "import os\nprint(os.getpid())")
	previousPID := lastPID
	lastPID = 0
	for _, o := range r.Outputs {
		if o.Type == "stream" {
			fmt.Sscanf(o.Text, "%d", &lastPID)
		}
	}
	if !r.OK || lastPID == 0 || lastPID == previousPID {
		t.Fatalf("replacement after cancellation: %+v", r)
	}
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}
	if syscall.Kill(lastPID, 0) == nil {
		t.Fatal("named kernel survived Close")
	}
	// A native ipykernel outside JUPYTER_PATH must not be discovered implicitly.
	empty := filepath.Join(root, "empty")
	if err := os.MkdirAll(empty, 0700); err != nil {
		t.Fatal(err)
	}
	env = append(env, "JUPYTER_PATH="+empty)
	other, err := Start(ctx, command, python, env, root, root)
	if err != nil {
		t.Fatal(err)
	}
	defer other.Close()
	r, err = other.Cell(ctx, "python3", "print('unlisted native kernel')")
	if err != nil || r.OK || len(r.Outputs) != 1 || r.Outputs[0].Evalue != `no Jupyter kernel named "python3" is installed` {
		t.Fatalf("native kernel escaped JUPYTER_PATH boundary: %+v %v", r, err)
	}
}

func TestCloseStopsAnUnresponsiveBridge(t *testing.T) {
	s := startStub(t)
	ready := filepath.Join(t.TempDir(), "ready")
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go s.Cell(ctx, "python3", "ignore-term:"+ready)
	deadline := time.Now().Add(2 * time.Second)
	for {
		if _, err := os.Stat(ready); err == nil {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("cell did not start")
		}
		time.Sleep(5 * time.Millisecond)
	}
	done := make(chan error, 1)
	go func() { done <- s.Close() }()
	select {
	case err := <-done:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(7 * time.Second):
		s.process.Load().Kill()
		<-done
		t.Fatal("Close never escalated a stuck active bridge")
	}
}

// The wrapper blocks before importing the embedded bridge. Pipes, rather than
// startup sleeps, establish that signals arrive before its handlers exist.
func TestCellControlDuringBridgeInitialization(t *testing.T) {
	python, err := exec.LookPath("python3")
	if err != nil {
		t.Skip("python3 is needed to exercise POSIX startup signals")
	}
	for _, replacement := range []bool{false, true} {
		for _, action := range []string{"interrupt", "restart", "cancel", "interrupt-cancel"} {
			t.Run(fmt.Sprintf("replacement=%v/%s", replacement, action), func(t *testing.T) {
				type gate struct{ ready, release *os.File }
				gates := make(chan gate, 2)
				command := func(_ string, args ...string) (*exec.Cmd, error) {
					readyR, readyW, e := os.Pipe()
					if e != nil {
						return nil, e
					}
					releaseR, releaseW, e := os.Pipe()
					if e != nil {
						return nil, e
					}
					for _, f := range []*os.File{readyR, readyW, releaseR, releaseW} {
						t.Cleanup(func() { f.Close() })
					}
					cmd := exec.Command(python, "-I", "-c", initializingBridgePython, args[len(args)-1])
					cmd.ExtraFiles = []*os.File{readyW, releaseR}
					gates <- gate{readyR, releaseW}
					return cmd, nil
				}
				s, err := Start(context.Background(), command, python, []string{"JUPYTER_PATH=/unused"}, t.TempDir(), "")
				if err != nil {
					t.Fatal(err)
				}
				t.Cleanup(func() { s.Close() })
				awaitGate := func() gate {
					t.Helper()
					g := <-gates
					started := make(chan error, 1)
					go func() { var b [1]byte; _, e := io.ReadFull(g.ready, b[:]); started <- e }()
					select {
					case e := <-started:
						if e != nil {
							t.Fatal(e)
						}
					case <-time.After(3 * time.Second):
						t.Fatal("wrapper did not reach initialization gate")
					}
					return g
				}
				g := awaitGate()
				if replacement {
					g.release.Write([]byte{1})
					if _, err := s.Exec(context.Background(), "python", "warmup"); err != nil {
						t.Fatal(err)
					}
					s.process.Load().Kill()
					<-s.b.done
					if _, err := s.Exec(context.Background(), "python", "dead"); err == nil {
						t.Fatal("expected failure clearing dead bridge")
					}
				}
				ctx, cancel := context.WithCancel(context.Background())
				defer cancel()
				type answer struct {
					r   CellResult
					err error
				}
				done := make(chan answer, 1)
				go func() { r, e := s.Cell(ctx, "test", "work"); done <- answer{r, e} }()
				if replacement {
					g = awaitGate()
				}
				deadline := time.Now().Add(3 * time.Second)
				for s.cell.Load() == nil {
					if time.Now().After(deadline) {
						t.Fatal("cell did not acquire call lock")
					}
					runtime.Gosched()
				}
				var restarted chan error
				switch action {
				case "interrupt":
					if err := s.Interrupt(); err != nil {
						t.Fatal(err)
					}
				case "restart":
					restarted = make(chan error, 1)
					restartCtx, stop := context.WithTimeout(context.Background(), 5*time.Second)
					defer stop()
					go func() { restarted <- s.RestartKernel(restartCtx, "test") }()
					// Wait for RestartKernel itself to queue its stop before
					// releasing initialization; no scheduling sleep is needed.
					c := s.cell.Load()
					for {
						c.mu.Lock()
						pending := c.pending
						c.mu.Unlock()
						if pending {
							break
						}
						if time.Now().After(deadline) {
							t.Fatal("restart did not queue a startup interrupt")
						}
						runtime.Gosched()
					}
				case "cancel", "interrupt-cancel":
					if action == "interrupt-cancel" {
						if err := s.Interrupt(); err != nil {
							t.Fatal(err)
						}
					}
					cancel()
					select {
					case a := <-done:
						if !errors.Is(a.err, context.Canceled) {
							t.Fatalf("cancel: %v", a.err)
						}
					case <-time.After(2 * time.Second):
						t.Fatal("cancel blocked during initialization")
					}
				}
				g.release.Write([]byte{1})
				if action != "cancel" && action != "interrupt-cancel" {
					select {
					case a := <-done:
						if a.err != nil || !a.r.Interrupted || a.r.OK {
							t.Fatalf("startup cell: %+v %v", a.r, a.err)
						}
					case <-time.After(5 * time.Second):
						t.Fatal("pending interrupt was lost")
					}
				}
				if restarted != nil {
					select {
					case e := <-restarted:
						if e != nil {
							t.Fatalf("early restart: %v", e)
						}
					case <-time.After(5 * time.Second):
						t.Fatal("restart blocked")
					}
				}
				r, e := s.Cell(context.Background(), "test", "after")
				if e != nil || !r.OK {
					t.Fatalf("bridge did not survive startup control: %+v %v", r, e)
				}
			})
		}
	}
}

const initializingBridgePython = `
import os, sys, types, runpy
os.write(3, b"1")
os.close(3)
os.read(4, 1)
os.close(4)
# Only kernel management is replaced. The real bridge parsing, handlers,
# startup protocol, pending interruption and output loop run unchanged.
class Client:
    def start_channels(self): pass
    def stop_channels(self): pass
    def wait_for_ready(self, timeout): pass
    def execute(self, code, allow_stdin): return "request"
    def get_iopub_msg(self, timeout): return {"parent_header":{"msg_id":"request"},"msg_type":"status","content":{"execution_state":"idle"}}
    def get_shell_msg(self, timeout): return {"parent_header":{"msg_id":"request"},"content":{"status":"ok"}}
class Manager:
    def __init__(self, **kw): self.kernel_name=kw["kernel_name"]; self.kernel_spec=types.SimpleNamespace(argv=[sys.executable],env={})
    def start_kernel(self, **kw): pass
    def client(self): return Client()
    def interrupt_kernel(self): pass
    def shutdown_kernel(self, **kw): pass
    def is_alive(self): return True
class Specs:
    def __init__(self, **kw): pass
    def find_kernel_specs(self): return {"test":"/unused"}
j=types.ModuleType("jupyter_client")
j.KernelManager=Manager
k=types.ModuleType("jupyter_client.kernelspec")
k.KernelSpecManager=Specs
sys.modules["jupyter_client"]=j
sys.modules["jupyter_client.kernelspec"]=k
runpy.run_path(sys.argv[1],run_name="__main__")
`

func TestLegacyExecDoesNotRequireReadiness(t *testing.T) {
	s, err := Start(context.Background(), plain, os.Args[0], append(os.Environ(), "LYK_BRIDGE_STUB=1", "LYK_BRIDGE_NO_READY=1"), t.TempDir(), "")
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	r, err := s.Exec(ctx, "python", "legacy")
	if err != nil || r != (Result{Stdout: "ran legacy", OK: true}) {
		t.Fatalf("%+v %v", r, err)
	}
	if err := s.Restart(ctx, "python"); err != nil {
		t.Fatal(err)
	}
}

func TestInterruptAfterCompletedCellDoesNotStopNextCell(t *testing.T) {
	python, err := exec.LookPath("python3")
	if err != nil {
		t.Skip("python3 is needed for the bridge protocol test")
	}
	script := strings.Replace(initializingBridgePython, "os.write(3, b\"1\")\nos.close(3)\nos.read(4, 1)\nos.close(4)", "", 1)
	command := func(_ string, args ...string) (*exec.Cmd, error) {
		return exec.Command(python, "-I", "-c", script, args[len(args)-1]), nil
	}
	s, err := Start(context.Background(), command, python, []string{"JUPYTER_PATH=/unused"}, t.TempDir(), "")
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if r, e := s.Cell(ctx, "test", "first"); e != nil || !r.OK {
		t.Fatalf("first cell: %+v %v", r, e)
	}
	if err := s.Interrupt(); err != nil {
		t.Fatal(err)
	}
	if r, e := s.Cell(ctx, "test", "second"); e != nil || !r.OK || r.Interrupted {
		t.Fatalf("idle stop leaked to next cell: %+v %v", r, e)
	}
}

func TestInterruptAfterExecutionBeforeReplyDoesNotStopNextCell(t *testing.T) {
	python, err := exec.LookPath("python3")
	if err != nil {
		t.Skip("python3 is needed for the bridge protocol test")
	}
	pipe := func() (*os.File, *os.File) {
		t.Helper()
		r, w, e := os.Pipe()
		if e != nil {
			t.Fatal(e)
		}
		t.Cleanup(func() { r.Close(); w.Close() })
		return r, w
	}
	completedR, completedW := pipe()
	releaseR, releaseW := pipe()
	signalR, signalW := pipe()
	script := strings.Replace(initializingBridgePython, "os.write(3, b\"1\")\nos.close(3)\nos.read(4, 1)\nos.close(4)", "", 1)
	script = strings.Replace(script, `runpy.run_path(sys.argv[1],run_name="__main__")`, completionBoundaryPython, 1)
	command := func(_ string, args ...string) (*exec.Cmd, error) {
		cmd := exec.Command(python, "-I", "-c", script, args[len(args)-1])
		cmd.ExtraFiles = []*os.File{completedW, releaseR, signalW}
		return cmd, nil
	}
	s, err := Start(context.Background(), command, python, []string{"JUPYTER_PATH=/unused"}, t.TempDir(), "")
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	done := make(chan error, 1)
	go func() {
		r, e := s.Cell(ctx, "test", "first")
		if e == nil && !r.OK {
			e = fmt.Errorf("first cell: %+v", r)
		}
		done <- e
	}()
	readByte := func(r *os.File) {
		t.Helper()
		done := make(chan error, 1)
		go func() { var b [1]byte; _, e := io.ReadFull(r, b[:]); done <- e }()
		select {
		case e := <-done:
			if e != nil {
				t.Fatal(e)
			}
		case <-ctx.Done():
			t.Fatal("bridge did not reach the expected pipe boundary")
		}
	}
	readByte(completedR)
	if err := s.Interrupt(); err != nil {
		t.Fatal(err)
	}
	// The actual handler has consumed the signal while the completed cell's
	// reply is withheld. Only then may this bridge accept the next cell.
	readByte(signalR)
	if _, err := releaseW.Write([]byte{1}); err != nil {
		t.Fatal(err)
	}
	if err := <-done; err != nil {
		t.Fatal(err)
	}
	if r, e := s.Cell(ctx, "test", "second"); e != nil || !r.OK || r.Interrupted {
		t.Fatalf("completion-boundary stop leaked to next cell: %+v %v", r, e)
	}
}

const completionBoundaryPython = `
g = runpy.run_path(sys.argv[1], run_name="bridge_under_test")
shared = g["run_cell"].__globals__
original_run = g["run_cell"]
original_interrupt = g["interrupt_cell"]
def boundary_run(name, code, timeout):
    result = original_run(name, code, timeout)
    if code == "first":
        os.write(3, b"1")
        os.read(4, 1)
    return result
def observe_interrupt(*args):
    original_interrupt(*args)
    os.write(5, b"1")
shared["run_cell"] = boundary_run
shared["interrupt_cell"] = observe_interrupt
shared["main"]()
`

func TestSubmittedCancellationDrainsOldBridgeBeforeReplacement(t *testing.T) {
	for _, queuedStop := range []bool{false, true} {
		t.Run(fmt.Sprintf("queued-stop=%v", queuedStop), func(t *testing.T) { submittedCancellationDrainsOldBridge(t, queuedStop) })
	}
}

func submittedCancellationDrainsOldBridge(t *testing.T, queuedStop bool) {
	python, err := exec.LookPath("python3")
	if err != nil {
		t.Skip("python3 is needed for the bridge protocol test")
	}
	completedR, completedW, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	defer completedR.Close()
	defer completedW.Close()
	base := strings.Replace(initializingBridgePython, "os.write(3, b\"1\")\nos.close(3)\nos.read(4, 1)\nos.close(4)", "", 1)
	delayed := strings.Replace(base, `runpy.run_path(sys.argv[1],run_name="__main__")`, delayedCancellationAckPython, 1)
	starts := 0
	command := func(_ string, args ...string) (*exec.Cmd, error) {
		script := base
		if starts == 0 {
			script = delayed
		}
		starts++
		cmd := exec.Command(python, "-I", "-c", script, args[len(args)-1])
		if starts == 1 {
			cmd.ExtraFiles = []*os.File{completedW}
		}
		return cmd, nil
	}
	s, err := Start(context.Background(), command, python, []string{"JUPYTER_PATH=/unused"}, t.TempDir(), "")
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	old := s.b
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan error, 1)
	go func() { _, err := s.Cell(ctx, "test", "first"); done <- err }()
	submitted := make(chan error, 1)
	go func() { var b [1]byte; _, err := io.ReadFull(completedR, b[:]); submitted <- err }()
	select {
	case err := <-submitted:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("cell was not physically submitted")
	}
	control := s.cell.Load()
	if control == nil {
		t.Fatal("submitted cell lost its control before a reply")
	}
	control.mu.Lock()
	ready := control.ready
	control.mu.Unlock()
	if ready {
		t.Fatal("test did not withhold the old cell acknowledgement")
	}
	if queuedStop {
		if err := s.Interrupt(); err != nil {
			t.Fatal(err)
		}
		control.mu.Lock()
		pending := control.pending
		control.mu.Unlock()
		if !pending {
			t.Fatal("old acknowledgement has no pending stop to deliver")
		}
	}
	cancel()
	select {
	case err := <-done:
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("cancel: %v", err)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("submitted cancellation did not retire the cooperative bridge")
	}
	// The cancellation result is a synchronization point: later cells may now
	// enter, so the old process and reader must already be fully finished.
	select {
	case <-old.done:
	default:
		t.Fatal("submitted cancellation released the bridge before its physical execution boundary")
	}
	control.mu.Lock()
	ready, finished := control.ready, control.finished
	control.mu.Unlock()
	if !ready || !finished {
		t.Fatal("the delayed old acknowledgement and final reply were not drained")
	}
	nextCtx, stop := context.WithTimeout(context.Background(), 3*time.Second)
	defer stop()
	r, err := s.Cell(nextCtx, "test", "second")
	if err != nil || !r.OK || r.Interrupted {
		t.Fatalf("old acknowledgement stopped replacement cell: %+v %v", r, err)
	}
	if starts != 2 || s.b == old || s.b.cmd.Process.Pid == old.cmd.Process.Pid {
		t.Fatal("next cell reused the canceled physical bridge")
	}
}

const delayedCancellationAckPython = `
g = runpy.run_path(sys.argv[1], run_name="bridge_under_test")
shared = g["run_cell"].__globals__
sink = shared["replies"]
original_shutdown = shared["shutdown"]
class DelayedReplies:
    def __init__(self): self.held=[]
    def write(self, text):
        frame = __import__("json").loads(text)
        if frame.get("id") == 1 and (frame.get("cell_ready") or "outputs" in frame):
            self.held.append(text)
            if "outputs" in frame: os.write(3, b"1")
            return len(text)
        return sink.write(text)
    def flush(self): sink.flush()
writer = DelayedReplies()
def release_on_shutdown(*args):
    held, writer.held = writer.held, []
    for text in held: sink.write(text)
    sink.flush()
    original_shutdown(*args)
shared["replies"] = writer
shared["shutdown"] = release_on_shutdown
shared["main"]()
`
