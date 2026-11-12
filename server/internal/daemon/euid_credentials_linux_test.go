//go:build linux

package daemon

import (
	"bufio"
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"lykeion/server/internal/accountlookup"
	"lykeion/server/pkg/agent"
)

// This fixture may create conventional credential names only inside a
// disposable root-owned container, never in a developer's account home.
func TestMismatchedHomeHidesEffectiveAccountCredentials(t *testing.T) {
	if os.Getenv("LYK_ISOLATED_CREDENTIAL_TEST") != "1" {
		t.Skip("requires disposable isolated container")
	}
	if _, err := os.Stat("/.dockerenv"); err != nil || os.Geteuid() != 0 {
		t.Fatal("fixture requires isolated root container")
	}
	account, err := accountlookup.Current()
	if err != nil || account.HomeDir != "/root" {
		t.Fatalf("unexpected isolated account: %+v %v", account, err)
	}
	ssh := filepath.Join(account.HomeDir, ".ssh")
	late := filepath.Join(account.HomeDir, ".netrc")
	if err := os.Mkdir(ssh, 0700); err != nil {
		t.Fatalf("refusing to touch existing credential directory: %v", err)
	}
	defer os.Remove(ssh)
	secret := filepath.Join(ssh, "id_fixture")
	f, err := os.OpenFile(secret, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = f.WriteString("fixture"); err != nil {
		f.Close()
		t.Fatal(err)
	}
	if err = f.Close(); err != nil {
		t.Fatal(err)
	}
	defer os.Remove(secret)
	if _, err := os.Lstat(late); !os.IsNotExist(err) {
		t.Fatalf("refusing to touch existing credential file: %v", err)
	}
	lateCreated := false
	defer func() {
		if lateCreated {
			_ = os.Remove(late)
		}
	}()
	if data, err := os.ReadFile(secret); err != nil || string(data) != "fixture" {
		t.Fatal("unsandboxed read control failed")
	}
	selected := t.TempDir()
	t.Setenv("HOME", selected)
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	t.Setenv("LYK_CONFIG", filepath.Join(t.TempDir(), "config.json"))
	run := t.TempDir()
	provider := t.TempDir()
	d := &daemon{cfg: Config{Home: t.TempDir(), StateDir: t.TempDir()}}
	p, err := d.cliPolicy(run, nil, false, agent.CLIPaths{Write: []string{provider}})
	if err != nil {
		t.Fatal(err)
	}
	if !slices.Contains(p.Hidden, ssh) || !slices.Contains(p.Hidden, late) {
		t.Fatalf("effective-account credential paths missing: %v", p.Hidden)
	}
	control := p
	control.Hidden = slices.DeleteFunc(slices.Clone(p.Hidden), func(path string) bool { return path == ssh || path == late })
	visible, err := control.Command(t.Context(), "/bin/sh", "-c", `/bin/cat "$1"`, "sh", secret)
	if err != nil {
		t.Fatal(err)
	}
	visible.Dir = run
	data, err := visible.CombinedOutput()
	if err != nil || string(data) != "fixture" {
		t.Fatalf("sandbox positive control could not read fixture: %q %v", data, err)
	}
	for _, unsafePath := range []string{account.HomeDir, ssh} {
		unsafe := p
		unsafe.Write = append(slices.Clone(p.Write), unsafePath)
		attempt, err := unsafe.Command(t.Context(), "/bin/sh", "-c", `printf changed > "$1"`, "sh", secret)
		if err == nil {
			attempt.Dir = run
			if out, err := attempt.CombinedOutput(); err == nil {
				t.Fatalf("writable credential ancestor %s permitted: %s", unsafePath, out)
			}
		}
		if data, err := os.ReadFile(secret); err != nil || string(data) != "fixture" {
			t.Fatalf("credential changed via %s: %q %v", unsafePath, data, err)
		}
	}
	providerCheck, err := p.Command(t.Context(), "/bin/sh", "-c", `printf allowed > "$1"`, "sh", filepath.Join(provider, "fixture"))
	if err != nil {
		t.Fatal(err)
	}
	providerCheck.Dir = run
	if out, err := providerCheck.CombinedOutput(); err != nil {
		t.Fatalf("provider grant failed: %s %v", out, err)
	}
	if data, err := os.ReadFile(filepath.Join(provider, "fixture")); err != nil || string(data) != "allowed" {
		t.Fatalf("provider fixture: %q %v", data, err)
	}
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	cmd, err := p.Command(ctx, "/bin/sh", "-c", `if /bin/cat "$1" >/dev/null 2>&1; then echo EXPOSED; exit 1; fi; echo READY; read go; if /bin/cat "$2" >/dev/null 2>&1; then echo EXPOSED; exit 1; fi; echo DENIED`, "sh", secret, late)
	if err != nil {
		t.Fatal(err)
	}
	cmd.Dir = run
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		t.Fatal(err)
	}
	stdin, err := cmd.StdinPipe()
	if err != nil {
		t.Fatal(err)
	}
	if err = cmd.Start(); err != nil {
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
		t.Fatalf("existing credential leaked or child failed: %q %v", line, err)
	}
	f, err = os.OpenFile(late, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600)
	if err != nil {
		t.Fatal(err)
	}
	lateCreated = true
	if _, err = f.WriteString("fixture"); err != nil {
		f.Close()
		t.Fatal(err)
	}
	if err = f.Close(); err != nil {
		t.Fatal(err)
	}
	if _, err = fmt.Fprintln(stdin, "continue"); err != nil {
		t.Fatal(err)
	}
	if err = stdin.Close(); err != nil {
		t.Fatal(err)
	}
	out, err := io.ReadAll(reader)
	if err != nil {
		t.Fatal(err)
	}
	err = cmd.Wait()
	done = true
	if err != nil || strings.TrimSpace(string(out)) != "DENIED" {
		t.Fatalf("late credential leaked or child failed: %q %v", out, err)
	}

	// Retarget a selected HOME alias after policy assembly; the canonical
	// effective-account secret exclusions must remain bound to the account.
	alias := filepath.Join(t.TempDir(), "selected-home")
	if err := os.Symlink(account.HomeDir, alias); err != nil {
		t.Fatal(err)
	}
	t.Setenv("HOME", alias)
	retargeted, err := d.cliPolicy(run, nil, false, agent.CLIPaths{})
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(alias); err != nil {
		t.Fatal(err)
	}
	fakeTarget := t.TempDir()
	if err := os.Mkdir(filepath.Join(fakeTarget, ".lyk"), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(fakeTarget, alias); err != nil {
		t.Fatal(err)
	}
	retargetedCmd, err := retargeted.Command(t.Context(), "/bin/sh", "-c", `if /bin/cat "$1" >/dev/null 2>&1; then exit 71; fi; echo DENIED`, "sh", secret)
	if err != nil {
		t.Fatal(err)
	}
	retargetedCmd.Dir = run
	result, err := retargetedCmd.CombinedOutput()
	if err != nil || string(result) != "DENIED\n" {
		t.Fatalf("retargeted home exposed credential: %q %v", result, err)
	}
}

// The account alias and fixed token path exist only in the disposable container.
func TestAccountHomeAliasRetargetKeepsTokenHidden(t *testing.T) {
	if os.Getenv("LYK_ISOLATED_ACCOUNT_ALIAS_TEST") != "1" {
		t.Skip("requires disposable isolated account fixture")
	}
	if _, err := os.Stat("/.dockerenv"); err != nil || os.Geteuid() != 0 {
		t.Fatal("fixture requires isolated root container")
	}
	account, err := accountlookup.Current()
	if err != nil || account.HomeDir != "/root-alias" {
		t.Fatalf("unexpected isolated account: %+v %v", account, err)
	}
	canonical, err := filepath.EvalSymlinks(account.HomeDir)
	if err != nil || canonical != "/root" {
		t.Fatalf("unexpected account target %q %v", canonical, err)
	}
	configParent := filepath.Join(canonical, ".config")
	if err := os.Mkdir(configParent, 0700); err != nil {
		t.Fatalf("refusing to touch existing config parent: %v", err)
	}
	defer os.Remove(configParent)
	configDir := filepath.Join(configParent, "lyk")
	if err := os.Mkdir(configDir, 0700); err != nil {
		t.Fatal(err)
	}
	defer os.Remove(configDir)
	token := filepath.Join(configDir, "config.json")
	f, err := os.OpenFile(token, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = f.WriteString("token-fixture"); err != nil {
		f.Close()
		t.Fatal(err)
	}
	if err = f.Close(); err != nil {
		t.Fatal(err)
	}
	defer os.Remove(token)
	if data, err := os.ReadFile(token); err != nil || string(data) != "token-fixture" {
		t.Fatal("unsandboxed token control failed")
	}
	t.Setenv("HOME", t.TempDir())
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	t.Setenv("LYK_CONFIG", filepath.Join(t.TempDir(), "config.json"))
	run := t.TempDir()
	d := &daemon{cfg: Config{Home: t.TempDir(), StateDir: t.TempDir()}}
	p, err := d.cliPolicy(run, nil, false, agent.CLIPaths{})
	if err != nil {
		t.Fatal(err)
	}
	if !slices.Contains(p.Hidden, token) {
		t.Fatal("canonical account token is not hidden")
	}
	if err := os.Remove(account.HomeDir); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(t.TempDir(), account.HomeDir); err != nil {
		t.Fatal(err)
	}
	control := p
	control.Hidden = slices.DeleteFunc(slices.Clone(p.Hidden), func(path string) bool { return path == token })
	visible, err := control.Command(t.Context(), "/bin/sh", "-c", `/bin/cat "$1"`, "sh", token)
	if err != nil {
		t.Fatal(err)
	}
	visible.Dir = run
	out, err := visible.CombinedOutput()
	if err != nil || string(out) != "token-fixture" {
		t.Fatalf("sandbox positive control failed: %q %v", out, err)
	}
	denied, err := p.Command(t.Context(), "/bin/sh", "-c", `if /bin/cat "$1" >/dev/null 2>&1; then exit 71; fi; echo DENIED`, "sh", token)
	if err != nil {
		t.Fatal(err)
	}
	denied.Dir = run
	out, err = denied.CombinedOutput()
	if err != nil || string(out) != "DENIED\n" {
		t.Fatalf("retarget exposed original token: %q %v", out, err)
	}
}
