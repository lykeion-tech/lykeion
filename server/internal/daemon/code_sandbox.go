package daemon

import (
	"context"

	"lykeion/server/internal/daemon/sandbox"
	"lykeion/server/pkg/agent"
)

// CodeSandbox confines a CLI to runDir as an ordinary run's sandbox does, for
// a tool that runs agents outside the daemon (cmd/aristotle). home is the
// restricted parent of run folders; stateDir stays hidden from the CLI.
func CodeSandbox(ctx context.Context, home, stateDir, runDir string) (agent.SandboxCommand, error) {
	if err := sandbox.Check(ctx); err != nil {
		return nil, err
	}
	d := &daemon{cfg: Config{Home: home, StateDir: stateDir}}
	return d.cliSandbox(runDir, nil, false), nil
}
