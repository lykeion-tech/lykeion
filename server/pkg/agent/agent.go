package agent

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"
)

type Message struct {
	Type    string          `json:"type"`
	Tool    string          `json:"tool,omitempty"`
	Content json.RawMessage `json:"content"`
}
type ExecOptions struct {
	// ScratchReservation is a parent-owned lock borrowed for this execution.
	// With nil, the adapter acquires and releases its own reservation.
	ScratchReservation *ScratchReservation
	Dir                string
	Env                []string
	// MCPToken is the run's own ltk_ token, the agent's LYK_TOKEN.
	Model, MCPToken, Resume string
	// DoorURL and DoorToken reach the daemon's tool door, the one MCP server
	// the CLI is given; every connector call goes through it.
	DoorURL, DoorToken string
	// Inherit runs the CLI as the account's own: its sign-in, history, personal
	// settings, skills, instructions, hooks and MCP all apply. Nothing is copied.
	Inherit bool
	// SkillsFrom is a folder of skill folders Lykeion staged for this run.
	// Each time the scratch is prepared they are copied afresh into the run
	// folder's skills folder, but only for a run that inherits the account's
	// setup; the account's own skills folder is never written.
	SkillsFrom string
	// ReadOnly lets the CLI's own tools read, but not write files or run
	// commands that change anything.
	ReadOnly bool
	// ClaudeBaseline is the opaque usage snapshot a prior Lykeion attempt
	// returned for Resume. It is consumed before launch.
	ClaudeBaseline []byte
	// OnMessage, when set, durably accepts each normalized message instead of
	// publishing it to Session.Messages. An error stops the child and is reported.
	OnMessage func(Message) error
}

const (
	ReasonRuntimeOffline   = "runtime_offline"
	ReasonProviderNetwork  = "provider_network"
	ReasonContextExhausted = "context_exhausted"
	ReasonAgentError       = "agent_error"
)

type Result struct {
	Usage             []Usage
	SessionID, Reason string
	Err               error
	RetainedEvidence  []string
	// ResumeRefused means the CLI rejected Resume before doing any model work,
	// so a fresh session can run without unaccounted spend.
	ResumeRefused bool
	// Model is the model the attempt ran on, as chosen or reported.
	Model string
	// ClaudeSnapshot is the cumulative usage baseline for resuming SessionID,
	// present only after a clean terminal result.
	ClaudeSnapshot []byte
}
type Session struct {
	Messages <-chan Message
	Done     <-chan Result
}
type Backend interface {
	Execute(context.Context, string, ExecOptions) (*Session, error)
}

// Harnesses maps every harness Lykeion can run to the binary the daemon looks
// for on PATH. The database harness constraints mirror these names.
var Harnesses = map[string]string{"claude": "claude", "codex": "codex", "cursor": "cursor-agent", "antigravity": "agy"}

// ReadOnlyHarnesses are the harnesses whose adapter honours ExecOptions.ReadOnly;
// the others refuse a read-only run with ErrNoReadOnly.
var ReadOnlyHarnesses = map[string]bool{"claude": true, "codex": true, "cursor": true}

func New(harness, bin string) (Backend, error) {
	switch harness {
	case "claude":
		return Claude{Bin: bin}, nil
	case "antigravity":
		return Antigravity{Bin: bin}, nil
	case "cursor":
		return Cursor{Bin: bin}, nil
	case "codex":
		return Codex{Bin: bin}, nil
	}
	return nil, fmt.Errorf("unknown harness %q", harness)
}
func raw(v any) json.RawMessage { b, _ := json.Marshal(v); return b }
func classify(text string) string {
	s := strings.ToLower(text)
	for _, h := range []string{"prompt_too_long", "prompt is too long", "context window", "context_length_exceeded", "maximum context length", "input exceeds the maximum length of"} {
		if strings.Contains(s, h) {
			return ReasonContextExhausted
		}
	}
	for _, h := range []string{"econnreset", "econnrefused", "etimedout", "enotfound", "eai_again", "getaddrinfo", "connection error", "connection reset", "connection refused", "network is unreachable", "stream disconnected", "rate limit", "overloaded"} {
		if strings.Contains(s, h) {
			return ReasonProviderNetwork
		}
	}
	return ReasonAgentError
}

type emitMessage func(Message)
type parser func([]byte, emitMessage, *Result) (terminal bool, err error)

// errStop lets a terminal parser stop a CLI which remains alive after its result.
var errStop = errors.New("stop after the terminal event")

var errMissingTerminal = errors.New("agent exited without a terminal result")

