// Package configpath resolves Lykeion's personal token-bearing configuration.
package configpath

import (
	"os"
	"path/filepath"
	"runtime"
	"slices"
)

func Default() (string, error) {
	dir, err := os.UserConfigDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, "lyk", "config.json"), nil
}

func Actual() (string, error) {
	if path := os.Getenv("LYK_CONFIG"); path != "" {
		return path, nil
	}
	return Default()
}

// Private includes absent names: a login can create a config during a run.
// XDG is also protected on macOS for configurations shared with Linux tooling.
func Private() ([]string, error) {
	def, err := Default()
	if err != nil {
		return nil, err
	}
	paths := []string{def}
	home, err := os.UserHomeDir()
	if err != nil {
		return nil, err
	}
	if runtime.GOOS == "linux" {
		paths = append(paths, filepath.Join(home, ".config", "lyk", "config.json"))
	}
	if xdg := os.Getenv("XDG_CONFIG_HOME"); xdg != "" {
		paths = append(paths, filepath.Join(xdg, "lyk", "config.json"))
	}
	if actual := os.Getenv("LYK_CONFIG"); actual != "" {
		paths = append(paths, actual)
	}
	var absolute []string
	for _, path := range paths {
		resolved, err := filepath.Abs(path)
		if err != nil {
			return nil, err
		}
		if !slices.Contains(absolute, resolved) {
			absolute = append(absolute, resolved)
		}
	}
	return absolute, nil
}
