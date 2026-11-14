//go:build linux

package configpath

import (
	"os"
	"os/user"
	"path/filepath"
	"slices"
	"strconv"
	"testing"

	"golang.org/x/sys/unix"
)

func TestPrivateHidesEffectiveAccountCredentialsWhenHomeDiffers(t *testing.T) {
	selected := t.TempDir()
	actual := t.TempDir()
	t.Setenv("HOME", selected)
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	t.Setenv("LYK_CONFIG", filepath.Join(t.TempDir(), "config.json"))
	lookup := func(id string) (*user.User, error) {
		if id != strconv.Itoa(os.Geteuid()) {
			t.Fatalf("wrong uid %q", id)
		}
		return &user.User{Uid: id, HomeDir: actual}, nil
	}
	paths, err := private(lookup)
	if err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{".ssh", ".aws", ".gnupg", ".config/gh", ".netrc", ".docker/config.json", ".kube", ".npmrc", ".pypirc", ".git-credentials"} {
		path := filepath.Join(actual, name)
		if !slices.Contains(paths, path) {
			t.Errorf("missing effective-account secret %s", path)
		}
		if _, err := os.Lstat(path); !os.IsNotExist(err) {
			t.Errorf("unexpected filesystem mutation %s: %v", path, err)
		}
	}
}

func TestPrivateIncludesEffectiveCredentialsForSameHomeAlias(t *testing.T) {
	actual := t.TempDir()
	alias := filepath.Join(t.TempDir(), "alias")
	if err := os.Symlink(actual, alias); err != nil {
		t.Fatal(err)
	}
	t.Setenv("HOME", alias)
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	t.Setenv("LYK_CONFIG", filepath.Join(t.TempDir(), "config.json"))
	lookup := func(id string) (*user.User, error) { return &user.User{Uid: id, HomeDir: actual}, nil }
	paths, err := private(lookup)
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(actual, ".ssh")
	count := 0
	for _, p := range paths {
		if p == path {
			count++
		}
	}
	if count != 1 {
		t.Fatalf("expected one canonical credential exclusion, got %d", count)
	}
	if err := os.Remove(alias); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(t.TempDir(), alias); err != nil {
		t.Fatal(err)
	}
	if !slices.Contains(paths, path) {
		t.Fatal("effective home protection changed after selected alias retarget")
	}
}

func TestPrivateRejectsUnresolvableHomes(t *testing.T) {
	actual := t.TempDir()
	selected := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	t.Setenv("LYK_CONFIG", filepath.Join(t.TempDir(), "config.json"))
	lookup := func(id string) (*user.User, error) { return &user.User{Uid: id, HomeDir: actual}, nil }
	t.Setenv("HOME", filepath.Join(selected, "missing"))
	if paths, err := private(lookup); err == nil || paths != nil {
		t.Fatalf("missing selected home accepted: %v %v", paths, err)
	}
	file := filepath.Join(selected, "file")
	if err := os.WriteFile(file, nil, 0600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("HOME", file)
	if paths, err := private(lookup); err == nil || paths != nil {
		t.Fatalf("file selected home accepted: %v %v", paths, err)
	}
	t.Setenv("HOME", selected)
	rootAlias := filepath.Join(t.TempDir(), "root")
	if err := os.Symlink("/", rootAlias); err != nil {
		t.Fatal(err)
	}
	lookup = func(id string) (*user.User, error) { return &user.User{Uid: id, HomeDir: rootAlias}, nil }
	if paths, err := private(lookup); err == nil || paths != nil {
		t.Fatalf("root account alias accepted: %v %v", paths, err)
	}
}

// Runs only in a disposable container; unmounts normally and retains paths if uncertain.
func TestPrivateHidesEffectiveHomeThroughBindAlias(t *testing.T) {
	if os.Getenv("LYK_ISOLATED_BIND_ALIAS_TEST") != "1" {
		t.Skip("requires disposable mount namespace")
	}
	if _, err := os.Stat("/.dockerenv"); err != nil || os.Geteuid() != 0 {
		t.Fatal("requires isolated root container")
	}
	actual, err := os.MkdirTemp("", "lyk-actual-")
	if err != nil {
		t.Fatal(err)
	}
	alias, err := os.MkdirTemp("", "lyk-alias-")
	if err != nil {
		_ = os.Remove(actual)
		t.Fatal(err)
	}
	mounted := false
	defer func() {
		if mounted {
			if err := unix.Unmount(alias, 0); err != nil {
				t.Errorf("ordinary unmount failed; fixture retained: %v", err)
				return
			}
		}
		_ = os.Remove(alias)
		_ = os.Remove(actual)
	}()
	if err := unix.Mount(actual, alias, "", unix.MS_BIND, ""); err != nil {
		t.Fatal(err)
	}
	mounted = true
	a, err := os.Stat(actual)
	if err != nil {
		t.Fatal(err)
	}
	b, err := os.Stat(alias)
	if err != nil {
		t.Fatal(err)
	}
	if !os.SameFile(a, b) {
		t.Fatal("mount alias positive control did not match inode")
	}
	t.Setenv("HOME", alias)
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	t.Setenv("LYK_CONFIG", filepath.Join(t.TempDir(), "config.json"))
	lookup := func(id string) (*user.User, error) { return &user.User{Uid: id, HomeDir: actual}, nil }
	paths, err := private(lookup)
	if err != nil {
		t.Fatal(err)
	}
	if !slices.Contains(paths, filepath.Join(actual, ".ssh")) {
		t.Fatal("bind alias suppressed effective-home secret exclusion")
	}
}
