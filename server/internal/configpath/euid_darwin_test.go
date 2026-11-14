//go:build darwin

package configpath

import (
	"bufio"
	"context"
	"fmt"
	"io"
	"os"
	"os/user"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"lykeion/server/internal/daemon/sandbox"
)

func TestEffectiveUIDPrivatePathsHiddenFromMacSandbox(t *testing.T) {
	for _, target := range []struct{ name, ancestor, parent, secret string }{
		{"xdg-config", ".config", ".config/lyk", ".config/lyk/config.json"},
		{"xdg-profiles", ".config", ".config/lyk", ".config/lyk/profiles/desktop-updates-v1/sentinel"},
		{"mac-config", "Library", "Library/Application Support/lyk", "Library/Application Support/lyk/config.json"},
		{"mac-profiles", "Library", "Library/Application Support/lyk", "Library/Application Support/lyk/profiles/desktop/sentinel"},
	} {
		for _, tc := range []struct{ late, linkedHome bool }{{false, false}, {true, false}, {false, true}, {true, true}} {
			t.Run(fmt.Sprintf("%s_late=%t_link=%t", target.name, tc.late, tc.linkedHome), func(t *testing.T) {
				fakeHome, accountHome := t.TempDir(), t.TempDir()
				lookupHome := accountHome
				if tc.linkedHome {
					lookupHome = filepath.Join(t.TempDir(), "account-home")
					if err := os.Symlink(accountHome, lookupHome); err != nil {
						t.Fatal(err)
					}
				}
				t.Setenv("HOME", fakeHome)
				t.Setenv("XDG_CONFIG_HOME", t.TempDir())
				t.Setenv("LYK_CONFIG", filepath.Join(t.TempDir(), "custom.json"))
				lookup := func(id string) (*user.User, error) {
					return &user.User{Uid: id, HomeDir: lookupHome}, nil
				}
				paths, err := private(lookup)
				if err != nil {
					t.Fatal(err)
				}
				config := filepath.Join(accountHome, target.ancestor)
				secret := filepath.Join(accountHome, target.secret)
				work := t.TempDir()
				visible := filepath.Join(work, "visible")
				if err := os.WriteFile(visible, []byte("VISIBLE\n"), 0600); err != nil {
					t.Fatal(err)
				}
				public := filepath.Join(accountHome, "public")
				if err := os.WriteFile(public, []byte("ACCOUNT_VISIBLE\n"), 0600); err != nil {
					t.Fatal(err)
				}
				if err := os.MkdirAll(filepath.Join(accountHome, target.parent), 0700); err != nil {
					t.Fatal(err)
				}
				create := func() {
					t.Helper()
					if err := os.MkdirAll(filepath.Dir(secret), 0700); err != nil {
						t.Fatal(err)
					}
					if err := os.WriteFile(secret, []byte("PRIVATE"), 0600); err != nil {
						t.Fatal(err)
					}
				}
				if !tc.late {
					create()
				}
				ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
				defer cancel()
				p := sandbox.Policy{Home: fakeHome, HomeRead: true, Read: []string{accountHome}, Write: []string{work}, Hidden: paths}
				script := `cat "$1" || exit 20; cat "$6" || exit 26; touch "$5" || exit 25; echo READY; IFS= read -r ignored || exit 21
if cat "$2" >/dev/null 2>&1; then echo READ_EXPOSED; exit 22; fi
if sh -c 'printf X >> "$1"' sh "$2" 2>/dev/null; then echo WRITE_EXPOSED; exit 23; fi
if mv "$3" "$4" 2>/dev/null; then echo RENAME_EXPOSED; exit 24; fi
echo SAFE`
				moved := filepath.Join(accountHome, "config-moved")
				cmd, err := p.Command(ctx, "/bin/sh", "-c", script, "sh", visible, secret, config, moved, filepath.Join(work, "own-write"), public)
				if err != nil {
					t.Fatal(err)
				}
				defer sandbox.CloseCommand(cmd)
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
				reader := bufio.NewReader(stdout)
				for _, expected := range []string{"VISIBLE\n", "ACCOUNT_VISIBLE\n", "READY\n"} {
					line, err := reader.ReadString('\n')
					if err != nil || line != expected {
						_ = stdin.Close()
						_ = cmd.Wait()
						t.Fatalf("sandbox positive control: %q, %v", line, err)
					}
				}
				if tc.late {
					create()
				}
				if got, err := os.ReadFile(secret); err != nil || string(got) != "PRIVATE" {
					t.Fatalf("unsandboxed read control: %q, %v", got, err)
				}
				if err := os.WriteFile(secret, []byte("PRIVATE"), 0600); err != nil {
					t.Fatalf("unsandboxed write control: %v", err)
				}
				if err := os.Rename(config, moved); err != nil {
					t.Fatalf("unsandboxed rename control: %v", err)
				}
				if err := os.Rename(moved, config); err != nil {
					t.Fatalf("restore unsandboxed rename control: %v", err)
				}
				if _, err := io.WriteString(stdin, "continue\n"); err != nil {
					t.Fatal(err)
				}
				_ = stdin.Close()
				out, err := io.ReadAll(reader)
				waitErr := cmd.Wait()
				if err != nil || waitErr != nil || strings.TrimSpace(string(out)) != "SAFE" {
					t.Fatalf("sandbox exposed UID profile root: %q, read=%v, wait=%v", out, err, waitErr)
				}
				p.Read = nil
				p.Write = []string{accountHome}
				if unsafe, err := p.Command(ctx, "/bin/true"); err == nil {
					sandbox.CloseCommand(unsafe)
					t.Fatal("sandbox accepted a writable ancestor of the protected profile root")
				}
			})
		}
	}
}
