package daemon

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"slices"
	"strings"

	"lykeion/server/internal/configpath"
	"lykeion/server/internal/daemon/envs"
	"lykeion/server/internal/daemon/sandbox"
	"lykeion/server/pkg/agent"
)

var homeSecrets = []string{".ssh", ".aws", ".gnupg", ".config/gh", ".netrc", ".docker/config.json", ".kube", ".npmrc", ".pypirc", ".git-credentials"}

func (d *daemon) cacheDir() string { return filepath.Join(d.cfg.Home, ".caches") }

// sourcesURL is the sources server on the API this daemon reaches. The URL the
// server names is built from the dashboard's public origin, which need not
// serve the API (a separate dev server, a static host).
func (d *daemon) sourcesURL() string { return strings.TrimRight(d.cfg.Server, "/") + "/mcp/sources" }

// privatePaths remain excluded even when this run starts no services: another
// run can write private service state or diagnostics while this one is active.
func (d *daemon) privatePaths() ([]string, error) {
	paths, err := configpath.Private()
	if err != nil {
		return nil, err
	}
	if d.cfg.StateDir != "" {
		paths = append([]string{filepath.Join(d.cfg.StateDir, "services"), filepath.Join(filepath.Dir(d.cfg.StateDir), "daemon.log"), d.cfg.StateDir}, paths...)
	}
	return paths, nil
}

func (d *daemon) cliPolicy(runDir string, study *envs.Study, readOnly bool, cli agent.CLIPaths, guests ...string) (sandbox.Policy, error) {
	guest := ""
	if len(guests) > 0 {
		guest = guests[0]
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return sandbox.Policy{}, err
	}
	p := sandbox.Policy{Home: home, HomeRead: true, Write: append([]string{d.cacheDir()}, cli.Write...), WriteData: cli.WriteData, WriteFiles: cli.WriteFiles, WriteSQLite: cli.WriteSQLite, Blocked: cli.Blocked, Keychain: cli.Keychain}
	// bubblewrap cannot grant a path that does not exist yet without opening
	// its whole folder, so on Linux the lock folders stay closed.
	if runtime.GOOS == "darwin" {
		p.LockDirs = cli.LockDirs
	}
	cache := d.runCacheDir(guest)
	if guest != "" {
		harness := filepath.Base(cli.ConfigHome)
		if d.cfg.StateDir == "" || (harness != "claude" && harness != "codex") || cli.ConfigHome != filepath.Join(d.cfg.StateDir, "guests", guest, harness) {
			return p, errors.New("guest CLI home does not match responsible user")
		}
		p = sandbox.Policy{StrictRead: true, Home: home, Write: []string{cache, cli.ConfigHome}, Blocked: cli.Blocked}
	}
	if err = os.MkdirAll(cache, 0700); err != nil {
		return p, err
	}
	fi, err := os.Lstat(cache)
	if err != nil {
		return p, err
	}
	if !fi.IsDir() || fi.Mode()&os.ModeSymlink != 0 {
		return p, errors.New("cache folder is not a real directory")
	}
	if err = os.Chmod(cache, 0700); err != nil {
		return p, err
	}
	folders := []string{runDir}
	if study != nil {
		if err := study.Bases.EnsureLeanRoot(); err != nil {
			return p, err
		}
		folders = append(folders, study.Dir)
		for _, base := range study.Bases.Folders() {
			if isDir(base) {
				p.Read = append(p.Read, base)
			}
		}
	}
	if readOnly {
		p.Read = append(p.Read, folders...)
	} else {
		p.Write = append(p.Write, folders...)
	}
	for _, secret := range homeSecrets {
		p.Hidden = append(p.Hidden, filepath.Join(home, secret))
	}
	private, err := d.privatePaths()
	if err != nil {
		return p, err
	}
	roots := []string{d.cfg.Home, filepath.Join(home, ".lyk")}
	if guest != "" {
		private = slices.DeleteFunc(private, func(path string) bool { return path == d.cfg.StateDir })
		roots = append(roots, d.cfg.StateDir, d.cacheDir())
		for _, path := range []string{filepath.Join(home, ".claude"), filepath.Join(home, ".claude.json"), filepath.Join(home, ".codex"), agent.AccountHome("claude"), agent.AccountHome("codex")} {
			if path != "" && !slices.Contains(p.Hidden, path) {
				p.Hidden = append(p.Hidden, path)
			}
		}
	}
	p.Hidden = append(p.Hidden, private...)
	if guest == "" && cli.ManagedSkills == "" {
		// Coresearch projects the account's customizations itself. A hidden
		// file stays hidden even where the CLI would otherwise write it.
		p.Hidden = append(p.Hidden, cli.Hidden...)
		p.WriteFiles = slices.DeleteFunc(slices.Clone(p.WriteFiles), func(path string) bool { return slices.Contains(cli.Hidden, path) })
	}
	if d.bases != nil {
		roots = append(roots, d.bases.Dir)
	}
	for _, root := range roots {
		if err := sandbox.PrepareRestrictedRoot(home, root); err != nil {
			return p, err
		}
		p.RestrictedRoots = append(p.RestrictedRoots, root)
	}
	// lyk is on the CLI's PATH as this executable. Running it needs no read
	// grant, but a shell looking it up (which, command -v) stats it, so it is
	// readable wherever the person's own reads don't already reach it.
	if self, err := os.Executable(); err == nil {
		if self, err = filepath.EvalSymlinks(self); err == nil && ((guest != "" && !slices.ContainsFunc(p.Write, func(dir string) bool { return insideStorage(dir, self) })) || (guest == "" && !insideStorage(home, self) &&
			!slices.ContainsFunc(slices.Concat(p.RestrictedRoots, p.Write), func(dir string) bool { return insideStorage(dir, self) }))) {
			p.Read = append(p.Read, self)
		}
	}
	if p.Keychain && runtime.GOOS == "darwin" {
		root := filepath.Join(home, "Library", "Keychains")
		err = filepath.WalkDir(root, func(path string, e os.DirEntry, err error) error {
			if errors.Is(err, os.ErrNotExist) {
				return nil
			}
			if err != nil {
				return err
			}
			if e.Type().IsRegular() && !(filepath.Dir(path) == root && sandbox.KeychainFile(e.Name())) {
				p.WriteData = append(p.WriteData, path)
			}
			return nil
		})
		if err != nil {
			return p, fmt.Errorf("keychain files: %w", err)
		}
	}
	return p, nil
}

