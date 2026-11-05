//go:build darwin

package agent

import (
	"errors"
	"os"
	"path/filepath"
	"syscall"
)

func mcpDescriptor(data []byte) (*os.File, error) {
	dir, err := os.MkdirTemp("/private/tmp", "lyk-mcp-")
	if err != nil {
		return nil, err
	}
	name := filepath.Join(dir, "config.json")
	f, err := os.OpenFile(name, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
	if err != nil {
		return nil, errors.Join(err, os.Remove(dir))
	}
	original, statErr := f.Stat()
	if statErr != nil {
		_ = f.Close()
		return nil, errors.Join(statErr, errors.New("private MCP configuration retained because identity could not be verified"))
	}
	if _, err = f.Write(data); err == nil {
		err = f.Chmod(0400)
	}
	err = errors.Join(err, f.Close())
	if err != nil {
		return nil, errors.Join(err, removeCreatedMCP(name, original))
	}
	f, err = os.OpenFile(name, os.O_RDONLY|syscall.O_NOFOLLOW, 0)
	if err == nil {
		var opened os.FileInfo
		opened, err = f.Stat()
		if err == nil && !os.SameFile(original, opened) {
			err = errors.New("MCP configuration identity changed")
		}
	}
	if err != nil {
		if f != nil {
			_ = f.Close()
		}
		return nil, errors.Join(err, removeCreatedMCP(name, original))
	}
	return f, err
}

func removeCreatedMCP(name string, original os.FileInfo) error {
	current, err := os.Lstat(name)
	if err != nil || !os.SameFile(original, current) {
		return errors.Join(errors.New("private MCP configuration changed during setup; retained"), err)
	}
	if err = os.Remove(name); err != nil {
		return err
	}
	return os.Remove(filepath.Dir(name))
}
