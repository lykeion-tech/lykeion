package envs

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"
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
