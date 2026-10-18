package agent

import (
	"errors"
	"io/fs"
	"os"
	"path/filepath"
)

// removePriorMCPConfig clears only a sole regular sidecar in an owned, locked
// scratch. Callers supply fixed adapter names; unrelated discovery stays intact
// for the guard to refuse.
func removePriorMCPConfig(dir, adapterDir, filename string) error {
	config := filepath.Join(dir, adapterDir)
	st, err := os.Lstat(config)
	if errors.Is(err, fs.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	if !st.IsDir() || st.Mode()&os.ModeSymlink != 0 {
		return nil
	}
	entries, err := os.ReadDir(config)
	if err != nil {
		return err
	}
	if len(entries) != 1 || entries[0].Name() != filename {
		return nil
	}
	file := filepath.Join(config, filename)
	st, err = os.Lstat(file)
	if err != nil {
		return err
	}
	if !st.Mode().IsRegular() {
		return nil
	}
	if err = os.Remove(file); err != nil {
		return err
	}
	return os.Remove(config)
}
