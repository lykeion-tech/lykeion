package agent

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"maps"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"time"
)

type Claude struct{ Bin string }

// claudeTools are pre-approved in every run. Reads are not among them: a run
// may read only its own folders (claudeRule), and a bare Glob or Grep allow
// would search past every Read path rule.
const claudeTools = "Skill,Bash(lyk:*),mcp__lyk_door"

const claudeCredFile = ".credentials.json"

// ScriptLimit is how long a script run through the door may take.
const ScriptLimit = 2 * time.Hour

// doorCallTimeout lets a door call outlast the script it runs, so the door's
// limit ends a long script and not the CLI's own tool-call timeout. In Claude
// Code the per-server timeout also lifts its request and idle timeouts.
const doorCallTimeout = ScriptLimit + 5*time.Minute

func (c Claude) Execute(ctx context.Context, prompt string, o ExecOptions) (*Session, error) {
	home, cleanup, err := prepareScratch(&o, "claude")
	if err != nil {
		return nil, err
	}
	if len(o.ClaudeBaseline) > 0 {
		if len(o.ClaudeBaseline) > claudeSnapshotLimit {
			cleanup(true)
			return nil, errors.New("Claude usage baseline exceeds size limit")
		}
		if err = writeClaudeSnapshot(filepath.Join(home, ".lyk-usage.json"), o.ClaudeBaseline); err != nil {
			cleanup(true)
			return nil, err
		}
	}
	accounting, err := beginClaudeUsage(home, o.Resume)
	if err != nil {
		cleanup(true)
		return nil, err
	}
	cfg := filepath.Join(o.Dir, ".lyk-mcp.json")
	// Replace only the adapter's prior file; never follow a planted link.
	exists, err := claudeMCPExists(cfg)
	if err != nil {
		cleanup(true)
		return nil, err
	}
	if exists {
		if err = os.Remove(cfg); err != nil {
			cleanup(true)
			return nil, err
		}
	}
	if err = privateFile(cfg, claudeMCP(o)); err != nil {
		cleanup(true)
		return nil, err
	}
	mode := "dontAsk"
	if !o.ReadOnly {
		mode = "default" // the account's own allow and deny rules decide the rest
	}
	// Glob and Grep obey Read path rules; reads elsewhere go to the account's
	// own rules, or are refused in an isolated run. The CLI's own homes in the
	// run folder are its alone: it uses them, its file tools may not.
	tools := []string{claudeTools}
	var deny []string
	if o.ReadOnly {
		// Plan mode would read any path, so a read-only run asks nothing and
		// may only read its folders, load skills and call connectors through
		// the door, never run code: no account rule edits or runs commands,
		// and nothing else is pre-approved.
		tools = []string{"Skill,mcp__lyk_door__call_tool"}
	}
	if rule := claudeRule("Edit", o.Dir, ""); rule != "" {
		if !o.ReadOnly {
			// The folder's own .claude settings, the CLI's homes, whose
			// settings and hooks the CLI loads, and its git repository, whose
			// hooks git runs, stay out of reach, as do the supervisor's
			// markers; a deny rule wins over every allow rule. A .git file
			// can point git at another repository, so .git itself is denied.
			tools = append(tools, rule)
			for _, sub := range []string{"/.claude", "/.cli-home", "/.home", "/.git"} {
				deny = append(deny, claudeRule("Edit", o.Dir, sub))
			}
			for _, file := range []string{"/.git", "/.agent-exit", "/.agent-process"} {
				deny = append(deny, strings.TrimSuffix(claudeRule("Edit", o.Dir, file), "/**)")+")")
			}
		}
		deny = append(deny, claudeRule("Read", o.Dir, "/.cli-home"), claudeRule("Read", o.Dir, "/.home"))
	}
	for _, dir := range append([]string{o.Dir}, o.ReadDirs...) {
		if rule := claudeRule("Read", dir, ""); rule != "" {
			tools = append(tools, rule)
		}
	}
	if o.ReadOnly {
		deny = append(deny, "Edit(//**)", "Bash")
	}
	if len(deny) > 0 {
		tools = append(append(tools, "--disallowedTools"), deny...)
	}
	// The person's own hooks and plugin hooks stay out of runs, so how a run
	// behaves doesn't depend on what they have installed.
	args := slices.Concat([]string{"-p", "--input-format", "stream-json", "--output-format", "stream-json", "--verbose", "--permission-mode", mode, "--allowedTools"}, tools, []string{"--mcp-config", cfg, "--settings", `{"disableAllHooks":true}`})
	if o.Model != "" {
		args = append(args, "--model", o.Model)
	}
	if o.Resume != "" {
		args = append(args, "--resume", o.Resume)
	}
	bin := c.Bin
	if bin == "" {
		bin = "claude"
	}
	cmd, err := command(ctx, bin, prompt, o, args...)
	if err != nil {
		cleanup(true)
		return nil, err
	}
	stdin, w, err := os.Pipe()
	if err != nil {
		cleanup(true)
		return nil, err
	}
	cmd.Stdin = stdin
	in := newClaudeInput(w)
	if err = in.send(claudeUserFrame(prompt)); err != nil {
		in.close()
		stdin.Close()
		cleanup(true)
		return nil, err
	}
	go func() {
		select {
		case <-ctx.Done():
			in.close()
		case <-in.stop:
		}
	}()
	finish := func(res *Result, _ emitMessage) { in.close(); accounting.finish(res) }
	sess, err := run(ctx, cmd, cleanup, parseClaude(o.Model, o.Resume, accounting, in), finish, o.OnMessage)
	// The child holds its own copy of the read end once started.
	stdin.Close()
	if err != nil {
		in.close()
	}
	return sess, err
}

