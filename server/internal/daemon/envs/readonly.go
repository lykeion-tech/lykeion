package envs

import (
	"context"
	"os"
	"path/filepath"
	"time"
)

// Ready checks existing project state without creating, repairing or executing it.
func (s *Study) Ready(lang string) error {
	if s.Bases == nil || !s.Bases.Ready(lang) {
		return notReady(lang)
	}
	info, err := os.Stat(filepath.Join(s.Dir, "python", "bin", "python"))
	if err != nil || !info.Mode().IsRegular() || info.Mode().Perm()&0111 == 0 {
		return notReady(Python)
	}
	version := s.RecordedPython()
	_, condaErr := os.Lstat(filepath.Join(s.Dir, "conda"))
	if (condaErr != nil && !os.IsNotExist(condaErr)) || (condaErr == nil && !s.condaReady()) {
		return notReady(Python)
	}
	if version != "" && version != DefaultPythonVersion && (!s.condaReady() || !s.condaPython()) {
		return notReady(Python)
	}
	switch lang {
	case R:
		if _, err := os.Stat(filepath.Join(s.Dir, "r/library")); err != nil {
			return notReady(R)
		}
		if _, err := os.Stat(filepath.Join(s.Dir, "r/Rprofile")); err != nil {
			return notReady(R)
		}
	case Julia:
		if _, err := os.Stat(filepath.Join(s.Dir, "julia/Project.toml")); err != nil {
			return notReady(Julia)
		}
	}
	return nil
}

// ExecutionLease serializes complete recorded executions and explicit setups.
// It is separate from preparation's lock, which those operations also acquire.
func (s *Study) ExecutionLease(ctx context.Context) (func(), error) {
	lock := s.Bases.lock("execution:" + s.Dir)
	for !lock.TryLock() {
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-time.After(10 * time.Millisecond):
		}
	}
	if err := ctx.Err(); err != nil {
		lock.Unlock()
		return nil, err
	}
	return lock.Unlock, nil
}
