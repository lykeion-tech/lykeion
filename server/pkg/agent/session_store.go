package agent

import (
	"os"
	"path/filepath"
	"regexp"
)

var sessionToken = regexp.MustCompile(`^[A-Za-z0-9-]{1,128}$`)

// SessionReachable reports whether the account's CLI store holds a transcript,
// including a session whose original working folder no longer exists.
func SessionReachable(harness, session string, configHome ...string) bool {
	if !sessionToken.MatchString(session) {
		return false
	}
	home := accountHome(harness)
	if len(configHome) != 0 {
		home = runConfigHome(harness, configHome[0])
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

func runConfigHome(harness, configHome string) string {
	if configHome != "" {
		return configHome
	}
	return accountHome(harness)
}
