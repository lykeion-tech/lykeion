package agent

import (
	"fmt"
	"os"
	"path/filepath"
	"runtime"
)

// PrepareGuestHome refreshes only the account sign-in in a run's config home.
func PrepareGuestHome(harness, ownerHome, dir string) error {
	var name string
	switch harness {
	case "codex":
		name = "auth.json"
	case "claude":
		if runtime.GOOS == "linux" {
			name = ".credentials.json"
		}
	default:
		return fmt.Errorf("unsupported CLI %q", harness)
	}
	if err := os.MkdirAll(filepath.Dir(dir), 0700); err != nil {
		return err
	}
	if err := privateDir(dir); err != nil {
		return err
	}
	if name == "" {
		return nil
	}
	source := filepath.Join(ownerHome, name)
	st, err := os.Lstat(source)
	if err != nil {
		return err
	}
	if !st.Mode().IsRegular() {
		return fmt.Errorf("CLI sign-in is not a regular file")
	}
	data, err := os.ReadFile(source)
	if err != nil {
		return err
	}
	f, err := os.CreateTemp(dir, ".signin-")
	if err != nil {
		return err
	}
	defer os.Remove(f.Name())
	if _, err = f.Write(data); err != nil {
		f.Close()
		return err
	}
	if err = f.Close(); err != nil {
		return err
	}
	return os.Rename(f.Name(), filepath.Join(dir, name))
}
