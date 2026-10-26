package daemon

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
)

// guestConnectorCommand gives a selected connector only the code sandbox and
// its private working folder. An executable grant never exposes its parent.
func (d *Door) guestConnectorCommand(ctx context.Context, name string, args ...string) (*exec.Cmd, error) {
	if err := d.sandboxReady(); err != nil {
		return nil, err
	}
	if strings.ContainsRune(name, os.PathSeparator) && !filepath.IsAbs(name) {
		name = filepath.Join(d.connectorDir, name)
	}
	bin, err := exec.LookPath(name)
	if err != nil {
		return nil, err
	}
	requested, err := filepath.Abs(bin)
	if err != nil {
		return nil, err
	}
	bin, err = filepath.EvalSymlinks(bin)
	if err != nil {
		return nil, err
	}
	for _, root := range d.policy.Write {
		resolved, err := filepath.EvalSymlinks(root)
		if err != nil {
			return nil, err
		}
		if (insideStorage(root, requested) || insideStorage(resolved, requested)) && !insideStorage(resolved, bin) {
			return nil, errors.New("connector command symlink escapes writable folder")
		}
	}
	info, err := os.Lstat(bin)
	if err != nil {
		return nil, err
	}
	if !info.Mode().IsRegular() || info.Mode().Perm()&0111 == 0 {
		return nil, errors.New("connector command is not a regular executable")
	}
	for _, dir := range []string{filepath.Join(d.connectorDir, "home"), filepath.Join(d.connectorDir, "tmp")} {
		if err := os.Mkdir(dir, 0700); err != nil && !os.IsExist(err) {
			return nil, err
		}
		info, err := os.Lstat(dir)
		if err != nil {
			return nil, err
		}
		if !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
			return nil, errors.New("connector state is not a real directory")
		}
	}
	p := d.policy
	p.Write = append(slices.Clone(p.Write), d.connectorDir)
	p.Read = slices.Clone(p.Read)
	granted := false
	for _, root := range slices.Concat(p.Read, p.Write) {
		resolved, err := filepath.EvalSymlinks(root)
		if err != nil {
			return nil, err
		}
		if insideStorage(resolved, bin) {
			granted = true
			break
		}
	}
	if !granted {
		p.Read = append(p.Read, bin)
	}
	cmd, err := p.Command(ctx, bin, args...)
	if err != nil {
		return nil, fmt.Errorf("guest connector sandbox: %w", err)
	}
	return cmd, nil
}

func (d *Door) guestConnectorEnv(extra map[string]string) []string {
	env := d.personEnv()
	for key, value := range extra {
		switch strings.ToUpper(key) {
		case "LYK_TOKEN", "LYK_DOOR", "LYK_DOOR_TOKEN", "LYK_SERVER", "LYK_CONFIG", "CLAUDE_CODE_OAUTH_TOKEN", "CLAUDE_CONFIG_DIR", "CODEX_HOME", "HOME", "USERPROFILE":
			continue
		}
		env = append(env, key+"="+value)
	}
	for key, sub := range cacheDirs {
		env = append(env, key+"="+filepath.Join(d.connectorDir, sub))
	}
	for key, sub := range map[string]string{"TMPDIR": "tmp", "TMP": "tmp", "TEMP": "tmp", "CLAUDE_CODE_TMPDIR": "tmp", "npm_config_cache": "npm", "UV_CACHE_DIR": "uv", "UV_PYTHON_INSTALL_DIR": "uv-python", "UV_TOOL_DIR": "uv-tools"} {
		env = append(env, key+"="+filepath.Join(d.connectorDir, sub))
	}
	return env
}