func (d *daemon) runCacheDir(guest string) string {
	if guest != "" {
		return filepath.Join(d.cacheDir(), "guests", guest)
	}
	return d.cacheDir()
}

func (d *daemon) cliSandbox(runDir string, study *envs.Study, readOnly bool, guests ...string) agent.SandboxCommand {
	guest := ""
	if len(guests) > 0 {
		guest = guests[0]
	}
	return func(ctx context.Context, cli agent.CLIPaths, name string, args ...string) (*exec.Cmd, error) {
		chat := d.hostForFolder(runDir)
		if chat != nil {
			if err := d.checkChat(ctx, chat.work); err != nil {
				return nil, err
			}
			if err := d.markChatWriter(chat); err != nil {
				return nil, err
			}
		}
		if d.sandboxErr != nil {
			return nil, sandbox.PolicyError(fmt.Errorf("CLI sandbox unavailable: %w", d.sandboxErr))
		}
		p, err := d.cliPolicy(runDir, study, readOnly, cli, guest)
		if err != nil {
			return nil, err
		}
		if runtime.GOOS == "linux" {
			if err := protectCLIExecutable(&p, cli, name); err != nil {
				return nil, err
			}
		}
		tempRoot := ""
		if guest != "" {
			bin, err := exec.LookPath(name)
			if err != nil {
				return nil, err
			}
			bin, err = filepath.EvalSymlinks(bin)
			if err != nil {
				return nil, err
			}
			p.Read = append(p.Read, bin)
			code, err := guestCLIPackagePaths(cli, bin)
			if err != nil {
				return nil, err
			}
			p.Read = append(p.Read, code...)
			name = bin
			tempRoot = runDir
		}
		tmp, err := os.MkdirTemp(tempRoot, "lyk-cli-")
		if err != nil {
			return nil, err
		}
		var placeholders []placeholder
		cleanup := func() {
			// Guest helpers share the run's immutable configuration guards.
			// Retain them until scratch retirement so an earlier helper's
			// canceled context cannot remove a later launch's bind targets.
			if guest == "" {
				for _, ph := range placeholders {
					ph.remove()
				}
			}
			os.RemoveAll(tmp)
		}
		if !readOnly {
			if err = protectRunFolder(&p, runDir, false, &placeholders); err != nil {
				cleanup()
				return nil, err
			}
		}
		if !readOnly && chat != nil {
			protectChatCLI(&p, runDir)
		}
		if cli.ManagedSkills != "" {
			if err = protectCoresearchSkills(&p, runDir, cli, &placeholders); err != nil {
				cleanup()
				return nil, err
			}
		}
		p.Write = append(p.Write, tmp)
		envArgs := []string{"TMPDIR=" + tmp, "TMP=" + tmp, "TEMP=" + tmp, "TMPPREFIX=" + filepath.Join(tmp, "zsh"), "CLAUDE_CODE_TMPDIR=" + tmp, "npm_config_cache=" + filepath.Join(d.runCacheDir(guest), "npm"), "UV_CACHE_DIR=" + filepath.Join(d.runCacheDir(guest), "uv"), "UV_PYTHON_INSTALL_DIR=" + filepath.Join(d.runCacheDir(guest), "uv-python"), "UV_TOOL_DIR=" + filepath.Join(d.runCacheDir(guest), "uv-tools"), name}
		cmd, err := p.Command(ctx, "/usr/bin/env", append(envArgs, args...)...)
		if err != nil {
			cleanup()
			return nil, err
		}
		context.AfterFunc(ctx, cleanup)
		return cmd, nil
	}
}

