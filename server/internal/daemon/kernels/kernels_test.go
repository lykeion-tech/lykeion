package kernels

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"lykeion/server/internal/daemon/envs"
)

// With LYK_BRIDGE_STUB=1 the test binary stands in for bridge.py: it answers
// each exec with "ran <code>", handles requests concurrently so that a second
// request arriving before the first is answered shows up as an overlap, and
// for "hang" sleeps past the request's timeout and answers as a restarted
// session would.
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

func startStub(t *testing.T) *Sessions {
	t.Helper()
	s, err := Start(context.Background(), os.Args[0], append(os.Environ(), "LYK_BRIDGE_STUB=1"), t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { s.Close() })
	return s
}

func TestStartWritesTheBridge(t *testing.T) {
	dir := t.TempDir()
	s, err := Start(context.Background(), os.Args[0], append(os.Environ(), "LYK_BRIDGE_STUB=1"), dir)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	b, err := os.ReadFile(dir + "/.lyk-bridge.py")
	if err != nil || string(b) != bridgePy {
		t.Fatalf("bridge not written: %v", err)
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
	python, _, err := st.Interpreter(envs.Python)
	if err != nil {
		t.Fatal(err)
	}
	env := append(os.Environ(), st.Env()...)
	env = append(env, "PATH="+st.PathPrefix()+string(os.PathListSeparator)+os.Getenv("PATH"))
	before := kernelProcesses(t)
	s, err := Start(context.Background(), python, env, st.Dir)
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
		t.Logf("%s %q in %s: %+v", lang, code, time.Since(start).Round(time.Millisecond), r)
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
	if r := run("python", "1/0"); r.OK || !strings.Contains(r.Error, "ZeroDivisionError") || strings.Contains(r.Error, "\x1b[") {
		t.Errorf("python error: %+v", r)
	}

	old := CellTimeout
	CellTimeout = 3 * time.Second
	r := run("python", "import time; time.sleep(30)")
	CellTimeout = old
	if r.OK || r.Error != "the cell ran past its time limit; the session restarted and its variables are gone" {
		t.Errorf("timeout: %+v", r)
	}
	if r := run("python", "print(x)"); r.OK || !strings.Contains(r.Error, "NameError") {
		t.Errorf("the session kept its variables after the restart: %+v", r)
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
		if strings.Contains(line, "ipykernel_launcher") || strings.Contains(line, "IRkernel::main") || strings.Contains(line, "IJulia.run_kernel") || strings.Contains(line, ".lyk-bridge.py") {
			found[strings.Fields(line)[0]] = line
		}
	}
	return found
}
