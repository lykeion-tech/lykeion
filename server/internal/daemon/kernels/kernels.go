// Package kernels runs a run's live Python, R and Julia sessions. A Python
// bridge started in the study's environment keeps one Jupyter kernel per
// language and talks JSON lines with the daemon.
package kernels

import (
	"bufio"
	"bytes"
	"cmp"
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
	"sync/atomic"
	"syscall"
	"time"
	"unicode/utf8"
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

const MaxCellOutputBytes = 20 << 20

const cellCutNote = "(output cut at 20 MiB)"

// Output is the common cell wire format. Image Data is base64 in JSON.
type Output struct {
	Type      string   `json:"type"`
	Name      string   `json:"name,omitempty"`
	Text      string   `json:"text,omitempty"`
	MIME      string   `json:"mime,omitempty"`
	Data      []byte   `json:"data,omitempty"`
	Ename     string   `json:"ename,omitempty"`
	Evalue    string   `json:"evalue,omitempty"`
	Traceback []string `json:"traceback,omitempty"`
}

type CellResult struct {
	Outputs        []Output `json:"outputs"`
	ExecutionCount int      `json:"execution_count"`
	OK             bool     `json:"ok"`
	Interrupted    bool     `json:"interrupted"`
}

type Result struct {
	Stdout, Error string
	OK            bool
}

// Command makes the command that starts the bridge: the door's sandbox.
type Command func(name string, args ...string) (*exec.Cmd, error)

type Sessions struct {
	command Command
	python  string
	script  string
	env     []string
	dir     string

	mu     sync.Mutex
	b      *bridge
	nextID int
	closed bool
	// The process handle is published independently of the serial call lock.
	// os.Process supports concurrent Signal and Wait calls.
	current atomic.Pointer[bridge]
	process atomic.Pointer[os.Process]
	cell    atomic.Pointer[cellControl]
	closing atomic.Bool
}

type reply struct {
	ID             int      `json:"id"`
	BridgePID      int      `json:"bridge_pid"`
	Kernel         string   `json:"kernel"`
	PIDState       bool     `json:"pid_state"`
	PIDs           []int    `json:"pids"`
	Ready          bool     `json:"ready"`
	CellReady      bool     `json:"cell_ready"`
	CellID         string   `json:"cell_id"`
	Outputs        []Output `json:"outputs"`
	ExecutionCount int      `json:"execution_count"`
	Interrupted    bool     `json:"interrupted"`
	Stdout         string   `json:"stdout"`
	Error          string   `json:"error"`
	OK             bool     `json:"ok"`
	Restarted      bool     `json:"restarted"`
}

type bridge struct {
	start    string
	pidState atomic.Pointer[pidState]
	cmd      *exec.Cmd
	in       io.WriteCloser
	replies  chan reply
	done     chan struct{}
	ready    chan struct{}
	// Submitted cells remain addressable after caller cancellation until their
	// reply arrives, so their queued stop cannot be reassigned to another cell.
	cells sync.Map
}

type cellControl struct {
	mu       sync.Mutex
	bridge   *bridge
	pending  bool
	ready    bool
	finished bool
}

func (c *cellControl) interrupt() error {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.finished {
		return nil
	}
	if !c.ready {
		c.pending = true
		return nil
	}
	if err := c.bridge.signal(syscall.SIGUSR1); err != nil && !errors.Is(err, os.ErrProcessDone) {
		return err
	}
	return nil
}

// Start writes the bridge to a file in tmp ("" for the system's temp
// folder), which Close removes, and starts it through command, which is
// required, in dir with
// env as its whole environment. Kernels start on each language's first cell.
// ctx is not kept: the bridge lives until Close, and a bridge that dies is
// started again by the next Exec.
func Start(ctx context.Context, command Command, python string, env []string, dir, tmp string) (*Sessions, error) {
	if command == nil {
		return nil, errors.New("the session bridge has no command to start it")
	}
	f, err := os.CreateTemp(tmp, "lyk-bridge-*.py")
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
	s := &Sessions{command: command, python: python, script: f.Name(), env: env, dir: dir}
	b, err := s.start()
	if err != nil {
		os.Remove(s.script)
		return nil, err
	}
	s.b = b
	s.process.Store(b.cmd.Process)
	s.current.Store(b)
	return s, nil
}

func (s *Sessions) start() (*bridge, error) {
	// Isolated mode keeps the bridge off the study's import paths and works
	// with every supported Python. The research kernels inherit their own
	// environment without this flag, including the study's PYTHONPATH.
	cmd, err := s.command(s.python, "-I", s.script)
	if err != nil {
		return nil, err
	}
	cmd.Env = s.env
	cmd.Dir = s.dir
	cmd.Stderr = &stderrLog{}
	// a kernel left running may hold stderr open after the bridge exits
	cmd.WaitDelay = 5 * time.Second
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
	b := &bridge{start: processStart(cmd.Process.Pid), cmd: cmd, in: in, replies: make(chan reply, 1), done: make(chan struct{}), ready: make(chan struct{})}
	go func() {
		// A bridge that breaks the protocol is told to stop, which shuts its
		// kernels down, and killed if it hasn't exited after 5 seconds.
		halt := func(msg string, err error) {
			slog.Warn(msg, "err", err)
			cmd.Process.Signal(syscall.SIGTERM)
			time.AfterFunc(5*time.Second, func() { cmd.Process.Kill() })
		}
		ready := false
		sc := bufio.NewScanner(out)
		sc.Buffer(nil, maxLine)
		for sc.Scan() {
			var r reply
			if err := json.Unmarshal(sc.Bytes(), &r); err != nil {
				halt("the session bridge wrote a line that isn't a reply; stopping it", err)
				break
			}
			if r.BridgePID > 0 {
				b.pidState.Store(&pidState{bridge: r.BridgePID, pids: r.PIDs, kernel: r.Kernel})
			}
			if r.PIDState {
				continue
			}
			if r.Ready {
				if !ready {
					ready = true
					close(b.ready)
				}
				continue
			}
			if r.CellReady {
				if value, ok := b.cells.Load(r.ID); ok {
					c := value.(*cellControl)
					c.mu.Lock()
					c.ready = true
					if c.pending && !c.finished {
						b.signal(syscall.SIGUSR1)
						c.pending = false
					}
					c.mu.Unlock()
				}
				continue
			}
			if value, ok := b.cells.LoadAndDelete(r.ID); ok {
				c := value.(*cellControl)
				c.mu.Lock()
				c.finished = true
				c.mu.Unlock()
				s.cell.CompareAndSwap(c, nil)
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

// Cell executes code in the named Jupyter kernel. Switching names retires the
// previous named kernel; legacy Exec sessions retain their own cache. Canceling
// a submitted cell's context retires the bridge and its variables before any
// later cell may run. Interrupt stops execution without retiring the bridge.
func (s *Sessions) Cell(ctx context.Context, kernel, code string) (CellResult, error) {
	return s.CellWithID(ctx, "", kernel, code)
}

// CellWithID preserves the cell ID already assigned by the server. The integer
// request ID remains the bridge transport correlation, including for Exec.
func (s *Sessions) CellWithID(ctx context.Context, cellID, kernel, code string) (CellResult, error) {
	r, err := s.call(ctx, map[string]any{"op": "cell", "cell_id": cellID, "kernel": kernel, "code": code, "timeout": CellTimeout.Seconds()})
	if err == nil && r.CellID != cellID {
		err = errors.New("the session bridge replied with a different cell_id")
	}
	return CellResult{Outputs: capCellOutputs(r.Outputs), ExecutionCount: r.ExecutionCount, OK: r.OK, Interrupted: r.Interrupted}, err
}

// Interrupt never acquires the call lock, which a running cell holds.
func (s *Sessions) Interrupt() error {
	if c := s.cell.Load(); c != nil {
		return c.interrupt()
	}
	return nil
}

// RestartKernel interrupts an active cell before waiting to send the restart.
// Repeated signals also cover the gap between submitting and executing a cell.
func (s *Sessions) RestartKernel(ctx context.Context, kernel string) error {
	for !s.mu.TryLock() {
		if err := s.Interrupt(); err != nil {
			return err
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(50 * time.Millisecond):
		}
	}
	defer s.mu.Unlock()
	r, err := s.callLocked(ctx, map[string]any{"op": "restart_kernel", "kernel": kernel})
	if err == nil && !r.OK {
		err = errors.New(cmp.Or(r.Error, "the "+kernel+" kernel could not be restarted"))
	}
	return err
}

// capCellOutputs measures encoding/json's actual wire size, including HTML
// escaping, image base64, separators and the final cut note. Images are atomic.
func capCellOutputs(outputs []Output) []Output {
	if outputs == nil {
		return []Output{}
	}
	encoded, _ := json.Marshal(outputs)
	if len(encoded) <= MaxCellOutputBytes {
		return outputs
	}
	note := Output{Type: "text", Text: cellCutNote}
	noteJSON, _ := json.Marshal(note)
	budget := MaxCellOutputBytes - len(noteJSON) - 1
	used := 2
	kept := make([]Output, 0, len(outputs)+1)
	for _, o := range outputs {
		b, _ := json.Marshal(o)
		sep := 0
		if len(kept) > 0 {
			sep = 1
		}
		if used+sep+len(b) <= budget {
			kept = append(kept, o)
			used += sep + len(b)
			continue
		}
		if o.Type == "stream" || o.Type == "text" {
			text := o.Text
			lo, hi := 0, len(text)
			for lo < hi {
				mid := (lo + hi + 1) / 2
				o.Text = text[:mid]
				b, _ = json.Marshal(o)
				if used+sep+len(b) <= budget {
					lo = mid
				} else {
					hi = mid - 1
				}
			}
			for lo > 0 && !utf8.ValidString(text[:lo]) {
				lo--
			}
			if lo > 0 {
				o.Text = text[:lo]
				kept = append(kept, o)
			}
		}
		break
	}
	return append(kept, note)
}

// Restart restarts lang's session, dropping its variables; a language with
// no session yet has nothing to restart.
func (s *Sessions) Restart(ctx context.Context, lang string) error {
	r, err := s.call(ctx, map[string]any{"op": "restart", "language": lang})
	if err == nil && !r.OK {
		err = errors.New(cmp.Or(r.Error, "the "+lang+" session could not be restarted"))
	}
	return err
}

// call sends req to the bridge under the next id and waits for its reply.
func (s *Sessions) call(ctx context.Context, req map[string]any) (reply, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.callLocked(ctx, req)
}

func (s *Sessions) callLocked(ctx context.Context, req map[string]any) (reply, error) {
	var control *cellControl
	if req["op"] == "cell" {
		control = &cellControl{}
		s.cell.Store(control)
		defer s.cell.CompareAndSwap(control, nil)
	}
	if err := ctx.Err(); err != nil {
		return reply{}, err
	}
	if s.closed || s.closing.Load() {
		return reply{}, errors.New("the run's sessions are closed")
	}
	if s.b == nil {
		b, err := s.start()
		if err != nil {
			return reply{}, err
		}
		s.b = b
		s.process.Store(b.cmd.Process)
		s.current.Store(b)
	}
	b := s.b
	s.nextID++
	id := s.nextID
	req["id"] = id
	gone := func() (reply, error) {
		s.process.CompareAndSwap(b.cmd.Process, nil)
		s.b = nil
		return reply{}, errors.New("the session bridge stopped; its sessions and their variables are gone")
	}
	limit := time.NewTimer(CellTimeout + restartGrace)
	defer limit.Stop()
	// Only the new controls require handler readiness. Legacy Exec/restart
	// commands and existing command adapters retain their original protocol.
	if req["op"] == "cell" || req["op"] == "restart_kernel" {
		select {
		case <-b.ready:
		case <-b.done:
			return gone()
		case <-ctx.Done():
			return reply{}, ctx.Err()
		case <-limit.C:
			stop(b)
			return gone()
		}
		if err := ctx.Err(); err != nil {
			return reply{}, err
		}
	}
	if control != nil {
		control.mu.Lock()
		control.bridge = b
		// Stops queued before submission belong to this request, not to a
		// signal that could arrive before Python has selected its target.
		req["interrupted"] = control.pending
		control.pending = false
		control.mu.Unlock()
		b.cells.Store(id, control)
	}
	line, err := json.Marshal(req)
	if err != nil {
		b.cells.Delete(id)
		return reply{}, err
	}
	if _, err := b.in.Write(append(line, '\n')); err != nil {
		stop(b)
		return gone()
	}
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
					s.process.CompareAndSwap(b.cmd.Process, nil)
					return r, nil
				}
			default:
			}
			return gone()
		case <-limit.C:
			stop(b)
			return gone()
		case <-ctx.Done():
			if req["op"] == "cell" {
				// SIGUSR1 carries no request ID. Keep the call lock until
				// this physical bridge and its reader are fully retired,
				// including any delayed acknowledgements for this cell.
				stop(b)
				for {
					select {
					case <-b.done:
						gone()
						return reply{}, ctx.Err()
					case <-b.replies:
						// Keep the reader moving until process completion.
					}
				}
			}
			return reply{}, ctx.Err()
		}
	}
}

// Close shuts the kernels down through the bridge. A bridge that doesn't exit
// is told to stop, which it also handles by shutting its kernels down, and is
// killed last. Close signals before acquiring the call lock so active cells
// cannot delay shutdown until their timeout. Later calls are refused.
func (s *Sessions) Close() error {
	s.closing.Store(true)
	var pending *os.Process
	var force *time.Timer
	// Escalation must run before taking mu: a bridge that ignores SIGTERM can
	// otherwise keep the call lock until the full cell timeout expires.
	for !s.mu.TryLock() {
		if p := s.process.Load(); p != nil && p != pending {
			if b := s.current.Load(); b != nil {
				_ = b.signal(syscall.SIGTERM)
			} else {
				p.Signal(syscall.SIGTERM)
			}
			pending = p
			if force != nil {
				force.Stop()
			}
			force = time.AfterFunc(5*time.Second, func() { p.Kill() })
		}
		time.Sleep(10 * time.Millisecond)
	}
	if force != nil {
		defer force.Stop()
	}
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
	s.process.CompareAndSwap(b.cmd.Process, nil)
	b.in.Write([]byte(`{"op":"shutdown"}` + "\n"))
	b.in.Close()
	if !exited(b, 10*time.Second) {
		stop(b)
	}
	return nil
}

func stop(b *bridge) {
	b.signal(syscall.SIGTERM)
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

// stderrLog writes what the bridge and its kernels print to stderr to the
// daemon's log a line at a time, each cut at 4 KiB, and drops the rest once
// 1 MiB has been logged: the code they run decides what that is.
type stderrLog struct {
	line   []byte
	cut    bool
	logged int
}

const maxStderrLine, maxStderr = 4 << 10, 1 << 20

func (w *stderrLog) Write(p []byte) (int, error) {
	n := len(p)
	for len(p) > 0 {
		i := bytes.IndexByte(p, '\n')
		part := p
		if i >= 0 {
			part = p[:i]
		}
		room := maxStderrLine - len(w.line)
		if len(part) > room {
			part, w.cut = part[:max(room, 0)], true
		}
		w.line = append(w.line, part...)
		if i < 0 {
			break
		}
		if w.logged < maxStderr {
			text := string(w.line)
			if w.cut {
				text += " [cut]"
			}
			slog.Warn("session bridge", "stderr", text)
			w.logged += len(w.line)
		}
		w.line, w.cut, p = w.line[:0], false, p[i+1:]
	}
	return n, nil
}
