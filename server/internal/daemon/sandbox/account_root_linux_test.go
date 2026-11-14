//go:build linux

package sandbox

import (
	"bufio"
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"slices"
	"syscall"
	"testing"

	"lykeion/server/internal/accountlookup"
)

func TestAccountRootRestrictionAllowsNarrowGrants(t *testing.T) {
	selected, actual := t.TempDir(), t.TempDir()
	project := filepath.Join(actual, "project")
	secret := filepath.Join(actual, "secret")
	child := filepath.Join(secret, "child")
	for _, dir := range []string{project, child} {
		if err := os.MkdirAll(dir, 0700); err != nil {
			t.Fatal(err)
		}
	}
	for _, strict := range []bool{false, true} {
		p := Policy{Home: selected, HomeRead: !strict, StrictRead: strict, Read: []string{project}, Hidden: []string{secret}}
		p, err := p.withAccountRoot(actual)
		if err != nil {
			t.Fatal(err)
		}
		resolved, err := p.resolve()
		if err != nil {
			t.Fatal(err)
		}
		root, err := filepath.EvalSymlinks(actual)
		if err != nil {
			t.Fatal(err)
		}
		if !slices.Contains(resolved.restricted, root) {
			t.Fatal("effective account root not restricted")
		}
		p.Read = []string{child}
		if _, err = p.withAccountRoot(actual); err == nil {
			t.Fatal("read grant inside hidden credential accepted")
		}
	}
}

func TestAccountRootRestrictionRejectsNestedHomes(t *testing.T) {
	actual := t.TempDir()
	nested := filepath.Join(actual, "nested")
	if err := os.Mkdir(nested, 0700); err != nil {
		t.Fatal(err)
	}
	for _, p := range []Policy{{Home: nested}, {Home: actual}} {
		other := actual
		if p.Home == actual {
			other = nested
		}
		if _, err := p.withAccountRoot(other); err == nil {
			t.Fatal("nested selected/effective homes accepted")
		}
	}
}

func TestAccountRootRestrictionRejectsSymlinksInsideEffectiveHome(t *testing.T) {
	selected, actual := t.TempDir(), t.TempDir()
	outside := t.TempDir()
	link := filepath.Join(actual, "linked-project")
	if err := os.Symlink(outside, link); err != nil {
		t.Fatal(err)
	}
	if _, err := (Policy{Home: selected, Read: []string{link}}).withAccountRoot(actual); err == nil {
		t.Fatal("link under effective home accepted")
	}
}

func TestAccountRootRestrictionKeepsSameHome(t *testing.T) {
	actual := t.TempDir()
	alias := filepath.Join(t.TempDir(), "alias")
	if err := os.Symlink(actual, alias); err != nil {
		t.Fatal(err)
	}
	p, err := (Policy{Home: alias}).withAccountRoot(actual)
	if err != nil {
		t.Fatal(err)
	}
	if len(p.RestrictedRoots) != 0 {
		t.Fatalf("same home was restricted: %v", p.RestrictedRoots)
	}
}

func TestAccountRootRejectsBindAlias(t *testing.T) {
	if os.Getenv("LYK_ISOLATED_ACCOUNT_ROOT_TEST") != "1" {
		t.Skip("requires isolated Linux mount namespace")
	}
	if _, err := os.Stat("/.dockerenv"); err != nil || os.Geteuid() != 0 {
		t.Fatal("requires disposable root container")
	}
	actual, alias := t.TempDir(), t.TempDir()
	if err := syscall.Mount(actual, alias, "", syscall.MS_BIND, ""); err != nil {
		t.Fatalf("bind mount control: %v", err)
	}
	defer func() {
		if err := syscall.Unmount(alias, 0); err != nil {
			t.Errorf("unmount fixture: %v", err)
		}
	}()
	if _, err := (Policy{Home: alias}).withAccountRoot(actual); err == nil {
		t.Fatal("distinct bind alias accepted")
	}
}

