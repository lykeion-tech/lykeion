package kernels

import (
	"context"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"testing"
	"time"

	"lykeion/server/internal/daemon/sandbox"
)

func TestRealNamespaceKernelPIDsAndInterrupt(t *testing.T) {
	if os.Getenv("LYK_TEST_NAMESPACE_KERNEL") != "1" {
		t.Skip("requires disposable Linux bwrap/Jupyter fixture")
	}
	dir := t.TempDir()
	home, _ := os.UserHomeDir()
	policy := sandbox.Policy{Home: home, HomeRead: true, Write: []string{dir}}
	s, e := Start(t.Context(), func(name string, args ...string) (*exec.Cmd, error) {
		return policy.Command(context.Background(), name, args...)
	}, "/usr/bin/python3", append(os.Environ(), "JUPYTER_PATH=/usr/share/jupyter"), dir, dir)
	if e != nil {
		t.Fatal(e)
	}
	defer s.Close()
	r, e := s.Cell(t.Context(), "python3", "import os; saved = 42; print(os.getpid())")
	if e != nil || !r.OK {
		t.Fatalf("%+v %v", r, e)
	}
	inner := 0
	for _, o := range r.Outputs {
		if o.Type == "stream" {
			inner, _ = strconv.Atoi(strings.TrimSpace(o.Text))
		}
	}
	if name := s.KernelName(); name != "python3" {
		t.Fatalf("actual kernel name %q", name)
	}
	pids := s.PIDs()
	if len(pids) != 1 || inner == 0 || pids[0] == inner {
		t.Fatalf("untranslated PIDs: %v inner%d", pids, inner)
	}
	stat, e := os.ReadFile("/proc/" + strconv.Itoa(pids[0]) + "/status")
	if e != nil || !strings.Contains(string(stat), "NSpid:") {
		t.Fatalf("host identity %v", e)
	}
	done := make(chan CellResult, 1)
	errs := make(chan error, 1)
	go func() { r, e := s.Cell(t.Context(), "python3", "import time; time.sleep(30)"); done <- r; errs <- e }()
	time.Sleep(300 * time.Millisecond)
	busy := s.PIDs()
	if len(busy) != 1 || busy[0] != pids[0] {
		t.Fatalf("busy PID publication %v", busy)
	}
	if e := s.Interrupt(); e != nil {
		t.Fatal(e)
	}
	select {
	case r := <-done:
		if e := <-errs; e != nil || !r.Interrupted {
			t.Fatalf("interrupt %+v %v", r, e)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("interrupt did not reach Python")
	}
	r, e = s.Cell(t.Context(), "python3", "print(saved)")
	if e != nil || !r.OK {
		t.Fatalf("variables lost: %+v %v", r, e)
	}
	if e = s.Close(); e != nil {
		t.Fatal(e)
	}
	if len(s.PIDs()) != 0 {
		t.Fatal("closed kernels retain PIDs")
	}
	if _, e = os.Stat("/proc/" + strconv.Itoa(pids[0])); !os.IsNotExist(e) {
		t.Fatalf("kernel survived close: %v", e)
	}
}

func TestRealDefaultKernelName(t *testing.T) {
	if os.Getenv("LYK_TEST_NAMESPACE_KERNEL") != "1" {
		t.Skip("requires disposable Linux bwrap/Jupyter fixture")
	}
	dir := t.TempDir()
	home, _ := os.UserHomeDir()
	policy := sandbox.Policy{Home: home, HomeRead: true, Write: []string{dir}}
	s, e := Start(t.Context(), func(name string, args ...string) (*exec.Cmd, error) {
		return policy.Command(context.Background(), name, args...)
	}, "/usr/bin/python3", append(os.Environ(), "JUPYTER_PATH=/usr/share/jupyter"), dir, dir)
	if e != nil {
		t.Fatal(e)
	}
	defer s.Close()
	r, e := s.Exec(t.Context(), "python", "print(1)")
	if e != nil || !r.OK {
		t.Fatalf("default kernel %+v %v", r, e)
	}
	if name := s.KernelName(); name != "python3" {
		t.Fatalf("default language was not resolved: %q", name)
	}
	s.Close()
	if name := s.KernelName(); name != "" {
		t.Fatalf("stopped kernel name: %q", name)
	}
}
