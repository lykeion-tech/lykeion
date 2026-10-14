package agent

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"runtime"
	"strings"
)

var discoveryNames = []string{".codex", ".claude", ".agents", ".claude.json", ".mcp.json", "AGENTS.md", "AGENTS.override.md", "CLAUDE.md", "CLAUDE.local.md"}

// prepareScratch protects discovery boundaries, not arbitrary filesystem access.
// Only this scratch's own history is reusable; personal config is never copied.
func prepareScratch(o *ExecOptions, harness, homeVar, defaultHome, credFile string) (home string, cleanup func(), err error) {
	if o.MCPToken != "" && !strings.HasPrefix(o.MCPToken, "ltk_") {
		return "", nil, errors.New("MCP token must be a run-scoped ltk_ token")
	}
	if o.Dir == "" {
		return "", nil, errors.New("agent scratch directory is required")
	}
	dir, err := filepath.Abs(o.Dir)
	if err != nil {
		return "", nil, err
	}
	dir, err = filepath.EvalSymlinks(dir)
	if err != nil {
		return "", nil, err
	}
	if err = checkAncestors(dir); err != nil {
		return "", nil, err
	}
	if err = checkManagedConfig(harness); err != nil {
		return "", nil, err
	}
	if err = privateDir(dir); err != nil {
		return "", nil, err
	}
	o.Dir = dir
	lock := filepath.Join(dir, ".agent-lock")
	f, err := os.OpenFile(lock, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600)
	if err != nil {
		return "", nil, fmt.Errorf("scratch is already in use or unavailable: %w", err)
	}
	f.Close()
	cred := ""
	cleanup = func() {
		if cred != "" {
			os.Remove(cred)
		}
		os.Remove(lock)
	}
	success := false
	defer func() {
		if !success {
			cleanup()
		}
	}()
	home = filepath.Join(dir, ".cli-home")
	userHome := filepath.Join(dir, ".home")
	marker := filepath.Join(dir, ".agent-owned")
	if st, e := os.Lstat(marker); e == nil && !st.Mode().IsRegular() {
		return "", cleanup, errors.New("scratch ownership marker is not a regular file")
	} else if e != nil && !errors.Is(e, fs.ErrNotExist) {
		return "", cleanup, e
	}
	owned, readErr := os.ReadFile(marker)
	if readErr != nil && !errors.Is(readErr, fs.ErrNotExist) {
		return "", cleanup, readErr
	}
	if readErr == nil && string(owned) != "lykeion-agent-v1:"+harness {
		return "", cleanup, errors.New("scratch belongs to a different harness")
	}
	for _, p := range []string{home, userHome} {
		if readErr != nil {
			entries, e := os.ReadDir(p)
			if e != nil && !errors.Is(e, fs.ErrNotExist) {
				return "", cleanup, e
			}
			if len(entries) > 0 {
				return "", cleanup, fmt.Errorf("refusing unowned CLI state in %s", p)
			}
		}
		if err = privateDir(p); err != nil {
			return "", cleanup, err
		}
		if err = checkOwnedState(p); err != nil {
			return "", cleanup, err
		}
	}
	if readErr != nil {
		if err = privateFile(marker, []byte("lykeion-agent-v1:"+harness)); err != nil {
			return "", cleanup, err
		}
	}
	for _, p := range []string{filepath.Join(userHome, ".config"), filepath.Join(userHome, ".local"), filepath.Join(userHome, ".local", "share"), filepath.Join(userHome, ".cache"), filepath.Join(userHome, ".state")} {
		if err = privateDir(p); err != nil {
			return "", cleanup, err
		}
	}
	realHome := os.Getenv(homeVar)
	if realHome == "" {
		real, err := os.UserHomeDir()
		if err != nil {
			return "", cleanup, err
		}
		realHome = filepath.Join(real, defaultHome)
	}
	target := filepath.Join(home, credFile)
	// Remove a stale copy from an interrupted run before deciding whether fresh
	// sign-in data exists. Removal never follows a stale symlink.
	if err = os.Remove(target); err != nil && !errors.Is(err, fs.ErrNotExist) {
		return "", cleanup, err
	}
	data, err := os.ReadFile(filepath.Join(realHome, credFile))
	if err != nil && !errors.Is(err, fs.ErrNotExist) {
		return "", cleanup, err
	}
	if err == nil {
		cred = target
		if err = privateFile(target, data); err != nil {
			return "", cleanup, err
		}
	}
	success = true
	return home, cleanup, nil
}

func checkAncestors(dir string) error {
	for parent := dir; ; parent = filepath.Dir(parent) {
		for _, name := range discoveryNames {
			p := filepath.Join(parent, name)
			if _, err := os.Lstat(p); err == nil {
				return fmt.Errorf("unsafe scratch discovery path: %s", p)
			} else if !errors.Is(err, fs.ErrNotExist) {
				return err
			}
		}
		if parent == filepath.Dir(parent) {
			break
		}
	}
	return nil
}

func checkManagedConfig(harness string) error {
	var paths []string
	if harness == "codex" {
		paths = []string{"/etc/codex/config.toml", "/etc/codex/managed_config.toml"}
	}
	if harness == "claude" {
		root := "/etc/claude-code"
		if runtime.GOOS == "darwin" {
			root = "/Library/Application Support/ClaudeCode"
		}
		paths = []string{filepath.Join(root, "managed-settings.json"), filepath.Join(root, "managed-settings.d"), filepath.Join(root, "managed-mcp.json")}
	}
	for _, p := range paths {
		if _, err := os.Lstat(p); err == nil {
			return fmt.Errorf("cannot isolate agent from managed configuration: %s", p)
		} else if !errors.Is(err, fs.ErrNotExist) {
			return err
		}
	}
	return nil
}

