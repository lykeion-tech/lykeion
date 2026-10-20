package agent

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
)

// scratchCleanup relinquishes the scratch lock after output accounting finishes.
type scratchCleanup func(release bool)

// RunFolderPath is a file or folder in a run's folder that a CLI or the
// daemon keeps for itself.
type RunFolderPath struct {
	Name   string
	Folder bool
	// A marker means something by existing (a lock, a live process), so
	// only its owner may ever create one.
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

// prepareScratch reserves the run folder and its daemon-owned accounting state.
func prepareScratch(o *ExecOptions, harness string) (home string, cleanup scratchCleanup, err error) {
	if o.Sandbox == nil {
		return "", nil, ErrNoSandbox
	}
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
	var unlock func()
	if o.ScratchReservation != nil {
		unlock, err = o.ScratchReservation.borrow(dir)
	} else {
		var r *ScratchReservation
		r, err = ReserveScratch(dir)
		if err == nil {
			unlock = r.Release
		}
	}
	if err != nil {
		return "", nil, err
	}
	var once sync.Once
	cleanup = func(release bool) {
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
	if err = privateDir(dir); err != nil {
		return "", cleanup, err
	}
	o.Dir = dir
	marker := filepath.Join(dir, ".agent-owned")
	if st, e := os.Lstat(marker); e == nil && !st.Mode().IsRegular() {
		return "", cleanup, errors.New("scratch ownership marker is not a regular file")
	} else if e != nil && !errors.Is(e, fs.ErrNotExist) {
		return "", cleanup, e
	}
	owned, e := os.ReadFile(marker)
	if e != nil && !errors.Is(e, fs.ErrNotExist) {
		return "", cleanup, e
	}
	if e == nil && string(owned) != "lykeion-agent-v1:"+harness {
		return "", cleanup, errors.New("scratch belongs to a different harness")
	}
	if errors.Is(e, fs.ErrNotExist) {
		if err = privateFile(marker, []byte("lykeion-agent-v1:"+harness)); err != nil {
			return "", cleanup, err
		}
	}
	home = filepath.Join(dir, ".cli-home")
	if err = privateDir(home); err != nil {
		return "", cleanup, err
	}
	if err = placeSkills(o, harness); err != nil {
		return "", cleanup, err
	}
	o.CLIPaths, err = SandboxPaths(harness, accountHome(harness), dir, o.Resume, runtime.GOOS)
	if err != nil {
		return "", cleanup, err
	}
	if runtime.GOOS == "linux" {
		if err = PlaceBlocked(o.CLIPaths); err != nil {
			return "", cleanup, err
		}
	}
	success = true
	return home, cleanup, nil
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
func ChildEnv(o ExecOptions) []string { return childEnv(o, o.CacheDir) }

func childEnv(o ExecOptions, cacheDir string) []string {
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
	for _, entry := range os.Environ() {
		k, v, ok := strings.Cut(entry, "=")
		if ok && inheritedEnvKey(k) {
			values[k] = v
		}
	}
	if cacheDir != "" {
		for k, sub := range map[string]string{"npm_config_cache": "npm", "UV_CACHE_DIR": "uv", "UV_PYTHON_INSTALL_DIR": "uv-python", "UV_TOOL_DIR": "uv-tools"} {
			values[k] = filepath.Join(cacheDir, sub)
		}
	}
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