// protectCLIExecutable keeps an installed CLI's code read-only when its own
// config home is writable. The protected child is an immutable bind on Linux.
func protectCLIExecutable(p *sandbox.Policy, cli agent.CLIPaths, name string) error {
	bin, err := exec.LookPath(name)
	if err != nil {
		return err
	}
	bin, err = filepath.EvalSymlinks(bin)
	if err != nil {
		return err
	}
	for _, raw := range cli.Write {
		root, err := filepath.EvalSymlinks(raw)
		if err != nil {
			return err
		}
		rel, err := filepath.Rel(root, bin)
		if err != nil {
			return err
		}
		if rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
			continue
		}
		child := strings.Split(rel, string(filepath.Separator))[0]
		if slices.Contains([]string{"sessions", "cache", "tmp", ".tmp", "shell_snapshots", "thread-writer-locks", "plugins", "auth.json", "models_cache.json", "history.jsonl", "session_index.jsonl", "installation_id", ".sqlite-maintenance.lock", "version.json"}, child) {
			return fmt.Errorf("CLI executable is inside required writable state %q", child)
		}
		if child == "." {
			return errors.New("CLI executable is a writable state root")
		}
		protected := filepath.Join(root, child)
		if !slices.Contains(p.Blocked, protected) {
			p.Blocked = append(p.Blocked, protected)
		}
	}
	return nil
}

// Codex's npm wrapper resolves a separate platform package at runtime. Grant
// those verified code packages individually, never their node_modules parent.
func guestCLIPackagePaths(cli agent.CLIPaths, bin string) ([]string, error) {
	if runtime.GOOS != "linux" || filepath.Base(cli.ConfigHome) != "codex" || filepath.Base(bin) != "codex.js" || filepath.Base(filepath.Dir(bin)) != "bin" {
		return nil, nil
	}
	root := filepath.Dir(filepath.Dir(bin))
	if filepath.Base(root) != "codex" || filepath.Base(filepath.Dir(root)) != "@openai" {
		return nil, nil
	}
	arch := ""
	switch runtime.GOARCH {
	case "amd64":
		arch = "x64"
	case "arm64":
		arch = "arm64"
	default:
		return nil, errors.New("unsupported Codex platform package")
	}
	native := filepath.Join(filepath.Dir(root), "codex-linux-"+arch)
	for path, name := range map[string]string{root: "@openai/codex", native: "@openai/codex"} {
		resolved, err := filepath.EvalSymlinks(path)
		if err != nil {
			return nil, fmt.Errorf("CLI code package: %w", err)
		}
		if resolved != path {
			return nil, errors.New("CLI code package must not be redirected")
		}
		manifest := filepath.Join(path, "package.json")
		info, err := os.Lstat(manifest)
		if err != nil {
			return nil, err
		}
		if !info.Mode().IsRegular() || info.Size() > 1<<20 {
			return nil, errors.New("invalid CLI code package manifest")
		}
		data, err := os.ReadFile(manifest)
		if err != nil {
			return nil, err
		}
		var pkg struct {
			Name string `json:"name"`
		}
		if json.Unmarshal(data, &pkg) != nil || pkg.Name != name {
			return nil, errors.New("unexpected CLI code package")
		}
	}
	return []string{root, native}, nil
}

