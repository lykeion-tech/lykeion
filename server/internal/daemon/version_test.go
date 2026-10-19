package daemon

import (
	"errors"
	"os"
	"path/filepath"
	"testing"

	"lykeion/server/internal/store"
	"lykeion/server/pkg/agent"
	db "lykeion/server/pkg/db/generated"
)

func TestDaemonReportsCLIVersionsAtRegistration(t *testing.T) {
	f := fixture(t)
	// The fake CLI answers --version and otherwise behaves as fakeClaude.
	wrapper := filepath.Join(t.TempDir(), "claude")
	must(t, os.WriteFile(wrapper, []byte("#!/bin/sh\n[ \"$1\" = --version ] && { echo '9.8.7 (Claude Code)'; exit 0; }\nexec "+f.cfg.Harnesses["claude"]+" \"$@\"\n"), 0o700))
	f.cfg.Harnesses = map[string]string{"claude": wrapper}
	want := errors.New("stop after registration")
	var got string
	var stored db.Runtime
	f.cfg.Ready = func() error {
		must(t, f.runs.Store.InWorkspace(t.Context(), f.ws, f.user, func(tx *store.Tx) error {
			rts, err := tx.Q.ListRuntimes(t.Context(), f.ws)
			if err == nil && len(rts) == 1 {
				got = rts[0].CliVersion
				stored = db.Runtime{Os: rts[0].Os, Arch: rts[0].Arch, Kernel: rts[0].Kernel, Language: rts[0].Language}
			}
			return err
		}))
		return want
	}
	if err := Run(t.Context(), f.cfg); !errors.Is(err, want) {
		t.Fatalf("run: %v", err)
	}
	if got != "9.8.7 (Claude Code)" {
		t.Fatalf("stored version %q", got)
	}
	// The platform is the machine's own; nothing absent is filled in.
	if want := agent.DetectPlatform(t.Context()); stored.Os != want.OS || stored.Arch != want.Arch || stored.Kernel != want.Kernel || stored.Language != want.Language {
		t.Fatalf("stored platform %+v, detected %+v", stored, want)
	}
}
