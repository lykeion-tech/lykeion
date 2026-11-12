//go:build darwin || linux

package sandbox

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestCheckProbeRequiresExecutionAndDenial(t *testing.T) {
	for _, tc := range []struct {
		name, output string
		fail, wantOK bool
	}{
		{"executed and denied", "STARTED\nDENIED\n", false, true},
		{"not executed", "", true, false},
		{"missing marker", "", false, false},
		{"readable", "STARTED\nEXPOSED\n", false, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			home := t.TempDir()
			sentinel := filepath.Join(home, "unrelated")
			if err := os.WriteFile(sentinel, []byte("keep"), 0600); err != nil {
				t.Fatal(err)
			}
			calls := 0
			var probe string
			command := func(p Policy, ctx context.Context, name string, args ...string) (*exec.Cmd, error) {
				calls++
				if calls == 1 {
					if p.Home != home {
						t.Fatalf("wrong account home %q", p.Home)
					}
					return exec.CommandContext(ctx, "/usr/bin/true"), nil
				}
				if calls != 2 || name != "/bin/sh" || len(args) != 4 || args[0] != "-c" || args[2] != "sh" {
					t.Fatalf("wrong probe command: %s %v", name, args)
				}
				probe = args[3]
				if p.Home != filepath.Dir(probe) || filepath.Dir(p.Home) != home || !strings.HasPrefix(filepath.Base(p.Home), ".lyk-sandbox-check-") {
					t.Fatalf("wrong owned probe home %q for %q", p.Home, probe)
				}
				if b, err := os.ReadFile(probe); err != nil || string(b) != "x" {
					t.Fatalf("positive control: %q %v", b, err)
				}
				if tc.fail {
					return exec.CommandContext(ctx, "/usr/bin/false"), nil
				}
				return exec.CommandContext(ctx, "/usr/bin/printf", "%s", tc.output), nil
			}
			err := checkHome(context.Background(), home, command)
			if (err == nil) != tc.wantOK {
				t.Fatalf("check: %v", err)
			}
			if calls != 2 {
				t.Fatalf("calls %d", calls)
			}
			if _, err := os.Stat(probe); !errors.Is(err, os.ErrNotExist) {
				t.Fatalf("probe not cleaned: %v", err)
			}
			if b, err := os.ReadFile(sentinel); err != nil || string(b) != "keep" {
				t.Fatalf("unrelated file changed: %q %v", b, err)
			}
		})
	}
}
