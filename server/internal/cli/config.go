// Package cli is the lyk command line: one client for people and, later, for
// agent roles and the daemon.
package cli

import (
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
)

type Config struct {
	Server    string `json:"server"`
	Token     string `json:"token"`
	Workspace string `json:"workspace"`
}

func configPath() (string, error) {
	if p := os.Getenv("LYK_CONFIG"); p != "" {
		return p, nil
	}
	dir, err := os.UserConfigDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, "lyk", "config.json"), nil
}

func LoadConfig() (Config, string, error) {
	p, err := configPath()
	if err != nil {
		return Config{}, "", err
	}
	var c Config
	b, err := os.ReadFile(p)
	if errors.Is(err, fs.ErrNotExist) {
		return c, p, nil
	}
	if err != nil {
		return c, p, err
	}
	return c, p, json.Unmarshal(b, &c)
}

// SaveConfig replaces the token-bearing config atomically with private permissions.
func SaveConfig(path string, c Config) error {
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return fmt.Errorf("create config directory: %w", err)
	}
	if err := os.Chmod(dir, 0o700); err != nil {
		return fmt.Errorf("restrict config directory: %w", err)
	}
	b, err := json.MarshalIndent(c, "", "  ")
	if err != nil {
		return err
	}
	tmp, err := os.CreateTemp(dir, ".config-*.json.tmp")
	if err != nil {
		return fmt.Errorf("create temp config: %w", err)
	}
	defer os.Remove(tmp.Name())
	if _, err := tmp.Write(append(b, '\n')); err != nil {
		tmp.Close()
		return fmt.Errorf("write temp config: %w", err)
	}
	if err := tmp.Close(); err != nil {
		return fmt.Errorf("close temp config: %w", err)
	}
	if err := os.Chmod(tmp.Name(), 0o600); err != nil {
		return fmt.Errorf("restrict config file: %w", err)
	}
	if err := os.Rename(tmp.Name(), path); err != nil {
		return fmt.Errorf("replace config: %w", err)
	}
	return nil
}
