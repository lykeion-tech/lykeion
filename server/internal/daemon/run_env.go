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