// claudeRule names Claude's file-edit or file-read tools (tool is Edit or
// Read) for everything under dir, or under sub inside it. The folder is
// matched literally: its glob characters are escaped. A name the CLI would
// split into several rules (commas, parentheses) gets no rule at all, so use
// there falls back to the permission mode.
func claudeRule(tool, dir, sub string) string {
	abs, err := filepath.Abs(dir)
	if err != nil || !filepath.IsAbs(abs) || strings.ContainsAny(abs, ",()\r\n") {
		return ""
	}
	var b strings.Builder
	for _, r := range abs {
		if strings.ContainsRune(`\*?[]{}!`, r) {
			b.WriteByte('\\')
		}
		b.WriteRune(r)
	}
	// A leading "//" makes the pattern absolute rather than settings-relative.
	return tool + "(/" + b.String() + sub + "/**)"
}

// claudeMCP names the daemon's tool door and nothing else.
func claudeMCP(o ExecOptions) []byte {
	servers := map[string]any{}
	if o.DoorURL != "" {
		servers["lyk_door"] = map[string]any{"type": "http", "url": o.DoorURL, "headers": map[string]string{"Authorization": "Bearer " + o.DoorToken}, "timeout": doorCallTimeout.Milliseconds()}
	}
	return raw(map[string]any{"mcpServers": servers})
}

type claudeTokens struct {
	Input      int64    `json:"inputTokens"`
	Output     int64    `json:"outputTokens"`
	CacheRead  int64    `json:"cacheReadInputTokens"`
	CacheWrite int64    `json:"cacheCreationInputTokens"`
	Cost       *float64 `json:"costUSD"`
}

