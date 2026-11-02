package daemon

import (
	"errors"
	"os/exec"
	"regexp"
	"strings"

	"lykeion/server/internal/daemon/sandbox"
	"lykeion/server/pkg/agent"
)

var (
	// A 401 counts only in the forms the CLIs and HTTP clients print it.
	unauthorized = regexp.MustCompile(`(?i)\b401 unauthorized|\b(?:status|http|api error:?|error:?)\s*(?:code\s*)?401\b`)
	// "Not logged in" counts only as a CLI's own message, at a line start or right after the folded "stderr:".
	notLoggedIn = regexp.MustCompile(`(?im)(?:^|stderr:)\s*(?:error:\s*)?not logged in\b`)
	// The sandbox package's launch and policy errors, which carry no type.
	sandboxSignatures = []string{"sandbox failed to start", "install bubblewrap", "no sandbox on", "sandbox policy", "sandbox path", "sandbox carved", "sandbox read folder", "sandbox restricted", "sandbox grant", "sandbox started but", "bwrap:", "sandbox-exec:"}
	signInSignatures  = map[string][]string{
		"claude": {"please run /login", "oauth token has expired", "invalid api key"},
		"codex":  {"refresh_token_reused", "refresh token was already used"},
	}
)

// classify names why a run failed when the cause is the machine rather than
// the agent: the CLI, the sandbox, the CLI's sign-in or the door. It returns
// agent_error when none applies; the caller keeps any more specific reason the
// agent package already found.
func classify(harness string, err error, stderr string, _ []agent.Message) string {
	if errors.Is(err, exec.ErrNotFound) {
		return agent.ReasonCLIMissing
	}
	text := stderr
	if err != nil {
		text = err.Error() + "\n" + stderr
	}
	lower := strings.ToLower(text)
	if errors.Is(err, sandbox.ErrLoopbackOnlyUnsupported) || containsAny(lower, sandboxSignatures) {
		return agent.ReasonSandbox
	}
	if containsAny(lower, signInSignatures[harness]) || unauthorized.MatchString(text) || notLoggedIn.MatchString(text) {
		return agent.ReasonSignIn
	}
	if strings.Contains(lower, "lyk_door") && containsAny(lower, []string{"failed to connect", "econnrefused", "connection refused"}) {
		return agent.ReasonDoor
	}
	return agent.ReasonAgentError
}

func containsAny(text string, needles []string) bool {
	for _, n := range needles {
		if strings.Contains(text, n) {
			return true
		}
	}
	return false
}

// failReason is the reason after a failure: one already set to something
// specific stays, and a generic one is replaced by the classified cause.
func failReason(current, harness string, err error) string {
	if current != "" && current != agent.ReasonAgentError {
		return current
	}
	return classify(harness, err, "", nil)
}
