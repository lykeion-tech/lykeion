package agent

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"sync"
)

// scratchCleanup removes credentials immediately; release also relinquishes the
// scratch lock after output accounting has finished.
type scratchCleanup func(release bool)

// PrelaunchDiscoveryError identifies isolated discovery state refused before launch.
type PrelaunchDiscoveryError struct{ Path string }

func (e *PrelaunchDiscoveryError) Error() string {
	return fmt.Sprintf("discovery configuration in isolated CLI state: %s", e.Path)
}

var discoveryNames = []string{".cursor", ".codex", ".claude", ".agents", ".claude.json", ".mcp.json", "AGENTS.md", "AGENTS.override.md", "CLAUDE.md", "CLAUDE.local.md"}

// RunFolderPath is a file or folder in a run's folder that a CLI or the
// daemon keeps for itself.
type RunFolderPath struct {
	Name   string
	Folder bool
	// A marker means something by existing (a lock, a live process, a
	// finished run), so only its owner may ever create one.
	Marker bool
	// A hidden path holds the door's token or the CLI's sign-in: code may
	// not even read it.
	Hidden bool
}

// RunFolderConfig lists what the CLIs read in their working folder as
// configuration or instructions, their private homes, the files the daemon
// places for a run, and the markers the daemon trusts. Code a run starts may
// read these but never write them, and may not read the hidden ones.
var RunFolderConfig = []RunFolderPath{
	{Name: ".claude", Folder: true}, {Name: ".codex", Folder: true}, {Name: ".cursor", Folder: true}, {Name: ".agents", Folder: true},
	{Name: ".cli-home", Folder: true, Hidden: true}, {Name: ".home", Folder: true, Hidden: true},
	{Name: ".claude.json"}, {Name: ".mcp.json"}, {Name: ".lyk-mcp.json", Hidden: true}, {Name: ".lyk-config.json", Hidden: true},
	{Name: "AGENTS.md"}, {Name: "AGENTS.override.md"}, {Name: "CLAUDE.md"}, {Name: "CLAUDE.local.md"}, {Name: "SKILL.md"},
	{Name: ".agent-owned", Marker: true}, {Name: ".agent-lock", Marker: true}, {Name: ".agent-process", Marker: true},
	{Name: ".finished", Marker: true},
	// The supervisor writes its exit marker from outside the sandbox, and
	// the daemon reads it and Antigravity's log: they get placeholders, so
	// code can't plant a link in their place.
	{Name: ".agent-exit"}, {Name: ".agent-exit.tmp"}, {Name: ".agy.log", Hidden: true},
	// The CLIs read these from their working folder by themselves, so no
	// source here names them: git's repository (hooks, fsmonitor), Cursor's
	// rules and ignore files, Antigravity's rules and workflows and the
	// Gemini settings and instructions it shares.
	{Name: ".git", Folder: true}, {Name: ".agent", Folder: true}, {Name: ".gemini", Folder: true},
	{Name: "GEMINI.md"}, {Name: ".geminiignore"}, {Name: ".cursorrules"}, {Name: ".cursorignore"}, {Name: ".cursorindexingignore"},
	// MCP configs inside the folders above that carry the door's token.
	{Name: ".cursor/mcp.json", Hidden: true}, {Name: ".agents/mcp_config.json", Hidden: true},
}

