package daemon

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"lykeion/server/internal/apiclient"
	"lykeion/server/internal/daemon/envs"
)

const setupDescription = "Set up this study's environment: python chooses one Python version, conda adds packages from conda-forge then bioconda, and lock installs an explicit conda lock file or environment.yml inside the working folder or attachments. A study keeps one Python version; a new Python has only ipykernel, so install any other packages you need. lean selects a Lean toolchain with an optional mathlib revision; its shared base is built by the daemon and used read-only by the study. Setup has a 30 minute limit."

type setupIn struct {
	Python  string   `json:"python,omitempty" jsonschema:"Python version from 3.9 through 3.15; one version is kept per study"`
	Conda   []string `json:"conda,omitempty" jsonschema:"package specs searched in conda-forge then bioconda"`
	Lock    string   `json:"lock,omitempty" jsonschema:"path relative to the working folder or its attachments to an explicit conda lock file or environment.yml"`
	Lean    string   `json:"lean,omitempty" jsonschema:"Lean toolchain, for example leanprover/lean4:v4.30.0"`
	Mathlib string   `json:"mathlib,omitempty" jsonschema:"Mathlib version tag or hexadecimal commit; defaults to the Lean toolchain tag"`
}

func (d *Door) setPython(ctx context.Context, v string) error {
	api := &apiclient.Client{Server: d.Server, Token: d.CallToken, HTTP: &http.Client{Timeout: callTimeout}}
	if err := api.Do(ctx, "PUT", "/api/role/python", map[string]string{"version": v}, nil); err != nil {
		return err
	}
	return d.Study.RememberPython(v)
}
func (d *Door) setUp(ctx context.Context, _ *mcp.CallToolRequest, in setupIn) (*mcp.CallToolResult, any, error) {
	if d.Study == nil {
		return nil, nil, errors.New("this run has no study environment")
	}
	ctx, cancel := d.runContext(ctx)
	defer cancel()
	ctx, stop := context.WithTimeout(ctx, 30*time.Minute)
	defer stop()
	for !d.executionMu.TryLock() {
		select {
		case <-ctx.Done():
			return nil, nil, ctx.Err()
		case <-time.After(10 * time.Millisecond):
		}
	}
	defer d.executionMu.Unlock()
	if err := ctx.Err(); err != nil {
		return nil, nil, err
	}
	if in.Python == "" && len(in.Conda) == 0 && in.Lock == "" && in.Lean == "" && in.Mathlib == "" {
		return nil, nil, errors.New("python, conda, lock or lean is required")
	}
	leanDir := ""
	if in.Lean != "" || in.Mathlib != "" {
		var err error
		leanDir, err = d.Study.Bases.EnsureLean(ctx, in.Lean, in.Mathlib)
		if err != nil {
			return nil, nil, err
		}
	}
	if in.Lock != "" {
		full, err := insideDir(d.Child().Dir, in.Lock)
		if err != nil {
			return nil, nil, err
		}
		info, err := os.Stat(full)
		if err != nil {
			return nil, nil, err
		}
		if !info.Mode().IsRegular() {
			return nil, nil, errors.New("lock must be a regular file")
		}
		in.Lock = filepath.Clean(full)
	}
	output := ""
	if in.Python != "" || len(in.Conda) != 0 || in.Lock != "" {
		before := d.Study.PythonGeneration()
		var err error
		output, err = d.Study.SetUpConda(ctx, in.Python, in.Conda, in.Lock)
		d.recordEnvironmentError(envs.Python, err)
		if d.Study.PythonGeneration() != before {
			if restartErr := d.refreshPythonSession(ctx); restartErr != nil {
				return nil, nil, errors.Join(err, restartErr)
			}
		}
		if err != nil {
			return nil, nil, err
		}
		if _, _, readyErr := d.interpreter(envs.Python); readyErr == nil {
			d.setEnvironmentFailure(envs.Python, false)
		}
		output = "A new Python includes only ipykernel; install other packages as needed.\n" + output
	}
	if leanDir != "" {
		if err := d.Study.UseLean(ctx, leanDir); err != nil {
			return nil, nil, err
		}
		_, revision, _ := strings.Cut(filepath.Base(leanDir), "@")
		home := filepath.Join(leanDir, "elan")
		quote := func(path string) string { return "'" + strings.ReplaceAll(path, "'", "'\"'\"'") + "'" }
		project := filepath.Join(d.Study.Dir, "lean")
		output += fmt.Sprintf("\nLean %s with Mathlib %s is ready. Lake project: %s\nUse run_script with a saved script that runs lake build in this project to record results. For an existing CLI shell:\nexport ELAN_HOME=%s\nexport PATH=%s:\"$PATH\"\ncd %s", in.Lean, revision, project, quote(home), quote(filepath.Join(home, "bin")), quote(project))
	}
	return cited("", "Environment set up. "+output), nil, nil
}

// The bridge keeps the same venv path when Python changes. Restart only
// its Python kernel so live R and Julia sessions retain their variables.
func (d *Door) refreshPythonSession(ctx context.Context) error {
	d.mu.Lock()
	sessions := d.kernels
	d.mu.Unlock()
	if sessions != nil {
		if err := sessions.Restart(ctx, "python"); err != nil {
			return err
		}
	}
	d.mu.Lock()
	d.kernelGeneration = d.Study.PythonGeneration()
	d.mu.Unlock()
	return nil
}
