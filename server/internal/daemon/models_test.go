package daemon

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"strings"
	"testing"

	"lykeion/server/pkg/agent"
)

func TestModelProbeFailureIsNonfatal(t *testing.T) {
	d := &daemon{cfg: Config{Home: t.TempDir(), Harnesses: map[string]string{"claude": "/missing/claude", "codex": "/missing/codex"}}, sandboxErr: errors.New("unavailable")}
	d.probeModels(t.Context())
	for _, h := range []string{"claude", "codex"} {
		if d.models[h] == nil || len(d.models[h]) != 0 {
			t.Fatal(d.models)
		}
	}
	d.models["claude"] = []agent.Model{{Value: "old", Label: "Old"}}
	d.probeModels(t.Context())
	if len(d.models["claude"]) != 0 {
		t.Fatal("failed refresh retained stale choices")
	}
}
func TestRealCLIModelCatalog(t *testing.T) {
	if os.Getenv("LYK_TEST_MODELS_REAL") != "1" {
		t.Skip("opt-in installed CLI observation")
	}
	d := &daemon{cfg: Config{Home: t.TempDir()}}
	for _, h := range []string{"claude", "codex"} {
		bin, e := exec.LookPath(h)
		if e != nil {
			t.Fatal(e)
		}
		models, e := agent.ListModels(t.Context(), h, bin, func(ctx context.Context, p agent.CLIPaths, n string, args ...string) (*exec.Cmd, error) {
			if h == "codex" {
				if !strings.HasPrefix(p.ConfigHome, p.Scratch+"/") || p.Keychain {
					t.Fatal("catalog accesses account store")
				}
				if _, e := os.Stat(p.ConfigHome + "/auth.json"); !os.IsNotExist(e) {
					t.Fatal("catalog contains credentials")
				}
			}
			return d.cliSandbox(p.Scratch, nil, false)(ctx, p, n, args...)
		})
		if e != nil || len(models) == 0 {
			t.Fatalf("%s: %d models: %v", h, len(models), e)
		}
		t.Logf("%s: %d models, first value %q", h, len(models), models[0].Value)
	}
}