// run drains stdout while exec concurrently feeds stdin. Waiting independently
// of message delivery lets cancellation and credential cleanup survive a slow
// consumer. Cancellation overflow is retained on disk, never silently dropped.
func run(ctx context.Context, cmd *exec.Cmd, cleanup scratchCleanup, parse parser, finish func(*Result, emitMessage), sink func(Message) error) (*Session, error) {
	original := cmd
	var gateRead, gateWrite *os.File
	var nonce string
	var err error
	cmd, gateRead, gateWrite, nonce, err = supervised(ctx, original)
	if err != nil {
		cleanup(true)
		return nil, err
	}
	defer gateRead.Close()
	defer gateWrite.Close()
	if err := configureProcess(cmd); err != nil {
		cleanup(true)
		return nil, err
	}
	stdout, writer, err := os.Pipe()
	if err != nil {
		cleanup(true)
		return nil, err
	}
	cmd.Stdout = writer
	stderr := &diagnosticTail{}
	cmd.Stderr = stderr
	// WaitDelay can kill the supervisor without checking ownership. Only the
	// verified shutdown path may retire it or complete its output streams.
	if err := cmd.Start(); err != nil {
		stdout.Close()
		writer.Close()
		cleanup(true)
		return nil, err
	}
	writer.Close()
	waited := make(chan error, 1)
	go func() { err := waitSupervised(cmd, nonce); cleanup(false); waited <- supervisorExit(cmd, nonce, err) }()
	if err := recordProcess(cmd, nonce); err != nil {
		gateWrite.Close()
		<-waited
		stdout.Close()
		cleanup(true)
		return nil, err
	}
	if err := ctx.Err(); err != nil {
		gateWrite.Close()
		cmd.Cancel()
		<-waited
		stdout.Close()
		cleanup(true)
		return nil, err
	}
	if _, err := gateWrite.Write([]byte("run\n")); err != nil {
		cmd.Cancel()
		<-waited
		stdout.Close()
		cleanup(true)
		return nil, err
	}
	gateWrite.Close()
	msgs := make(chan Message, 64)
	done := make(chan Result, 1)
	go func() {
		defer stdout.Close()
		var res Result
		terminal := false
		var emitErr error
		spill := &messageRetention{dir: cmd.Dir}
		defer spill.close()
		emit := func(m Message) {
			if emitErr != nil {
				return
			}
			if sink != nil {
				emitErr = sink(m)
				if emitErr != nil {
					cmd.Cancel()
				}
				return
			}
			if !spill.started {
				select {
				case msgs <- m:
					return
				default:
				}
				select {
				case msgs <- m:
					return
				case <-ctx.Done():
				}
			}
			emitErr = spill.put(m)
			if emitErr != nil {
				cmd.Cancel()
			}
		}

		sc := bufio.NewScanner(stdout)
		sc.Buffer(make([]byte, 0, 64<<10), 16<<20)
		var parseErr error
		stopped := false
		for sc.Scan() {
			var ended bool
			ended, parseErr = parse(sc.Bytes(), emit, &res)
			terminal = terminal || ended
			if emitErr != nil {
				parseErr = emitErr
			}
			if errors.Is(parseErr, errStop) {
				parseErr, stopped = nil, true
				cmd.Cancel()
				break
			}
			if parseErr != nil {
				cmd.Cancel()
				break
			}
		}
		if sc.Err() != nil {
			parseErr = fmt.Errorf("agent output: %w", sc.Err())
			cmd.Cancel()
		}
		waitErr := <-waited
		switch {
		case emitErr != nil:
			res.Err, res.Reason = emitErr, ReasonAgentError
		case ctx.Err() != nil:
			res.Err, res.Reason = ctx.Err(), ReasonRuntimeOffline
		case parseErr != nil:
			res.Err = parseErr
			res.Reason = ReasonAgentError
		case res.Err == nil && waitErr != nil && !stopped:
			res.Err = fmt.Errorf("agent exit: %w", waitErr)
		case res.Err == nil && !terminal:
			res.Err = errMissingTerminal
		}
		if finish != nil {
			finish(&res, emit)
		}
		if err := spill.close(); err != nil {
			emitErr = errors.Join(emitErr, err)
		}
		if emitErr != nil {
			res.Err, res.Reason = emitErr, ReasonAgentError
		} else if ctx.Err() != nil {
			res.Err, res.Reason = ctx.Err(), ReasonRuntimeOffline
		}
		cleanup(true)
		if res.Err != nil {
			detail := stderr.String()
			if detail != "" {
				res.Err = fmt.Errorf("%w; stderr: %s", res.Err, detail)
			}
			if res.Reason == "" {
				res.Reason = classify(res.Err.Error())
			}
		}
		res.RetainedEvidence = spill.paths
		close(msgs)
		done <- res
		close(done)
	}()
	return &Session{Messages: msgs, Done: done}, nil
}

// waitSupervised keeps the ownership witness until every descendant is stopped.
// The nonce-bound marker is written only after the CLI status and credentials
// have been settled; output is still drained independently by run.
func waitSupervised(cmd *exec.Cmd, nonce string) error {
	exited := make(chan error, 1)
	go func() { exited <- cmd.Wait() }()
	ticker := time.NewTicker(10 * time.Millisecond)
	defer ticker.Stop()
	for {
		select {
		case err := <-exited:
			return err
		case <-ticker.C:
			b, err := readMarker(filepath.Join(cmd.Dir, ".agent-exit"))
			if err != nil {
				continue
			}
			fields := strings.Fields(string(b))
			if len(fields) != 2 || fields[0] != nonce {
				continue
			}
			stopErr := cmd.Cancel()
			if errors.Is(stopErr, os.ErrProcessDone) {
				stopErr = nil
			}
			return errors.Join(<-exited, stopErr)
		}
	}
}

func command(ctx context.Context, bin, prompt string, o ExecOptions, home string, args ...string) *exec.Cmd {
	cmd := exec.CommandContext(ctx, bin, args...)
	cmd.Dir = o.Dir
	cmd.Env = childEnv(o, home)
	cmd.Stdin = strings.NewReader(prompt)
	return cmd
}