func privateDir(p string) error {
	if err := os.Mkdir(p, 0700); err != nil && !errors.Is(err, fs.ErrExist) {
		return err
	}
	st, err := os.Lstat(p)
	if err != nil {
		return err
	}
	if !st.IsDir() || st.Mode()&os.ModeSymlink != 0 {
		return fmt.Errorf("private directory is not a real directory: %s", p)
	}
	return os.Chmod(p, 0700)
}
func privateFile(p string, b []byte) error {
	f, err := os.OpenFile(p, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600)
	if err != nil {
		return err
	}
	_, err = f.Write(b)
	closeErr := f.Close()
	if err != nil {
		return err
	}
	return closeErr
}
func checkOwnedState(root string) error {
	return filepath.WalkDir(root, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if p == root {
			return nil
		}
		if d.Type()&os.ModeSymlink != 0 {
			return fmt.Errorf("symlink in isolated CLI state: %s", p)
		}
		n := d.Name()
		if n == "config.toml" || strings.HasSuffix(n, ".config.toml") || n == "settings.json" || n == "settings.local.json" || n == "plugins" || n == "skills" || n == "hooks" || n == "rules" {
			return fmt.Errorf("discovery configuration in isolated CLI state: %s", p)
		}
		for _, name := range discoveryNames {
			if n == name {
				return fmt.Errorf("discovery configuration in isolated CLI state: %s", p)
			}
		}
		return nil
	})
}

func filteredEnvKey(k string) bool {
	u := strings.ToUpper(k)
	switch u {
	case "LYK_TOKEN", "LYK_CONFIG", "LYK_SERVER", "CLAUDECODE", "CLAUDE_CODE_ENTRYPOINT", "CLAUDE_CODE_EXECPATH", "CLAUDE_CODE_SESSION_ID", "CLAUDE_CODE_SSE_PORT", "CODEX_THREAD_ID", "CODEX_SESSION_ID", "CODEX_APP_TOOLS_PIPE_PATH", "CODEX_INTERNAL_ORIGINATOR_OVERRIDE", "CODEX_CONFIG", "CODEX_CONFIG_TOML", "CODEX_MANAGED_CONFIG_PATH", "HOME", "USERPROFILE", "CLAUDE_CONFIG_DIR", "CODEX_HOME", "XDG_CONFIG_HOME", "XDG_CONFIG_DIRS", "XDG_DATA_HOME", "XDG_DATA_DIRS", "XDG_CACHE_HOME", "XDG_STATE_HOME", "BASH_ENV", "ENV", "ZDOTDIR":
		return true
	}
	return strings.HasPrefix(u, "CLAUDECODE_") || strings.HasPrefix(u, "LYKEION_")
}
func childEnv(o ExecOptions, home string) []string {
	values := map[string]string{}
	for _, entry := range os.Environ() {
		k, v, ok := strings.Cut(entry, "=")
		if ok && !filteredEnvKey(k) {
			values[k] = v
		}
	}
	for _, entry := range o.Env {
		k, v, ok := strings.Cut(entry, "=")
		if !ok {
			continue
		}
		if !filteredEnvKey(k) || k == "LYK_SERVER" {
			values[k] = v
		}
		if k == "LYK_CONFIG" {
			if isolated, ok := scratchConfigPath(o.Dir, v); ok {
				values[k] = isolated
			}
		}
	}
	if o.MCPToken != "" {
		values["LYK_TOKEN"] = o.MCPToken
	} else {
		for _, entry := range o.Env {
			if k, v, ok := strings.Cut(entry, "="); ok && k == "LYK_TOKEN" && strings.HasPrefix(v, "ltk_") {
				values[k] = v
			}
		}
	}
	userHome := filepath.Join(o.Dir, ".home")
	values["HOME"] = userHome
	values["USERPROFILE"] = userHome
	values["CODEX_HOME"] = home
	values["CLAUDE_CONFIG_DIR"] = home
	values["XDG_CONFIG_HOME"] = filepath.Join(userHome, ".config")
	values["XDG_CONFIG_DIRS"] = values["XDG_CONFIG_HOME"]
	values["XDG_DATA_HOME"] = filepath.Join(userHome, ".local", "share")
	values["XDG_DATA_DIRS"] = values["XDG_DATA_HOME"]
	values["XDG_CACHE_HOME"] = filepath.Join(userHome, ".cache")
	values["XDG_STATE_HOME"] = filepath.Join(userHome, ".state")
	env := make([]string, 0, len(values))
	for k, v := range values {
		env = append(env, k+"="+v)
	}
	return env
}

// A daemon may use /var or /tmp aliases while scratch resolves to /private.
// Resolve the existing parent and refuse links that lead outside the scratch.
func scratchConfigPath(dir, p string) (string, bool) {
	if !filepath.IsAbs(p) {
		return "", false
	}
	resolved, err := filepath.EvalSymlinks(p)
	if errors.Is(err, fs.ErrNotExist) {
		parent, e := filepath.EvalSymlinks(filepath.Dir(p))
		if e != nil {
			return "", false
		}
		resolved = filepath.Join(parent, filepath.Base(p))
	} else if err != nil {
		return "", false
	}
	rel, err := filepath.Rel(dir, resolved)
	return resolved, err == nil && rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator))
}
