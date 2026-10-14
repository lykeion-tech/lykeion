package agent

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"strings"
	"time"
)

type Message struct {
	Type    string          `json:"type"`
	Tool    string          `json:"tool,omitempty"`
	Content json.RawMessage `json:"content"`
}
type ExecOptions struct {
	Dir                             string
	Env                             []string
	Model, MCPURL, MCPToken, Resume string
	MCP                             []MCPServer
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
}
type Session struct {
	Messages <-chan Message
	Done     <-chan Result
}
type Backend interface {
	Execute(context.Context, string, ExecOptions) (*Session, error)
}

func New(harness, bin string) (Backend, error) {
	switch harness {
	case "claude":
		return Claude{Bin: bin}, nil
	case "codex":
		return Codex{Bin: bin}, nil
	}
	return nil, fmt.Errorf("unknown harness %q", harness)
}
func raw(v any) json.RawMessage { b, _ := json.Marshal(v); return b }
func classify(text string) string {
	s := strings.ToLower(text)
	for _, h := range []string{"prompt_too_long", "prompt is too long", "context window", "context_length_exceeded", "maximum context length"} {
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

// run drains stdout while exec concurrently feeds stdin. Waiting independently
// of message delivery lets cancellation and credential cleanup survive a slow
// consumer. Only cancellation may discard messages already being parsed.
func run(ctx context.Context, cmd *exec.Cmd, cleanup scratchCleanup, parse parser, finish func(*Result)) (*Session, error) {
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
	cmd.WaitDelay = time.Second
	if err := cmd.Start(); err != nil {
		stdout.Close()
		writer.Close()
		cleanup(true)
		return nil, err
	}
	writer.Close()
	waited := make(chan error, 1)
	go func() { err := cmd.Wait(); killProcessTree(cmd); cleanup(false); waited <- err }()
	msgs := make(chan Message, 64)
	done := make(chan Result, 1)
	go func() {
		defer stdout.Close()
		var res Result
		terminal := false
		emit := func(m Message) {
			select {
			case msgs <- m:
			case <-ctx.Done():
			}
		}
		sc := bufio.NewScanner(stdout)
		sc.Buffer(make([]byte, 0, 64<<10), 16<<20)
		var parseErr error
		for sc.Scan() {
			var ended bool
			ended, parseErr = parse(sc.Bytes(), emit, &res)
			terminal = terminal || ended
			if parseErr != nil {
				killProcessTree(cmd)
				break
			}
		}
		if sc.Err() != nil {
			parseErr = fmt.Errorf("agent output: %w", sc.Err())
			killProcessTree(cmd)
		}
		waitErr := <-waited
		switch {
		case ctx.Err() != nil:
			res.Err, res.Reason = ctx.Err(), ReasonRuntimeOffline
		case parseErr != nil:
			res.Err = parseErr
			res.Reason = ReasonAgentError
		case res.Err == nil && waitErr != nil:
			res.Err = fmt.Errorf("agent exit: %w", waitErr)
		case res.Err == nil && !terminal:
			res.Err = errors.New("agent exited without a terminal result")
		}
		if finish != nil {
			finish(&res)
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
		close(msgs)
		done <- res
		close(done)
	}()
	return &Session{Messages: msgs, Done: done}, nil
}

func command(ctx context.Context, bin, prompt string, o ExecOptions, home string, args ...string) *exec.Cmd {
	cmd := exec.CommandContext(ctx, bin, args...)
	cmd.Dir = o.Dir
	cmd.Env = childEnv(o, home)
	cmd.Stdin = strings.NewReader(prompt)
	return cmd
}
