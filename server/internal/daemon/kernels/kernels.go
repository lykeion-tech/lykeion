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
	"log/slog"
	"os"
	"os/exec"
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

// maxLine fits the longest reply the bridge writes: it caps stdout and error
// at 32 MiB each, measured as their JSON encoding, so a reply is those two
// plus a cut note apiece and a few short fields.
const maxLine = 2*(32<<20) + 1<<20

type Result struct {
	Stdout, Error string
	OK            bool
}

type Sessions struct {
	python string
	script string
	env    []string
	dir    string

	mu     sync.Mutex
	b      *bridge
	nextID int
	closed bool
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

// Start writes the bridge to a temporary file, which Close removes, and
// starts it in dir with env as its whole environment. Kernels start on each
// language's first cell. ctx is not kept: the bridge lives until Close, and a
// bridge that dies is started again by the next Exec.
func Start(ctx context.Context, python string, env []string, dir string) (*Sessions, error) {
	f, err := os.CreateTemp("", "lyk-bridge-*.py")
	if err != nil {
		return nil, err
	}
	_, err = f.WriteString(bridgePy)
	if cerr := f.Close(); err == nil {
		err = cerr
	}
	if err != nil {
		os.Remove(f.Name())
		return nil, err
	}
	s := &Sessions{python: python, script: f.Name(), env: env, dir: dir}
	b, err := s.start()
	if err != nil {
		os.Remove(s.script)
		return nil, err
	}
	s.b = b
	return s, nil
}

func (s *Sessions) start() (*bridge, error) {
	// -P keeps the script's and the working folder's own files off sys.path,
	// so a study's queue.py or random.py cannot replace what the bridge imports.
	cmd := exec.Command(s.python, "-P", s.script)
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
		// A bridge that breaks the protocol is told to stop, which shuts its
		// kernels down, and killed if it hasn't exited after 5 seconds.
		halt := func(msg string, err error) {
			slog.Warn(msg, "err", err)
			cmd.Process.Signal(syscall.SIGTERM)
			time.AfterFunc(5*time.Second, func() { cmd.Process.Kill() })
		}
		sc := bufio.NewScanner(out)
		sc.Buffer(nil, maxLine)
		for sc.Scan() {
			var r reply
			if err := json.Unmarshal(sc.Bytes(), &r); err != nil {
				halt("the session bridge wrote a line that isn't a reply; stopping it", err)
				break
			}
			b.replies <- r
		}
		if err := sc.Err(); err != nil {
			halt("reading the session bridge; stopping it", err)
		}
		// The bridge may still be writing; drain so Wait can return.
		io.Copy(io.Discard, out)
		cmd.Wait()
		close(b.done)
	}()
	return b, nil
}

// Exec runs one cell. Calls run one at a time. If the bridge has exited, the
// call fails and the next one starts a new bridge with fresh sessions.
func (s *Sessions) Exec(ctx context.Context, lang, code string) (Result, error) {
	r, err := s.call(ctx, map[string]any{"op": "exec", "language": lang, "code": code, "timeout": CellTimeout.Seconds()})
	return Result{Stdout: r.Stdout, Error: r.Error, OK: r.OK}, err
}

// Restart restarts lang's session, dropping its variables; a language with
// no session yet has nothing to restart.
func (s *Sessions) Restart(ctx context.Context, lang string) error {
	r, err := s.call(ctx, map[string]any{"op": "restart", "language": lang})
	if err == nil && !r.OK {
		err = errors.New(r.Error)
	}
	return err
}

// call sends req to the bridge under the next id and waits for its reply.
func (s *Sessions) call(ctx context.Context, req map[string]any) (reply, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return reply{}, errors.New("the run's sessions are closed")
	}
	if s.b == nil {
		b, err := s.start()
		if err != nil {
			return reply{}, err
		}
		s.b = b
	}
	b := s.b
	s.nextID++
	id := s.nextID
	req["id"] = id
	line, err := json.Marshal(req)
	if err != nil {
		return reply{}, err
	}
	gone := func() (reply, error) {
		s.b = nil
		return reply{}, errors.New("the session bridge stopped; its sessions and their variables are gone")
	}
	if _, err := b.in.Write(append(line, '\n')); err != nil {
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
				return r, nil
			}
		case <-b.done:
			select {
			case r := <-b.replies:
				if r.ID == id {
					s.b = nil
					return r, nil
				}
			default:
			}
			return gone()
		case <-limit.C:
			stop(b)
			return gone()
		case <-ctx.Done():
			return reply{}, ctx.Err()
		}
	}
}

// Close shuts the kernels down through the bridge. A bridge that doesn't exit
// is told to stop, which it also handles by shutting its kernels down, and is
// killed last. Close waits for a running Exec to return, so a caller cancels
// that Exec's context first. A later Exec is refused.
func (s *Sessions) Close() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if !s.closed {
		defer os.Remove(s.script)
	}
	s.closed = true
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
