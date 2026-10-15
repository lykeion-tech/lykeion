package agent

import (
	"os"
	"path/filepath"
	"regexp"
)

var sessionToken = regexp.MustCompile(`^[A-Za-z0-9-]{1,128}$`)

// SessionReachable reports whether this run's CLI can find the transcript of
// session. Each CLI resumes from its own store even after the original working
// folder is gone, so the store is what must be reachable. A private run's store
// is its scratch's .cli-home, which the next private run of the same scope and
// harness reuses; a run with no scope gets a new scratch and finds nothing.
func SessionReachable(harness, session string, inherit bool, dir string) bool {
	if !sessionToken.MatchString(session) {
		return false
	}
	home := filepath.Join(dir, ".cli-home")
	if inherit {
		home = accountHome(harness)
	}
	if home == "" {
		return false
	}
	var pattern string
	switch harness {
	case "claude":
		pattern = filepath.Join(home, "projects", "*", session+".jsonl")
	case "codex":
		pattern = filepath.Join(home, "sessions", "*", "*", "*", "rollout-*-"+session+".jsonl")
	case "cursor":
		// cursor-agent stores chats by workspace in its data folder.
		pattern = filepath.Join(home, "chats", "*", session, "store.db")
	case "antigravity":
		pattern = filepath.Join(home, ".gemini", "antigravity-cli", "brain", session, ".system_generated", "logs", "transcript.jsonl")
	default:
		return false
	}
	matches, err := filepath.Glob(pattern)
	if err != nil {
		return false
	}
	for _, m := range matches {
		if st, err := os.Stat(m); err == nil && st.Mode().IsRegular() {
			return true
		}
	}
	return false
}

// accountHome is the CLI's own config home in the daemon's environment. Only
// Claude and Codex run on the account's setup; the others have none here.
func accountHome(harness string) string {
	var env, dflt string
	switch harness {
	case "claude":
		env, dflt = "CLAUDE_CONFIG_DIR", ".claude"
	case "codex":
		env, dflt = "CODEX_HOME", ".codex"
	default:
		return ""
	}
	if v := os.Getenv(env); v != "" {
		return v
	}
	real, err := os.UserHomeDir()
	if err != nil {
		return ""
	}
	return filepath.Join(real, dflt)
}
