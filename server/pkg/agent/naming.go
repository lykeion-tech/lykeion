package agent

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"strings"
	"sync"
)

// NamingSession holds a separate conversation with no tools, discovery or
// research continuity. Only adapters with verified tool denial may open one.
type NamingSession struct {
	delegate interface {
		Ask(context.Context, string) (string, error)
		Close() Result
	}
	context context.Context
	input   *os.File
	cancel  context.CancelFunc
	answers chan namingAnswer
	ended   chan struct{}
	result  Result
	ask     sync.Mutex
	close   sync.Once
}
type namingAnswer struct {
	text string
	err  error
}

func OpenNamingSession(ctx context.Context, harness, bin, dir, model string, sandbox SandboxCommand) (*NamingSession, error) {
	if sandbox == nil {
		return nil, ErrNoSandbox
	}
	if harness == "codex" {
		return openCodexNaming(ctx, bin, dir, model, sandbox)
	}
	if harness != "claude" {
		return nil, errors.New("this harness has no verified tool-free naming mode")
	}
	processCtx, cancel := context.WithCancel(ctx)
	opts := ExecOptions{Dir: dir, Model: model, Sandbox: sandbox}
	home, cleanup, err := prepareScratch(&opts, "claude")
	if err != nil {
		cancel()
		return nil, err
	}
	args := []string{"-p", "--input-format", "stream-json", "--output-format", "stream-json", "--verbose", "--permission-mode", "dontAsk", "--tools", "", "--strict-mcp-config", "--mcp-config", `{"mcpServers":{}}`, "--setting-sources", "", "--disable-slash-commands", "--no-session-persistence"}
	if model != "" {
		args = append(args, "--model", model)
	}
	if bin == "" {
		bin = "claude"
	}
	cmd, err := command(processCtx, bin, "", opts, args...)
	if err != nil {
		cleanup(true)
		cancel()
		return nil, err
	}
	reader, writer, err := os.Pipe()
	if err != nil {
		cleanup(true)
		cancel()
		return nil, err
	}
	cmd.Stdin = reader
	n := &NamingSession{context: processCtx, input: writer, cancel: cancel, answers: make(chan namingAnswer, 1), ended: make(chan struct{})}
	accounting, err := beginClaudeUsage(home, "")
	if err != nil {
		reader.Close()
		writer.Close()
		cleanup(true)
		cancel()
		return nil, err
	}
	base := parseClaude(model, "", accounting, nil)
	var said strings.Builder
	var forbidden bool
	parse := func(line []byte, _ emitMessage, res *Result) (bool, error) {
		emit := func(m Message) {
			if m.Type == "tool_use" {
				forbidden = true
			}
			if m.Type == "text" {
				var text string
				if json.Unmarshal(m.Content, &text) == nil {
					said.WriteString(text)
				}
			}
		}
		var kind struct {
			Type string `json:"type"`
		}
		if json.Unmarshal(line, &kind) == nil && kind.Type == "control_request" {
			return false, errors.New("naming session requested a capability")
		}
		terminal, err := base(line, emit, res)
		if forbidden {
			return false, errors.New("naming session attempted a tool")
		}
		if err == nil && terminal {
			answer := namingAnswer{text: said.String(), err: res.Err}
			said.Reset()
			select {
			case n.answers <- answer:
			case <-processCtx.Done():
			}
		}
		return terminal, err
	}
	sess, err := run(processCtx, cmd, cleanup, parse, nil, func(Message) error { return nil })
	reader.Close()
	if err != nil {
		writer.Close()
		cancel()
		return nil, err
	}
	go func() { n.result = <-sess.Done; close(n.ended) }()
	return n, nil
}

func (n *NamingSession) Ask(ctx context.Context, prompt string) (string, error) {
	if n.delegate != nil {
		return n.delegate.Ask(ctx, prompt)
	}
	n.ask.Lock()
	defer n.ask.Unlock()
	if err := n.context.Err(); err != nil {
		return "", err
	}
	if err := ctx.Err(); err != nil {
		return "", err
	}
	select {
	case <-n.ended:
		return "", errors.New("naming session ended")
	default:
	}
	// A timed-out question retires the whole session; its late answer can never
	// become the answer to a later batch.
	payload, err := json.Marshal(claudeUserFrame(prompt))
	if err != nil {
		return "", err
	}
	written := make(chan error, 1)
	go func() { _, e := n.input.Write(append(payload, '\n')); written <- e }()
	select {
	case err = <-written:
		if err != nil {
			n.cancel()
			return "", err
		}
	case <-ctx.Done():
		n.cancel()
		return "", ctx.Err()
	case <-n.ended:
		return "", errors.New("naming session ended")
	}
	select {
	case answer := <-n.answers:
		if err := ctx.Err(); err != nil {
			n.cancel()
			return "", err
		}
		if answer.err != nil {
			n.cancel()
		}
		return answer.text, answer.err
	case <-ctx.Done():
		n.cancel()
		return "", ctx.Err()
	case <-n.ended:
		return "", errors.New("naming session ended")
	}
}
func (n *NamingSession) Close() Result {
	if n.delegate != nil {
		return n.delegate.Close()
	}
	n.close.Do(func() { n.input.Close(); n.cancel() })
	<-n.ended
	return n.result
}
