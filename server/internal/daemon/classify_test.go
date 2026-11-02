package daemon

import (
	"errors"
	"fmt"
	"os/exec"
	"testing"

	"lykeion/server/internal/daemon/sandbox"
	"lykeion/server/pkg/agent"
)

func TestClassify(t *testing.T) {
	for _, tc := range []struct {
		name, harness string
		err           error
		stderr, want  string
	}{
		{"missing binary", "claude", &exec.Error{Name: "claude", Err: exec.ErrNotFound}, "", agent.ReasonCLIMissing},
		{"missing wrapped", "codex", fmt.Errorf("start: %w", exec.ErrNotFound), "", agent.ReasonCLIMissing},
		{"sandbox failed to start", "claude", errors.New("sandbox failed to start: exit status 71: sandbox-exec: sandbox_apply: Operation not permitted"), "", agent.ReasonSandbox},
		{"no bubblewrap", "codex", errors.New("install bubblewrap"), "", agent.ReasonSandbox},
		{"sandbox policy", "claude", fmt.Errorf("launch: %w", sandbox.ErrLoopbackOnlyUnsupported), "", agent.ReasonSandbox},
		{"claude sign in", "claude", errors.New("exit status 1"), "Invalid API key · Please run /login", agent.ReasonSignIn},
		{"claude expired", "claude", errors.New("exit status 1"), "API Error: 401 OAuth token has expired", agent.ReasonSignIn},
		{"claude not logged in", "claude", errors.New("Not logged in"), "", agent.ReasonSignIn},
		{"codex sign in", "codex", errors.New("exit status 1"), "ERROR: unexpected status 401 Unauthorized: Missing bearer", agent.ReasonSignIn},
		{"codex not logged in", "codex", errors.New("exit status 1"), "Error: not logged in", agent.ReasonSignIn},
		{"codex refresh token", "codex", errors.New("exit status 1"), "Your refresh_token was already used", agent.ReasonSignIn},
		{"door", "claude", errors.New("exit status 1"), "MCP server lyk_door failed to connect: connect ECONNREFUSED 127.0.0.1:41233", agent.ReasonDoor},
		{"id containing 401 is not sign in", "claude", errors.New("exit status 1"), "wrote /tmp/run-14015/out.csv", agent.ReasonAgentError},
		{"plain agent error", "claude", errors.New("agent exited without a terminal result"), "Traceback: ValueError: bad input", agent.ReasonAgentError},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := classify(tc.harness, tc.err, tc.stderr, nil); got != tc.want {
				t.Fatalf("classify = %q, want %q", got, tc.want)
			}
		})
	}
}
