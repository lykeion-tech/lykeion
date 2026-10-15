package agent

import (
	"context"
	"encoding/json"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
)

type Cursor struct {
	Bin string
}

// CursorModel is passed when the agent run names no model: Cursor reports no cost,
// so the adapter must know which model its usage is priced as.
const CursorModel = "gpt-5"

// cursorCredFile is the sign-in state cursor-agent keeps in its data folder.
const cursorCredFile = "cli-config.json"

// errPrivateOnly refuses a run on the account's own setup: this CLI runs only
// in a private scratch home, which the daemon forces for it.
var errPrivateOnly = errors.New("this harness runs only in private mode")

// Execute runs cursor-agent in print mode with stream-json events; the prompt
// goes on stdin. cursor-agent may stay alive after its result event, so the
// parser stops the process there.
func (c Cursor) Execute(ctx context.Context, prompt string, o ExecOptions) (*Session, error) {
	if o.Inherit {
		return nil, errPrivateOnly
	}
	home, cleanup, err := prepareScratch(&o, "cursor", "CURSOR_DATA_DIR", ".cursor", cursorCredFile)
	if err != nil {
		return nil, err
	}
	// The workspace's .cursor/mcp.json lists the servers; the approvals file in
	// the private data folder lets them start without a prompt nobody would answer.
	if err = privateDir(filepath.Join(o.Dir, ".cursor")); err != nil {
		cleanup(true)
		return nil, err
	}
	for _, f := range []struct {
		path string
		body []byte
	}{
		{filepath.Join(o.Dir, ".cursor", "mcp.json"), claudeMCP(o)},
		{filepath.Join(home, "mcp-approvals.json"), cursorApprovals(o)},
	} {
		// Replace only the adapter's prior file; removing a planted link never
		// follows it.
		if err = os.Remove(f.path); err != nil && !errors.Is(err, fs.ErrNotExist) {
			cleanup(true)
			return nil, err
		}
		if err = privateFile(f.path, f.body); err != nil {
			cleanup(true)
			return nil, err
		}
	}
	model := o.Model
	if model == "" {
		model = CursorModel
	}
	args := []string{"-p", "--output-format", "stream-json", "--workspace", o.Dir, "--model", model}
	if o.ReadOnly {
		args = append(args, "--mode", "ask")
	} else {
		args = append(args, "--yolo")
	}
	if o.Resume != "" {
		args = append(args, "--resume", o.Resume)
	}
	bin := c.Bin
	if bin == "" {
		bin = "cursor-agent"
	}
	return run(ctx, command(ctx, bin, prompt, o, home, args...), cleanup, parseCursor(model), nil, o.OnMessage)
}

// cursorApprovals names every server in mcp.json, so none waits for a person.
func cursorApprovals(o ExecOptions) []byte {
	names := []string{}
	for _, s := range o.MCP {
		names = append(names, ServerName(s.Name))
	}
	if o.MCPURL != "" {
		names = append(names, "lyk_sources")
	}
	b, _ := json.Marshal(names)
	return b
}

func parseCursor(model string) parser {
	return func(line []byte, emit emitMessage, res *Result) (bool, error) {
		var ev struct {
			Type     string                     `json:"type"`
			Subtype  string                     `json:"subtype"`
			Session  string                     `json:"session_id"`
			Text     string                     `json:"text"`
			IsError  bool                       `json:"is_error"`
			Result   string                     `json:"result"`
			Message  json.RawMessage            `json:"message"`
			ToolCall map[string]json.RawMessage `json:"tool_call"`
			Usage    *struct {
				Input      int64 `json:"inputTokens"`
				Output     int64 `json:"outputTokens"`
				CacheRead  int64 `json:"cacheReadTokens"`
				CacheWrite int64 `json:"cacheWriteTokens"`
			} `json:"usage"`
		}
		if json.Unmarshal(line, &ev) != nil {
			return false, nil
		}
		cp := json.RawMessage(append([]byte(nil), line...))
		switch ev.Type {
		case "system":
			if ev.Session != "" {
				res.SessionID = ev.Session
			}
			if ev.Subtype == "error" {
				res.Err = errors.New(string(line))
			}
			emit(Message{Type: "log", Content: cp})
		case "assistant":
			var m struct {
				Content []struct{ Type, Text string } `json:"content"`
			}
			json.Unmarshal(ev.Message, &m)
			for _, c := range m.Content {
				if c.Type == "text" {
					emit(Message{Type: "text", Content: raw(c.Text)})
				}
			}
		case "text":
			emit(Message{Type: "text", Content: raw(ev.Text)})
		case "thinking":
			emit(Message{Type: "thinking", Content: raw(ev.Text)})
		case "tool_call", "tool_use", "tool_result":
			typ := "tool_use"
			if ev.Type == "tool_result" || ev.Subtype == "completed" {
				typ = "tool_result"
			}
			emit(Message{Type: typ, Tool: toolName(ev.ToolCall), Content: cp})
		case "step_finish":
			emit(Message{Type: "status", Content: cp})
		case "result":
			if ev.Session != "" {
				res.SessionID = ev.Session
			}
			if u := ev.Usage; u != nil {
				res.Usage = []Usage{{Model: model, InputTokens: u.Input, OutputTokens: u.Output,
					CacheReadTokens: u.CacheRead, CacheWriteTokens: u.CacheWrite}}
			}
			if ev.IsError {
				msg := ev.Result
				if msg == "" {
					msg = "cursor-agent reported an error"
				}
				res.Err = errors.New(msg)
			}
			return true, errStop
		default:
			emit(Message{Type: "log", Content: cp})
		}
		return false, nil
	}
}

// toolName is the one key of Cursor's tool_call object, such as readToolCall.
func toolName(tc map[string]json.RawMessage) string {
	for k := range tc {
		return k
	}
	return ""
}

// removePriorCursorConfig clears only the prior adapter file in an owned,
// locked scratch. Unexpected discovery entries remain for the guard to refuse.
func removePriorCursorConfig(dir string) error {
	config := filepath.Join(dir, ".cursor")
	st, err := os.Lstat(config)
	if errors.Is(err, fs.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	if !st.IsDir() || st.Mode()&os.ModeSymlink != 0 {
		return nil
	}
	entries, err := os.ReadDir(config)
	if err != nil {
		return err
	}
	if len(entries) != 1 || entries[0].Name() != "mcp.json" {
		return nil
	}
	file := filepath.Join(config, "mcp.json")
	st, err = os.Lstat(file)
	if err != nil {
		return err
	}
	if !st.Mode().IsRegular() {
		return nil
	}
	if err = os.Remove(file); err != nil {
		return err
	}
	return os.Remove(config)
}
