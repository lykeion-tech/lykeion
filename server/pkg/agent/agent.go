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
	Type string `json:"type"`
	Tool string `json:"tool,omitempty"`
	// Server names the MCP server a tool call went to.
	Server  string          `json:"server,omitempty"`
	Content json.RawMessage `json:"content"`
}
type ExecOptions struct {
	// Coresearch uses scoped sources and any connector selected for this turn.
	Coresearch bool
	// Aristotle permits the server-frozen model for a personal conversation.
	Aristotle  bool
	SourcesURL string

	// Guest excludes ambient account settings and credentials from child processes.
	Guest bool
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
	Sandbox            SandboxCommand
	CLIPaths           CLIPaths
	CacheDir           string
	// ConfigHome overrides the account config home for this run.
	ConfigHome string
	// SkillsFrom is a folder of skill folders Lykeion staged for this run.
	// Each time the scratch is prepared they are copied afresh into the run
	// folder's skills folder; the account's own skills folder is never written.
	SkillsFrom string
	// ReadOnly lets the CLI's own tools read, but not write files or run
	// commands that change anything.
	ReadOnly bool
	// ReadDirs are folders besides Dir that a writable run's CLI may read
	// without asking: the study's add-on folder and the language bases.
	ReadDirs []string
	// ClaudeBaseline is the opaque usage snapshot a prior Lykeion attempt
	// returned for Resume. It is consumed before launch.
	ClaudeBaseline []byte
	// OutputSchema, when set, is a JSON Schema the CLI holds its final
	// answer to; Result.Output carries that answer.
	OutputSchema string
	// OnMessage, when set, durably accepts each normalized message instead of
	// publishing it to Session.Messages. An error stops the child and is reported.
	OnMessage func(Message) error
}

const (
	ReasonRuntimeOffline   = "runtime_offline"
	ReasonProviderNetwork  = "provider_network"
	ReasonContextExhausted = "context_exhausted"
	ReasonAgentError       = "agent_error"
	ReasonSignIn           = "sign_in"
	ReasonCLIMissing       = "cli_missing"
	ReasonCLITooOld        = "cli_too_old"
	ReasonDoor             = "door"
	ReasonCLIBroken        = "cli_broken"
	ReasonSandbox          = "sandbox"
	ReasonFenceBreach      = "fence_breach"
	ReasonHandInInvalid    = "hand_in_invalid"
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
	// Output is the run's final answer: Claude's structured output, or its
	// result text when it has none; Codex's last message.
	Output string
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
var Harnesses = map[string]string{"claude": "claude", "codex": "codex"}

// ReadOnlyHarnesses are the harnesses whose adapter honours ExecOptions.ReadOnly.
var ReadOnlyHarnesses = map[string]bool{"claude": true, "codex": true}

func New(harness, bin string) (Backend, error) {
	switch harness {
	case "claude":
		return Claude{Bin: bin}, nil
	case "antigravity", "cursor":
		return nil, ErrHarnessRemoved
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

// SandboxCommand creates an OS-confined CLI command from its computed state grants.
type SandboxCommand func(context.Context, CLIPaths, string, ...string) (*exec.Cmd, error)

var ErrNoSandbox = errors.New("this run has no sandbox")
var ErrHarnessRemoved = errors.New("Cursor and Antigravity are no longer supported; choose Claude Code or Codex")

func command(ctx context.Context, bin, prompt string, o ExecOptions, args ...string) (*exec.Cmd, error) {
	if o.Sandbox == nil {
		return nil, ErrNoSandbox
	}
	cmd, err := o.Sandbox(ctx, o.CLIPaths, bin, args...)
	if err != nil {
		return nil, err
	}
	if cmd == nil {
		return nil, errors.New("sandbox returned no command")
	}
	cmd.Dir = o.Dir
	cmd.Env = childEnv(o, o.CacheDir)
	cmd.Stdin = strings.NewReader(prompt)
	return cmd, nil
}
