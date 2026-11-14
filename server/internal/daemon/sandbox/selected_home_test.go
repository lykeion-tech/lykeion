//go:build darwin || linux

package sandbox

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"os/user"
	"path/filepath"
	"testing"

	"lykeion/server/internal/accountlookup"
)

func TestCheckSelectedHome(t *testing.T) {
	valid := t.TempDir()
	alias := filepath.Join(t.TempDir(), "alias")
	if err := os.Symlink(valid, alias); err != nil {
		t.Fatal(err)
	}
	file := filepath.Join(t.TempDir(), "file")
	if err := os.WriteFile(file, []byte("fixture"), 0600); err != nil {
		t.Fatal(err)
	}
	for _, tt := range []struct {
		name, home string
		ok         bool
	}{
		{"valid", valid, true}, {"symlink", alias, true}, {"missing", filepath.Join(t.TempDir(), "missing"), false},
		{"file", file, false}, {"unset", "", false}, {"relative", "relative", false}, {"dotdot", valid + "/../invalid", false}, {"root", "/", false},
	} {
		t.Run(tt.name, func(t *testing.T) {
			t.Setenv("HOME", tt.home)
			err := CheckSelectedHome()
			if tt.ok && err != nil {
				t.Fatal(err)
			}
			if !tt.ok && !errors.Is(err, ErrSelectedHome) {
				t.Fatalf("unexpected error: %v", err)
			}
		})
	}
}

func TestDesktopCheckRejectsInvalidHomeBeforeProbe(t *testing.T) {
	called := false
	account := func() (*user.User, error) { return &user.User{HomeDir: t.TempDir()}, nil }
	command := func(Policy, context.Context, string, ...string) (*exec.Cmd, error) {
		called = true
		return nil, errors.New("slow probe")
	}
	err := checkDesktop(context.Background(), account, func() error { return ErrSelectedHome }, command)
	if !errors.Is(err, ErrSelectedHome) || called {
		t.Fatalf("invalid home did not stop probe: %v called=%v", err, called)
	}
}

func TestDesktopCheckPreservesAccountErrorAndCancellation(t *testing.T) {
	selectedCalled := false
	probeCalled := false
	account := func() (*user.User, error) { return nil, accountlookup.ErrUnavailable }
	selected := func() error { selectedCalled = true; return ErrSelectedHome }
	command := func(Policy, context.Context, string, ...string) (*exec.Cmd, error) {
		probeCalled = true
		return nil, errors.New("unexpected")
	}
	if err := checkDesktop(context.Background(), account, selected, command); !errors.Is(err, accountlookup.ErrUnavailable) || selectedCalled || probeCalled {
		t.Fatalf("account precedence failed: %v", err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := checkDesktop(ctx, account, selected, command); !errors.Is(err, context.Canceled) || selectedCalled || probeCalled {
		t.Fatalf("cancellation failed: %v", err)
	}
}
