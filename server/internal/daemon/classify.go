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
	// Sandbox launch failures that reach the daemon as text, not as sandbox.ErrPolicy.
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
	// A scope the server built and the daemon refuses is retried as the
	// harness's failure; the machine is not flagged for it.
	var scope errContinuityScope
	if errors.As(err, &scope) {
		return agent.ReasonRuntimeOffline
	}
	text := stderr
	if err != nil {
		text = err.Error() + "\n" + stderr
	}
	lower := strings.ToLower(text)
	if errors.Is(err, sandbox.ErrPolicy) || errors.Is(err, sandbox.ErrLoopbackOnlyUnsupported) || containsAny(lower, sandboxSignatures) {
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

// failReport marks r failed with err, keeping a specific reason already set.
func failReport(r *Report, harness string, err error) {
	r.Kind = "fail"
	r.Error = err.Error()
	r.FailureReason = failReason(r.FailureReason, harness, err)
}

// failResult fails r with a finished CLI's error. The agent package's own
// reason is set first so the text match cannot override it.
func failResult(r *Report, harness string, res agent.Result) {
	r.FailureReason = res.Reason
	failReport(r, harness, res.Err)
}
