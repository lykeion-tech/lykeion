package daemon

import (
	"context"
	"log/slog"
	"os/exec"
	"slices"

	"lykeion/server/pkg/agent"
)

// Model probes are serial and independent of registration and turn dispatch.
func (d *daemon) probeModels(ctx context.Context) {
	d.modelProbeMu.Lock()
	defer d.modelProbeMu.Unlock()
	models := map[string][]agent.Model{}
	names := make([]string, 0, len(d.cfg.Harnesses))
	for h := range d.cfg.Harnesses {
		names = append(names, h)
	}
	slices.Sort(names)
	for _, h := range names {
		if ctx.Err() != nil {
			return
		}
		list, e := agent.ListModels(ctx, h, d.cfg.Harnesses[h], func(ctx context.Context, paths agent.CLIPaths, bin string, args ...string) (*exec.Cmd, error) {
			return d.cliSandbox(paths.Scratch, nil, false)(ctx, paths, bin, args...)
		})
		if e != nil {
			slog.Warn("CLI model discovery unavailable", "harness", h)
			list = []agent.Model{}
		}
		models[h] = list
	}
	d.mu.Lock()
	d.models = models
	d.mu.Unlock()
}
