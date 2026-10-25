package envs

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"
)

var setupLimit = 30 * time.Minute
var pythonVersionPattern = regexp.MustCompile(`^3\.(9|1[0-4])$`)

func ValidPythonVersion(v string) bool { return pythonVersionPattern.MatchString(v) }
func PythonConflict(v string) error {
	return fmt.Errorf("This study uses Python %s; a study keeps one Python version.", v)
}

type studyState struct {
	version    string
	generation uint64
}

func (s *Study) state() studyState {
	s.Bases.mu.Lock()
	defer s.Bases.mu.Unlock()
	if s.Bases.studies == nil {
		s.Bases.studies = map[string]studyState{}
	}
	state := s.Bases.studies[s.Dir]
	if state.version == "" {
		state.version = s.PythonVersion
		s.Bases.studies[s.Dir] = state
	}
	return state
}
func (s *Study) RecordedPython() string   { return s.state().version }
func (s *Study) PythonGeneration() uint64 { return s.state().generation }
func (s *Study) RememberPython(v string) error {
	if !ValidPythonVersion(v) {
		return errors.New("python must be a version from 3.9 to 3.14")
	}
	s.state()
	s.Bases.mu.Lock()
	defer s.Bases.mu.Unlock()
	state := s.Bases.studies[s.Dir]
	if state.version != "" && state.version != v {
		return PythonConflict(state.version)
	}
	state.version = v
	s.Bases.studies[s.Dir] = state
	return nil
}
func (s *Study) pin(ctx context.Context, v string) error {
	if old := s.RecordedPython(); old != "" && old != v {
		return PythonConflict(old)
	}
	if s.PersistPython != nil {
		if err := s.PersistPython(ctx, v); err != nil {
			return err
		}
	}
	return s.RememberPython(v)
}

// studyLock is shared by runs of the same study, including a newly claimed
// Study after its folders have been removed. Waiting consumes the call limit.
func (s *Study) studyLock(ctx context.Context) (func(), error) {
	l := s.Bases.lock("study:" + s.Dir)
	for !l.TryLock() {
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-time.After(10 * time.Millisecond):
		}
	}
	if err := ctx.Err(); err != nil {
		l.Unlock()
		return nil, err
	}
	return l.Unlock, nil
}

// CheckDefaultPython notices installs from any language or script. The
// regular package inventory remains independent of this one-time pin check.
func (s *Study) CheckDefaultPython(ctx context.Context) error {
	if s.Run == nil {
		return errNoRunner
	}
	unlock, err := s.studyLock(ctx)
	if err != nil {
		return err
	}
	defer unlock()
	return s.checkDefaultPython(ctx)
}
func (s *Study) checkDefaultPython(ctx context.Context) error {
	if s.RecordedPython() != "" || !s.hasVenv() {
		return nil
	}
	out, err := s.Run(ctx, nil, s.Dir, filepath.Join(s.Dir, "python", "bin", "python"), "-m", "pip", "freeze", "--local")
	if err != nil {
		return err
	}
	if strings.TrimSpace(string(out)) != "" {
		return s.pin(ctx, "3.13")
	}
	return nil
}
func (s *Study) condaReady() bool {
	fi, err := os.Lstat(filepath.Join(s.Dir, "conda", ".lyk-ready"))
	return err == nil && fi.Mode().IsRegular()
}
func (s *Study) condaPython() bool {
	fi, err := os.Stat(filepath.Join(s.Dir, "conda", "bin", "python"))
	return err == nil && !fi.IsDir()
}

