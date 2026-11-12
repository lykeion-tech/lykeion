// Package configpath resolves Lykeion's personal token-bearing configuration.
package configpath

import (
	"errors"
	"os"
	"os/user"
	"path/filepath"
	"runtime"
	"slices"
	"strconv"
	"strings"

	"lykeion/server/internal/accountlookup"
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
	return private(accountlookup.LookupId)
}

func private(lookup func(string) (*user.User, error)) ([]string, error) {
	def, err := Default()
	if err != nil {
		return nil, err
	}
	paths := []string{def, filepath.Join(filepath.Dir(def), "profiles")}
	home, err := os.UserHomeDir()
	if err != nil {
		return nil, err
	}
	if runtime.GOOS == "linux" || runtime.GOOS == "darwin" {
		paths = append(paths, filepath.Join(home, ".config", "lyk", "config.json"), filepath.Join(home, ".config", "lyk", "profiles"))
		uid := strconv.Itoa(os.Geteuid())
		account, err := lookup(uid)
		if err != nil || account == nil || account.Uid != uid || !filepath.IsAbs(account.HomeDir) || account.HomeDir == "/" || filepath.Clean(account.HomeDir) != account.HomeDir || strings.ContainsFunc(account.HomeDir, func(r rune) bool { return r < 0x20 || r == 0x7f }) {
			return nil, errors.New("cannot safely identify the current account's home; ensure the operating system can resolve your user account")
		}
		config := filepath.Join(account.HomeDir, ".config", "lyk")
		paths = append(paths, filepath.Join(config, "config.json"), filepath.Join(config, "profiles"))
		if runtime.GOOS == "darwin" {
			config = filepath.Join(account.HomeDir, "Library", "Application Support", "lyk")
			paths = append(paths, filepath.Join(config, "config.json"), filepath.Join(config, "profiles"))
		}
	}
	if xdg := os.Getenv("XDG_CONFIG_HOME"); xdg != "" {
		paths = append(paths, filepath.Join(xdg, "lyk", "config.json"), filepath.Join(xdg, "lyk", "profiles"))
	}
	if actual := os.Getenv("LYK_CONFIG"); actual != "" {
		paths = append(paths, actual)
		for parent := filepath.Dir(actual); parent != "." && parent != string(filepath.Separator); parent = filepath.Dir(parent) {
			if filepath.Base(parent) == "profiles" {
				paths = append(paths, parent)
				break
			}
		}
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
