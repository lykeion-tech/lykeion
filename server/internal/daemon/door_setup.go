package daemon

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"lykeion/server/internal/apiclient"
)

const setupDescription = "Set up this study's environment: python chooses one Python version, conda adds packages from conda-forge then bioconda, and lock installs an explicit conda lock file or environment.yml inside the working folder or attachments. A study keeps one Python version; a new Python has only ipykernel, so install any other packages you need. Setup runs inside the study sandbox and has a 30 minute limit."

type setupIn struct {
	Python  string   `json:"python,omitempty" jsonschema:"Python version from 3.9 through 3.14; one version is kept per study"`
	Conda   []string `json:"conda,omitempty" jsonschema:"package specs searched in conda-forge then bioconda"`
	Lock    string   `json:"lock,omitempty" jsonschema:"path relative to the working folder or its attachments to an explicit conda lock file or environment.yml"`
	Lean    string   `json:"lean,omitempty" jsonschema:"Lean toolchain; Lean setup is not available yet"`
	Mathlib string   `json:"mathlib,omitempty" jsonschema:"Mathlib revision; Lean setup is not available yet"`
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
	if in.Lean != "" || in.Mathlib != "" {
		return nil, nil, errors.New("Lean setup is not available yet")
	}
	if in.Python == "" && len(in.Conda) == 0 && in.Lock == "" {
		return nil, nil, errors.New("python, conda or lock is required")
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
	before := d.Study.PythonGeneration()
	output, err := d.Study.SetUpConda(ctx, in.Python, in.Conda, in.Lock)
	if d.Study.PythonGeneration() != before {
		if restartErr := d.refreshPythonSession(ctx); restartErr != nil {
			return nil, nil, errors.Join(err, restartErr)
		}
	}
	if err != nil {
		return nil, nil, err
	}
	return cited("", fmt.Sprintf("Environment set up. A new Python includes only ipykernel; install other packages as needed.\n%s", output)), nil, nil
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
