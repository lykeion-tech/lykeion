package envs

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"
)

func TestReadOnlyStudyDoesNotRepair(t *testing.T) {
	b, _ := fakeBases(t)
	for _, p := range []string{"python/ready", "python/lock.txt"} {
		path := filepath.Join(b.Dir, p)
		os.MkdirAll(filepath.Dir(path), 0700)
		os.WriteFile(path, nil, 0600)
	}
	s := ForStudy(b, t.TempDir())
	s.ReadOnly = true
	called := false
	s.Run = func(context.Context, []string, string, string, ...string) ([]byte, error) {
		called = true
		return nil, nil
	}
	_, _, err := s.Interpreter(Python)
	if !errors.Is(err, ErrNotReady) || called {
		t.Fatal(err, called)
	}
	entries, _ := os.ReadDir(s.Dir)
	if len(entries) != 0 {
		t.Fatal(entries)
	}
}

func TestExecutionLeaseCoordinatesIndependentBases(t *testing.T) {
	root, project := t.TempDir(), t.TempDir()
	a, b := ForStudy(NewBases(root), project), ForStudy(NewBases(root), project)
	release, err := a.ExecutionLease(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	defer release()
	ctx, cancel := context.WithTimeout(t.Context(), 50*time.Millisecond)
	defer cancel()
	other, err := b.ExecutionLease(ctx)
	if err == nil {
		other()
		t.Fatal("second Bases entered shared project while first holds lease")
	}
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatal(err)
	}
	release()
	other, err = b.ExecutionLease(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	other()
}

func TestExecutionLeaseProcessHelper(t *testing.T) {
	if os.Getenv("LYK_LEASE_HELPER") != "1" {
		return
	}
	s := ForStudy(NewBases(os.Getenv("LYK_LEASE_BASES")), os.Getenv("LYK_LEASE_PROJECT"))
	release, err := s.ExecutionLease(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	defer release()
	fmt.Println("lease acquired")
	time.Sleep(30 * time.Second)
}
func TestExecutionLeaseCrossProcessCrashAndCancellation(t *testing.T) {
	bases, project := t.TempDir(), t.TempDir()
	exe, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, exe, "-test.run=^TestExecutionLeaseProcessHelper$")
	cmd.Env = append(os.Environ(), "LYK_LEASE_HELPER=1", "LYK_LEASE_BASES="+bases, "LYK_LEASE_PROJECT="+project)
	pipe, err := cmd.StdoutPipe()
	if err != nil {
		t.Fatal(err)
	}
	if err = cmd.Start(); err != nil {
		t.Fatal(err)
	}
	defer cmd.Process.Kill()
	scanner := bufio.NewScanner(pipe)
	if !scanner.Scan() || scanner.Text() != "lease acquired" {
		t.Fatal("child never acquired project lease")
	}
	s := ForStudy(NewBases(bases), project)
	wait, stop := context.WithTimeout(t.Context(), 50*time.Millisecond)
	defer stop()
	if release, e := s.ExecutionLease(wait); e == nil {
		release()
		t.Fatal("another profile entered child-owned environment")
	} else if !errors.Is(e, context.DeadlineExceeded) {
		t.Fatal(e)
	}
	if err = cmd.Process.Kill(); err != nil {
		t.Fatal(err)
	}
	_ = cmd.Wait()
	release, err := s.ExecutionLease(ctx)
	if err != nil {
		t.Fatal(err)
	}
	release()
}