// in is nil for the F2 text-stdin transport, whose control frames stay logs.
func parseClaude(fallbackModel, resume string, accounting *claudeAccounting, in *claudeInput) parser {
	sawWork := false
	return func(line []byte, emit emitMessage, res *Result) (bool, error) {
		var ev struct {
			Type           string   `json:"type"`
			Session        string   `json:"session_id"`
			Model          string   `json:"model"`
			IsError        bool     `json:"is_error"`
			Cost           *float64 `json:"total_cost_usd"`
			Result         string   `json:"result"`
			Subtype        string   `json:"subtype"`
			Errors         []string `json:"errors"`
			TerminalReason string   `json:"terminal_reason"`
			NumTurns       int      `json:"num_turns"`
			Usage          *struct {
				Input      int64 `json:"input_tokens"`
				Output     int64 `json:"output_tokens"`
				CacheRead  int64 `json:"cache_read_input_tokens"`
				CacheWrite int64 `json:"cache_creation_input_tokens"`
			} `json:"usage"`
			ModelUsage map[string]claudeTokens `json:"modelUsage"`
			Message    json.RawMessage         `json:"message"`
		}
		if err := json.Unmarshal(line, &ev); err != nil {
			return false, fmt.Errorf("invalid Claude event: %w", err)
		}
		if ev.Session != "" {
			res.SessionID = ev.Session
		}
		if ev.Type == "system" && ev.Model != "" && len(ev.Model) <= 200 {
			res.Model = ev.Model
		}
		switch ev.Type {
		case "control_request":
			if in == nil {
				emit(Message{Type: "log", Content: append(json.RawMessage(nil), line...)})
				return false, nil
			}
			reply, record, err := claudeControl(line)
			if err != nil {
				return false, err
			}
			// The durable record precedes the reply the CLI acts on.
			emit(record)
			if err := in.send(reply); err != nil {
				return false, fmt.Errorf("answer Claude control request: %w", err)
			}
		case "assistant", "user":
			if ev.Type == "assistant" {
				sawWork = true
			}
			var message struct {
				Content []json.RawMessage `json:"content"`
			}
			if err := json.Unmarshal(ev.Message, &message); err != nil {
				return false, fmt.Errorf("invalid Claude message: %w", err)
			}
			for _, c := range message.Content {
				var item struct {
					Type, Name, Text, Thinking string
					Input                      json.RawMessage
				}
				if err := json.Unmarshal(c, &item); err != nil {
					return false, fmt.Errorf("invalid Claude content: %w", err)
				}
				switch item.Type {
				case "text":
					emit(Message{Type: "text", Content: raw(item.Text)})
				case "thinking":
					emit(Message{Type: "thinking", Content: raw(item.Thinking)})
				case "tool_use":
					// The whole block, so its id pairs it with its tool_result.
					emit(Message{Type: "tool_use", Tool: item.Name, Content: c})
				case "tool_result":
					emit(Message{Type: "tool_result", Content: c})
				}
			}
		case "result":
			if in != nil {
				in.close()
			}
			if resume != "" && !sawWork && ev.IsError && ev.NumTurns == 0 && (ev.Cost == nil || *ev.Cost == 0) && len(ev.ModelUsage) == 0 {
				res.ResumeRefused = true
			}
			// Result usage is authoritative and already excludes Claude's caches from
			// input tokens. Repeated terminal snapshots replace rather than add totals.
			res.Usage = nil
			for _, model := range slices.Sorted(maps.Keys(ev.ModelUsage)) {
				u := ev.ModelUsage[model]
				if strings.TrimSpace(model) == "" {
					continue
				}
				res.Usage = append(res.Usage, Usage{Model: model, InputTokens: u.Input, OutputTokens: u.Output, CacheReadTokens: u.CacheRead, CacheWriteTokens: u.CacheWrite, CostUSD: u.Cost})
			}
			if res.Model == "" && len(res.Usage) > 0 {
				res.Model = res.Usage[0].Model
			}
			if len(res.Usage) == 0 && (ev.Cost != nil || ev.Usage != nil) {
				model := ev.Model
				if model == "" {
					model = fallbackModel
				}
				if model == "" {
					model = "claude-unattributed"
				}
				u := Usage{Model: model, CostUSD: ev.Cost}
				if ev.Usage != nil {
					u.InputTokens, u.OutputTokens = ev.Usage.Input, ev.Usage.Output
					u.CacheReadTokens, u.CacheWriteTokens = ev.Usage.CacheRead, ev.Usage.CacheWrite
				}
				res.Usage = []Usage{u}
			}
			if strings.TrimSpace(ev.TerminalReason) == "prompt_too_long" {
				res.Err = fmt.Errorf("Claude terminal_reason=prompt_too_long: context exhausted: %s", ev.Result)
				res.Reason = ReasonContextExhausted
			} else if ev.IsError || strings.HasPrefix(ev.Subtype, "error") {
				detail := strings.TrimSpace(strings.Join(append([]string{ev.Result}, ev.Errors...), "\n"))
				if detail == "" {
					detail = "Claude returned an error result without details"
				}
				res.Err = errors.New(detail)
			}
			if err := accounting.normalize(res, ev.ModelUsage); err != nil {
				res.Usage = nil
				// Accounting uncertainty must not erase the provider's terminal
				// failure or its retry classification. No ambiguous spend is sent.
				if res.Err != nil {
					res.Err = fmt.Errorf("%w; usage unavailable: %v", res.Err, err)
					return true, nil
				}
				return false, err
			}
			return true, nil
		default:
			emit(Message{Type: "log", Content: append(json.RawMessage(nil), line...)})
		}
		return false, nil
	}
}

// claudeMCPExists checks the replaceable adapter file without following links.
func claudeMCPExists(path string) (bool, error) {
	st, err := os.Lstat(path)
	if errors.Is(err, os.ErrNotExist) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	if !st.Mode().IsRegular() {
		return false, fmt.Errorf("MCP config is not a regular file: %s", path)
	}
	return true, nil
}
