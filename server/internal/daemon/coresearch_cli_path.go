package daemon

import (
	"io"
	"os"
	"path/filepath"
)

// coresearchCLIPath creates a run-private, read-only sandbox projection of the
// daemon executable under the command name expected by Coresearch prompts.
func coresearchCLIPath(executable string) (string, func(), error) {
	source, err := filepath.EvalSymlinks(executable)
	if err != nil {
		return "", nil, err
	}
	info, err := os.Stat(source)
	if err != nil {
		return "", nil, err
	}
	if !info.Mode().IsRegular() || info.Mode()&0111 == 0 {
		return "", nil, os.ErrPermission
	}
	dir, err := os.MkdirTemp("", "lyk-coresearch-bin-")
	if err != nil {
		return "", nil, err
	}
	created := dir
	cleanup := func() { _ = os.RemoveAll(created) }
	dir, err = filepath.EvalSymlinks(dir)
	if err != nil {
		cleanup()
		return "", nil, err
	}
	destination := filepath.Join(dir, "lyk")
	if err = os.Link(source, destination); err != nil {
		var in *os.File
		in, err = os.Open(source)
		if err == nil {
			var out *os.File
			out, err = os.OpenFile(destination, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0500)
			if err == nil {
				_, err = io.Copy(out, in)
				if closeErr := out.Close(); err == nil {
					err = closeErr
				}
			}
			_ = in.Close()
		}
	}
	if err != nil {
		cleanup()
		return "", nil, err
	}
	return dir, cleanup, nil
}