// protectCoresearchSkills exposes the managed skill folder without exposing
// project settings or customizations from the account and ancestor folders.
func protectCoresearchSkills(p *sandbox.Policy, run string, cli agent.CLIPaths, placeholders *[]placeholder) error {
	run, err := filepath.EvalSymlinks(run)
	if err != nil {
		return err
	}
	configHome, err := filepath.EvalSymlinks(cli.ConfigHome)
	if err != nil {
		return err
	}
	claude, codex := filepath.Join(run, ".claude", "skills"), filepath.Join(run, ".agents", "skills")
	if cli.ManagedSkills != claude && cli.ManagedSkills != codex {
		return errors.New("managed skills must belong to the chat folder")
	}

	hide := func(path string) {
		if !slices.Contains(p.Hidden, path) {
			p.Hidden = append(p.Hidden, path)
		}
	}
	// The managed configuration parent is read-only already. Mask every
	// existing sibling of skills; no new sibling can be planted by the run.
	for _, name := range []string{".claude", ".codex", ".agents"} {
		path := filepath.Join(run, name)
		if name == ".codex" && cli.ManagedSkills == codex {
			// Configuration itself stays unreadable; the separate agent-definition
			// directory uses an empty projection for CLI discovery.
			config := filepath.Join(path, "config.toml")
			if _, err := os.Lstat(config); err == nil {
				hide(config)
			} else if !errors.Is(err, os.ErrNotExist) {
				return err
			}
			continue
		}
		if !insideStorage(path, cli.ManagedSkills) {
			hide(path)
			continue
		}
		entries, err := os.ReadDir(path)
		if err != nil {
			return err
		}
		for _, entry := range entries {
			if entry.Name() != "skills" {
				hide(filepath.Join(path, entry.Name()))
			}
		}
	}
	accountEntries := []string{"settings.json", "settings.local.json", "commands", "agents", "hooks", "rules", "output-styles", "workflows", "CLAUDE.md", "AGENTS.md"}
	// exec's --ignore-user-config suppresses Codex's config.toml. Leaving the
	// account file readable preserves auth and config-loader compatibility.
	if cli.ManagedSkills == claude {
		accountEntries = append(accountEntries, "config.toml", "plugins", "skills")
	}
	accountWritable := slices.Contains(p.Write, cli.ConfigHome) || slices.Contains(p.WriteSQLite, cli.ConfigHome)
	for _, entry := range accountEntries {
		if cli.ManagedSkills == codex && entry == "agents" {
			continue
		}
		target := filepath.Join(configHome, entry)
		if accountWritable {
			if _, err := os.Lstat(target); errors.Is(err, os.ErrNotExist) {
				ph, err := makePlaceholder(target, filepath.Ext(entry) == "")
				if err != nil {
					return err
				}
				*placeholders = append(*placeholders, ph)
			} else if err != nil {
				return err
			}
		}
		hide(target)
	}

	if cli.ManagedSkills == codex {
		// Project the whole customization sources so newly created siblings stay
		// excluded. Native bundled skills are excluded too; only the existing
		// plugin runtime cache keeps its state grant.

		skills := filepath.Join(configHome, "skills")
		if err := agent.PrepareCLIStateDir(configHome, skills); err != nil {
			return err
		}
		p.CarvedRoots = append(p.CarvedRoots, skills)
		p.Blocked = slices.DeleteFunc(p.Blocked, func(path string) bool {
			canonical, err := filepath.EvalSymlinks(path)
			return err == nil && canonical == skills
		})
		plugins := filepath.Join(configHome, "plugins")
		cache := filepath.Join(plugins, "cache")
		if err := agent.PrepareCLIStateDir(configHome, cache); err != nil {
			return err
		}
		p.CarvedRoots = append(p.CarvedRoots, plugins)
		if !slices.Contains(p.Write, cache) {
			p.Write = append(p.Write, cache)
		}

		agents := filepath.Join(configHome, "agents")
		if err := agent.PrepareCLIStateDir(configHome, agents); err != nil {
			return err
		}
		p.CarvedRoots = append(p.CarvedRoots, agents)
		p.Blocked = slices.DeleteFunc(p.Blocked, func(path string) bool {
			canonical, err := filepath.EvalSymlinks(path)
			return err == nil && canonical == agents
		})
		root, err := os.OpenRoot(run)
		if err != nil {
			return err
		}
		err = root.MkdirAll(filepath.Join(".codex", "agents"), 0700)
		root.Close()
		if err != nil {
			return err
		}
		p.CarvedRoots = append(p.CarvedRoots, filepath.Join(run, ".codex", "agents"))
	}

	maskParent := func(dir string) {
		for _, name := range []string{".claude", ".codex", ".agents"} {
			path := filepath.Join(dir, name)
			if path == configHome {
				continue
			}
			if name == ".agents" {
				hide(path)
				continue
			}
			// CLI installations can live inside the account's .codex or .claude
			// folder. Hide configuration entries without masking installed code.
			for _, entry := range append(accountEntries, "config.toml", "CLAUDE.md", "skills") {

				hide(filepath.Join(path, entry))
			}
		}
		for _, name := range []string{"CLAUDE.md", "CLAUDE.local.md", "AGENTS.md", "AGENTS.override.md", ".mcp.json"} {
			hide(filepath.Join(dir, name))
		}
	}

	for dir := filepath.Dir(run); ; dir = filepath.Dir(dir) {
		maskParent(dir)
		if dir == filepath.Dir(dir) {
			break
		}
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return err
	}
	maskParent(home)
	return nil
}
