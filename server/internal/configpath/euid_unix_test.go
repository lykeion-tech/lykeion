//go:build darwin || linux

package configpath

import (
	"errors"
	"os"
	"os/user"
	"path/filepath"
	"runtime"
	"slices"
	"strconv"
	"strings"
	"testing"
)

func TestPrivateIncludesEffectiveUIDProfileRootWithForgedEnvironment(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	t.Setenv("LYK_CONFIG", filepath.Join(t.TempDir(), "custom.json"))
	accountHome := t.TempDir()
	lookup := func(id string) (*user.User, error) {
		if id != strconv.Itoa(os.Geteuid()) {
			t.Fatalf("looked up UID %q", id)
		}
		return &user.User{Uid: id, HomeDir: accountHome}, nil
	}
	paths, err := private(lookup)
	if err != nil {
		t.Fatal(err)
	}
	configDir := filepath.Join(accountHome, ".config", "lyk")
	expected := []string{filepath.Join(configDir, "config.json"), filepath.Join(configDir, "profiles")}
	if runtime.GOOS == "darwin" {
		configDir = filepath.Join(accountHome, "Library", "Application Support", "lyk")
		expected = append(expected, filepath.Join(configDir, "config.json"), filepath.Join(configDir, "profiles"))
	}
	for _, path := range expected {
		if !slices.Contains(paths, path) {
			t.Fatalf("effective UID private path %s omitted: %v", path, paths)
		}
		if _, err := os.Stat(path); !os.IsNotExist(err) {
			t.Fatalf("path discovery changed an absent path: %v", err)
		}
	}
	for _, path := range []string{filepath.Join(os.Getenv("HOME"), ".config", "lyk", "profiles"), filepath.Join(os.Getenv("XDG_CONFIG_HOME"), "lyk", "profiles"), os.Getenv("LYK_CONFIG")} {
		if !slices.Contains(paths, path) {
			t.Errorf("existing protected root omitted: %s", path)
		}
	}
}

func TestPrivateFailsClosedWhenEffectiveUIDLookupUnavailable(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	t.Setenv("LYK_CONFIG", filepath.Join(t.TempDir(), "custom.json"))
	failed := func(string) (*user.User, error) { return nil, errors.New("unavailable") }
	if paths, err := private(failed); err == nil || paths != nil || !strings.Contains(err.Error(), "resolve your user account") {
		t.Fatalf("unavailable account must fail closed with actionable error: %v, %v", paths, err)
	}
	id := strconv.Itoa(os.Geteuid())
	for name, account := range map[string]*user.User{
		"nil":       nil,
		"wrong uid": {Uid: "not-" + id, HomeDir: t.TempDir()},
		"relative":  {Uid: id, HomeDir: "relative"},
		"root":      {Uid: id, HomeDir: "/"},
		"unclean":   {Uid: id, HomeDir: "/tmp/../tmp"},
		"control":   {Uid: id, HomeDir: "/tmp/bad\nhome"},
	} {
		t.Run(name, func(t *testing.T) {
			got, err := private(func(string) (*user.User, error) { return account, nil })
			if err == nil || got != nil || !strings.Contains(err.Error(), "resolve your user account") {
				t.Fatalf("invalid account must fail closed with actionable error: %v, %v", got, err)
			}
		})
	}
}
