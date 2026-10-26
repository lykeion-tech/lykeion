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
			if e.Type().IsRegular() {
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
		if d.sandboxErr != nil {
			return nil, fmt.Errorf("CLI sandbox unavailable: %w", d.sandboxErr)
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