// The effective account and fixture data exist only in a disposable container.
func TestProductionCommandRestrictsDifferentAccountHome(t *testing.T) {
	if os.Getenv("LYK_ISOLATED_ACCOUNT_ROOT_TEST") != "1" {
		t.Skip("requires isolated Linux account fixture")
	}
	if _, err := os.Stat("/.dockerenv"); err != nil || os.Geteuid() != 0 {
		t.Fatal("requires disposable root container")
	}
	account, err := accountlookup.Current()
	if err != nil || account.HomeDir != "/root" {
		t.Fatalf("unexpected isolated account: %+v %v", account, err)
	}
	for _, strict := range []bool{false, true} {
		t.Run(fmt.Sprintf("strict=%v", strict), func(t *testing.T) {
			root, err := os.MkdirTemp(account.HomeDir, ".lyk-account-root-")
			if err != nil {
				t.Fatal(err)
			}
			defer os.RemoveAll(root)
			selected := t.TempDir()
			project := filepath.Join(root, "project")
			provider := filepath.Join(root, "provider")
			for _, dir := range []string{project, provider} {
				if err := os.Mkdir(dir, 0700); err != nil {
					t.Fatal(err)
				}
			}
			ordinary := filepath.Join(root, "ordinary")
			private := filepath.Join(project, "private")
			visible := filepath.Join(project, "visible")
			late := filepath.Join(project, "late")
			for _, file := range []string{ordinary, private, visible} {
				if err := os.WriteFile(file, []byte("fixture"), 0600); err != nil {
					t.Fatal(err)
				}
			}
			if data, err := os.ReadFile(private); err != nil || string(data) != "fixture" {
				t.Fatal("outside positive control failed")
			}
			p := Policy{Home: selected, HomeRead: !strict, StrictRead: strict, Read: []string{project}, Write: []string{provider}, Hidden: []string{private, late}}
			ctx, cancel := context.WithCancel(t.Context())
			defer cancel()
			script := `/bin/cat "$1" >/dev/null || exit 71; if /bin/cat "$2" >/dev/null 2>&1; then exit 72; fi; if /bin/cat "$3" >/dev/null 2>&1; then exit 73; fi; printf own > "$4" || exit 74; echo READY; read go; if /bin/cat "$5" >/dev/null 2>&1; then exit 75; fi; echo DENIED`
			cmd, err := p.Command(ctx, "/bin/sh", "-c", script, "sh", visible, ordinary, private, filepath.Join(provider, "own"), late)
			if err != nil {
				t.Fatal(err)
			}
			cmd.Dir = "/"
			stdout, err := cmd.StdoutPipe()
			if err != nil {
				t.Fatal(err)
			}
			stdin, err := cmd.StdinPipe()
			if err != nil {
				t.Fatal(err)
			}
			if err := cmd.Start(); err != nil {
				t.Fatal(err)
			}
			done := false
			defer func() {
				if !done {
					cancel()
					_ = cmd.Wait()
				}
			}()
			reader := bufio.NewReader(stdout)
			if line, err := reader.ReadString('\n'); err != nil || line != "READY\n" {
				t.Fatalf("initial policy checks failed: %q %v", line, err)
			}
			if data, err := os.ReadFile(filepath.Join(provider, "own")); err != nil || string(data) != "own" {
				t.Fatalf("provider grant failed: %q %v", data, err)
			}
			if err := os.WriteFile(late, []byte("fixture"), 0600); err != nil {
				t.Fatal(err)
			}
			if _, err := fmt.Fprintln(stdin, "continue"); err != nil {
				t.Fatal(err)
			}
			if err := stdin.Close(); err != nil {
				t.Fatal(err)
			}
			out, err := io.ReadAll(reader)
			if err != nil {
				t.Fatal(err)
			}
			err = cmd.Wait()
			done = true
			if err != nil || string(out) != "DENIED\n" {
				t.Fatalf("late path leaked or child failed: %q %v", out, err)
			}
		})
	}
}
