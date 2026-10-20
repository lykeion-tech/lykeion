// Package kernels runs a run's live Python, R and Julia sessions. A Python
// bridge started in the study's environment keeps one Jupyter kernel per
// language and talks JSON lines with the daemon.
package kernels

import (
	"bufio"
	"context"
	_ "embed"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"sync"
	"syscall"
	"time"
)

//go:embed bridge.py
var bridgePy string

// CellTimeout is how long one cell may run before its session restarts.
var CellTimeout = 10 * time.Minute

// restartGrace is how long past CellTimeout the bridge has to interrupt and
// restart a kernel before the daemon gives up on the bridge itself.
const restartGrace = 5 * time.Minute

type Result struct {
	Stdout, Error string
	OK            bool
}

type Sessions struct {
	python string
	env    []string
	dir    string

	mu     sync.Mutex
	b      *bridge
	nextID int
}

type reply struct {
	ID        int    `json:"id"`
	Stdout    string `json:"stdout"`
	Error     string `json:"error"`
	OK        bool   `json:"ok"`
	Restarted bool   `json:"restarted"`
}

type bridge struct {
	cmd     *exec.Cmd
	in      io.WriteCloser
	replies chan reply
	done    chan struct{}
}

// Start writes the bridge into dir and starts it there with env as its whole
// environment. Kernels start on each language's first cell.
func Start(ctx context.Context, python string, env []string, dir string) (*Sessions, error) {
	if err := os.WriteFile(filepath.Join(dir, ".lyk-bridge.py"), []byte(bridgePy), 0o644); err != nil {
		return nil, err
	}
	s := &Sessions{python: python, env: env, dir: dir}
	b, err := s.start()
	if err != nil {
		return nil, err
	}
	s.b = b
	return s, nil
}

func (s *Sessions) start() (*bridge, error) {
	cmd := exec.Command(s.python, ".lyk-bridge.py")
	cmd.Env = s.env
	cmd.Dir = s.dir
	cmd.Stderr = os.Stderr
	in, err := cmd.StdinPipe()
	if err != nil {
		return nil, err
	}
	out, err := cmd.StdoutPipe()
	if err != nil {
		return nil, err
	}
	if err := cmd.Start(); err != nil {
		return nil, fmt.Errorf("starting the session bridge: %w", err)
	}
	b := &bridge{cmd: cmd, in: in, replies: make(chan reply, 1), done: make(chan struct{})}
	go func() {
		sc := bufio.NewScanner(out)
		sc.Buffer(nil, 64<<20)
		for sc.Scan() {
			var r reply
			if json.Unmarshal(sc.Bytes(), &r) == nil {
				b.replies <- r
			}
		}
		cmd.Wait()
		close(b.done)
	}()
	return b, nil
}

// Exec runs one cell. Calls run one at a time. If the bridge has exited, the
// call fails and the next one starts a new bridge with fresh sessions.
func (s *Sessions) Exec(ctx context.Context, lang, code string) (Result, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.b == nil {
		b, err := s.start()
		if err != nil {
			return Result{}, err
		}
		s.b = b
	}
	b := s.b
	s.nextID++
	id := s.nextID
	req, err := json.Marshal(map[string]any{"id": id, "op": "exec", "language": lang, "code": code, "timeout": CellTimeout.Seconds()})
	if err != nil {
		return Result{}, err
	}
	gone := func() (Result, error) {
		s.b = nil
		return Result{}, errors.New("the session bridge stopped; its sessions and their variables are gone")
	}
	if _, err := b.in.Write(append(req, '\n')); err != nil {
		stop(b)
		return gone()
	}
	limit := time.NewTimer(CellTimeout + restartGrace)
	defer limit.Stop()
	for {
		select {
		case r := <-b.replies:
			// A reply to a call abandoned through its context is skipped.
			if r.ID == id {
				return Result{Stdout: r.Stdout, Error: r.Error, OK: r.OK}, nil
			}
		case <-b.done:
			return gone()
		case <-limit.C:
			stop(b)
			return gone()
		case <-ctx.Done():
			return Result{}, ctx.Err()
		}
	}
}

// Close shuts the kernels down through the bridge. A bridge that doesn't exit
// is told to stop, which it also handles by shutting its kernels down, and is
// killed last.
func (s *Sessions) Close() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.b == nil {
		return nil
	}
	b := s.b
	s.b = nil
	b.in.Write([]byte(`{"op":"shutdown"}` + "\n"))
	b.in.Close()
	if !exited(b, 10*time.Second) {
		stop(b)
	}
	return nil
}

func stop(b *bridge) {
	b.cmd.Process.Signal(syscall.SIGTERM)
	if !exited(b, 5*time.Second) {
		b.cmd.Process.Kill()
		exited(b, 10*time.Second)
	}
}

// exited waits up to d for the bridge to exit, discarding replies nobody is
// waiting for so the reader can reach the end of the pipe.
func exited(b *bridge, d time.Duration) bool {
	timeout := time.After(d)
	for {
		select {
		case <-b.replies:
		case <-b.done:
			return true
		case <-timeout:
			return false
		}
	}
}
