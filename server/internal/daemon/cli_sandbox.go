package daemon

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"slices"
	"strings"

	"lykeion/server/internal/daemon/envs"
	"lykeion/server/internal/daemon/sandbox"
	"lykeion/server/pkg/agent"
)

var homeSecrets = []string{".ssh", ".aws", ".gnupg", ".config/gh", ".netrc", ".docker/config.json", ".kube", ".npmrc", ".pypirc", ".git-credentials"}

func (d *daemon) cacheDir() string { return filepath.Join(d.cfg.Home, ".caches") }

func (d *daemon) cliPolicy(runDir string, study *envs.Study, readOnly bool, cli agent.CLIPaths) (sandbox.Policy, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return sandbox.Policy{}, err
	}
	p := sandbox.Policy{Home: home, HomeRead: true, Write: append([]string{d.cacheDir()}, cli.Write...), WriteData: cli.WriteData, WriteFiles: cli.WriteFiles, WriteSQLite: cli.WriteSQLite, Blocked: cli.Blocked, Keychain: cli.Keychain}
	if err = os.MkdirAll(d.cacheDir(), 0700); err != nil {
		return p, err
	}
	fi, err := os.Lstat(d.cacheDir())
	if err != nil {
		return p, err
	}
	if !fi.IsDir() || fi.Mode()&os.ModeSymlink != 0 {
		return p, errors.New("cache folder is not a real directory")
	}
	if err = os.Chmod(d.cacheDir(), 0700); err != nil {
		return p, err
	}
	folders := []string{runDir}
	if study != nil {
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
	if d.cfg.StateDir != "" {
		p.Hidden = append(p.Hidden, d.cfg.StateDir)
	}
	cfg, err := os.UserConfigDir()
	if err != nil {
		return p, err
	}
	p.Hidden = append(p.Hidden, filepath.Join(cfg, "lyk", "config.json"))
	if actual := os.Getenv("LYK_CONFIG"); actual != "" {
		actual, err = filepath.Abs(actual)
		if err != nil {
			return p, err
		}
		p.Hidden = append(p.Hidden, actual)
	}
	roots := []string{d.cfg.Home, filepath.Join(home, ".lyk")}
	if d.bases != nil {
		roots = append(roots, d.bases.Dir)
	}
	for _, root := range roots {
		if err := sandbox.PrepareRestrictedRoot(home, root); err != nil {
			return p, err
		}
		p.RestrictedRoots = append(p.RestrictedRoots, root)
	}
	if cli.Keychain && runtime.GOOS == "darwin" {
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

func (d *daemon) cliSandbox(runDir string, study *envs.Study, readOnly bool) agent.SandboxCommand {
	return func(ctx context.Context, cli agent.CLIPaths, name string, args ...string) (*exec.Cmd, error) {
		if d.sandboxErr != nil {
			return nil, fmt.Errorf("CLI sandbox unavailable: %w", d.sandboxErr)
		}
		p, err := d.cliPolicy(runDir, study, readOnly, cli)
		if err != nil {
			return nil, err
		}
		if runtime.GOOS == "linux" {
			if err := protectCLIExecutable(&p, cli, name); err != nil {
				return nil, err
			}
		}
		tmp, err := os.MkdirTemp("", "lyk-cli-")
		if err != nil {
			return nil, err
		}
		var placeholders []placeholder
		cleanup := func() {
			for _, ph := range placeholders {
				ph.remove()
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
		envArgs := []string{"TMPDIR=" + tmp, "TMP=" + tmp, "TEMP=" + tmp, "npm_config_cache=" + filepath.Join(d.cacheDir(), "npm"), "UV_CACHE_DIR=" + filepath.Join(d.cacheDir(), "uv"), "UV_PYTHON_INSTALL_DIR=" + filepath.Join(d.cacheDir(), "uv-python"), "UV_TOOL_DIR=" + filepath.Join(d.cacheDir(), "uv-tools"), name}
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