func (s *Study) SetUpConda(ctx context.Context, python string, pkgs []string, lock string) (string, error) {
	if s.Run == nil {
		return "", errNoRunner
	}
	ctx, cancel := context.WithTimeout(ctx, setupLimit)
	defer cancel()
	unlock, err := s.studyLock(ctx)
	if err != nil {
		return "", err
	}
	defer unlock()
	out, err := s.setUpConda(ctx, python, pkgs, lock)
	if err != nil {
		return out, setupFailure{err}
	}
	return out, nil
}
func (s *Study) setUpConda(ctx context.Context, python string, pkgs []string, lock string) (string, error) {
	if python != "" && !ValidPythonVersion(python) {
		return "", errors.New("python must be a version from 3.9 to 3.14")
	}
	for _, p := range pkgs {
		if strings.TrimSpace(p) == "" || strings.HasPrefix(p, "-") || strings.Contains(p, "::") || strings.ContainsAny(p, "/\\\x00\r\n") {
			return "", errors.New("conda packages must be package specs from conda-forge or bioconda")
		}
	}
	if old := s.RecordedPython(); old != "" && python != "" && old != python {
		return "", PythonConflict(old)
	}
	if err := s.checkDefaultPython(ctx); err != nil {
		return "", err
	}
	if python != "" {
		if err := s.pin(ctx, python); err != nil {
			return "", err
		}
	}
	if !s.Bases.Ready(Python) {
		return "", notReady(Python)
	}
	prefix := filepath.Join(s.Dir, "conda")
	ready := s.condaReady()
	hadPython := s.condaPython()
	action := "install"
	if !ready {
		action = "create"
	}
	if !ready {
		if _, err := s.Run(ctx, nil, s.Dir, "/bin/sh", "-c", `rm -rf -- "$1"`, "sh", prefix); err != nil {
			return "", err
		}
	}
	mm := filepath.Join(s.Bases.Dir, "micromamba", micromambaVersion, "micromamba")
	env := []string{"MAMBA_ROOT_PREFIX=" + filepath.Join(s.Dir, ".mamba")}
	args := []string{"--no-rc", action, "-y", "-p", prefix, "-c", "conda-forge", "-c", "bioconda", "--override-channels"}
	v := s.RecordedPython()
	if v != "" {
		args = append(args, "python="+v, "ipykernel")
	}
	args = append(args, pkgs...)
	if lock != "" {
		args = append(args, "--file", lock)
	}
	plan, err := s.Run(ctx, env, s.Dir, mm, append(append([]string{}, args...), "--dry-run", "--json")...)
	if err != nil {
		return tail(string(plan), 4096), err
	}
	var result struct {
		Success bool `json:"success"`
		Actions struct {
			Link []struct {
				Name    string `json:"name"`
				Version string `json:"version"`
			} `json:"LINK"`
		} `json:"actions"`
	}
	if err := json.Unmarshal(plan, &result); err != nil {
		return "", fmt.Errorf("reading conda resolution: %w", err)
	}
	if !result.Success {
		return "", fmt.Errorf("conda could not resolve the environment: %s", tail(string(plan), 4096))
	}
	resolved := ""
	for _, pkg := range result.Actions.Link {
		if pkg.Name == "python" {
			parts := strings.Split(pkg.Version, ".")
			if len(parts) >= 2 {
				resolved = strings.Join(parts[:2], ".")
			}
			if !ValidPythonVersion(resolved) {
				return "", errors.New("conda resolved an unsupported Python version")
			}
		}
	}
	if resolved != "" {
		if err := s.pin(ctx, resolved); err != nil {
			return "", err
		}
	}
	// A dependency or explicit lock can supply Python without the python
	// argument. Ensure it has the kernel after applying that exact input.
	if _, err := s.Run(ctx, nil, s.Dir, "/bin/sh", "-c", `rm -f -- "$1/.lyk-ready"`, "sh", prefix); err != nil {
		return "", err
	}
	out, err := s.Run(ctx, env, s.Dir, mm, args...)
	if err != nil {
		return tail(string(out), 4096), err
	}
	if s.condaPython() {
		actual, err := s.Run(ctx, env, s.Dir, filepath.Join(prefix, "bin", "python"), "-I", "-c", `import sys; print('%d.%d' % sys.version_info[:2])`)
		if err != nil {
			return tail(string(out), 4096), err
		}
		actualVersion := strings.TrimSpace(string(actual))
		if err := s.pin(ctx, actualVersion); err != nil {
			return "", err
		}
		if v == "" || lock != "" {
			extra, err := s.Run(ctx, env, s.Dir, mm, "--no-rc", "install", "-y", "-p", prefix, "-c", "conda-forge", "-c", "bioconda", "--override-channels", "python="+actualVersion, "ipykernel")
			out = append(out, extra...)
			if err != nil {
				return tail(string(out), 4096), err
			}
		}
		if !ready || !hadPython || !s.hasVenv() {
			if err := s.condaVenv(ctx); err != nil {
				return tail(string(out), 4096), err
			}
		}
	}
	exported, err := s.Run(ctx, env, s.Dir, mm, "--no-rc", "env", "export", "-p", prefix, "--explicit")
	if err != nil {
		return tail(string(out), 4096), err
	}
	// Both files are written in the sandbox: study code may have planted links.
	if _, err := s.Run(ctx, nil, s.Dir, "/bin/sh", "-c", `printf '%s' "$2" > "$1/lock.txt" && : > "$1/.lyk-ready"`, "sh", prefix, string(exported)); err != nil {
		return tail(string(out), 4096), err
	}
	return tail(string(out), 4096), nil
}
func (s *Study) condaVenv(ctx context.Context) error {
	_, err := s.Run(ctx, nil, s.Dir, filepath.Join(s.Dir, "conda", "bin", "python"), "-m", "venv", "--clear", "--system-site-packages", filepath.Join(s.Dir, "python"))
	if err == nil {
		s.Bases.mu.Lock()
		state := s.Bases.studies[s.Dir]
		state.generation++
		s.Bases.studies[s.Dir] = state
		s.Bases.mu.Unlock()
	}
	return err
}

// Preserve the original cause for cancellation checks while returning the
// installer's final diagnostics instead of an unbounded command transcript.
type setupFailure struct{ error }

func (e setupFailure) Error() string { return tail(e.error.Error(), 4096) }
func (e setupFailure) Unwrap() error { return e.error }
