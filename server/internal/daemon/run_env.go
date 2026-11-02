package daemon

import (
	"os"
	"path/filepath"
)

// lykDir is the directory holding this executable, resolved through
// symlinks: the sandbox grants read on the resolved target, so a PATH entry
// through the unresolved link would be refused.
func lykDir() (string, error) {
	self, err := os.Executable()
	if err != nil {
		return "", err
	}
	return resolvedDir(self)
}

func resolvedDir(exe string) (string, error) {
	exe, err := filepath.EvalSymlinks(exe)
	if err != nil {
		return "", err
	}
	return filepath.Dir(exe), nil
}

// runPath is a CLI's PATH value: lyk's resolved directory, then the person's
// own PATH. The study's interpreters are never on it.
func runPath() (string, error) {
	lyk, err := lykDir()
	if err != nil {
		return "", err
	}
	return lyk + string(os.PathListSeparator) + os.Getenv("PATH"), nil
}

// cliEnv is the environment a study run's CLI starts with. A hand-in run
// answers in its final message and the daemon posts it, so its CLI holds no
// token, server address, config path or lyk on its PATH.
func cliEnv(handIn bool, token, server, dir string) ([]string, error) {
	if handIn {
		return []string{"PATH=" + os.Getenv("PATH")}, nil
	}
	path, err := runPath()
	if err != nil {
		return nil, err
	}
	return []string{"LYK_TOKEN=" + token, "LYK_SERVER=" + server, "LYK_CONFIG=" + filepath.Join(dir, ".lyk-config.json"), "PATH=" + path}, nil
}

// doorEnv is what the CLI needs to reach the door. Claude Code carries the
// door's token in its MCP config; Codex reads it from the environment, so a
// Codex hand-in run gets that one variable and nothing else.
func doorEnv(handIn bool, harness string, door *Door) []string {
	if handIn {
		if harness == "codex" {
			return []string{"LYK_DOOR_TOKEN=" + door.Token}
		}
		return nil
	}
	return []string{"LYK_DOOR=" + door.URL, "LYK_DOOR_TOKEN=" + door.Token}
}
