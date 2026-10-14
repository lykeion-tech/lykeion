package agent

import (
	"os"
	"path/filepath"
	"regexp"
)

var sessionToken = regexp.MustCompile(`^[A-Za-z0-9-]{1,128}$`)

// SessionReachable reports whether this run's CLI can find the transcript of
// session. Both CLIs resume from their own store even after the original
// working folder is gone, so the store is what must be reachable. A private
// run's store is its new scratch, which never holds an earlier session.
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

// accountHome is the CLI's own config home in the daemon's environment.
func accountHome(harness string) string {
	env, dflt := "CLAUDE_CONFIG_DIR", ".claude"
	if harness == "codex" {
		env, dflt = "CODEX_HOME", ".codex"
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