// prepareScratch protects discovery boundaries, not arbitrary filesystem access.
// Only this scratch's own history is reusable; personal config is never copied.
func prepareScratch(o *ExecOptions, harness, homeVar, defaultHome, credFile string) (home string, cleanup scratchCleanup, err error) {
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
	if !o.Inherit {
		if err = checkManagedConfig(harness); err != nil {
			return "", nil, err
		}
	}
	var unlock func()
	if o.ScratchReservation != nil {
		unlock, err = o.ScratchReservation.borrow(dir)
	} else {
		var reservation *ScratchReservation
		reservation, err = ReserveScratch(dir)
		if err == nil {
			unlock = reservation.Release
		}
	}
	if err != nil {
		return "", nil, err
	}
	if err = privateDir(dir); err != nil {
		unlock()
		return "", nil, err
	}
	o.Dir = dir
	cred := ""
	var once sync.Once
	cleanup = func(release bool) {
		if cred != "" {
			os.Remove(cred)
		}
		if release {
			once.Do(unlock)
		}
	}
	success := false
	defer func() {
		if !success {
			cleanup(true)
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
	if !o.Inherit {
		if harness == "cursor" && readErr == nil {
			if err = removePriorCursorConfig(dir); err != nil {
				return "", cleanup, err
			}
		}
		if harness == "antigravity" && readErr == nil {
			if err = removePriorAntigravityConfig(dir); err != nil {
				return "", cleanup, err
			}
		}
		if err = checkAncestors(dir); err != nil {
			return "", cleanup, err
		}
	}
	var discovery *PrelaunchDiscoveryError
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
		// The daemon owns these folders and rewrites them on every prepare.
		for _, placed := range placedFolders {
			if err = os.RemoveAll(filepath.Join(p, placed)); err != nil {
				return "", cleanup, err
			}
		}
		if err = checkOwnedState(p); err != nil {
			var refused *PrelaunchDiscoveryError
			if !errors.As(err, &refused) {
				return "", cleanup, err
			}
			if discovery == nil {
				discovery = refused
			}
		}
	}
	if discovery != nil {
		// Adapter-owned root files are outside the isolated-home scan. Their
		// structural refusals must take precedence over a fresh-folder retry.
		if harness == "claude" {
			if _, err = claudeMCPExists(filepath.Join(dir, ".lyk-mcp.json")); err != nil {
				return "", cleanup, err
			}
		}
		if readErr != nil || o.Inherit {
			return "", cleanup, errors.New(discovery.Error())
		}
		return "", cleanup, discovery
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
	if o.Inherit {
		if err = placeSkills(o, harness); err != nil {
			return "", cleanup, err
		}
		// The account's own config home stays authoritative; no sign-in copy.
		success = true
		return home, cleanup, nil
	}
	realHome := os.Getenv(homeVar)
	if realHome == "" {
		real, err := os.UserHomeDir()
		if err != nil {
			return "", cleanup, err
		}
		realHome = filepath.Join(real, defaultHome)
	}
	if runtime.GOOS == "darwin" {
		if err = placeKeychainStandIn(home); err != nil {
			return "", cleanup, err
		}
	}
	target := filepath.Join(home, credFile)
	// Remove a stale copy from an interrupted run before deciding whether fresh
	// sign-in data exists. Removal never follows a stale symlink.
	if err = os.Remove(target); err != nil && !errors.Is(err, fs.ErrNotExist) {
		return "", cleanup, err
	}
	data, err := readSignIn(filepath.Join(realHome, credFile), harness, runtime.GOOS, os.Getenv(homeVar) != "", defaultClaudeSignIn)
	if err != nil {
		return "", cleanup, err
	}
	if len(data) != 0 {
		cred = target
		if err = privateDir(filepath.Dir(target)); err != nil {
			return "", cleanup, err
		}
		if err = privateFile(target, data); err != nil {
			return "", cleanup, err
		}
	}
	success = true
	return home, cleanup, nil
}

// keychainStandIn is the folder in a private run's CLI home that holds the
// run's own security tool, first on its PATH. The CLI home is out of reach of
// the agent's file tools and of sandboxed code, so neither can swap the tool
// the CLI then runs as the person.
const keychainStandIn = ".lyk-bin"

// On macOS the Security framework finds the keychain through HOME, so under a
// private run's stand-in home there is none, and a keychain write makes macOS
// ask the person to find one. Claude Code tries the keychain through the
// security tool before its sign-in file, so a private run gets a security
// tool that finds nothing and stores nothing, and every CLI keeps to the
// sign-in file as it does on Linux.
func placeKeychainStandIn(home string) error {
	// prepareScratch has already removed the folder of an earlier run.
	dir := filepath.Join(home, keychainStandIn)
	if err := privateDir(dir); err != nil {
		return err
	}
	// 44 is the security tool's "item not found".
	return os.WriteFile(filepath.Join(dir, "security"), []byte("#!/bin/sh\necho 'security: a private run has no keychain' >&2\nexit 44\n"), 0700)
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

// placedFolders are the skill folders and the keychain stand-in the daemon
// places in an isolated CLI state root; checkOwnedState exempts them there
// and nowhere deeper.
var placedFolders = []string{"skills", ".agents", keychainStandIn}

func checkOwnedState(root string) error {
	var discovery *PrelaunchDiscoveryError
	err := filepath.WalkDir(root, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if p == root {
			return nil
		}
		if filepath.Dir(p) == root && d.IsDir() && slices.Contains(placedFolders, d.Name()) {
			return filepath.SkipDir
		}
		if d.Type()&os.ModeSymlink != 0 {
			return fmt.Errorf("symlink in isolated CLI state: %s", p)
		}
		n := d.Name()
		if n == "config.toml" || strings.HasSuffix(n, ".config.toml") || n == "settings.json" || n == "settings.local.json" || n == "plugins" || n == "skills" || n == "hooks" || n == "rules" {
			if discovery == nil {
				discovery = &PrelaunchDiscoveryError{Path: p}
			}
			return nil
		}
		for _, name := range discoveryNames {
			if n == name {
				if discovery == nil {
					discovery = &PrelaunchDiscoveryError{Path: p}
				}
				return nil
			}
		}
		return nil
	})
	if err != nil {
		return err
	}
	if discovery != nil {
		return discovery
	}
	return nil
}

func filteredEnvKey(k string) bool {
	u := strings.ToUpper(k)
	switch u {
	case "LYK_TOKEN", "LYK_CONFIG", "LYK_SERVER", "CLAUDECODE", "CLAUDE_CODE_ENTRYPOINT", "CLAUDE_CODE_EXECPATH", "CLAUDE_CODE_SESSION_ID", "CLAUDE_CODE_SSE_PORT", "CODEX_THREAD_ID", "CODEX_SESSION_ID", "CODEX_APP_TOOLS_PIPE_PATH", "CODEX_INTERNAL_ORIGINATOR_OVERRIDE", "CODEX_CONFIG", "CODEX_CONFIG_TOML", "CODEX_MANAGED_CONFIG_PATH", "HOME", "USERPROFILE", "CLAUDE_CONFIG_DIR", "CODEX_HOME", "CURSOR_DATA_DIR", "GEMINI_CLI_HOME", "XDG_CONFIG_HOME", "XDG_CONFIG_DIRS", "XDG_DATA_HOME", "XDG_DATA_DIRS", "XDG_CACHE_HOME", "XDG_STATE_HOME", "BASH_ENV", "ENV", "ZDOTDIR":
		return true
	}
	return strings.HasPrefix(u, "CLAUDECODE_") || strings.HasPrefix(u, "LYKEION_")
}

// ChildEnv is the environment a run's own child processes get: the one the
// CLI gets, with the run's config home.
func ChildEnv(o ExecOptions) []string { return childEnv(o, filepath.Join(o.Dir, ".cli-home")) }

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
	if o.Inherit {
		for _, entry := range os.Environ() {
			k, v, ok := strings.Cut(entry, "=")
			if ok && inheritedEnvKey(k) {
				values[k] = v
			}
		}
		return envList(values)
	}
	userHome := filepath.Join(o.Dir, ".home")
	if runtime.GOOS == "darwin" {
		rest := values["PATH"]
		if rest == "" {
			rest = "/usr/bin:/bin:/usr/sbin:/sbin"
		}
		values["PATH"] = filepath.Join(home, keychainStandIn) + string(filepath.ListSeparator) + rest
	}
	values["HOME"] = userHome
	values["USERPROFILE"] = userHome
	values["CODEX_HOME"] = home
	values["CLAUDE_CONFIG_DIR"] = home
	values["CURSOR_DATA_DIR"] = home
	values["GEMINI_CLI_HOME"] = home
	values["XDG_CONFIG_HOME"] = filepath.Join(userHome, ".config")
	values["XDG_CONFIG_DIRS"] = values["XDG_CONFIG_HOME"]
	values["XDG_DATA_HOME"] = filepath.Join(userHome, ".local", "share")
	values["XDG_DATA_DIRS"] = values["XDG_DATA_HOME"]
	values["XDG_CACHE_HOME"] = filepath.Join(userHome, ".cache")
	values["XDG_STATE_HOME"] = filepath.Join(userHome, ".state")
	return envList(values)
}

// inheritedEnvKey names the account locations a continuity run keeps as-is.
func inheritedEnvKey(k string) bool {
	switch strings.ToUpper(k) {
	case "HOME", "USERPROFILE", "CLAUDE_CONFIG_DIR", "CODEX_HOME", "CURSOR_DATA_DIR", "GEMINI_CLI_HOME", "XDG_CONFIG_HOME", "XDG_CONFIG_DIRS", "XDG_DATA_HOME", "XDG_DATA_DIRS", "XDG_CACHE_HOME", "XDG_STATE_HOME":
		return true
	}
	return false
}
func envList(values map[string]string) []string {
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
