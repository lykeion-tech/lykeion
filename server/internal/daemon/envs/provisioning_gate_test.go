package envs

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
)

func TestUnboundedSharedProvisioningFailsClosed(t *testing.T) {
	for _, name := range []string{Python, R, Julia, "lean"} {
		t.Run(name, func(t *testing.T) {
			root := t.TempDir()
			b := NewBases(root)
			called := false
			b.Fetch = func(context.Context, string, string, string) error {
				called = true
				return errors.New("unexpected fetch")
			}
			b.Run = func(context.Context, []string, string, string, ...string) ([]byte, error) {
				called = true
				return nil, errors.New("unexpected run")
			}
			path := filepath.Join(root, name)
			if name == "lean" {
				slug, err := leanPair(testLean, "")
				if err != nil {
					t.Fatal(err)
				}
				path = filepath.Join(path, slug)
			}
			if err := os.MkdirAll(path, 0755); err != nil {
				t.Fatal(err)
			}
			sentinel := filepath.Join(path, "incomplete-user-data")
			if err := os.WriteFile(sentinel, []byte("preserve"), 0600); err != nil {
				t.Fatal(err)
			}
			var err error
			if name == "lean" {
				_, err = b.EnsureLean(context.Background(), testLean, "")
			} else {
				err = b.Ensure(context.Background(), name)
			}
			if !errors.Is(err, ErrBoundedProvisioningUnavailable) || !strings.Contains(err.Error(), "supported update") {
				t.Fatalf("expected actionable bounded provisioning error, got %v", err)
			}
			if called {
				t.Fatal("invoked a downloader or installer")
			}
			if data, err := os.ReadFile(sentinel); err != nil || string(data) != "preserve" {
				t.Fatalf("changed existing data: %q %v", data, err)
			}
		})
	}
}

func TestReadyBasesRemainUsableWithoutProvisioning(t *testing.T) {
	root := t.TempDir()
	b := NewBases(root)
	b.Fetch = func(context.Context, string, string, string) error { t.Fatal("unexpected fetch"); return nil }
	b.Run = func(context.Context, []string, string, string, ...string) ([]byte, error) {
		t.Fatal("unexpected run")
		return nil, nil
	}
	for _, name := range []string{Python, R, Julia} {
		path := b.baseDir(name)
		if err := os.MkdirAll(path, 0755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(path, "ready"), []byte("ready"), 0644); err != nil {
			t.Fatal(err)
		}
		if err := b.Ensure(context.Background(), name); err != nil {
			t.Fatalf("%s: %v", name, err)
		}
	}
	slug, err := leanPair(testLean, "")
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(root, "lean", slug)
	if err := os.MkdirAll(path, 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(path, "ready"), []byte("ready"), 0644); err != nil {
		t.Fatal(err)
	}
	got, err := b.EnsureLean(context.Background(), testLean, "")
	if err != nil || got != path {
		t.Fatalf("ready lean: %q %v", got, err)
	}
	if data, err := os.ReadFile(filepath.Join(path, "ready")); err != nil || string(data) != "ready" {
		t.Fatalf("ready lean marker changed: %q %v", data, err)
	}
}

func TestUnavailableLeanRejectsInvalidNamesAndSymlink(t *testing.T) {
	root := t.TempDir()
	b := NewBases(root)
	for _, toolchain := range []string{"../invalid", "leanprover/lean4:v4;rm"} {
		if _, err := b.EnsureLean(t.Context(), toolchain, ""); err == nil || errors.Is(err, ErrBoundedProvisioningUnavailable) {
			t.Fatalf("invalid toolchain %q: %v", toolchain, err)
		}
	}
	if err := os.Symlink(t.TempDir(), filepath.Join(root, "lean")); err != nil {
		t.Fatal(err)
	}
	if _, err := b.EnsureLean(t.Context(), testLean, ""); err == nil || errors.Is(err, ErrBoundedProvisioningUnavailable) {
		t.Fatalf("symlink root accepted: %v", err)
	}
}

func TestConcurrentUnavailableProvisioningAndCancellation(t *testing.T) {
	b := NewBases(t.TempDir())
	b.Fetch = func(context.Context, string, string, string) error { return errors.New("unexpected fetch") }
	var wg sync.WaitGroup
	for range 16 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if err := b.Ensure(context.Background(), Python); err == nil || !strings.Contains(err.Error(), "bounded") {
				t.Errorf("unexpected result: %v", err)
			}
		}()
	}
	wg.Wait()
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := b.Ensure(ctx, Python); !errors.Is(err, context.Canceled) {
		t.Fatalf("cancellation: %v", err)
	}
	if _, err := b.EnsureLean(ctx, testLean, ""); !errors.Is(err, context.Canceled) {
		t.Fatalf("lean cancellation: %v", err)
	}
}

func TestMissingBaseDoesNotPinStudyPython(t *testing.T) {
	b := NewBases(t.TempDir())
	s := ForStudy(b, t.TempDir())
	persisted := false
	s.PersistPython = func(context.Context, string) error { persisted = true; return nil }
	s.Run = func(context.Context, []string, string, string, ...string) ([]byte, error) {
		t.Fatal("missing base started a subprocess")
		return nil, nil
	}
	_, err := s.SetUpConda(t.Context(), "3.11", nil, "")
	if !errors.Is(err, ErrNotReady) {
		t.Fatalf("expected not-ready error, got %v", err)
	}
	if persisted || s.RecordedPython() != "" {
		t.Fatalf("missing base persisted Python version %q", s.RecordedPython())
	}
}
